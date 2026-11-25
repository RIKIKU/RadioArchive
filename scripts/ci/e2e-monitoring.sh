#!/bin/sh
# RadioArchive's monitoring files in PagerArchive's monitoring stack (SPEC.md
# R90, R91; ADR 0042), end to end, as docs/deploy.md's "Monitoring" sets
# them up: the stack from PagerArchive's release, RadioArchive's scrape,
# alert rules and dashboards in its include folders, and the API joined to
# its network by an override. Then:
#   - every RadioArchive rule is provisioned, and evaluates without error;
#   - Prometheus has the API's metrics, scraped with the token;
#   - the three dashboards load, and every panel's query runs;
#   - "Recorder disconnected" fires once a recorder's client has been gone
#     for 2 minutes, and clears when it's back.
#
#   sh scripts/ci/e2e-monitoring.sh <client image>
#
# Expects the compose stack (compose.yaml, with METRICS_TOKEN in .env) to
# be up. Leaves the monitoring stack and the override behind for the logs;
# the job removes them.
set -eu

image=${1:?usage: e2e-monitoring.sh <client image>}
# The release of PagerArchive whose monitoring stack is checked: the first
# with include folders (its #108). Renovate moves it.
PA_RELEASE=${PA_RELEASE:-v1.23.0}
api_port=${API_PORT:-8080}
grafana=127.0.0.1:${GRAFANA_PORT:-13000}
udp_port=${E2E_UDP_PORT:-19852}
mon=$(pwd)/.e2e-monitoring
client=radioarchive-e2e-client-mon-$$
fail() { echo "FAIL: $*" >&2; exit 1; }
# The monitoring stack's compose project: its own name (pagerarchive-monitoring)
# unless MON_PROJECT names another, for a host where PagerArchive's runs.
monc() { (cd "$mon/deploy/monitoring" && docker compose ${MON_PROJECT:+-p "$MON_PROJECT"} "$@"); }

cleanup() { docker rm -f "$client" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# ── PagerArchive's stack, from its release ─────────────────────────────────
rm -rf "$mon" && mkdir -p "$mon"
base=https://github.com/RIKIKU/PagerArchive/releases/download/$PA_RELEASE
curl -fsSL "$base/pagerarchive-config.tar.gz" | tar -xz -C "$mon"
cd "$mon/deploy/monitoring"
curl -fsSL -o compose.yaml "$base/monitoring-compose.yaml"
curl -fsSL -o .env "$base/monitoring-env.example"
password=$(head -c 24 /dev/urandom | base64 | tr -d '/+=')
{
	echo "GRAFANA_ADMIN_PASSWORD=$password"
	echo "PA_METRICS_TOKEN=no-pagerarchive-here-0123456789"
	echo "GRAFANA_BIND=$grafana"
	echo "PA_AUTHENTIK_SERVER_ADDRESS="
	echo "PA_AUTHENTIK_WORKER_ADDRESS="
} >>.env

# RadioArchive's files in its include folders (docs/deploy.md).
repo=$(cd ../../.. && pwd)
token=$(sed -n 's/^METRICS_TOKEN=//p' "$repo/.env")
[ -n "$token" ] || fail "the stack's .env has no METRICS_TOKEN"
cp "$repo/deploy/monitoring/alloy.d/radioarchive.alloy" include/alloy.d/
printf '%s' "$token" >include/alloy.d/radioarchive-metrics-token
cp "$repo/deploy/monitoring/alerts.d/radioarchive.yaml" include/alerts.d/
cp -R "$repo/deploy/monitoring/dashboards.d/RadioArchive" include/dashboards.d/
chmod -R a+rX include
chmod 600 include/alloy.d/radioarchive-metrics-token
monc up --detach --wait --wait-timeout 300

# The API on the stack's network, as its override puts it.
cd "$repo"
cat >compose.override.yaml <<'YAML'
services:
  api:
    networks:
      default:
      monitoring:
        aliases: [radioarchive]
networks:
  monitoring:
    external: true
YAML
docker compose up --detach --wait api

gf() { curl -fsS -u "admin:$password" "http://$grafana$1"; }
i=0
until gf /api/health >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -le 300 ] || fail "Grafana never answered"
	sleep 1
done
monc logs alloy grafana 2>&1 | grep 'include:' || true

# ── The rules ──────────────────────────────────────────────────────────────
uids=$(sed -n 's/^ *- uid: \(ra-[a-z-]*\)$/\1/p' deploy/monitoring/alerts.d/radioarchive.yaml)
[ "$(echo "$uids" | wc -l)" -ge 13 ] || fail "the rule file has $(echo "$uids" | wc -l) rules"
for u in $uids; do
	code=$(curl -s -o /dev/null -w '%{http_code}' -u "admin:$password" "http://$grafana/api/v1/provisioning/alert-rules/$u")
	[ "$code" = 200 ] || fail "rule $u isn't provisioned (HTTP $code)"
done
echo "ok: $(echo "$uids" | wc -l) rules provisioned"

# ── The scrape ─────────────────────────────────────────────────────────────
q() { curl -fsS -u "admin:$password" -G "http://$grafana/api/datasources/proxy/uid/prometheus/api/v1/query" --data-urlencode "query=$1"; }
i=0
until [ "$(q 'max(up{job="radioarchive"})' | jq -r '.data.result[0].value[1] // empty')" = 1 ]; do
	i=$((i + 1))
	[ "$i" -le 120 ] || fail "Prometheus never had the API's metrics: is it scraped with the token?"
	sleep 2
done
q 'radioarchive_build_info' | jq -e '.data.result | length == 1' >/dev/null || fail "no build info"
echo "ok: Prometheus has RadioArchive's metrics"

# ── The dashboards ─────────────────────────────────────────────────────────
for d in deploy/monitoring/dashboards.d/RadioArchive/*.json; do
	uid=$(jq -r .uid "$d")
	gf "/api/dashboards/uid/$uid" | jq -e '.dashboard.panels | length > 0' >/dev/null || fail "dashboard $uid isn't loaded"
	jq -r '.panels[].targets[].expr' "$d" | while read -r expr; do
		[ "$(q "$expr" | jq -r .status)" = success ] || fail "a query on $uid fails: $expr"
	done
done
echo "ok: the dashboards load and every panel's query runs"

# ── "Recorder disconnected" fires and clears ───────────────────────────────
name="e2e-monitoring-$(date +%s)"
key=$(docker compose exec -T api radioarchive recorder add "$name" | grep -o 'rak_[A-Za-z0-9_-]*')
up_client() {
	docker run -d --name "$client" --network host -e SERVER_URL="http://127.0.0.1:$api_port" -e CLIENT_KEY="$key" \
		-e UDP_ADDR="127.0.0.1:$udp_port" --tmpfs /var/lib/radioarchive-client/queue:mode=1777 "$image" >/dev/null
}
alerting() {
	gf /api/prometheus/grafana/api/v1/alerts |
		jq -e --arg r "$name" 'any(.data.alerts[]; .labels.alertname == "Recorder disconnected" and .labels.recorder == $r and .state == "Alerting")' >/dev/null
}
up_client
i=0
until [ "$(q "max(radioarchive_recorder_connected{recorder=\"$name\"})" | jq -r '.data.result[0].value[1] // empty')" = 1 ]; do
	i=$((i + 1))
	[ "$i" -le 60 ] || fail "the recorder never showed as connected"
	sleep 2
done
docker rm -f "$client" >/dev/null
echo "the recorder's client stopped at $(date -u +%T)"
i=0
until alerting; do
	i=$((i + 1))
	[ "$i" -le 100 ] || fail "\"Recorder disconnected\" never fired"
	sleep 3
done
echo "ok: \"Recorder disconnected\" fired at $(date -u +%T)"
up_client
i=0
while alerting; do
	i=$((i + 1))
	[ "$i" -le 60 ] || fail "\"Recorder disconnected\" never cleared"
	sleep 3
done
echo "ok: it cleared once the client was back, at $(date -u +%T)"

# Every RadioArchive rule evaluates without an error.
# Grafana's answer holds the rules' descriptions with raw line breaks in
# them, which jq refuses: control characters go first.
gf /api/prometheus/grafana/api/v1/rules | tr -d '\000-\037' >"$mon/rules.json"
bad=$(jq -r '.data.groups[]? | select(.name == "RadioArchive") | .rules[]? | select(.health != "ok") | .name + ": " + (.lastError // .health)' "$mon/rules.json") ||
	fail "Grafana's rules didn't parse: $(head -c 400 "$mon/rules.json")"
jq -e '[.data.groups[]? | select(.name == "RadioArchive") | .rules[]?] | length >= 13' "$mon/rules.json" >/dev/null ||
	fail "Grafana doesn't list RadioArchive's rules: $(head -c 400 "$mon/rules.json")"
[ -z "$bad" ] || fail "rules in error: $bad"
echo "ok: every RadioArchive rule evaluates"
