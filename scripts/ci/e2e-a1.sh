#!/bin/sh
# A1 (SPEC.md section 8) end to end with the real images: the fake recorder,
# then the recorder client's container, then the API's container, then
# PostgreSQL. Two overlapping calls on talkgroup 1001 must give 2 calls,
# each with its live frames, its recording and its own transmissions.
#
#   sh scripts/ci/e2e-a1.sh <client image>
#
# Expects the compose stack to be up (docker compose up --wait), with the
# API on 127.0.0.1:${API_PORT:-8080}, and Go on the PATH for the fake
# recorder.
set -eu

image=${1:?usage: e2e-a1.sh <client image>}
api_port=${API_PORT:-8080}
udp_port=${E2E_UDP_PORT:-19850}
media=$(mktemp -d "${TMPDIR:-/tmp}/radioarchive-e2e.XXXXXX")
name="e2e-a1-$(date +%s)"
client="radioarchive-e2e-client-$$"

cleanup() {
	docker logs "$client" 2>&1 | tail -n 50 || true
	docker rm -f "$client" >/dev/null 2>&1 || true
	rm -rf "$media" 2>/dev/null || true
}
trap cleanup EXIT

psql() { docker compose exec -T db psql -U radioarchive -d radioarchive -tAc "$1"; }

# The recorder and its key, from the server shell (R10).
key=$(docker compose exec -T api radioarchive recorder add "$name" | grep -o 'rak_[A-Za-z0-9_-]*')
test -n "$key"

# The test fixture's system EX with talkgroup 1001, and the recorder's `ex`
# attached to it (R11, R12). The admin API that does this in the Go tests
# isn't served until admin sign-in exists, so the fixture goes straight in.
psql "INSERT INTO systems (id, label, name) VALUES (1, 'EX', 'Example network') ON CONFLICT DO NOTHING;
	INSERT INTO tags (name) VALUES ('dispatch') ON CONFLICT DO NOTHING;
	INSERT INTO talkgroups (system_id, talkgroup_id, label, name, tag_id)
	SELECT 1, 1001, 'DISP N', 'Dispatch North', id FROM tags WHERE name = 'dispatch' ON CONFLICT DO NOTHING;
	INSERT INTO attachments (recorder_id, short_name, system_id) SELECT id, 'ex', 1 FROM recorders WHERE name = '$name';" >/dev/null

# trunk-recorder's media directory: the fake recorder writes the WAVs here,
# and the client deletes them once acknowledged, so it runs as their owner,
# as it would beside trunk-recorder.
mkdir -p "$media/app/media"

docker run -d --name "$client" --network host --user "$(id -u):$(id -g)" \
	-e SERVER_URL="http://127.0.0.1:$api_port" -e CLIENT_KEY="$key" -e UDP_ADDR="127.0.0.1:$udp_port" \
	-v "$media/app/media:/app/media" "$image" >/dev/null

# Wait for the live connection.
i=0
until docker logs "$client" 2>&1 | grep -q '"live connection open"'; do
	i=$((i + 1))
	if [ "$i" -gt 100 ]; then echo "FAIL: the client never connected" >&2; exit 1; fi
	sleep 0.1
done

go run ./testing/fakerecorder/cmd/fakerecorder \
	-scenario testing/fakerecorder/testdata/scenarios/a1-overlapping.json \
	-addr "127.0.0.1:$udp_port" -recordings "$media"

calls="FROM calls c JOIN recorders r ON r.id = c.recorder_id WHERE r.name = '$name'"
i=0
# Both the recording and the live call's end (which records its frame count)
# must have arrived: they come over separate connections.
until [ "$(psql "SELECT count(*) $calls AND c.recording_path IS NOT NULL AND c.live_frames > 0")" = 2 ]; do
	i=$((i + 1))
	if [ "$i" -gt 300 ]; then echo "FAIL: the recordings never arrived" >&2; psql "SELECT * $calls"; exit 1; fi
	sleep 0.1
done

got=$(psql "SELECT c.call_num, c.talkgroup, c.live_frames,
	(SELECT string_agg(t.offset_ms || ':' || t.unit, ',' ORDER BY t.offset_ms) FROM transmissions t WHERE t.call_id = c.id)
	$calls ORDER BY c.call_num")
want="1|1001|75|0:5001,1000:5123
2|1001|75|0:5100"
echo "$got"
if [ "$got" != "$want" ]; then
	printf 'FAIL: want\n%s\n' "$want" >&2
	exit 1
fi

# Each recording is an Ogg/Opus file on the volume, named by its call ID.
for row in $(psql "SELECT c.id || '=' || c.recording_path $calls"); do
	id=${row%%=*}
	path=${row#*=}
	case "$path" in *"$id"*) ;; *) echo "FAIL: $path isn't named by call $id" >&2; exit 1 ;; esac
	# distroless has no shell or cat, so copy the file out.
	docker compose cp "api:/var/lib/radioarchive/recordings/$path" "$media/check.opus" >/dev/null
	test "$(head -c 4 "$media/check.opus")" = OggS || { echo "FAIL: $path isn't Ogg" >&2; exit 1; }
	rm "$media/check.opus"
done

# trunk-recorder's WAVs are gone once the server acknowledged them.
left=$(find "$media" -name '*.wav' | wc -l)
test "$left" -eq 0 || { echo "FAIL: $left WAVs left after upload" >&2; exit 1; }

echo "ok: A1 end to end (fake recorder -> client image -> API image -> PostgreSQL)"
