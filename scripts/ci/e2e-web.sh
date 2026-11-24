#!/bin/sh
# The browser tests (web/tests) against the binary, with the app built into
# it, and its database holding the test fixture's setup (issues #54, #59),
# with the recorder Bench's client connected, for the playback tests that
# play the fake recorder's calls (issue #58).
#
#   sh scripts/ci/e2e-web.sh [playwright arguments]
#
# With no arguments the tests run in all three browsers; CI runs one
# browser per job, with --project=chromium, firefox or webkit.
#
# Needs Go, a C compiler and libopus (the recorder client is cgo), Node with
# web/'s dependencies installed (npm ci), and Playwright's browsers. Firefox
# plays Web Audio only to a sound server, so where `pulseaudio` is installed
# and none runs, one is started (it plays to nothing). The database is a
# fresh postgres:18 started here with Docker, or, with no Docker (CI runs
# this in Playwright's image), an empty database as its owner in
# RA_E2E_DATABASE_URL.
#
# The tests run twice: first all but the playback tests, in parallel, then
# the playback tests (@live in their titles), one at a time, since they share
# the one recorder.
#
# The queue's tests (issue #60) also need the recorder healthy, then deaf
# (A27): while they run, a fake recorder sends Bench's client one healthy
# heartbeat every 2 s, as trunk-recorder would, unless the file named in
# RA_HEARTBEATS_HELD exists. The recorder notice comes 3 s after the
# recorder goes deaf (RECORDER_NOTICE_AFTER), not 2 min. They change
# talkgroups as an admin would with the fixture's command (RA_WEBFIXTURE,
# on RA_DATABASE_URL).
#
# The Recent calls page's tests (issue #79) search made-up calls the
# fixture's command stores before the tests start; those that change a
# talkgroup, or store a call of their own (on RA_RECORDINGS), are @live
# too, so they run one at a time. The API runs in TZ=Australia/Melbourne.
#
# The call link's A16 test (issue #80) starts, ends and deletes a call of
# its own with the fixture's command, so it's @live as well.
#
# The basket's A19 test (issue #81) dates a call of its own and runs the
# nightly retention job, and another hides 1001 for a while, with the
# fixture's command: @live too.
#
# A shared conversation's tests (issue #82) that store calls of their own,
# hidden, ignored and deleted ones among them, are @live as well.
set -eu
cd "$(dirname "$0")/../.."

port=${RA_E2E_PORT:-18090}
udp=${RA_E2E_UDP_PORT:-19890}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/radioarchive-web.XXXXXX")
db=""
pid=""
client=""
pulse=""
beats=""

cleanup() {
	rc=$?
	[ -z "$beats" ] || kill "$beats" 2>/dev/null || true
	[ -z "$client" ] || kill "$client" 2>/dev/null || true
	[ -z "$pulse" ] || pulseaudio --kill 2>/dev/null || true
	[ -z "$pid" ] || kill "$pid" 2>/dev/null || true
	[ -z "$db" ] || docker rm -f -v "$db" >/dev/null 2>&1 || true
	if [ "$rc" -ne 0 ] && [ -f "$tmp/api.log" ]; then tail -n 50 "$tmp/api.log" >&2; fi
	if [ "$rc" -ne 0 ] && [ -f "$tmp/client.log" ]; then tail -n 50 "$tmp/client.log" >&2; fi
	rm -rf "$tmp"
}
trap cleanup EXIT

# wait_for <seconds> <what> <command...>: every wait is bounded, so what
# never starts fails the run in minutes, naming itself.
wait_for() {
	secs=$1 what=$2
	shift 2
	end=$(($(date +%s) + secs))
	until "$@" >/dev/null 2>&1; do
		[ "$(date +%s)" -lt "$end" ] || { echo "e2e-web.sh: $what wasn't ready after $secs s" >&2; return 1; }
		sleep 1
	done
}

if [ -n "${RA_E2E_DATABASE_URL:-}" ]; then
	DATABASE_URL=$RA_E2E_DATABASE_URL
else
	dbport=${RA_E2E_DB_PORT:-55490}
	db="radioarchive-e2e-web-$$"
	docker run -d --name "$db" -e POSTGRES_PASSWORD=test -p "127.0.0.1:$dbport:5432" postgres:18 >/dev/null
	wait_for 120 "the database" docker exec "$db" pg_isready -U postgres -h 127.0.0.1
	DATABASE_URL="postgres://postgres:test@127.0.0.1:$dbport/postgres?sslmode=disable"
fi

if command -v pulseaudio >/dev/null 2>&1 && ! pulseaudio --check 2>/dev/null; then
	if pulseaudio --daemonize --exit-idle-time=-1 2>/dev/null; then
		pulse=1
	else
		echo "e2e-web.sh: couldn't start pulseaudio; Firefox may play nothing" >&2
	fi
fi

(cd web && npm run build)
CGO_ENABLED=0 go build -trimpath -o "$tmp/radioarchive" ./cmd/radioarchive
CGO_ENABLED=0 go build -trimpath -o "$tmp/fakerecorder" ./testing/fakerecorder/cmd/fakerecorder
CGO_ENABLED=1 go build -trimpath -o "$tmp/radioarchive-client" ./cmd/radioarchive-client
CGO_ENABLED=0 go build -trimpath -o "$tmp/webfixture" ./testing/webfixture
mkdir -p "$tmp/recordings" "$tmp/media" "$tmp/queue"
# The deployment's time zone isn't the browsers' (UTC), so the Recent calls
# page's tests show its times are TZ's (section 13).
TZ=Australia/Melbourne DATABASE_URL=$DATABASE_URL LISTEN_ADDR="127.0.0.1:$port" RECORDINGS_DIR="$tmp/recordings" RECORDER_NOTICE_AFTER=3s \
	"$tmp/radioarchive" serve >"$tmp/api.log" 2>&1 &
pid=$!
# Node, not curl: Playwright's image may have no curl.
wait_for 120 "the API" node -e "fetch(process.argv[1]).then((r) => process.exit(r.ok ? 0 : 1), () => process.exit(1))" \
	"http://127.0.0.1:$port/healthz"
# The recorder Bench and its key (R10), then the test fixture's setup with
# Bench attached, once the API has migrated the database.
key=$(DATABASE_URL=$DATABASE_URL "$tmp/radioarchive" recorder add Bench | grep -o 'rak_[A-Za-z0-9_-]*')
test -n "$key"
"$tmp/webfixture" "$DATABASE_URL" Bench

# Bench's recorder client, beside the fake recorder's "trunk-recorder".
SERVER_URL="http://127.0.0.1:$port" CLIENT_KEY="$key" UDP_ADDR="127.0.0.1:$udp" QUEUE_DIR="$tmp/queue" \
	"$tmp/radioarchive-client" >"$tmp/client.log" 2>&1 &
client=$!
wait_for 60 "the recorder client's live connection" grep -q '"live connection open"' "$tmp/client.log"
# The archive the Recent calls page's tests search (issue #79): made-up
# calls as Bench's, with their recordings on the volume.
"$tmp/webfixture" "$DATABASE_URL" calls Bench "$tmp/recordings"

fixtures=$(pwd)/testing/fakerecorder/testdata/fixtures

# trunk-recorder's heartbeat: healthy, every 2 s, unless held (above).
held="$tmp/heartbeats-held"
cat >"$tmp/heartbeat.json" <<'EOF'
{"boot_id": 6000, "epoch_ms": 0, "calls": {},
 "events": [{"at_ms": 0, "type": "heartbeat", "systems": [{"sys_num": 0, "short_name": "ex", "system_type": "p25", "decode_rate": 40}]}]}
EOF
(
	while :; do
		[ -e "$held" ] || "$tmp/fakerecorder" -scenario "$tmp/heartbeat.json" -addr "127.0.0.1:$udp" -fixtures "$fixtures" >/dev/null 2>&1 || true
		sleep 2
	done
) &
beats=$!
# The client relays it within its 5 s heartbeat: Bench is heard, not deaf.
sleep 6

cd web
RA_URL="http://127.0.0.1:$port" npx playwright test --grep-invert @live --pass-with-no-tests "$@"
RA_URL="http://127.0.0.1:$port" RA_FAKERECORDER="$tmp/fakerecorder" RA_FIXTURES="$fixtures" \
	RA_UDP_ADDR="127.0.0.1:$udp" RA_MEDIA="$tmp/media" RA_HEARTBEATS_HELD="$held" \
	RA_WEBFIXTURE="$tmp/webfixture" RA_DATABASE_URL="$DATABASE_URL" RA_RECORDINGS="$tmp/recordings" \
	npx playwright test --grep @live --workers=1 --pass-with-no-tests "$@"
