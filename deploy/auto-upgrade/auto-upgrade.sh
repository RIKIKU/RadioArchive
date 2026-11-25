#!/usr/bin/env bash
# Automatic upgrades (SPEC.md R87, R106; ADR 0044; docs/deploy.md, "Automatic
# upgrades"): RadioArchive's adapted copy of PagerArchive's
# deploy/auto-upgrade/auto-upgrade.sh (its ADR 0030). Off unless switched on.
# Part of the release's archive, so it's in deploy/auto-upgrade/ next to
# compose.yaml, and runs on the host, as the user who runs `docker compose`
# for this install, from cron (or a systemd user timer):
#
#   deploy/auto-upgrade/auto-upgrade.sh enable [--systemd]   switch it on: hourly, from
#                                    cron (or a systemd user timer; --no-schedule: neither)
#   deploy/auto-upgrade/auto-upgrade.sh disable              switch it off
#   deploy/auto-upgrade/auto-upgrade.sh run                  one check, now
#   deploy/auto-upgrade/auto-upgrade.sh status               what it last did
#   deploy/auto-upgrade/auto-upgrade.sh verify [vX.Y.Z]      check a release's files
#                                                            and image, nothing more
#   deploy/auto-upgrade/auto-upgrade.sh retry X.Y.Z          try a release marked failed again
#   deploy/auto-upgrade/auto-upgrade.sh clear                after a run that stopped part way
#
# Each run: the newest full release (not a draft, not a pre-release) from
# GitHub's releases API. If it's newer than what's running, in the same major
# version, not marked failed and its notes don't start with "Upgrade steps":
#   1. verify: its files' and the server image's attestations, with a pinned
#      GitHub CLI this script fetches and checks by its checksum
#   2. back up: the backup service's `now` (the same checked backup as the
#      nightly one), kept aside, plus copies of compose.yaml, .env,
#      compose.override.yaml and deploy/
#   3. upgrade: the verified files installed, pull, up -d
#   4. health check: containers running, the API answering and running the new
#      version, your own URLs, and the call rule (R87): if at least 20 calls
#      arrived in the 2 hours before, a call must arrive within 30 minutes
#   5. on failure: the previous release's files and image back, its database
#      restored from step 2's backup if the new version changed it, and the
#      release marked failed so it isn't tried again
#
# Recorder clients and the plugin are upgraded by hand (R86): this script
# upgrades the server only. Listener sockets drop while the API restarts and
# reconnect with their last event ID (R87).
#
# What it did goes to auto-upgrade/status/status.json, which the API reads
# (for the admin pages and /metrics), and one JSON line per step to
# auto-upgrade/status/auto-upgrade.log. Neither ever holds a talkgroup's or a
# unit's name: only versions, times, counts and fixed text.
#
# Never two runs at once (a lock), and never while a backup is running. Nor
# while another app on the host upgrades (R106): every run holds the
# host-wide lock /run/lock/archive-upgrade.lock (flock -n), which
# PagerArchive's upgrade script takes too, from its check to its rollback.
# Held, the run is skipped ("another upgrade is running") and tried next hour.
# Each app backs up and rolls back only its own database.

RA_GH_VERSION=2.103.3
RA_GH_SHA256=4c806978ffce1cec70853302a56d70ac6da4f19bae7adca4e1a8ab00633dd91d
RA_JQ_VERSION=1.8.2
RA_JQ_SHA256=b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f
RA_REPO=RIKIKU/RadioArchive

set -u
umask 022

# ── Settings ───────────────────────────────────────────────────────────────
# setting <NAME> <default>: the environment's value, else .env's, else the default.
setting() {
	local v
	if [ -n "${!1+x}" ]; then
		printf '%s' "${!1}"
		return
	fi
	v=$(sed -n "s/^$1=//p" "$RA_DIR/.env" 2>/dev/null | tail -n 1)
	v=${v%\"}
	v=${v#\"}
	printf '%s' "${v:-$2}"
}

load_settings() {
	HOME_AU=$(setting AUTO_UPGRADE_HOME "$RA_DIR/auto-upgrade")
	case $HOME_AU in /*) ;; *) HOME_AU=$RA_DIR/$HOME_AU ;; esac
	STATUS_DIR=$HOME_AU/status
	STATUS=$STATUS_DIR/status.json
	LOG=$STATUS_DIR/auto-upgrade.log
	RELEASES=$HOME_AU/releases.json
	API=$(setting AUTO_UPGRADE_API "https://api.github.com/repos/$RA_REPO")
	TOKEN=$(setting AUTO_UPGRADE_GITHUB_TOKEN "")
	PREFIX=$(setting AUTO_UPGRADE_IMAGE_PREFIX "ghcr.io/rikiku/")
	CALL_WINDOW=$(setting AUTO_UPGRADE_CALL_WINDOW 30)
	CALLS_BEFORE_MIN=$(setting AUTO_UPGRADE_CALLS_BEFORE 20)
	START_TIMEOUT=$(setting AUTO_UPGRADE_START_TIMEOUT 600)
	SETTLE=$(setting AUTO_UPGRADE_SETTLE 20)
	CHECK_URLS=$(setting AUTO_UPGRADE_CHECK_URLS "")
	KEEP=$(setting AUTO_UPGRADE_KEEP 3)
	GH_OVERRIDE=$(setting AUTO_UPGRADE_GH "")
	for n in CALL_WINDOW CALLS_BEFORE_MIN START_TIMEOUT SETTLE KEEP; do
		case ${!n} in '' | *[!0-9]*) echo "auto-upgrade: a number setting ($n) must be a whole number" >&2 && exit 2 ;; esac
	done
}

# ── Output: the log, the status for the API ────────────────────────────────
now() { date -u +%s; }

json_str() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g' | tr -d '\000-\037'; }

# event <level> <step> <result> [reason]: one JSON line per step. The reason
# is fixed text, versions and counts.
event() {
	local line
	line=$(printf '{"time":"%s","level":"%s","step":"%s","result":"%s","from":"%s","to":"%s","reason":"%s","seconds":%s}' \
		"$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2" "$3" "$(json_str "${FROM:-}")" "$(json_str "${TO:-}")" \
		"$(json_str "${4:-}")" "$(($(now) - ${STEP_START:-$(now)}))")
	mkdir -p "$STATUS_DIR"
	printf '%s\n' "$line" >>"$LOG"
	echo "auto-upgrade: $2 $3${4:+: $4}"
	STEP_START=$(now)
}

# rotate_log: past 1 MB the log moves aside, once.
rotate_log() {
	[ -f "$LOG" ] || return 0
	[ "$(wc -c <"$LOG")" -lt 1048576 ] || mv -f "$LOG" "$LOG.1"
}

# status <jq filter> [jq args...]: changes the status file atomically.
status() {
	local f=$1
	shift
	mkdir -p "$STATUS_DIR"
	chmod 755 "$HOME_AU" "$STATUS_DIR" 2>/dev/null
	[ -s "$STATUS" ] || echo '{"enabled": false, "failures": 0, "rollbacks": 0, "failed_versions": [], "history": []}' >"$STATUS"
	if "$JQ" "$@" "$f" "$STATUS" >"$STATUS.partial"; then
		chmod 644 "$STATUS.partial" && mv -f "$STATUS.partial" "$STATUS"
	else
		rm -f "$STATUS.partial"
		echo "auto-upgrade: couldn't write the status" >&2
	fi
}

status_get() { "$JQ" -r "$1 // empty" "$STATUS" 2>/dev/null; }

# A run's last_check_at is when it started, which is when it checked.
CHECKED_AT='.last_check_at = (($ENV.AU_RUN_START // "") | if . == "" then ($t|tonumber) else tonumber end)'

# DONE: a run has ended: idle, with no start and no target.
DONE='.state = "idle" | .running_since = 0 | .target_version = ""'

# checked <result> <note>: the end of a run that changed nothing.
checked() {
	status "$CHECKED_AT | $DONE"' | .last_check_result = $r | .last_check_note = $n
		| .running_version = $v | .latest_version = $l' \
		--arg t "$(now)" --arg r "$1" --arg n "$2" --arg v "${FROM:-}" --arg l "${TO:-}"
}

# ── Tools ──────────────────────────────────────────────────────────────────
dc() { (cd "$RA_DIR" && docker compose "$@"); }

sha() { sha256sum "$1" | cut -d' ' -f1; }

curl_api() {
	if [ -n "$TOKEN" ]; then
		curl -fsSL --max-time 60 -H "Accept: application/vnd.github+json" -H "Authorization: Bearer $TOKEN" "$API$1"
	else
		curl -fsSL --max-time 60 -H "Accept: application/vnd.github+json" "$API$1"
	fi
}

# fetch_pinned <url> <file> <sha256>: downloaded once, checked on every run.
fetch_pinned() {
	if [ -f "$2" ] && [ "$(sha "$2")" = "$3" ]; then
		return 0
	fi
	rm -f "$2"
	curl -fsSL --max-time 300 -o "$2.partial" "$1" || return 1
	if [ "$(sha "$2.partial")" != "$3" ]; then
		rm -f "$2.partial"
		return 1
	fi
	mv -f "$2.partial" "$2"
}

# tools: jq, and the GitHub CLI for `gh attestation verify`, each a pinned
# release checked against its published checksum. The CLI needs no sign-in:
# the attestations come from GitHub's API, which needs no token for a public
# repository, and are verified offline against Sigstore's public roots.
# AUTO_UPGRADE_GH names another CLI (tests).
tools() {
	local bin=$HOME_AU/bin
	mkdir -p "$bin"
	JQ=$bin/jq-$RA_JQ_VERSION
	if command -v jq >/dev/null && [ -n "${RA_TEST_JQ:-}" ]; then
		JQ=$(command -v jq)
	else
		fetch_pinned "https://github.com/jqlang/jq/releases/download/jq-$RA_JQ_VERSION/jq-linux-amd64" "$JQ" "$RA_JQ_SHA256" || return 1
		chmod 755 "$JQ"
	fi
	if [ -n "$GH_OVERRIDE" ]; then
		GH=$GH_OVERRIDE
		[ -x "$GH" ] || return 1
		return 0
	fi
	local tgz=$bin/gh_${RA_GH_VERSION}_linux_amd64.tar.gz
	fetch_pinned "https://github.com/cli/cli/releases/download/v$RA_GH_VERSION/gh_${RA_GH_VERSION}_linux_amd64.tar.gz" "$tgz" "$RA_GH_SHA256" || return 1
	rm -rf "$bin/gh"
	mkdir -p "$bin/gh"
	tar -xzf "$tgz" -C "$bin/gh" --strip-components=2 "gh_${RA_GH_VERSION}_linux_amd64/bin/gh" || return 1
	GH=$bin/gh/gh
	export GH_CONFIG_DIR=$bin/gh-config GH_NO_UPDATE_NOTIFIER=1 GH_PROMPT_DISABLED=1
	unset GH_TOKEN GITHUB_TOKEN
}

# ── Versions ───────────────────────────────────────────────────────────────
# Every version and tag taken in is checked whole before it reaches a path
# or a command: a release is X.Y.Z and its tag vX.Y.Z, nothing else.
is_release() { [[ $1 =~ ^[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$ ]]; }
is_tag() { [[ $1 =~ ^v[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$ ]]; }
is_commit() { [[ $1 =~ ^[0-9a-f]{40}$ ]]; }
newer() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n 1)" = "$1" ]; }
minor_of() { printf '%s' "${1%.*}"; }

running_version() {
	dc exec -T api /usr/local/bin/radioarchive version 2>/dev/null | sed -n 's/^radioarchive //p' | tr -d '\r' | head -n 1
}

# latest_release: the newest full release's tag, from the list, which it
# keeps in a file for release_field and asset_url.
latest_release() {
	local tag
	curl_api "/releases?per_page=50" >"$RELEASES" || return 1
	tag=$("$JQ" -r '[.[] | select(.draft == false and .prerelease == false)
		| select(.tag_name | test("\\Av[0-9]+\\.[0-9]+\\.[0-9]+\\z"))]
		| sort_by(.tag_name | ltrimstr("v") | split(".") | map(tonumber)) | last // empty
		| .tag_name' "$RELEASES") || return 1
	[ -z "$tag" ] || is_tag "$tag" || return 1
	printf '%s\n' "$tag"
}

# tag_commit <tag>: the commit the release's tag names.
tag_commit() {
	local c
	c=$(curl_api "/commits/$1" 2>/dev/null | "$JQ" -r '.sha // empty' 2>/dev/null)
	is_commit "$c" || return 1
	printf '%s' "$c"
}

release_field() { "$JQ" -r --arg t "$1" ".[] | select(.tag_name == \$t) | $2" "$RELEASES"; }
asset_url() { release_field "$1" ".assets[] | select(.name == \"$2\") | .browser_download_url"; }

# ── Verify ─────────────────────────────────────────────────────────────────
# attested <subject> <digest> <name>: the subject's attestations from
# GitHub's API, verified by the pinned CLI: signed by this repository's
# .github/workflows/release.yml on main, on a GitHub-hosted runner, built from
# the release's tag's commit.
attested() {
	local bundles
	bundles=$(mktemp --suffix=.jsonl)
	if ! curl_api "/attestations/$2" 2>/dev/null | "$JQ" -c '.attestations[].bundle' >"$bundles" 2>/dev/null || [ ! -s "$bundles" ]; then
		rm -f "$bundles"
		REASON="no attestation for $3"
		return 1
	fi
	if ! "$GH" attestation verify "$1" --repo "$RA_REPO" --signer-workflow "$RA_REPO/.github/workflows/release.yml" \
		--source-ref refs/heads/main ${SOURCE_DIGEST:+--source-digest "$SOURCE_DIGEST"} --deny-self-hosted-runners \
		--bundle "$bundles" >/dev/null 2>&1; then
		rm -f "$bundles"
		REASON="the attestation of $3 doesn't verify"
		return 1
	fi
	rm -f "$bundles"
}

# verify_image <image:tag>: pulled, and its digest attested. Sets IMAGE_ID.
verify_image() {
	local digest
	docker pull -q "$1" >/dev/null 2>&1 || {
		REASON="couldn't pull ${1##*/}"
		return 1
	}
	digest=$(docker image inspect -f '{{range .RepoDigests}}{{println .}}{{end}}' "$1" | grep "^${1%:*}@" | head -n 1)
	digest=${digest#*@}
	[ -n "$digest" ] || {
		REASON="no digest for ${1##*/}"
		return 1
	}
	attested "oci://${1%:*}@$digest" "$digest" "${1##*/}" || return 1
	IMAGE_ID=$(docker image inspect -f '{{.Id}}' "$1")
}

# verify <tag>: the release's files downloaded into
# auto-upgrade/releases/<version> and attested, then the server's image.
# Sets FILES_SHA (what's installed is checked against it) and VERIFIED.
verify() {
	local tag=$1 v=${1#v} dir url f d
	SOURCE_DIGEST=""
	is_tag "$tag" || {
		REASON="that isn't a release's tag (vX.Y.Z)"
		return 1
	}
	SOURCE_DIGEST=$(tag_commit "$tag") || {
		SOURCE_DIGEST=""
		REASON="couldn't find the commit $tag names"
		return 1
	}
	dir=$HOME_AU/releases/$v
	rm -rf "$dir"
	mkdir -p "$dir"
	FILES="compose.yaml radioarchive-config.tar.gz"
	FILES_SHA=""
	for f in $FILES; do
		url=$(asset_url "$tag" "$f")
		if [ -z "$url" ]; then
			REASON="the release has no $f yet"
			return 1
		fi
		curl -fsSL --max-time 300 -o "$dir/$f" "$url" || {
			REASON="couldn't download $f"
			return 1
		}
		d=$(sha "$dir/$f")
		attested "$dir/$f" "sha256:$d" "$f" || return 1
		FILES_SHA="$FILES_SHA $f=$d"
	done
	grep -q "image: ${PREFIX}radioarchive:$(minor_of "$v")\$" "$dir/compose.yaml" || {
		REASON="compose.yaml doesn't name radioarchive:$(minor_of "$v")"
		return 1
	}
	IMAGES="${PREFIX}radioarchive:$v"
	verify_image "$IMAGES" || return 1
	VERIFIED=" $IMAGE_ID"
}

# verified_file <file> <name>: the file installed is the one verified.
verified_file() {
	case " $FILES_SHA " in *" $2=$(sha "$1") "*) return 0 ;; esac
	return 1
}

# ── The database ───────────────────────────────────────────────────────────
dbq() { dc exec -T db psql -X -q -At -U radioarchive -d "$1" -c "$2" 2>/dev/null | tr -d '\r'; }

# fingerprint: what an upgrade can change in the databases: RadioArchive's
# migrations and Authentik's.
fingerprint() {
	local a b
	a=$(dbq radioarchive "SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied") || a=""
	b=$(dbq authentik "SELECT CASE WHEN to_regclass('public.django_migrations') IS NULL THEN 0 ELSE (SELECT count(*) FROM django_migrations) END") || b=""
	if [ -z "$a" ] || [ -z "$b" ]; then
		echo unreadable
	else
		echo "$a|$b"
	fi
}

# ── Health ─────────────────────────────────────────────────────────────────
# api_url: the API's port published on the host, else its container's address.
api_url() {
	local p ip
	p=$(dc port api 8080 2>/dev/null | head -n 1)
	case $p in
	0.0.0.0:* | "[::]:"*) echo "http://127.0.0.1:${p##*:}" && return ;;
	?*) echo "http://$p" && return ;;
	esac
	ip=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "$(dc ps -q api)" 2>/dev/null | awk '{print $1}')
	[ -n "$ip" ] && echo "http://$ip:8080"
}

# all_running: every service of the stack running, none restarting or exited.
all_running() {
	local out bad
	out=$(dc ps -a --format '{{.Service}} {{.State}}' 2>/dev/null) || return 1
	[ -n "$out" ] || return 1
	bad=$(printf '%s\n' "$out" | awk '$2 != "running" { print $1 }' | tr '\n' ' ')
	[ -z "$bad" ] || {
		REASON="not running: $bad"
		return 1
	}
}

# api_up <version>: the API answers /healthz (it listens only after its
# migrations have run) and runs <version>.
api_up() {
	local url got end=$(($(now) + START_TIMEOUT))
	while :; do
		url=$(api_url)
		got=$(running_version)
		if [ -n "$url" ] && curl -fsS --max-time 10 "$url/healthz" >/dev/null 2>&1 && [ "$got" = "$1" ]; then
			return 0
		fi
		if [ "$(now)" -ge "$end" ]; then
			REASON="the API didn't answer as $1 within $START_TIMEOUT s (it runs ${got:-nothing})"
			return 1
		fi
		sleep 5
	done
}

urls_up() {
	local u end
	for u in $CHECK_URLS; do
		end=$(($(now) + START_TIMEOUT))
		until curl -fsS -o /dev/null --max-time 20 "$u" 2>/dev/null; do
			if [ "$(now)" -ge "$end" ]; then
				REASON="$u didn't answer"
				return 1
			fi
			sleep 10
		done
	done
}

# calls_since <unix seconds>: calls stored that started after then.
calls_since() { dbq radioarchive "SELECT count(*) FROM calls WHERE started_at > to_timestamp($1)"; }

# call_rule: R87's health check of calls. If at least CALLS_BEFORE_MIN (20)
# calls arrived in the 2 hours before the upgrade, one must arrive within
# CALL_WINDOW (30) minutes of the restart. A quiet network is never a reason
# to roll back. Sets CALL_NOTE.
call_rule() {
	local n end
	if [ "$CALLS_BEFORE" -lt "$CALLS_BEFORE_MIN" ]; then
		CALL_NOTE="the call check was skipped: $CALLS_BEFORE calls in the 2 hours before, fewer than $CALLS_BEFORE_MIN"
		return 0
	fi
	end=$((RESTARTED + CALL_WINDOW * 60))
	while :; do
		n=$(calls_since "$RESTARTED")
		if [ "${n:-0}" -gt 0 ]; then
			CALL_NOTE="a call arrived after the restart"
			return 0
		fi
		if [ "$(now)" -ge "$end" ]; then
			REASON="no call arrived within $CALL_WINDOW minutes of the restart, with $CALLS_BEFORE in the 2 hours before"
			return 1
		fi
		sleep "${CALL_POLL:-30}"
	done
}

# health <version>: the checks after an upgrade, the call rule included.
health() {
	local v=$1 id
	api_up "$v" || return 1
	# A container that starts and then fails shows within a minute.
	sleep "$SETTLE"
	all_running || return 1
	id=$(docker inspect -f '{{.Image}}' "$(dc ps -q api)" 2>/dev/null)
	case " $VERIFIED " in *" $id "*) ;; *)
		REASON="the API runs an image that wasn't verified"
		return 1
		;;
	esac
	urls_up || return 1
	call_rule || return 1
	api_up "$v" && all_running
}

# ── Backups ────────────────────────────────────────────────────────────────
# backup_busy: the backup service is running one.
backup_busy() {
	local r
	r=$(dc exec -T backup sh -c 'cat /status/status.json 2>/dev/null' | "$JQ" -r '.running_since // 0' 2>/dev/null)
	[ "${r:-0}" != 0 ]
}

# back_up: the backup service's `now`, kept aside in the backup folder's
# pre-upgrade/<set>/ by hard links, so a backup made later that day can't
# replace it. Then the install's files.
back_up() {
	local out
	out=$(dc exec -T backup radioarchive-backup now 2>&1)
	case $out in *"another backup is running"*)
		BUSY=1
		REASON="a backup is running"
		return 1
		;;
	esac
	NIGHT=$(printf '%s\n' "$out" | sed -n 's/^backup: \([0-9-]*\) good:.*/\1/p' | tail -n 1)
	if [ -z "$NIGHT" ]; then
		REASON="the backup failed: $(printf '%s\n' "$out" | sed -n 's/^backup: failed: //p' | tail -n 1)"
		return 1
	fi
	[[ $SET =~ ^[0-9]{8}T[0-9]{6}Z-[0-9.]+-to-[0-9.]+$ ]] || {
		REASON="the backup set's name isn't one this script makes"
		return 1
	}
	dc exec -T backup sh -c 'set -e; d=/backups/pre-upgrade/$1; mkdir -p "$d"
		for f in radioarchive.dump authentik.dump counts.tsv check; do ln -f "/backups/$2.$f" "$d/$2.$f"; done
		[ ! -e "/backups/$2.setup.json" ] || ln -f "/backups/$2.setup.json" "$d/$2.setup.json"
		chown "${BACKUP_UID:-0}:${BACKUP_GID:-0}" /backups/pre-upgrade "$d"; chmod 750 /backups/pre-upgrade "$d"
		ls -1d /backups/pre-upgrade/*/ | sort | head -n -"$3" | while IFS= read -r old; do rm -rf -- "$old"; done' \
		sh "$SET" "$NIGHT" "$KEEP" || {
		REASON="couldn't keep the backup aside"
		return 1
	}
	FILES_DIR=$HOME_AU/files/$SET
	mkdir -p "$FILES_DIR"
	chmod 700 "$HOME_AU/files" "$FILES_DIR"
	cp -a "$RA_DIR/compose.yaml" "$RA_DIR/.env" "$RA_DIR/deploy" "$FILES_DIR/" || {
		REASON="couldn't copy the install's files"
		return 1
	}
	[ ! -f "$RA_DIR/compose.override.yaml" ] || cp -a "$RA_DIR/compose.override.yaml" "$FILES_DIR/"
	ls -1d "$HOME_AU"/files/*/ | sort | head -n -"$KEEP" | while IFS= read -r old; do rm -rf -- "$old"; done
}

# images_of: "image ID<TAB>image name" for each container, so a rollback can
# point the names back at the images that were running.
images_of() {
	local c
	for c in $(dc ps -q 2>/dev/null); do
		docker inspect -f '{{.Image}}	{{.Config.Image}}' "$c"
	done | sort -u
}

# ── Upgrade ────────────────────────────────────────────────────────────────
install_files() {
	local dir=$HOME_AU/releases/$TO f
	for f in $FILES; do
		verified_file "$dir/$f" "$f" || {
			REASON="$f changed after it was verified"
			return 1
		}
	done
	cp "$dir/compose.yaml" "$RA_DIR/compose.yaml.partial" && mv -f "$RA_DIR/compose.yaml.partial" "$RA_DIR/compose.yaml" &&
		tar -xzf "$dir/radioarchive-config.tar.gz" -C "$RA_DIR" || {
		REASON="couldn't install the release's files"
		return 1
	}
}

# our_image_verified: after the pull, the image Compose will run is the
# verified one; one whose tag moved since (a patch release came out) is
# verified too, or refused.
our_image_verified() {
	local ref id
	for ref in $(cat "$RA_DIR/compose.yaml" "$RA_DIR/compose.override.yaml" 2>/dev/null |
		sed -n -E "s#^ *image: *(${PREFIX//./\\.}radioarchive:[0-9.]+) *\$#\1#p" | sort -u); do
		id=$(docker image inspect -f '{{.Id}}' "$ref" 2>/dev/null)
		case " $VERIFIED " in *" $id "*) continue ;; esac
		SOURCE_DIGEST="" verify_image "$ref" || return 1
		VERIFIED="$VERIFIED $IMAGE_ID"
	done
}

upgrade() {
	install_files || return 1
	dc config -q >/dev/null 2>&1 || {
		REASON="the new compose files don't load"
		return 1
	}
	dc pull -q >/dev/null 2>&1 || {
		REASON="docker compose pull failed"
		return 1
	}
	our_image_verified || return 1
	dc up -d >/dev/null 2>&1 || {
		REASON="docker compose up -d failed"
		return 1
	}
	local i=0
	RESTARTED=""
	while [ -z "$RESTARTED" ] && [ $i -lt 30 ]; do
		RESTARTED=$(dbq radioarchive "SELECT extract(epoch FROM now())::bigint")
		[ -n "$RESTARTED" ] || sleep 2
		i=$((i + 1))
	done
	[ -n "$RESTARTED" ] || RESTARTED=$(now)
}

# ── Rollback ───────────────────────────────────────────────────────────────
# rollback: the previous release's files and image, and its database from the
# pre-upgrade backup if the new version changed the database.
rollback() {
	local fp_after restore="" d vol=$DB_VOLUME
	status '.state = "rolling-back"'
	fp_after=$(fingerprint)
	[ "$fp_after" = "$FP_BEFORE" ] || restore=1
	event info rollback started "the database $([ -n "$restore" ] && echo changed: it will be restored || echo "is unchanged")"
	if [ -n "$restore" ]; then
		d=/backups/pre-upgrade/$SET
		dc run --rm --no-deps -T --entrypoint sh backup -c "set -e
			for f in radioarchive.dump authentik.dump counts.tsv check; do
				if [ -e /backups/$NIGHT.\$f ] && [ ! /backups/$NIGHT.\$f -ef $d/$NIGHT.\$f ]; then
					mkdir -p $d/after-upgrade; chown \${BACKUP_UID:-0}:\${BACKUP_GID:-0} $d/after-upgrade
					mv -f /backups/$NIGHT.\$f $d/after-upgrade/
				fi
				ln -f $d/$NIGHT.\$f /backups/$NIGHT.\$f
			done" >/dev/null 2>&1 || {
			ROLLBACK_FAILED="the pre-upgrade backup couldn't be put back in the backup folder"
			return 1
		}
		dc down >/dev/null 2>&1
	fi
	cp -a "$FILES_DIR/compose.yaml" "$FILES_DIR/.env" "$RA_DIR/" && cp -a "$FILES_DIR/deploy/." "$RA_DIR/deploy/" || {
		ROLLBACK_FAILED="the previous files couldn't be put back"
		return 1
	}
	[ ! -f "$FILES_DIR/compose.override.yaml" ] || cp -a "$FILES_DIR/compose.override.yaml" "$RA_DIR/"
	# The names point at the images that were running again, so nothing is pulled.
	printf '%s\n' "$IMAGES_BEFORE" | while IFS='	' read -r id ref; do
		[ -z "$id" ] || docker tag "$id" "$ref" 2>/dev/null
	done
	if [ -n "$restore" ]; then
		STEP_START=$(now)
		event info restore started "into a new database, the documented restore"
		docker volume rm "$vol" >/dev/null 2>&1 || {
			ROLLBACK_FAILED="the database's volume couldn't be removed for the restore"
			event error restore failed "$ROLLBACK_FAILED"
			return 1
		}
		dc up -d --wait db >/dev/null 2>&1
		if ! dc run --rm --no-deps -T backup restore "$NIGHT" >/dev/null 2>&1; then
			ROLLBACK_FAILED="the restore of the pre-upgrade backup ($NIGHT) failed: the stack is left stopped"
			event error restore failed "$ROLLBACK_FAILED"
			return 1
		fi
		RESTORED=1
		event info restore ok "the database is as it was before the upgrade, without the calls since the last backup"
	fi
	dc up -d --remove-orphans >/dev/null 2>&1
	VERIFIED=$(printf '%s\n' "$IMAGES_BEFORE" | cut -f1 | tr '\n' ' ')
	if ! api_up "$FROM" || ! all_running; then
		ROLLBACK_FAILED="after the rollback: $REASON"
		return 1
	fi
}

# ── The host-wide lock ─────────────────────────────────────────────────────
# One lock for every app's automatic upgrades on this host (R106, #128), so
# two never upgrade at once: PagerArchive's script takes the same file with
# flock -n. Made readable and writable by everyone, as each app may run as its
# own user; a read is all flock needs. RA_TEST_HOST_LOCK: tests only.
HOST_LOCK=${RA_TEST_HOST_LOCK:-/run/lock/archive-upgrade.lock}

# host_locked_run: a run, holding the host-wide lock the whole time, its
# pre-upgrade backup and any rollback included.
host_locked_run() {
	[ -e "$HOST_LOCK" ] || (umask 000 && : >>"$HOST_LOCK") 2>/dev/null
	if [ ! -r "$HOST_LOCK" ]; then
		STEP_START=$(now)
		event error check refused "the host-wide upgrade lock $HOST_LOCK can't be made or read: see docs/deploy.md"
		checked refused "the host-wide upgrade lock can't be made or read, so nothing was done"
		return 1
	fi
	exec 8<"$HOST_LOCK"
	if ! flock -n 8; then
		FROM=$(status_get .running_version) TO=$(status_get .latest_version)
		STEP_START=$(now)
		event info check skipped "another upgrade is running"
		checked skipped "another upgrade is running on this host: trying again next hour"
		exec 8<&-
		return 0
	fi
	run
}

# ── A run ──────────────────────────────────────────────────────────────────
fail_upgrade() { # <step> <reason>
	local why=$2 rb=false rs=false outcome
	event error "$1" failed "$why"
	STEP_START=$(now)
	ROLLBACK_FAILED="" RESTORED=""
	if rollback; then
		rb=true
		[ -z "$RESTORED" ] || rs=true
		outcome=rolled-back
		event warn rollback ok "back on $FROM"
	else
		outcome=rollback-failed
		event error rollback failed "$ROLLBACK_FAILED"
	fi
	status "$CHECKED_AT | $DONE"' | .last_check_result = $o | .last_check_note = $w
		| .failures += 1 | .rollbacks += (if $rb == "true" then 1 else 0 end)
		| .failed_versions = ((.failed_versions + [$to]) | unique)
		| .last_failure = {at: ($t|tonumber), from: $from, to: $to, step: $step, reason: $w, rolled_back: ($rb == "true"),
			restored: ($rs == "true"), rollback_error: $rf, backup: $set}
		| .running_version = (if $rb == "true" then $from else "" end) | .latest_version = $to
		| .history = (.history + [{at: ($t|tonumber), from: $from, to: $to, result: $o}])[-20:]' \
		--arg t "$(now)" --arg o "$outcome" --arg w "$why" --arg rb "$rb" --arg rs "$rs" --arg from "$FROM" --arg to "$TO" \
		--arg step "$1" --arg rf "${ROLLBACK_FAILED:-}" --arg set "$SET"
	return 1
}

run() {
	local latest major notes
	rotate_log
	STEP_START=$(now)
	FROM="" TO=""
	case $(status_get .state) in upgrading | health-check | rolling-back)
		event error check refused "a run stopped part way, while $(status_get .state): see docs/deploy.md, then run clear"
		status '.last_check_at = ($t|tonumber) | .last_check_result = "interrupted"' --arg t "$(now)"
		return 1
		;;
	esac
	AU_RUN_START=$(now)
	export AU_RUN_START
	status '.state = "checking" | .running_since = ($t|tonumber) | .last_check_at = ($t|tonumber) | .target_version = ""
		| .last_check_result = "in-progress" | .last_check_note = "checking for a release now"' --arg t "$AU_RUN_START"
	FROM=$(running_version)
	if [ -z "$FROM" ]; then
		event warn check skipped "the API isn't running"
		checked skipped "the API isn't running, so nothing was done"
		return 0
	fi
	if ! is_release "$FROM"; then
		case $FROM in *[!0-9A-Za-z.+-]* | ?????????????????????????????????*) FROM="" ;; esac
		event info check skipped "the running version (${FROM:-not a version}) isn't a release"
		checked skipped "the running version (${FROM:-not a version}) isn't a release, so it isn't upgraded"
		return 0
	fi
	if ! latest=$(latest_release) || [ -z "$latest" ]; then
		event warn check failed "couldn't read the releases from GitHub"
		checked error "couldn't read the releases from GitHub"
		return 1
	fi
	TO=${latest#v}
	if ! newer "$TO" "$FROM"; then
		event info check up-to-date
		checked up-to-date "$FROM is the latest release"
		return 0
	fi
	major=${FROM%%.*}
	if [ "${TO%%.*}" != "$major" ]; then
		event info check skipped "$TO is a new major version: upgrade by hand"
		checked skipped "$TO is a new major version: upgrade by hand (docs/deploy.md)"
		return 0
	fi
	if "$JQ" -e --arg v "$TO" '.failed_versions | index($v)' "$STATUS" >/dev/null 2>&1; then
		event info check skipped "$TO failed before"
		checked skipped "$TO failed before, so it isn't tried again (retry $TO to try it)"
		return 0
	fi
	notes=$(release_field "$latest" '.body // ""' | tr -d '\r' | sed -n '/[^[:space:]]/{p;q}')
	case $notes in *"Upgrade steps"*)
		event info check skipped "$TO has upgrade steps"
		checked skipped "$TO's release notes have Upgrade steps: upgrade by hand"
		return 0
		;;
	esac
	if backup_busy; then
		event info check skipped "a backup is running"
		checked skipped "a backup is running: trying again next hour"
		return 0
	fi
	event info check newer "$TO is out"

	status '.state = "verifying" | .target_version = $to | .last_check_note = ($to + " is out: upgrading to it")' --arg to "$TO"
	REASON=""
	if ! verify "$latest"; then
		event error verify refused "$REASON"
		checked refused "$TO wasn't installed: $REASON"
		return 1
	fi
	event info verify ok "files and image attested"

	status '.state = "backing-up"'
	SET="$(date -u +%Y%m%dT%H%M%SZ)-$FROM-to-$TO" BUSY=""
	if ! back_up; then
		if [ -n "$BUSY" ]; then
			event info backup skipped "$REASON"
			checked skipped "a backup is running: trying again next hour"
			return 0
		fi
		event error backup failed "$REASON"
		checked refused "$TO wasn't installed: $REASON"
		return 1
	fi
	event info backup ok "night $NIGHT kept aside, and the files"

	FP_BEFORE=$(fingerprint)
	IMAGES_BEFORE=$(images_of)
	DB_VOLUME=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/var/lib/postgresql"}}{{.Name}}{{end}}{{end}}' "$(dc ps -q db)")
	CALLS_BEFORE=$(dbq radioarchive "SELECT count(*) FROM calls WHERE started_at > now() - interval '2 hours'")
	CALLS_BEFORE=${CALLS_BEFORE:-0}

	status '.state = "upgrading"'
	if ! upgrade; then
		fail_upgrade upgrade "$REASON"
		return 1
	fi
	event info upgrade ok "pulled and started"

	status '.state = "health-check"'
	CALL_NOTE=""
	if ! health "$TO"; then
		fail_upgrade health "$REASON"
		return 1
	fi
	event info health ok "$CALL_NOTE"
	status "$CHECKED_AT | $DONE"' | .last_check_result = "upgraded"
		| .last_check_note = $n | .running_version = $to | .latest_version = $to
		| .last_upgrade = {at: ($t|tonumber), from: $from, to: $to}
		| .history = (.history + [{at: ($t|tonumber), from: $from, to: $to, result: "upgraded"}])[-20:]' \
		--arg t "$(now)" --arg n "Upgraded from $FROM; $CALL_NOTE" --arg from "$FROM" --arg to "$TO"
	rm -rf "$HOME_AU/releases/$TO"
}

# ── Switching it on and off ────────────────────────────────────────────────
CRON_MARK="# radioarchive-auto-upgrade"

enable() {
	local minute line systemd="" schedule=1
	case ${1:-} in
	--systemd) systemd=1 ;;
	--no-schedule) schedule="" ;;
	esac
	tools || {
		echo "couldn't fetch jq and the GitHub CLI from GitHub and check them (is the network up?), or AUTO_UPGRADE_GH isn't a program" >&2
		return 1
	}
	mkdir -p "$STATUS_DIR"
	chmod 755 "$HOME_AU" "$STATUS_DIR"
	if ! grep -q '^AUTO_UPGRADE_STATUS_DIR=' "$RA_DIR/.env"; then
		printf '\n# Automatic upgrades (docs/deploy.md): their status, which the API reads.\nAUTO_UPGRADE_STATUS_DIR=%s\n' \
			"./${STATUS_DIR#"$RA_DIR"/}" >>"$RA_DIR/.env"
		echo "added AUTO_UPGRADE_STATUS_DIR to .env"
	fi
	status '.enabled = true | .enabled_at = ($t|tonumber) | if (.state // "") == "" then .state = "idle" else . end' --arg t "$(now)"
	if [ -z "$schedule" ]; then
		echo "on, with no schedule: run deploy/auto-upgrade/auto-upgrade.sh run from your own scheduler, hourly"
		return 0
	fi
	minute=$(($(od -An -N1 -tu1 /dev/urandom | tr -d ' ') % 60))
	if [ -n "$systemd" ]; then
		local units=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user
		mkdir -p "$units"
		cat >"$units/radioarchive-auto-upgrade.service" <<UNIT
[Unit]
Description=RadioArchive automatic upgrade check ($RA_DIR)

[Service]
Type=oneshot
WorkingDirectory=$RA_DIR
ExecStart=$RA_DIR/deploy/auto-upgrade/auto-upgrade.sh run
UNIT
		cat >"$units/radioarchive-auto-upgrade.timer" <<UNIT
[Unit]
Description=RadioArchive automatic upgrade check, hourly

[Timer]
OnCalendar=*-*-* *:$(printf '%02d' "$minute"):00
Persistent=false

[Install]
WantedBy=timers.target
UNIT
		systemctl --user daemon-reload && systemctl --user enable --now radioarchive-auto-upgrade.timer || return 1
		echo "on: a systemd user timer, hourly at :$(printf '%02d' "$minute") (it runs while you're logged out only with lingering: loginctl enable-linger)"
	else
		line="$minute * * * * cd '$RA_DIR' && PATH=/usr/local/bin:/usr/bin:/bin bash deploy/auto-upgrade/auto-upgrade.sh run >'$HOME_AU/last-run.txt' 2>&1 $CRON_MARK $RA_DIR"
		{ crontab -l 2>/dev/null | grep -vF "$CRON_MARK $RA_DIR"; echo "$line"; } | crontab - || return 1
		echo "on: your crontab runs it hourly at :$(printf '%02d' "$minute")"
	fi
	echo "now run: docker compose up -d   (so the API reads the status), then: deploy/auto-upgrade/auto-upgrade.sh run"
}

disable() {
	local units=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user
	if crontab -l 2>/dev/null | grep -qF "$CRON_MARK $RA_DIR"; then
		crontab -l 2>/dev/null | grep -vF "$CRON_MARK $RA_DIR" | crontab -
	fi
	if [ -f "$units/radioarchive-auto-upgrade.timer" ] && grep -q "$RA_DIR" "$units/radioarchive-auto-upgrade.service" 2>/dev/null; then
		systemctl --user disable --now radioarchive-auto-upgrade.timer >/dev/null 2>&1
		rm -f "$units/radioarchive-auto-upgrade.timer" "$units/radioarchive-auto-upgrade.service"
		systemctl --user daemon-reload 2>/dev/null
	fi
	if [ -x "${JQ:-}" ]; then
		status '.enabled = false | .disabled_at = ($t|tonumber)' --arg t "$(now)"
	fi
	FROM="" TO="" event info check off "switched off"
	echo "off: no more checks. The admin pages say so. Upgrade by hand as docs/deploy.md says."
}

main() {
	RA_DIR=${AUTO_UPGRADE_INSTALL_DIR:-$(cd "$(dirname "$0")/../.." && pwd)}
	[ -f "$RA_DIR/compose.yaml" ] || {
		echo "auto-upgrade: no compose.yaml in $RA_DIR" >&2
		return 2
	}
	load_settings
	unset AU_RUN_START
	mkdir -p "$HOME_AU"
	local cmd=${1:-}
	[ $# -eq 0 ] || shift
	case $cmd in
	retry)
		is_release "${1#v}" || {
			echo "usage: auto-upgrade.sh retry X.Y.Z" >&2
			return 2
		}
		;;
	verify)
		[ -z "${1:-}" ] || is_tag "$1" || {
			echo "usage: auto-upgrade.sh verify [vX.Y.Z]" >&2
			return 2
		}
		;;
	esac
	case $cmd in
	run | verify | retry | clear | status | enable | disable) ;;
	*)
		sed -n '9,18p' "$0" | sed 's/^# \{0,1\}//' >&2
		return 2
		;;
	esac
	for t in docker curl flock sha256sum; do
		command -v "$t" >/dev/null || {
			echo "auto-upgrade: needs $t" >&2
			return 2
		}
	done
	case $cmd in
	enable)
		enable "$@"
		return
		;;
	disable)
		tools >/dev/null 2>&1
		disable
		return
		;;
	esac
	exec 9>"$HOME_AU/lock"
	if ! flock -n 9; then
		echo "auto-upgrade: another run is going" >&2
		return 1
	fi
	if ! tools; then
		JQ=${JQ:-false}
		STEP_START=$(now)
		event error verify refused "the pinned jq or GitHub CLI couldn't be fetched or didn't match its checksum, so nothing can be verified"
		[ ! -x "$JQ" ] || checked refused "nothing can be verified: the pinned tools couldn't be fetched"
		return 1
	fi
	case $cmd in
	run)
		if [ "$(status_get .enabled)" != true ]; then
			echo "auto-upgrade is off: deploy/auto-upgrade/auto-upgrade.sh enable" >&2
			return 1
		fi
		host_locked_run
		;;
	status) "$JQ" . "$STATUS" 2>/dev/null || echo "no status yet: auto-upgrade has never run here" ;;
	retry)
		status '.failed_versions -= [$v]' --arg v "${1#v}"
		echo "${1#v} will be tried at the next run"
		;;
	clear)
		status "$DONE"' | if .last_check_result == "interrupted" then .last_check_result = "cleared" else . end
			| if .last_failure then .last_failure.cleared_at = ($t|tonumber) else . end' --arg t "$(now)"
		echo "cleared: the admin pages' alert goes, and the next run checks again. Check the install first: docs/deploy.md, \"Automatic upgrades\""
		;;
	verify)
		local tag=${1:-}
		latest_release >/dev/null || {
			echo "couldn't read the releases from GitHub" >&2
			return 1
		}
		[ -n "$tag" ] || tag=$(latest_release)
		REASON=""
		if verify "$tag"; then
			echo "verified: $tag's files ($FILES) and image ($IMAGES) were signed by $RA_REPO's release.yml on main, on a GitHub-hosted runner, from $tag's commit $SOURCE_DIGEST"
		else
			echo "NOT verified: $tag: $REASON" >&2
			return 1
		fi
		;;
	esac
}

# Sourced (by its checks), it only defines the functions.
if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
	main "$@"
	exit $?
fi
