#!/bin/sh
# Checks what the PowerShell module's sign-in relies on in the compose
# stack's Authentik (ADR 0028): testing/oauthprobe, with two made-up users
# made for the run in Authentik's shell. Prints checks, never a token.
#
#   sh scripts/oauth-probe.sh [oauthprobe flags]
#
# Uses .env (scripts/dev-env.sh) and the running stack (docker compose up).
set -eu
cd "$(dirname "$0")/.."
set -a
. ./.env
set +a

secret() { head -c 48 /dev/urandom | base64 | tr -d '/+=\n' | cut -c1-"$1"; }

# make_user <email> <password>: an active user, as the sign-up leaves one.
make_user() {
	docker compose exec -T authentik-server ak shell -c "
from authentik.core.models import User
u = User.objects.create(username='$1', email='$1', name='Made Up Probe', attributes={'email_proven': '$1'})
u.set_password('$2')
u.save()
" >/dev/null 2>&1
}

run=$(secret 6 | tr 'A-Z' 'a-z')
a="probe-$run@example.test"
b="probe-device-$run@example.test"
pa=$(secret 24)
pb=$(secret 24)
make_user "$a" "$pa"
make_user "$b" "$pb"
docker compose exec -T authentik-server ak version 2>/dev/null | head -n 1 || true
go run ./testing/oauthprobe -authentik "$AUTHENTIK_URL" -email "$a" -password "$pa" -device-email "$b" -device-password "$pb" "$@"
