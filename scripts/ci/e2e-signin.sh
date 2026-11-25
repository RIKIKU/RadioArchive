#!/bin/sh
# Listeners' sign-in (SPEC.md A33, ADR 0009) in a real browser against the
# compose stack: the API's image built from this checkout, its database
# with the test fixture's setup, Authentik with the blueprints, and Mailpit
# catching the sign-up codes. Authentik's own pages are used, as a person
# would.
#
#   sh scripts/ci/e2e-signin.sh [playwright arguments]
#
# Uses .env, or writes one with fresh made-up secrets (scripts/dev-env.sh).
# COMPOSE_PROJECT_NAME and the ports in .env keep it apart from other
# stacks on the host. Runs Playwright with web/'s dependencies, or, with
# RA_PLAYWRIGHT_IMAGE set (CI), in that Playwright image on the host's
# network.
set -eu
cd "$(dirname "$0")/../.."

[ -f .env ] || sh scripts/dev-env.sh > .env
set -a
. ./.env
set +a

# wait_for <seconds> <what> <command...>: every wait is bounded, so what
# never starts fails the run in minutes, naming itself.
wait_for() {
	secs=$1 what=$2
	shift 2
	end=$(($(date +%s) + secs))
	until "$@" >/dev/null 2>&1; do
		[ "$(date +%s)" -lt "$end" ] || { echo "e2e-signin.sh: $what wasn't ready after $secs s" >&2; return 1; }
		sleep 2
	done
}

docker compose up -d --build --wait --wait-timeout 600
# Authentik answers before its blueprints are applied: wait for the radio's
# provider, which they make. (The API holds no Authentik token: saved
# settings are in its own database, ADR 0021.)
wait_for 300 "Authentik's radioarchive provider" \
	curl -fsS "$AUTHENTIK_URL/application/o/radioarchive/.well-known/openid-configuration"
wait_for 120 "the API" curl -fsS "$APP_URL/healthz"
# Authentik's first pages after a start are slow; load them once first.
for flow in radioarchive-authentication radioarchive-signup; do
	curl -fsS -o /dev/null "$AUTHENTIK_URL/if/flow/$flow/" || true
	curl -fsS -o /dev/null "$AUTHENTIK_URL/api/v3/flows/executor/$flow/?query=" || true
done

# The test fixture's setup, once the API has migrated the database.
go run ./testing/webfixture "postgres://radioarchive:$POSTGRES_PASSWORD@127.0.0.1:$DB_PORT/radioarchive?sslmode=disable"

# The first admin's address (ADR 0025), which one test signs up with.
export RA_URL="$APP_URL" RA_AUTHENTIK_URL="$AUTHENTIK_URL" RA_MAILPIT_URL="http://localhost:$MAILPIT_PORT" \
	RA_INITIAL_ADMIN_EMAIL="${INITIAL_ADMIN_EMAIL:-}"
cd web
if [ -n "${RA_PLAYWRIGHT_IMAGE:-}" ]; then
	docker run --rm --network host --ipc=host -e RA_URL -e RA_AUTHENTIK_URL -e RA_MAILPIT_URL -e RA_INITIAL_ADMIN_EMAIL -e RA_SHOTS -e CI -e HOME=/root \
		-v "$PWD/..:/w" -w /w/web "$RA_PLAYWRIGHT_IMAGE" \
		sh -c 'npm ci --no-fund --no-audit && npx playwright test -c playwright.signin.config.ts "$@"' sh "$@"
else
	npx playwright test -c playwright.signin.config.ts "$@"
fi
