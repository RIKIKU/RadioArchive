#!/bin/sh
# The automatic upgrade (SPEC.md R87, R106; ADR 0044) end to end, against
# stand-ins for GitHub (a local web server with releases, commits and
# attestations), its CLI (which accepts every attestation) and GHCR (a local
# registry), on a real install made from the release's files:
#   - the call rule, with fewer and with 20 or more calls before;
#   - a run while another app holds the host-wide lock is skipped, exit 0;
#   - 0.1.0 upgrades to 0.1.1, which passes the health check;
#   - 0.1.2, whose API never runs as 0.1.2, is rolled back to 0.1.1 and
#     marked failed, and the API says so to admins and in /metrics.
#
#   sh scripts/ci/e2e-auto-upgrade.sh
#
# Needs Docker, curl, jq, flock and python3. Ports: 127.0.0.1:5000 (the
# registry), 8765 (the stand-in GitHub), 18190 and 19190 (the install).
set -eu
repo=$(cd "$(dirname "$0")/../.." && pwd)
work=$repo/.e2e-auto-upgrade
reg=127.0.0.1:5000
gh_port=8765
fail() { echo "FAIL: $*" >&2; exit 1; }

cleanup() {
	(cd "$work/install" 2>/dev/null && docker compose -p ra-au down --volumes >/dev/null 2>&1) || true
	docker rm -f ra-au-registry >/dev/null 2>&1 || true
	[ -z "${ghpid:-}" ] || kill "$ghpid" 2>/dev/null || true
}
trap cleanup EXIT
rm -rf "$work" && mkdir -p "$work/install" "$work/gh/assets" "$work/bin"

# ── The call rule, the script's functions alone ───────────────────────────
bash -eu -s "$repo/deploy/auto-upgrade/auto-upgrade.sh" <<'BASH' || fail "the call rule"
# shellcheck disable=SC1090
. "$1"
CALLS_BEFORE_MIN=20 CALL_WINDOW=1 CALL_POLL=1 RESTARTED=$(now)
calls_since() { echo "${STUB_CALLS:-0}"; }
CALLS_BEFORE=19
call_rule
case $CALL_NOTE in *skipped*) ;; *) exit 1 ;; esac
CALLS_BEFORE=20 STUB_CALLS=1
call_rule
[ "$CALL_NOTE" = "a call arrived after the restart" ]
RESTARTED=$(($(now) - 59)) CALLS_BEFORE=25 STUB_CALLS=0
if call_rule; then exit 1; fi
[ "$REASON" = "no call arrived within 1 minutes of the restart, with 25 in the 2 hours before" ]
BASH
echo "ok: the call rule: skipped under 20 calls before; with 20 or more, a call after the restart passes and none fails"

# ── Images, in a local registry ────────────────────────────────────────────
docker run -d --name ra-au-registry -p "$reg:5000" registry:2.8.3 >/dev/null
for v in 0.1.0 0.1.1; do
	docker build -q --build-arg VERSION=$v -t "$reg/radioarchive:$v" "$repo" >/dev/null
	docker push -q "$reg/radioarchive:$v" >/dev/null
done
docker tag "$reg/radioarchive:0.1.0" "$reg/radioarchive:0.1" && docker push -q "$reg/radioarchive:0.1" >/dev/null

# ── The stand-in GitHub ────────────────────────────────────────────────────
# release <version>: its files, in the stand-in's assets, named by the
# local registry, and the release listed.
release() {
	sh "$repo/scripts/ci/release-assets.sh" "$1" "$work/gh/assets/v$1" >/dev/null
	sed -i "s#ghcr.io/rikiku/#$reg/#" "$work/gh/assets/v$1/compose.yaml"
	printf '{"sha":"%s"}' "$(printf '%040d' 0 | tr 0 a)" >"$work/gh/commits/v$1"
}
releases() { # <version>...: the releases the stand-in lists
	{
		printf '['
		sep=""
		for v; do
			printf '%s{"tag_name":"v%s","draft":false,"prerelease":false,"body":"","assets":[' "$sep" "$v"
			printf '{"name":"compose.yaml","browser_download_url":"http://127.0.0.1:%s/assets/v%s/compose.yaml"},' "$gh_port" "$v"
			printf '{"name":"radioarchive-config.tar.gz","browser_download_url":"http://127.0.0.1:%s/assets/v%s/radioarchive-config.tar.gz"}]}' "$gh_port" "$v"
			sep=,
		done
		printf ']'
	} >"$work/gh/releases"
}
mkdir -p "$work/gh/commits"
cat >"$work/gh.py" <<'PY'
import http.server, sys, os
root = sys.argv[2]
class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k): super().__init__(*a, directory=root, **k)
    def do_GET(self):
        if self.path.startswith("/attestations/"):
            b = b'{"attestations":[{"bundle":{"made_up":true}}]}'
            self.send_response(200); self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b); return
        self.path = self.path.split("?")[0]
        return super().do_GET()
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
python3 "$work/gh.py" "$gh_port" "$work/gh" &
ghpid=$!
# The stand-in CLI: every attestation verifies; what it was asked is logged.
cat >"$work/bin/gh" <<'SH'
#!/bin/sh
echo "$*" >>"$(dirname "$0")/gh.log"
[ "$1 $2" = "attestation verify" ]
SH
chmod 755 "$work/bin/gh"

# ── An install of 0.1.0, from its release's files ──────────────────────────
release 0.1.0
releases 0.1.0
cd "$work/install"
cp "$work/gh/assets/v0.1.0/compose.yaml" "$work/gh/assets/v0.1.0/env.example" .
tar -xzf "$work/gh/assets/v0.1.0/radioarchive-config.tar.gz"
s() { head -c 48 /dev/urandom | base64 | tr -d '/+=\n' | cut -c1-32; }
sed -e "s#^APP_URL=.*#APP_URL=http://127.0.0.1:18190#" -e "s#^AUTHENTIK_URL=.*#AUTHENTIK_URL=http://127.0.0.1:19190#" \
	-e "s#^POSTGRES_PASSWORD=.*#POSTGRES_PASSWORD=$(s)#" -e "s#^AUTHENTIK_DB_PASSWORD=.*#AUTHENTIK_DB_PASSWORD=$(s)#" \
	-e "s#^AUTHENTIK_SECRET_KEY=.*#AUTHENTIK_SECRET_KEY=$(s)$(s)#" -e "s#^METRICS_TOKEN=.*#METRICS_TOKEN=$(s)#" \
	-e "s#^AUTHENTIK_API_TOKEN=.*#AUTHENTIK_API_TOKEN=$(s)#" -e "s#^OIDC_CLIENT_SECRET=.*#OIDC_CLIENT_SECRET=$(s)#" \
	-e "s#^AUTHENTIK_ADMIN_PASSWORD=.*#AUTHENTIK_ADMIN_PASSWORD=$(s)#" env.example >.env
{
	echo "API_BIND=127.0.0.1:18190"
	echo "AUTHENTIK_BIND=127.0.0.1:19190"
	echo "BACKUP_UID=$(id -u)"
	echo "BACKUP_GID=$(id -g)"
	echo "AUTO_UPGRADE_API=http://127.0.0.1:$gh_port"
	echo "AUTO_UPGRADE_IMAGE_PREFIX=$reg/"
	echo "AUTO_UPGRADE_GH=$work/bin/gh"
	echo "AUTO_UPGRADE_START_TIMEOUT=90"
	echo "AUTO_UPGRADE_SETTLE=5"
} >>.env
# The whole stack, Authentik too: a backup needs both databases' tables.
export COMPOSE_PROJECT_NAME=ra-au RA_TEST_JQ=1 RA_TEST_HOST_LOCK=$work/archive-upgrade.lock
docker compose up -d --wait --wait-timeout 420 >/dev/null
au() { bash deploy/auto-upgrade/auto-upgrade.sh "$@"; }
au enable --no-schedule
docker compose up -d --wait --wait-timeout 420 >/dev/null
[ "$(docker compose exec -T api radioarchive version)" = "radioarchive 0.1.0" ] || fail "the install doesn't run 0.1.0"
token=$(sed -n 's/^METRICS_TOKEN=//p' .env)
metric() { curl -fsS -H "Authorization: Bearer $token" http://127.0.0.1:18190/metrics | sed -n "s/^$1 //p"; }
st() { jq -r "$1" auto-upgrade/status/status.json; }

# ── The host-wide lock ─────────────────────────────────────────────────────
release 0.1.1
releases 0.1.0 0.1.1
docker tag "$reg/radioarchive:0.1.1" "$reg/radioarchive:0.1" && docker push -q "$reg/radioarchive:0.1" >/dev/null
out=$(flock "$RA_TEST_HOST_LOCK" bash deploy/auto-upgrade/auto-upgrade.sh run)
echo "$out" | grep -q "another upgrade is running" || fail "a run beside another upgrade wasn't skipped: $out"
[ "$(st .last_check_result)" = skipped ] || fail "the skip wasn't recorded"
[ "$(docker compose exec -T api radioarchive version)" = "radioarchive 0.1.0" ] || fail "a skipped run upgraded"
echo "ok: a run while another app holds $RA_TEST_HOST_LOCK is skipped, exit 0"

# ── 0.1.0 to 0.1.1 ─────────────────────────────────────────────────────────
au run
[ "$(st .last_check_result)" = upgraded ] || fail "0.1.1 wasn't installed: $(st .last_check_note)"
[ "$(docker compose exec -T api radioarchive version)" = "radioarchive 0.1.1" ] || fail "the API doesn't run 0.1.1"
grep -q "image: $reg/radioarchive:0.1\$" compose.yaml || fail "the installed compose.yaml isn't 0.1.1's"
grep -q -- "--signer-workflow RIKIKU/RadioArchive/.github/workflows/release.yml" "$work/bin/gh.log" || fail "the attestations weren't verified as release.yml's"
ls backups/pre-upgrade/*-0.1.0-to-0.1.1/*.radioarchive.dump >/dev/null || fail "no pre-upgrade backup kept"
case $(st .last_check_note) in *"the call check was skipped"*) ;; *) fail "the call rule's note: $(st .last_check_note)" ;; esac
echo "ok: 0.1.0 upgraded to 0.1.1, verified, backed up first, healthy ($(st .last_check_note))"
[ "$(metric radioarchive_auto_upgrade_failing)" = 0 ] || fail "/metrics says the upgrade is failing"

# ── 0.1.2, which never runs as 0.1.2: rolled back ──────────────────────────
release 0.1.2
releases 0.1.0 0.1.1 0.1.2
docker tag "$reg/radioarchive:0.1.1" "$reg/radioarchive:0.1.2" && docker push -q "$reg/radioarchive:0.1.2" >/dev/null
docker tag "$reg/radioarchive:0.1.1" "$reg/radioarchive:0.1" && docker push -q "$reg/radioarchive:0.1" >/dev/null
if au run; then fail "a failed upgrade's run exited 0"; fi
[ "$(st .last_check_result)" = rolled-back ] || fail "0.1.2 wasn't rolled back: $(st .last_check_result) $(st .last_check_note)"
[ "$(st '.failed_versions | index("0.1.2") != null')" = true ] || fail "0.1.2 isn't marked failed"
[ "$(docker compose exec -T api radioarchive version)" = "radioarchive 0.1.1" ] || fail "the API isn't back on 0.1.1"
grep -q "image: $reg/radioarchive:0.1\$" compose.yaml || fail "the files aren't 0.1.1's again"
echo "ok: 0.1.2 failed its health check ($(st .last_failure.reason)) and was rolled back to 0.1.1"
au run >/dev/null || true
[ "$(st .last_check_result)" = skipped ] || fail "0.1.2 was tried again"
[ "$(metric radioarchive_auto_upgrade_failing)" = 1 ] || fail "/metrics doesn't say the upgrade failed"
echo "ok: 0.1.2 isn't tried again, and /metrics says the upgrade failed"
au clear >/dev/null
[ "$(metric radioarchive_auto_upgrade_failing)" = 0 ] || fail "clear didn't clear it"
echo "ok: clear clears it"
