#!/bin/sh
# Nightly backups (SPEC.md R89, ADR 0041), PagerArchive's backup script
# (its deploy/backup/backup.sh, ADR 0020) adapted, run by upstream's
# postgres:18 image, so pg_dump always matches the database. Part of the
# release's archive.
#
#   serve         the `backup` service: dumps both databases each night at
#                 BACKUP_AT, checks the dumps by restoring them, and prunes
#   now           one backup, at once: docker compose exec backup radioarchive-backup now
#   status        what the last runs did (the status file)
#   restore NIGHT into an empty install:
#                 docker compose run --rm backup restore 2026-11-03
#
# A night in the folder (/backups):
#   2026-11-03.radioarchive.dump, 2026-11-03.authentik.dump   pg_dump's custom format
#   2026-11-03.counts.tsv   each table's rows, from the snapshot the dump read
#   2026-11-03.setup.json   the setup export (R53), as the API last wrote it
#   2026-11-03.check        the check's result: "ok", or "failed: why"
# Each is written as <name>.partial and renamed when complete, and the
# check's result last. So a file with a final name is complete.
#
# Recordings are never backed up (R89), nor is call data: the rows of
# calls, transmissions and rejected_calls are left out of RadioArchive's
# dump (pg_dump --exclude-table-data), so their counts are 0. Everything
# else is in it: the setup, recorders' key hashes, settings, the audit log,
# accounts and signed-in listeners' saved settings (R110).
#
# The check restores each night's dumps into a throwaway PostgreSQL in this
# container, listening on its socket only, and compares every table's rows
# with the night's counts.
#
# P1: what pg_dump, pg_restore and PostgreSQL say on failure can quote a row,
# so it's thrown away, and every error here is fixed text. Nothing reads a
# dump's contents into a message or a log line.
set -u

# RA_TEST_ROOT: the script's own checks run it in a folder rather than a
# container.
ROOT=${RA_TEST_ROOT:-}
DIR=$ROOT/backups
STATUS=$ROOT/status/status.json
EXPORT=$ROOT/setup-export/setup.json
LOCK=$ROOT/tmp/radioarchive-backup.lock
KEEP=${BACKUP_KEEP_DAYS:-14}
AT=${BACKUP_AT:-04:00}
OWNER=${BACKUP_UID:-0}:${BACKUP_GID:-0}
SOCKET=/var/run/postgresql
THROWAWAY=/var/lib/postgresql/check
TAB=$(printf '\t')
# PostgreSQL's database name : the name in the folder.
DATABASES="radioarchive:radioarchive authentik:authentik"
# Call data, left out of RadioArchive's dump (R89).
CALL_TABLES=public.calls,public.transmissions,public.rejected_calls

# Every ordinary table, as "SELECT name, rows" queries psql then runs (\gexec).
COUNT_SQL="SELECT format('SELECT %L, count(*) FROM %I.%I', n.nspname || '.' || c.relname, n.nspname, c.relname)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r' AND n.nspname <> 'information_schema' AND n.nspname !~ '^pg_'
ORDER BY 1 \\gexec"

say() { echo "backup: $*"; }

case $KEEP in '' | *[!0-9]* | 0)
	say "BACKUP_KEEP_DAYS must be a whole number above 0"
	exit 2
	;;
esac
if ! printf '%s' "$AT" | grep -Eqx '([01][0-9]|2[0-3]):[0-5][0-9]'; then
	say "BACKUP_AT must be a time of day, HH:MM"
	exit 2
fi

# place <file>: the owner and mode the folder's files have, before the rename.
place() {
	chown "$OWNER" "$1" 2>/dev/null
	chmod 640 "$1"
}

# field <key>: a value from the status file, which only this script writes.
field() {
	sed -n "s/^ *\"$1\": \"\{0,1\}\([^\",]*\)\"\{0,1\},\{0,1\}\$/\1/p" "$STATUS" 2>/dev/null
}

# write_status: for the API (R89). Times are seconds since 1970. The fields
# are those PagerArchive's backup writes for a database (#128), so in the
# combined deployment the API reads PagerArchive's file for this database
# in its place.
write_status() {
	mkdir -p "${STATUS%/*}" && chmod 755 "${STATUS%/*}"
	cat >"$STATUS.partial" <<JSON
{
  "database": "radioarchive",
  "since": ${s_since:-0},
  "last_night": "${s_night:-}",
  "last_run_at": ${s_run_at:-0},
  "last_run_ok": ${s_ok:-false},
  "running_since": ${s_running:-0},
  "last_good_at": ${s_good_at:-0},
  "last_good_night": "${s_good_night:-}",
  "last_good_bytes": ${s_good_bytes:-0},
  "last_good_dump_seconds": ${s_good_dump:-0},
  "last_good_check_seconds": ${s_good_check:-0},
  "nights": ${s_nights:-0},
  "error": "${s_error:-}"
}
JSON
	chmod 644 "$STATUS.partial" && mv -f "$STATUS.partial" "$STATUS"
}

read_status() {
	s_since=$(field since) s_night=$(field last_night) s_run_at=$(field last_run_at) s_ok=$(field last_run_ok)
	s_running=$(field running_since)
	s_good_at=$(field last_good_at) s_good_night=$(field last_good_night) s_good_bytes=$(field last_good_bytes)
	s_good_dump=$(field last_good_dump_seconds) s_good_check=$(field last_good_check_seconds)
	s_nights=$(field nights) s_error=$(field error)
}

# nights: the nights in the folder with their counts, oldest first.
nights() {
	for _nf in "$DIR"/*.counts.tsv; do
		[ -e "$_nf" ] || continue
		_nf=${_nf##*/}
		echo "${_nf%.counts.tsv}"
	done | sort
}

# dump_database <database> <name> <night> <counts file> [excluded]: the dump
# and the table counts from one snapshot, so the check compares like with
# like while calls keep arriving. An open transaction holds the snapshot for
# both. [excluded]: "schema.table,..." whose rows are left out: their counts
# are 0, as a restore of the dump has them.
dump_database() {
	_db=$1 _partial=$DIR/$3.$2.dump.partial _excluded=${5:-} _exargs=""
	for _t in $(printf '%s' "$_excluded" | tr , ' '); do
		_exargs="$_exargs --exclude-table-data=$_t"
	done
	psql -X -q -At -d "$_db" -c 'SELECT 1' >/dev/null 2>&1 || return 1
	_fifo=$(mktemp -u) _snap=$(mktemp)
	mkfifo "$_fifo"
	psql -X -q -At -d "$_db" <"$_fifo" >"$_snap" 2>/dev/null &
	_holder=$!
	exec 3>"$_fifo"
	echo "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SELECT pg_export_snapshot();" >&3
	_i=0
	until [ -s "$_snap" ] || [ $_i -ge 300 ] || ! kill -0 "$_holder" 2>/dev/null; do
		sleep 0.1
		_i=$((_i + 1))
	done
	_id=$(head -n 1 "$_snap")
	_rc=1
	if [ -n "$_id" ]; then
		{
			echo "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY; SET TRANSACTION SNAPSHOT '$_id';"
			echo "$COUNT_SQL"
			echo "COMMIT;"
		} | psql -X -q -At -F "$TAB" -v ON_ERROR_STOP=1 -d "$_db" 2>/dev/null |
			awk -F "$TAB" -v OFS="$TAB" -v ex=",$_excluded," 'index(ex, "," $1 ",") { $2 = 0 } { print }' | LC_ALL=C sort >"$4"
		# No tables at all counts as a failure: a backup with nothing in it is no backup.
		# shellcheck disable=SC2086 # $_exargs: fixed names, one argument each
		if [ -s "$4" ] && nice -n 10 pg_dump -d "$_db" -Fc --no-privileges --snapshot="$_id" $_exargs -f "$_partial" >/dev/null 2>&1; then
			_rc=0
		fi
	fi
	echo "COMMIT;" >&3
	exec 3>&-
	wait "$_holder" 2>/dev/null
	rm -f "$_fifo" "$_snap"
	[ $_rc -eq 0 ] && [ -s "$_partial" ] && place "$_partial"
}

# excluded <database>: the tables whose rows its dump leaves out.
excluded() {
	[ "$1" != radioarchive ] || printf '%s' "$CALL_TABLES"
}

# prune <night>: deletes nights older than the setting, by the date in their
# names (copying can change a file's time). Only called after a good check,
# so there's always a good night left, however long backups had been failing.
prune() {
	_cut=$(date -d "$1 -$((KEEP - 1)) days" +%Y%m%d)
	for _f in "$DIR"/[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9].*; do
		[ -e "$_f" ] || continue
		_d=${_f##*/}
		_d=${_d%%.*}
		[ "$(echo "$_d" | tr -d -)" -ge "$_cut" ] || rm -f "$_f"
	done
}

# backup_failed <at> <why>: records a failed run. The last good backup stays.
backup_failed() {
	s_run_at=$1 s_ok=false s_error=$2 s_running=0
	write_status
	say "failed: $2"
}

remove_partials() {
	rm -f "$DIR"/*.partial
}

# ── The check ──────────────────────────────────────────────────────────────
# throwaway_start: a new PostgreSQL in this container, listening on nothing
# but its socket, with its log thrown away.
throwaway_stop() {
	gosu postgres pg_ctl -D "$THROWAWAY" -m immediate -w stop >/dev/null 2>&1
	rm -rf "$THROWAWAY"
}
throwaway_start() {
	[ ! -d "$THROWAWAY" ] || throwaway_stop
	mkdir -p "$THROWAWAY" "$SOCKET"
	chown postgres "$THROWAWAY" "$SOCKET"
	gosu postgres initdb -D "$THROWAWAY" -U postgres --auth=trust >/dev/null 2>&1 || return 1
	gosu postgres pg_ctl -D "$THROWAWAY" -w -t 300 -l /dev/null -o "-c listen_addresses='' \
		-c unix_socket_directories=$SOCKET -c shared_buffers=32MB -c maintenance_work_mem=64MB \
		-c max_parallel_maintenance_workers=0 -c fsync=off -c full_page_writes=off -c synchronous_commit=off \
		-c wal_level=minimal -c max_wal_senders=0 -c autovacuum=off -c log_statement=none" start >/dev/null 2>&1
}

# differs <name> <night> <counts of the restored database, on stdin>: the
# first table that differs from the night's counts, or nothing. Table names
# and numbers only.
differs() {
	_want=$(mktemp) _got=$(mktemp)
	sed -n "s/^$1$TAB//p" "$DIR/$2.counts.tsv" | LC_ALL=C sort >"$_want"
	LC_ALL=C sort >"$_got"
	if [ ! -s "$_want" ]; then
		echo "no counts"
	elif ! cmp -s "$_want" "$_got"; then
		diff "$_want" "$_got" | sed -n 's/^[<>] //p' | head -n 1 | tr '\t' ' ' | tr -cd 'a-zA-Z0-9_. '
	fi
	rm -f "$_want" "$_got"
}

# check_one <database> <name> <night>: restores one dump into the throwaway
# database and compares tables and row counts. Prints "ok", or "failed: why".
check_one() {
	if ! gosu postgres createdb -h "$SOCKET" -U postgres "$1" >/dev/null 2>&1; then
		echo "failed: couldn't create a database to restore into"
		return
	fi
	# Opened here, as root: the folder's files aren't the postgres user's.
	if ! gosu postgres pg_restore -h "$SOCKET" -U postgres -d "$1" --exit-on-error --no-owner --no-privileges \
		<"$DIR/$3.$2.dump" >/dev/null 2>&1; then
		echo "failed: couldn't restore the $2 dump"
		return
	fi
	_diff=$(echo "$COUNT_SQL" | gosu postgres psql -h "$SOCKET" -U postgres -X -q -At -F "$TAB" -d "$1" 2>/dev/null | differs "$2" "$3")
	if [ -n "$_diff" ]; then
		echo "failed: found the $2 restore differs from the dump at $_diff"
		return
	fi
	echo ok
}

# check_night <night>: prints "ok", or "failed: why".
check_night() {
	if ! throwaway_start; then
		echo "failed: couldn't start its throwaway database"
		return
	fi
	for _pair in $DATABASES; do
		_r=$(check_one "${_pair%%:*}" "${_pair##*:}" "$1")
		if [ "$_r" != ok ]; then
			throwaway_stop
			echo "$_r"
			return
		fi
	done
	throwaway_stop
	echo ok
}

# put <file> <text>: a result, under its final name only when complete.
put() {
	printf '%s\n' "$2" >"$1.partial"
	place "$1.partial"
	mv -f "$1.partial" "$1"
}

# ── A night ────────────────────────────────────────────────────────────────
# run_backup <night> <at>: dump, check, prune.
run_backup() {
	_night=$1 _at=$2 _start=$(date +%s)
	read_status
	[ "$_night" != "$(date +%F)" ] || s_night=$_night
	s_running=$_start
	write_status
	mkdir -p "$DIR"
	remove_partials
	chown "$OWNER" "$DIR" 2>/dev/null
	if ! pg_isready -q -t 10; then
		backup_failed "$_at" "the database couldn't be reached"
		return 1
	fi
	_counts=$(mktemp)
	for _pair in $DATABASES; do
		_db=${_pair%%:*} _name=${_pair##*:}
		if ! dump_database "$_db" "$_name" "$_night" "$_counts" "$(excluded "$_db")"; then
			remove_partials
			rm -f "$_counts"
			backup_failed "$_at" "the dump of $_name failed"
			return 1
		fi
		sed "s/^/$_name$TAB/" "$_counts" >>"$DIR/$_night.counts.tsv.partial"
	done
	rm -f "$_counts"
	place "$DIR/$_night.counts.tsv.partial"
	# The setup export beside the dumps (R89): the API writes it hourly.
	# Without one, the backup is still good: the dump holds the setup too.
	_export=no
	if [ -s "$EXPORT" ] && cp "$EXPORT" "$DIR/$_night.setup.json.partial"; then
		place "$DIR/$_night.setup.json.partial"
		_export=yes
	fi
	# A night done again replaces the old one's files, its check's result first.
	rm -f "$DIR/$_night.check" "$DIR/$_night.counts.tsv" "$DIR/$_night.setup.json"
	for _pair in $DATABASES; do
		mv -f "$DIR/$_night.${_pair##*:}.dump.partial" "$DIR/$_night.${_pair##*:}.dump"
	done
	[ "$_export" = no ] || mv -f "$DIR/$_night.setup.json.partial" "$DIR/$_night.setup.json"
	[ "$_export" = yes ] || say "$_night: no setup export from the API to keep beside the dumps"
	mv -f "$DIR/$_night.counts.tsv.partial" "$DIR/$_night.counts.tsv"
	_dumped=$(date +%s)

	_result=$(check_night "$_night")
	put "$DIR/$_night.check" "$_result"
	if [ "$_result" != ok ]; then
		backup_failed "$_at" "the check ${_result#failed: }"
		return 1
	fi

	prune "$_night"
	s_run_at=$_at s_ok=true s_error="" s_good_at=$_at s_good_night=$_night s_running=0
	s_good_dump=$((_dumped - _start)) s_good_check=$(($(date +%s) - _dumped))
	s_good_bytes=$(cat "$DIR/$_night.radioarchive.dump" "$DIR/$_night.authentik.dump" | wc -c)
	s_nights=$(nights | wc -l)
	write_status
	say "$_night good: dumps took $s_good_dump s, the check $s_good_check s, $s_good_bytes bytes, $s_nights nights kept"
}

# locked <command...>: one backup at a time. The lock goes with the process.
locked() {
	mkdir -p "${LOCK%/*}"
	(
		flock -n 9 || {
			say "another backup is running"
			exit 1
		}
		"$@"
	) 9>"$LOCK"
}

serve() {
	_hhmm=$(echo "$AT" | tr -d :)
	read_status
	if [ -z "$s_since" ] || [ "$s_since" = 0 ]; then
		# A new install has nothing to catch up on. Its first backup is at the
		# next BACKUP_AT, and it's overdue 36 hours from here if that never comes.
		s_since=$(date +%s)
		[ "$(date +%H%M)" -lt "$_hhmm" ] || s_night=$(date +%F)
		write_status
	elif [ "${s_running:-0}" != 0 ]; then
		# A run stopped part way, with its container: it isn't running now.
		s_running=0
		write_status
	fi
	say "nightly at $AT, keeping $KEEP days"
	while :; do
		# A minute's pause first: a start that's catching up on a night missed
		# while it was down leaves an upgrade's migrations to finish.
		sleep 60
		[ "$(date +%H%M)" -ge "$_hhmm" ] || continue
		[ "$(field last_night)" != "$(date +%F)" ] || continue
		locked run_backup "$(date +%F)" "$(date +%s)" || true
	done
}

# ── Restoring ──────────────────────────────────────────────────────────────
# restore <night> [--unchecked]: into the install's empty databases, then the
# restored counts compared with the night's. Refuses anything else, so it
# can't overwrite a running archive.
restore() {
	_night=${1:-}
	if ! printf '%s' "$_night" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'; then
		echo "usage: docker compose run --rm backup restore YYYY-MM-DD [--unchecked]" >&2
		echo "nights in the backup folder:" >&2
		nights >&2
		return 2
	fi
	for _pair in $DATABASES; do
		if [ ! -s "$DIR/$_night.${_pair##*:}.dump" ] || [ ! -s "$DIR/$_night.counts.tsv" ]; then
			echo "$_night isn't complete: the backup folder has no ${_pair##*:} dump or counts for it" >&2
			return 1
		fi
	done
	_result=$(cat "$DIR/$_night.check" 2>/dev/null || echo "no result")
	if [ "$_result" != ok ] && [ "${2:-}" != --unchecked ]; then
		echo "$_night didn't pass its check ($_result). Choose another night, or add --unchecked to restore it anyway" >&2
		return 1
	fi
	_i=0
	until pg_isready -q -t 2; do
		_i=$((_i + 1))
		if [ $_i -ge 60 ]; then
			echo "the database didn't answer" >&2
			return 1
		fi
		sleep 2
	done
	_tables="SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r' AND n.nspname = 'public'"
	for _pair in $DATABASES; do
		if [ "$(psql -X -At -d "${_pair%%:*}" -c "$_tables" 2>/dev/null)" != 0 ]; then
			echo "this install isn't empty: ${_pair##*:} has tables, so nothing was changed." >&2
			echo "To replace it: docker compose down -v (this deletes the databases and the recordings volume, not the backup folder), then docker compose up -d db, and run this again before the rest." >&2
			return 1
		fi
	done
	say "restoring $_night"
	for _pair in $DATABASES; do
		_db=${_pair%%:*} _name=${_pair##*:}
		if ! pg_restore -d "$_db" --exit-on-error --single-transaction --no-privileges <"$DIR/$_night.$_name.dump" >/dev/null 2>&1; then
			echo "the $_name dump didn't restore" >&2
			return 1
		fi
		_diff=$(echo "$COUNT_SQL" | psql -X -q -At -F "$TAB" -d "$_db" 2>/dev/null | differs "$_name" "$_night")
		if [ -n "$_diff" ]; then
			echo "the $_name restore differs from the night's counts at $_diff" >&2
			return 1
		fi
		say "$_name restored, with the night's tables and row counts"
	done
	say "$_night is restored, without calls or recordings. Start the stack: docker compose up -d"
}

case ${1:-} in
serve) serve ;;
now)
	# A backup at once, for you or a test: --night YYYY-MM-DD names the
	# night, and --at SECONDS when it happened.
	shift
	_n=$(date +%F) _a=$(date +%s)
	while [ $# -gt 0 ]; do
		case $1 in
		--night) _n=$2 ;;
		--at) _a=$2 ;;
		*)
			echo "usage: radioarchive-backup now [--night YYYY-MM-DD] [--at SECONDS]" >&2
			exit 2
			;;
		esac
		shift 2
	done
	locked run_backup "$_n" "$_a"
	;;
status) cat "$STATUS" ;;
restore)
	shift
	restore "$@"
	;;
*)
	echo "usage: radioarchive-backup serve | now | status | restore YYYY-MM-DD [--unchecked]" >&2
	exit 2
	;;
esac
