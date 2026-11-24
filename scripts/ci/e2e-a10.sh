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

# The test fixture's EX with 1001 public and 1003 "Admin" hidden.
psql "INSERT INTO systems (id, label, name) VALUES (1, 'EX', 'Example network') ON CONFLICT DO NOTHING;
	INSERT INTO tags (name) VALUES ('dispatch'), ('admin') ON CONFLICT DO NOTHING;
	INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id)
	SELECT 1, 1001, 'DISP N', 'Dispatch North', id FROM tags WHERE name = 'dispatch' ON CONFLICT DO NOTHING;
	INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id, state)
	SELECT 1, 1003, 'ADMIN', 'Admin', id, 'hidden' FROM tags WHERE name = 'admin'
	ON CONFLICT (system_id, talkgroup_id) DO UPDATE SET state = 'hidden';
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

# Each of 1003's calls answers exactly as a call that was never stored.
never=$(answer GET /api/v1/calls/6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f/recording -H 'Range: bytes=0-')
echo "$never" | grep -q '^HTTP/1.1 404' || fail "a call never stored: $never"
for id in $hidden; do
	got=$(answer GET "/api/v1/calls/$id/recording" -H 'Range: bytes=0-')
	test "$got" = "$never" || fail "hidden $id answers
$got
but a call never stored answers
$never"
	got=$(answer QUERY /api/v1/calls -H 'Content-Type: application/json' --data "{\"ids\":[\"$id\"]}")
	gone=$(answer QUERY /api/v1/calls -H 'Content-Type: application/json' --data '{"ids":["6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f"]}')
	test "$got" = "$gone" || fail "hidden $id's details differ from a call never stored"
done

# Nothing was deleted to hide them.
for path in $(psql "SELECT c.recording_path $calls AND c.talkgroup = 1003"); do
	docker compose cp "api:/var/lib/radioarchive/recordings/$path" "$media/check.opus" >/dev/null ||
		fail "hidden recording $path is gone from the volume"
done

echo "ok: A10 end to end (1003 hidden, 3 recordings: the public API shows none of them)"
