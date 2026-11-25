#!/bin/sh
# SPEC.md section 9 (issue #155) on a clean host, as the deploy guide
# (docs/deploy.md) has an operator do it, from the release's files as a dry
# run of the release writes them (scripts/ci/release-assets.sh), with the
# images built here under the names the files give them:
#   1. the server's compose.yaml and env.example start the whole stack,
#      Authentik included, behind the guide's nginx example with
#      TRUSTED_PROXY set; recorder-compose.yaml starts the recorder side
#      with the fake recorder in place of trunk-recorder and a radio;
#   2. the first admin signs up and is made admin, creates system EX,
#      imports the fixture's setup and attaches the recorder from the
#      shell's `recorder add`;
#   3. a browser presses Start listening with {1001}, hears a fixture call
#      live within 2 s, replays it from Recent calls, shares it and
#      downloads a basket.
# Each command is printed before it runs (+ ...): CI keeps the whole output
# as the job's artifact, the evidence (step 5).
#
#   RA_PLAYWRIGHT_IMAGE=<Playwright image> sh scripts/ci/e2e-section9.sh
#
# Without RA_PLAYWRIGHT_IMAGE it runs web/'s own Playwright. Ports:
# 127.0.0.1:18400 (nginx: the radio), 18401 (nginx: Authentik), 18402
# (Mailpit), 18410/18411 (the published API and Authentik ports).
set -eu
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=$repo/.e2e-section9
version=0.1.0
fail() { echo "FAIL: $*" >&2; exit 1; }
rm -rf "$work" && mkdir -p "$work/server" "$work/recorder" "$work/media"
log() { echo "+ $*"; "$@"; }

cleanup() {
	(cd "$work/recorder" 2>/dev/null && docker compose -p ra-s9-recorder down --volumes >/dev/null 2>&1) || true
	(cd "$work/server" 2>/dev/null && docker compose -p ra-s9 down --volumes >/dev/null 2>&1) || true
	docker rm -f ra-s9-nginx >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ── The release's files, and its images, built here ───────────────────────
log sh "$repo/scripts/ci/release-assets.sh" "$version" "$work/release" >/dev/null
docker build -q --build-arg VERSION=$version -t ghcr.io/rikiku/radioarchive:0.1 "$repo" >/dev/null
docker build -q --build-arg VERSION=$version -f "$repo/recorder/client/Dockerfile" -t ghcr.io/rikiku/radioarchive-client:0.1 "$repo" >/dev/null
CGO_ENABLED=0 go build -C "$repo" -o "$work/fakerecorder" ./testing/fakerecorder/cmd/fakerecorder

# ── Step 1: the server, as the guide installs it ───────────────────────────
cd "$work/server"
log cp ../release/compose.yaml ../release/env.example .
log tar -xzf ../release/radioarchive-config.tar.gz
s() { head -c 48 /dev/urandom | base64 | tr -d '/+=\n' | cut -c1-32; }
admin=first-admin-$(s | tr 'A-Z' 'a-z' | cut -c1-8)@example.test
sed -e "s#^APP_URL=.*#APP_URL=http://127.0.0.1:18400#" -e "s#^AUTHENTIK_URL=.*#AUTHENTIK_URL=http://127.0.0.1:18401#" \
	-e "s#^INITIAL_ADMIN_EMAIL=.*#INITIAL_ADMIN_EMAIL=$admin#" -e "s#^TZ=.*#TZ=UTC#" \
	-e "s#^POSTGRES_PASSWORD=.*#POSTGRES_PASSWORD=$(s)#" -e "s#^AUTHENTIK_DB_PASSWORD=.*#AUTHENTIK_DB_PASSWORD=$(s)#" \
	-e "s#^AUTHENTIK_SECRET_KEY=.*#AUTHENTIK_SECRET_KEY=$(s)$(s)#" -e "s#^METRICS_TOKEN=.*#METRICS_TOKEN=$(s)#" \
	-e "s#^AUTHENTIK_API_TOKEN=.*#AUTHENTIK_API_TOKEN=$(s)#" -e "s#^OIDC_CLIENT_SECRET=.*#OIDC_CLIENT_SECRET=$(s)#" \
	-e "s#^AUTHENTIK_ADMIN_PASSWORD=.*#AUTHENTIK_ADMIN_PASSWORD=$(s)#" \
	-e "s#^SMTP_HOST=.*#SMTP_HOST=mailpit#" -e "s#^SMTP_PORT=.*#SMTP_PORT=1025#" -e "s#^SMTP_USE_TLS=.*#SMTP_USE_TLS=false#" \
	env.example >.env
{
	echo "API_BIND=127.0.0.1:18410"
	echo "AUTHENTIK_BIND=127.0.0.1:18411"
} >>.env
chmod 600 .env
# The operator's own change (the guide's "Your own changes"): a mail catcher
# in place of a mail server, for this check.
cat >compose.override.yaml <<'YAML'
services:
  mailpit:
    image: axllent/mailpit:v1.31.5
    environment:
      MP_SMTP_AUTH_ACCEPT_ANY: "true"
      MP_SMTP_AUTH_ALLOW_INSECURE: "true"
    ports:
      - "127.0.0.1:18402:8025"
YAML
export COMPOSE_PROJECT_NAME=ra-s9
log docker compose up -d --wait --wait-timeout 600
log curl -fsS http://127.0.0.1:18410/healthz

# The guide's nginx example, on the host's network, listening on two ports
# for this check (TLS is the operator's) and forwarding to the published ones.
sed -e '0,/listen 443 ssl;/s//listen 127.0.0.1:18400;/' -e 's/listen 443 ssl;/listen 127.0.0.1:18401;/' -e '/http2 on;/d' \
	-e 's#127.0.0.1:8080#127.0.0.1:18410#g' -e 's#127.0.0.1:9000#127.0.0.1:18411#g' \
	"$work/server/deploy/nginx/radioarchive.conf" >"$work/nginx.conf"
log docker run -d --name ra-s9-nginx --network host -v "$work/nginx.conf:/etc/nginx/conf.d/default.conf:ro" nginx:1.28.0 >/dev/null
# TRUSTED_PROXY, as the guide finds it: the gateway the published ports
# arrive through.
gw=$(docker network inspect ra-s9_default -f '{{(index .IPAM.Config 0).Gateway}}')
echo "TRUSTED_PROXY=$gw" >>.env
echo "+ TRUSTED_PROXY=$gw   (in .env)"
log docker compose up -d --wait api
i=0
until curl -fsS http://127.0.0.1:18400/healthz >/dev/null 2>&1; do
	i=$((i + 1)); [ $i -le 60 ] || fail "nginx never forwarded to the API"; sleep 1
done
[ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:18400/metrics)" = 404 ] || fail "nginx doesn't deny /metrics"
echo "ok: the stack is up behind nginx; /metrics answers 404 from outside"
i=0
until curl -fsS http://127.0.0.1:18401/application/o/radioarchive/.well-known/openid-configuration >/dev/null 2>&1; do
	i=$((i + 1)); [ $i -le 150 ] || fail "Authentik's radioarchive provider never appeared"; sleep 2
done
# The blueprints make the provider first and its flows after: wait until the
# radio's sign-in and sign-up flows answer too.
for flow in radioarchive-authentication radioarchive-signup; do
	i=0
	until curl -fsS -o /dev/null "http://127.0.0.1:18401/api/v3/flows/executor/$flow/?query=" 2>/dev/null; do
		i=$((i + 1)); [ $i -le 150 ] || fail "Authentik's $flow flow never appeared"; sleep 2
	done
	curl -fsS -o /dev/null "http://127.0.0.1:18401/if/flow/$flow/" || true
done
# Authentik keeps the provider's flow from before its blueprint bound it for
# a while: give its cache a minute.
sleep 60

# ── The recorder side, the fake recorder in place of trunk-recorder ────────
recorder=s9-north-hill
key=$(docker compose exec -T api radioarchive recorder add "$recorder" | grep -o 'rak_[A-Za-z0-9_-]*')
[ -n "$key" ] || fail "recorder add printed no key"
echo "+ docker compose exec api radioarchive recorder add $recorder   (key printed once; not logged here)"
cd "$work/recorder"
log cp ../release/recorder-compose.yaml compose.yaml
sed -e "s#^SERVER_URL=.*#SERVER_URL=http://127.0.0.1:18400#" -e "s#^CLIENT_KEY=.*#CLIENT_KEY=$key#" ../release/recorder-env.example >.env
chmod 600 .env
# No radio here: trunk-recorder's place is held by a container that does
# nothing, on the host's network, where the fake recorder sends to the client.
cat >compose.override.yaml <<YAML
services:
  trunk-recorder:
    image: busybox:1.37.0
    command: [sleep, infinity]
    network_mode: host
    devices: !reset []
    volumes: !override
      - $work/media:$work/media
  client:
    volumes: !override
      - $work/media:$work/media
      - queue:/var/lib/radioarchive-client/queue
YAML
export COMPOSE_PROJECT_NAME=ra-s9-recorder
log docker compose up -d
i=0
until docker compose logs client 2>&1 | grep -q '"live connection open"'; do
	i=$((i + 1)); [ $i -le 60 ] || { docker compose logs client; fail "the client never connected through nginx"; }; sleep 1
done
echo "ok: the recorder client connected through nginx"

# ── Steps 2 and 3, in a browser ────────────────────────────────────────────
export RA_URL=http://127.0.0.1:18400 RA_MAILPIT_URL=http://127.0.0.1:18402 RA_ADMIN_EMAIL=$admin RA_RECORDER=$recorder \
	RA_FIXTURE_SETUP=$repo/testing/setup/fixture.json RA_FAKERECORDER=$work/fakerecorder \
	RA_FIXTURES=$repo/testing/fakerecorder/testdata/fixtures RA_UDP_ADDR=127.0.0.1:9850 RA_MEDIA=$work/media
cd "$repo/web"
rc=0
if [ -n "${RA_PLAYWRIGHT_IMAGE:-}" ]; then
	docker run --rm --network host --ipc=host -e RA_URL -e RA_MAILPIT_URL -e RA_ADMIN_EMAIL -e RA_RECORDER -e RA_FIXTURE_SETUP \
		-e RA_FAKERECORDER -e RA_FIXTURES -e RA_UDP_ADDR -e RA_MEDIA -e CI -e HOME=/root -v "$repo:$repo" -w "$repo/web" "$RA_PLAYWRIGHT_IMAGE" \
		sh -c 'npm ci --no-fund --no-audit >/dev/null && npx playwright test -c playwright.section9.config.ts' || rc=$?
else
	npx playwright test -c playwright.section9.config.ts || rc=$?
fi
[ "$rc" = 0 ] || fail "steps 2 and 3 in the browser"
cd "$work/server"
export COMPOSE_PROJECT_NAME=ra-s9
log docker compose exec -T db psql -U radioarchive -d radioarchive -tAc "SELECT count(*) FROM calls WHERE recording_path IS NOT NULL"
echo "ok: section 9 (steps 1-3) on an install from the release's files, behind the nginx example"
