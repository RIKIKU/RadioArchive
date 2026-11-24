#!/bin/sh
# Writes a .env for the development and test stack (compose.yaml) to
# stdout, with fresh random secrets: no secret is ever committed.
#
#   sh scripts/dev-env.sh > .env
#
# The ports can be chosen, so several stacks can share a host:
#
#   API_PORT=18161 AUTHENTIK_PORT=19161 MAILPIT_PORT=18261 DB_PORT=55461 sh scripts/dev-env.sh
set -eu

secret() { head -c 64 /dev/urandom | base64 | tr -d '/+=\n' | cut -c1-"$1"; }

api=${API_PORT:-8080}
ak=${AUTHENTIK_PORT:-9000}
cat <<EOF
# Made-up settings for a throwaway development or test stack, written by
# scripts/dev-env.sh. Never use them for a deployment.
API_PORT=$api
AUTHENTIK_PORT=$ak
MAILPIT_PORT=${MAILPIT_PORT:-8025}
DB_PORT=${DB_PORT:-5432}
APP_URL=http://localhost:$api
AUTHENTIK_URL=http://localhost:$ak
POSTGRES_PASSWORD=$(secret 32)
AUTHENTIK_DB_PASSWORD=$(secret 32)
AUTHENTIK_SECRET_KEY=$(secret 60)
AUTHENTIK_ADMIN_EMAIL=akadmin@example.test
AUTHENTIK_ADMIN_PASSWORD=$(secret 24)
AUTHENTIK_API_TOKEN=$(secret 60)
OIDC_CLIENT_SECRET=$(secret 60)
LISTENER_SIGNUP=${LISTENER_SIGNUP:-true}
EOF
