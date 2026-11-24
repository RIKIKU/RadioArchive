#!/bin/sh
# The browser tests (web/tests) against the binary, with the app built into
# it, and its database holding the test fixture's setup (issues #54, #59).
#
#   sh scripts/ci/e2e-web.sh [playwright arguments]
#
# Needs Go, Node with web/'s dependencies installed (npm ci), and
# Playwright's browsers. The database is a fresh postgres:18 started here
# with Docker, or, with no Docker (CI runs this in Playwright's image), an
# empty database as its owner in RA_E2E_DATABASE_URL.
set -eu
cd "$(dirname "$0")/../.."

port=${RA_E2E_PORT:-18090}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/radioarchive-web.XXXXXX")
db=""
pid=""

cleanup() {
	rc=$?
	[ -z "$pid" ] || kill "$pid" 2>/dev/null || true
	[ -z "$db" ] || docker rm -f -v "$db" >/dev/null 2>&1 || true
	if [ "$rc" -ne 0 ] && [ -f "$tmp/api.log" ]; then tail -n 50 "$tmp/api.log" >&2; fi
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

(cd web && npm run build)
CGO_ENABLED=0 go build -trimpath -o "$tmp/radioarchive" ./cmd/radioarchive
mkdir -p "$tmp/recordings"
DATABASE_URL=$DATABASE_URL LISTEN_ADDR="127.0.0.1:$port" RECORDINGS_DIR="$tmp/recordings" \
	"$tmp/radioarchive" serve >"$tmp/api.log" 2>&1 &
pid=$!
# Node, not curl: Playwright's image may have no curl.
wait_for 120 "the API" node -e "fetch(process.argv[1]).then((r) => process.exit(r.ok ? 0 : 1), () => process.exit(1))" \
	"http://127.0.0.1:$port/healthz"
# The test fixture's setup, once the API has migrated the database.
go run ./testing/webfixture "$DATABASE_URL"

cd web
RA_URL="http://127.0.0.1:$port" npx playwright test "$@"
