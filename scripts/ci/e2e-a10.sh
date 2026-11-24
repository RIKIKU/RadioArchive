#!/bin/sh
# A10 (SPEC.md section 8) for storage and the API, end to end with the real
# images: talkgroup 1003 is hidden and gets 3 recordings from the fake
# recorder, through the recorder client's container, into the API's. The
# binary's public API must list and play only the public call, and answer
# for each of 1003's calls byte for byte as for a call that was never
# stored. The binary serves no admin API (ADR 0003), so none is tried.
#
#   sh scripts/ci/e2e-a10.sh <client image>
#
# Expects the compose stack to be up, as e2e-a1.sh does.
set -eu

image=${1:?usage: e2e-a10.sh <client image>}
api="http://127.0.0.1:${API_PORT:-8080}"
udp_port=${E2E_UDP_PORT:-19850}
media=$(mktemp -d "${TMPDIR:-/tmp}/radioarchive-e2e.XXXXXX")
name="e2e-a10-$(date +%s)"
client="radioarchive-e2e-client-a10-$$"

cleanup() {
	docker logs "$client" 2>&1 | tail -n 50 || true
	docker rm -f "$client" >/dev/null 2>&1 || true
	rm -rf "$media" 2>/dev/null || true
}
trap cleanup EXIT

psql() { docker compose exec -T db psql -U radioarchive -d radioarchive -tAc "$1"; }
fail() { echo "FAIL: $*" >&2; exit 1; }

key=$(docker compose exec -T api radioarchive recorder add "$name" | grep -o 'rak_[A-Za-z0-9_-]*')
test -n "$key"

# The test fixture's EX with 1001 public, 1002 "Fireground 41" public with
# fireground channel number 41, and 1003 "Admin" hidden, here with number
# 43. The fixture's calls are dated November 2025, so retention is set long
# enough that a nightly run (R88) during the test keeps them.
psql "INSERT INTO settings (key, value) VALUES ('retention_days', '3650')
	ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
	INSERT INTO systems (id, label, name) VALUES (1, 'EX', 'Example network') ON CONFLICT DO NOTHING;
	INSERT INTO tags (name) VALUES ('dispatch'), ('fireground'), ('admin') ON CONFLICT DO NOTHING;
	INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id)
	SELECT 1, 1001, 'DISP N', 'Dispatch North', id FROM tags WHERE name = 'dispatch' ON CONFLICT DO NOTHING;
	INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, fireground_channel)
	SELECT 1, 1002, 'FG 41', 'Fireground 41', id, 41 FROM tags WHERE name = 'fireground' ON CONFLICT DO NOTHING;
	INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, state, fireground_channel)
	SELECT 1, 1003, 'ADMIN', 'Admin', id, 'hidden', 43 FROM tags WHERE name = 'admin'
	ON CONFLICT (system_id, talkgroup_id) DO UPDATE SET state = 'hidden', fireground_channel = 43;
	INSERT INTO attachments (recorder_id, short_name, system_id) SELECT id, 'ex', 1 FROM recorders WHERE name = '$name';" >/dev/null

mkdir -p "$media/app/media"
docker run -d --name "$client" --network host --user "$(id -u):$(id -g)" \
	-e SERVER_URL="$api" -e CLIENT_KEY="$key" -e UDP_ADDR="127.0.0.1:$udp_port" \
	-v "$media/app/media:/app/media" "$image" >/dev/null
i=0
until docker logs "$client" 2>&1 | grep -q '"live connection open"'; do
	i=$((i + 1))
	if [ "$i" -gt 100 ]; then fail "the client never connected"; fi
	sleep 0.1
done

go run ./testing/fakerecorder/cmd/fakerecorder \
	-scenario testing/fakerecorder/testdata/scenarios/a10-hidden.json \
	-addr "127.0.0.1:$udp_port" -recordings "$media"

calls="FROM calls c JOIN recorders r ON r.id = c.recorder_id WHERE r.name = '$name' AND c.recording_path IS NOT NULL"
i=0
until [ "$(psql "SELECT count(*) $calls")" = 4 ]; do
	i=$((i + 1))
	if [ "$i" -gt 300 ]; then psql "SELECT * $calls"; fail "the recordings never arrived"; fi
	sleep 0.1
done
pub=$(psql "SELECT c.id $calls AND c.talkgroup = 1001")
hidden=$(psql "SELECT c.id $calls AND c.talkgroup = 1003 ORDER BY c.call_num")
test "$(echo "$hidden" | wc -l)" -eq 3 || fail "want 3 recordings on 1003: $hidden"

# answer METHOD PATH [curl args]: status, headers but Date, and body.
answer() {
	method=$1 path=$2
	shift 2
	curl -sS -X "$method" -D "$media/h" -o "$media/b" "$@" "$api$path"
	grep -iv '^date:' "$media/h" | tr -d '\r'
	cat "$media/b"
}

# The call list gives the public call only, and nothing of 1003.
ids=$(printf '"%s",' "$pub" $hidden | sed 's/,$//')
for method in QUERY POST; do
	list=$(answer "$method" /api/v1/calls -H 'Content-Type: application/json' --data "{\"ids\":[$ids]}")
	echo "$list" | grep -q "\"id\":\"$pub\"" || fail "$method /api/v1/calls doesn't list the public call: $list"
	if echo "$list" | sed "s/$pub//g" | grep -E -q '1003|Admin|ADMIN'; then
		fail "$method /api/v1/calls leaks 1003: $list"
	fi
	for id in $hidden; do
		if echo "$list" | grep -q "$id"; then fail "$method /api/v1/calls lists hidden call $id"; fi
	done
done

# The public recording plays, with range requests.
answer GET "/api/v1/calls/$pub/recording" -H 'Range: bytes=0-3' | grep -q '^HTTP/1.1 206' || fail "no range playback"
grep -q 'OggS' "$media/b" || fail "the public recording isn't Ogg"
grep -iq '^cache-control: private, max-age=31536000, immutable' "$media/h" || fail "public Cache-Control"

# The public call downloads as the stored file, byte for byte, named for
# its start in the API's TZ and its talkgroup (R62, R63, A17).
answer GET "/api/v1/calls/$pub/download" >/dev/null
grep -q '^HTTP/1.1 200' "$media/h" || fail "the public download: $(cat "$media/h")"
grep -iq '^content-disposition: attachment; filename="[0-9]\{4\}-[0-9][0-9]-[0-9][0-9]_[0-9]\{6\}_DISP N_1001\.opus"' "$media/h" ||
	fail "the download's file name: $(cat "$media/h")"
docker compose cp "api:/var/lib/radioarchive/recordings/$(psql "SELECT c.recording_path $calls AND c.talkgroup = 1001")" "$media/stored.opus" >/dev/null
test "$(sha256sum <"$media/b")" = "$(sha256sum <"$media/stored.opus")" || fail "the download isn't the stored file"

# Zips (R65, ADR 0015): by GET (the list packed in the URL), QUERY and
# POST, a list of the public call and 1003's calls gives the public call's
# zip alone, byte for byte and header for header, and nothing of 1003.
# zipurl ID...: the GET path of a zip of these loose calls (fewer than 128).
zipurl() {
	hex=$(printf '010000%02x' "$#"; for id in "$@"; do printf '%s' "$id" | tr -d -; done; printf '00')
	printf '/api/v1/zip/%s' "$(printf '%s' "$hex" | xxd -r -p | base64 -w0 | tr '+/' '-_' | tr -d '=')"
}
# zipanswer NAME METHOD PATH [curl args]: the answer's headers (but Date)
# in $media/NAME.h and its body in $media/NAME.b.
zipanswer() {
	n=$1 method=$2 path=$3
	shift 3
	curl -sS -X "$method" -D "$media/$n.raw" -o "$media/$n.b" "$@" "$api$path"
	grep -iv '^date:' "$media/$n.raw" | tr -d '\r' >"$media/$n.h"
}
hiddenids=$(printf ',"%s"' $hidden)
for method in GET QUERY POST; do
	if [ "$method" = GET ]; then
		zipanswer zpub GET "$(zipurl "$pub")"
		zipanswer zmix GET "$(zipurl "$pub" $hidden)"
	else
		zipanswer zpub "$method" /api/v1/zip -H 'Content-Type: application/json' --data "{\"calls\":[\"$pub\"]}"
		zipanswer zmix "$method" /api/v1/zip -H 'Content-Type: application/json' --data "{\"calls\":[\"$pub\"$hiddenids]}"
	fi
	grep -q '^HTTP/1.1 200' "$media/zpub.h" || fail "$method zip of the public call: $(cat "$media/zpub.h")"
	grep -iq '^content-type: application/zip' "$media/zpub.h" || fail "$method zip's type: $(cat "$media/zpub.h")"
	test "$(head -c 2 "$media/zpub.b")" = PK || fail "$method zip isn't a zip"
	cmp -s "$media/zpub.h" "$media/zmix.h" || fail "$method zip with 1003's calls has other headers:
$(cat "$media/zmix.h")
than the public call's:
$(cat "$media/zpub.h")"
	cmp -s "$media/zpub.b" "$media/zmix.b" || fail "$method zip with 1003's calls isn't the public call's zip"
	if sed "s/$pub//g" "$media/zmix.b" | grep -a -E -q '1003|Admin|ADMIN'; then fail "$method zip leaks 1003"; fi
	for id in $hidden; do
		if grep -a -q "$id" "$media/zmix.b"; then fail "$method zip names hidden call $id"; fi
	done
done
# Resumed: a range of the GET zip is the rest of the whole.
zipanswer zall GET "$(zipurl "$pub" $hidden)"
etag=$(grep -i '^etag:' "$media/zall.h" | cut -d' ' -f2-)
zipanswer zrest GET "$(zipurl "$pub" $hidden)" -H 'Range: bytes=100-' -H "If-Range: $etag"
grep -q '^HTTP/1.1 206' "$media/zrest.h" || fail "the zip's range: $(cat "$media/zrest.h")"
tail -c +101 "$media/zall.b" | cmp -s - "$media/zrest.b" || fail "the zip's range isn't the rest of it"

# Each of 1003's calls answers exactly as a call that was never stored.
never=$(answer GET /api/v1/calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f/recording -H 'Range: bytes=0-')
echo "$never" | grep -q '^HTTP/1.1 404' || fail "a call never stored: $never"
neverdl=$(answer GET /api/v1/calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f/download -H 'Range: bytes=0-')
test "$neverdl" = "$never" || fail "a call never stored: its download answers
$neverdl
but its recording
$never"
for id in $hidden; do
	got=$(answer GET "/api/v1/calls/$id/recording" -H 'Range: bytes=0-')
	test "$got" = "$never" || fail "hidden $id answers
$got
but a call never stored answers
$never"
	got=$(answer GET "/api/v1/calls/$id/download" -H 'Range: bytes=0-')
	test "$got" = "$never" || fail "hidden $id's download answers
$got
but a call never stored answers
$never"
	got=$(answer QUERY /api/v1/calls -H 'Content-Type: application/json' --data "{\"ids\":[\"$id\"]}")
	gone=$(answer QUERY /api/v1/calls -H 'Content-Type: application/json' --data '{"ids":["6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"]}')
	test "$got" = "$gone" || fail "hidden $id's details differ from a call never stored"
done

# The app shell built into the binary: a hidden call's path answers exactly
# as a call's never stored, in any look, and names none of 1003's calls.
# (The page is static: the comparison, not a search for "1003", is the
# check, as its asset names are hashes.)
never=$(answer GET /calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f)
echo "$never" | grep -q '^HTTP/1.1 200' || fail "the app shell: $never"
echo "$never" | grep -q '_app/immutable' || fail "the binary has no app built in: $never"
for id in $hidden; do
	for cookie in '' 'look=console; theme=dark'; do
		got=$(answer GET "/calls/$id" -H "Cookie: $cookie")
		want=$(answer GET /calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f -H "Cookie: $cookie")
		test "$got" = "$want" || fail "the app shell for hidden $id differs from a call never stored:
$got
$want"
		if echo "$got" | grep -q "$id"; then fail "the app shell names hidden call $id"; fi
	done
done

# The catalogue and search (R77) list the public talkgroups and nothing of
# 1003, uncached (R74).
cat=$(answer GET /api/v1/catalogue)
echo "$cat" | grep -q '"id":1001' || fail "the catalogue doesn't list 1001: $cat"
grep -iq '^cache-control: no-store' "$media/h" || fail "the catalogue's Cache-Control"
for q in "" 1003 Admin ADMIN admin 100; do
	got=$(answer GET "/api/v1/catalogue/search?q=$q")
	echo "$got" | grep -q '^HTTP/1.1 200' || fail "search $q: $got"
	if echo "$cat$got" | grep -E -q '1003|Admin|ADMIN|"admin"'; then fail "the catalogue or search $q leaks 1003: $cat$got"; fi
done

# Fireground lookup (R101): 041 finds 1002; 1003's number answers byte for
# byte as a number no talkgroup has.
answer GET /api/v1/fireground/041 | grep -q '"id":1002' || fail "fireground 041 doesn't find 1002"
unknown=$(answer GET /api/v1/fireground/99)
echo "$unknown" | grep -q '^HTTP/1.1 404' || fail "fireground 99: $unknown"
for n in 43 043; do
	got=$(answer GET "/api/v1/fireground/$n")
	test "$got" = "$unknown" || fail "fireground $n (hidden 1003) answers
$got
but 99 answers
$unknown"
done

# Recent calls and search (R46, ADR 0013) over the fixture's calls: the
# public call is listed and none of 1003's, and a filter naming 1003
# answers byte for byte as one naming a talkgroup that doesn't exist. (The
# fake recorder's calls share their start times, so a range holding only
# 1003's calls is the Go harness's probe, not this one's.)
iso() { psql "SELECT to_char(($1) AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"') $calls"; }
from=$(iso "date_trunc('second', min(c.started_at))")
to=$(iso "date_trunc('second', max(c.started_at)) + interval '2 s'")
window="from=$from&to=$to"
recent=$(answer GET "/api/v1/recent-calls?$window")
echo "$recent" | grep -q "\"id\":\"$pub\"" || fail "recent calls don't list the public call: $recent"
grep -iq '^cache-control: no-store' "$media/h" || fail "recent calls' Cache-Control"
if echo "$recent" | sed "s/$pub//g" | grep -E -q '1003|Admin|ADMIN'; then fail "recent calls leak 1003: $recent"; fi
for id in $hidden; do
	if echo "$recent" | grep -q "$id"; then fail "recent calls list hidden call $id"; fi
done
unknown=$(answer GET "/api/v1/recent-calls?$window&talkgroups=4711")
echo "$unknown" | grep -q '"calls":\[\]' || fail "a filter naming 4711: $unknown"
for tgs in 1003 1.1003 1003,4711; do
	got=$(answer GET "/api/v1/recent-calls?$window&talkgroups=$tgs")
	test "$got" = "$unknown" || fail "a filter naming $tgs answers
$got
but one naming 4711 answers
$unknown"
done
# The same searches with their parameters in a JSON body (ADR 0016): each
# answers as its GET does.
for method in QUERY POST; do
	got=$(answer "$method" /api/v1/recent-calls -H 'Content-Type: application/json' --data "{\"from\":\"$from\",\"to\":\"$to\"}")
	test "$got" = "$recent" || fail "$method recent calls answer
$got
but GET answers
$recent"
	for tgs in '"4711"' '"1003"' '"1.1003"' '"1003","4711"'; do
		got=$(answer "$method" /api/v1/recent-calls -H 'Content-Type: application/json' \
			--data "{\"from\":\"$from\",\"to\":\"$to\",\"talkgroups\":[$tgs]}")
		test "$got" = "$unknown" || fail "$method recent calls naming $tgs answer
$got
but GET naming 4711 answers
$unknown"
	done
done

# Nothing was deleted to hide them.
for path in $(psql "SELECT c.recording_path $calls AND c.talkgroup = 1003"); do
	docker compose cp "api:/var/lib/radioarchive/recordings/$path" "$media/check.opus" >/dev/null ||
		fail "hidden recording $path is gone from the volume"
done

echo "ok: A10 end to end (1003 hidden, 3 recordings: the public API, catalogue and fireground lookup show none of them)"
