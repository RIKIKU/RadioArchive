#!/bin/sh
# The PowerShell module's behaviour tests (issue #116, ADR 0028) against the
# compose stack that scripts/ci/e2e-signin.sh started: the module's
# sign-in through Authentik's own pages in a real browser, with a device
# code, a second window, the admin commands, A26's module part and a 1003
# recording saved byte for byte (web/module-tests).
#
#   RA_PWSH_DIR=<folder holding pwsh> RA_PS_MODULES=<folder with SecretManagement and SecretStore> \
#     sh scripts/ci/e2e-module.sh [playwright arguments]
#
# Uses .env and the running stack. With RA_PLAYWRIGHT_IMAGE set (CI), runs
# Playwright in that image on the host's network, with the two folders
# mounted read-only.
set -eu
cd "$(dirname "$0")/../.."
: "${RA_PWSH_DIR:?set RA_PWSH_DIR to the folder holding pwsh}"
: "${RA_PS_MODULES:?set RA_PS_MODULES to the folder holding SecretManagement and SecretStore}"
set -a
. ./.env
set +a

wait_for() {
	secs=$1 what=$2
	shift 2
	end=$(($(date +%s) + secs))
	until "$@" >/dev/null 2>&1; do
		[ "$(date +%s)" -lt "$end" ] || { echo "e2e-module.sh: $what wasn't ready after $secs s" >&2; return 1; }
		sleep 2
	done
}
wait_for 300 "Authentik's module client" \
	curl -fsS "$AUTHENTIK_URL/application/o/radioarchive-powershell/.well-known/openid-configuration"
wait_for 120 "the API" curl -fsS "$APP_URL/healthz"

# A stored call on hidden 1003, with its recording on the API's volume, for
# the module to save. The recorder Bench (its key isn't needed: no client
# runs) and the fixture's attachment first; both are left as they are when
# they exist already.
db="postgres://radioarchive:$POSTGRES_PASSWORD@127.0.0.1:$DB_PORT/radioarchive?sslmode=disable"
docker compose exec -T api /usr/local/bin/radioarchive recorder add Bench >/dev/null 2>&1 || true
go run ./testing/webfixture "$db" Bench
work=$PWD/.e2e-module
rm -rf "$work"
mkdir -p "$work/recordings"
id=$(go run ./testing/webfixture "$db" call-dated Bench "$work/recordings" 1003 9003 1h | tail -n 1)
rel=$(docker compose exec -T db psql -U radioarchive -d radioarchive -tAc "SELECT recording_path FROM calls WHERE id = '$id'")
test -f "$work/recordings/$rel"
chmod -R a+rX "$work/recordings"
docker compose cp "$work/recordings/." api:/var/lib/radioarchive/recordings/

export RA_URL="$APP_URL" RA_AUTHENTIK_URL="$AUTHENTIK_URL" RA_MAILPIT_URL="http://localhost:$MAILPIT_PORT" \
	RA_INITIAL_ADMIN_EMAIL="${INITIAL_ADMIN_EMAIL:-}"
cd web
if [ -n "${RA_PLAYWRIGHT_IMAGE:-}" ]; then
	docker run --rm --network host --ipc=host -e RA_URL -e RA_AUTHENTIK_URL -e RA_MAILPIT_URL -e RA_INITIAL_ADMIN_EMAIL -e CI -e HOME=/root \
		-e RA_PWSH=/opt/pwsh/pwsh -e RA_PS_MODULES=/opt/psmodules -e RA_RECORDING_FILE="/w/.e2e-module/recordings/$rel" \
		-v "$RA_PWSH_DIR:/opt/pwsh:ro" -v "$RA_PS_MODULES:/opt/psmodules:ro" \
		-v "$PWD/..:/w" -w /w/web "$RA_PLAYWRIGHT_IMAGE" \
		sh -c 'npm ci --no-fund --no-audit && npx playwright test -c playwright.module.config.ts "$@"' sh "$@"
else
	RA_PWSH="$RA_PWSH_DIR/pwsh" RA_PS_MODULES="$RA_PS_MODULES" RA_RECORDING_FILE="$work/recordings/$rel" \
		npx playwright test -c playwright.module.config.ts "$@"
fi
