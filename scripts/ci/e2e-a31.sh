#!/bin/sh
# A31 (SPEC.md section 8; R89, ADR 0041) end to end with the real images:
# the backup service's backup of the compose stack, once A1, A10, sign-in
# and the module have put calls, accounts, saved settings and audit entries
# in it, restored with the backup's own restore into an empty install,
# has the setup, the audit log and the settings and no call rows, and the
# setup export sits beside the dumps.
#
#   sh scripts/ci/e2e-a31.sh
#
# Expects the compose stack (compose.yaml) to be up with its .env, and its
# backup folder at ./backups. The empty install is a second compose project
# of the same files, its database alone, on a port of its own; it's removed
# afterwards.
set -eu

fail() { echo "FAIL: $*" >&2; exit 1; }
psql() { docker compose exec -T db psql -U radioarchive -d radioarchive -tAc "$1"; }
empty=ra-a31-restore
cleanup() { DB_PORT=55431 docker compose -p "$empty" down --volumes >/dev/null 2>&1 || true; }
trap cleanup EXIT

# The setup export is written within a minute of a change (ADR 0041): wait
# until it has the fixture's system.
i=0
until docker compose exec -T api radioarchive healthcheck >/dev/null 2>&1 &&
	docker compose exec -T backup cat /setup-export/setup.json 2>/dev/null | grep -q '"name": "Example network"'; do
	i=$((i + 1))
	[ "$i" -le 90 ] || fail "the API never wrote the setup export with the fixture's system"
	sleep 1
done

calls=$(psql "SELECT count(*) FROM calls")
audit=$(psql "SELECT count(*) FROM audit_log")
[ "$calls" -gt 0 ] || fail "the stack has no calls to leave out"
[ "$audit" -gt 0 ] || fail "the stack has no audit entries to keep"
echo "the stack: $calls calls, $audit audit entries"

docker compose exec -T backup sh /usr/local/bin/radioarchive-backup now
status=$(docker compose exec -T backup cat /status/status.json)
echo "$status"
echo "$status" | grep -q '"last_run_ok": true' || fail "the backup didn't succeed"
# The night is the backup's date, in its TZ.
night=$(echo "$status" | sed -n 's/^ *"last_good_night": "\([0-9-]*\)",$/\1/p')
[ -n "$night" ] || fail "the status names no good night"
for f in radioarchive.dump authentik.dump counts.tsv setup.json check; do
	[ -s "backups/$night.$f" ] || fail "backups/$night.$f is missing"
done
[ "$(cat "backups/$night.check")" = ok ] || fail "the check says $(cat "backups/$night.check")"
grep -q '"name": "Example network"' "backups/$night.setup.json" || fail "the setup export beside the dumps has no fixture system"
tab=$(printf '\t')
for t in calls transmissions rejected_calls; do
	grep -q "^radioarchive${tab}public.$t${tab}0\$" "backups/$night.counts.tsv" || fail "the counts say $t's rows were kept"
done

# The admin API reports it, and so do the metrics (ADR 0040, 0041).
token=$(sed -n 's/^METRICS_TOKEN=//p' .env)
curl -fsS -H "Authorization: Bearer $token" "http://127.0.0.1:${API_PORT:-8080}/metrics" | grep -qx 'radioarchive_backup_failing 0' ||
	fail "/metrics doesn't say the backup is good"

# An empty install: the same files, a project of its own, its database alone.
DB_PORT=55431 docker compose -p "$empty" up --detach --wait db
DB_PORT=55431 docker compose -p "$empty" run --rm --no-deps backup restore "$night"
rpsql() { DB_PORT=55431 docker compose -p "$empty" exec -T db psql -U radioarchive -d radioarchive -tAc "$1"; }
for q in "systems:SELECT count(*) FROM systems" "talkgroups:SELECT count(*) FROM talkgroups" "units:SELECT count(*) FROM units" \
	"recorders:SELECT count(*) FROM recorders" "settings:SELECT count(*) FROM settings" "audit log:SELECT count(*) FROM audit_log" \
	"accounts:SELECT count(*) FROM accounts"; do
	n=$(rpsql "${q#*:}")
	[ "$n" -gt 0 ] || fail "the restore has no ${q%%:*}"
	echo "restored ${q%%:*}: $n"
done
[ "$(rpsql "SELECT count(*) FROM audit_log")" = "$audit" ] || fail "the restore's audit log isn't the stack's"
for t in calls transmissions rejected_calls; do
	n=$(rpsql "SELECT count(*) FROM $t")
	[ "$n" = 0 ] || fail "the restore has $n rows of $t"
done
n=$(DB_PORT=55431 docker compose -p "$empty" exec -T db psql -U radioarchive -d authentik -tAc "SELECT count(*) FROM authentik_core_user")
[ "$n" -gt 0 ] || fail "Authentik's restore has no users"
echo "ok: A31, the backup restored into an empty install has the setup, the audit log, the settings and accounts, and no call rows"
