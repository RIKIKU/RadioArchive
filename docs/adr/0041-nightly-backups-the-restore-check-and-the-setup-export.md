---
status: proposed
---

# 41. Nightly backups, the restore check and the setup export

Date: 2026-11-26

RadioArchive backs up its database and Authentik's every night, with call data left out, and checks each night's dumps by restoring them (`SPEC.md` R89, A31). It's PagerArchive's design (its ADR 0020 and `deploy/backup/backup.sh`), adapted. These are the implementer's choices where R89 leaves room (spec "How to use this spec", item 6). Built in [Nightly backups, the restore check and the setup export](https://github.com/RIKIKU/RadioArchive/issues/150).

## Summary

| Part | Choice |
|---|---|
| **The service** | `backup`, on upstream's `postgres:18` image, so `pg_dump` always matches the server. It runs `deploy/backup/backup.sh` from the release's archive, as the database's superuser |
| **When** | Each night at `BACKUP_AT` (default 04:00, in `TZ`), an hour after the nightly retention run (03:00), so expired recordings' rows are already gone. A new install's first backup is at the next `BACKUP_AT`. `radioarchive-backup now` backs up at once |
| **What** | Both databases in `pg_dump`'s custom format, each with its table counts from the same snapshot. RadioArchive's dump leaves out the rows of `calls`, `transmissions` and `rejected_calls` (`--exclude-table-data`), so it holds the setup, recorders' key hashes, attachments, settings, the audit log, accounts and saved settings (R89, R110). Recordings are never backed up |
| **The setup export** | The API writes the setup export (R53, as the admin export answers it) to a volume of its own at start, then whenever the setup changes, checking each minute. The backup copies it beside the night's dumps as `NIGHT.setup.json`. Without one, the night is still good: the dump holds the setup too |
| **The check** | Each night's dumps are restored into a throwaway PostgreSQL in the backup's own container, listening on its socket only, and every table's rows are compared with the night's counts. `NIGHT.check` holds `ok` or `failed: why` |
| **Keeping** | `BACKUP_KEEP_DAYS` (default 14) nights, by the date in their names, pruned only after a good night, so a good night is always left |
| **Files** | `BACKUP_DIR` (default `./backups`), files mode 640 owned by `BACKUP_UID:BACKUP_GID`. Each is written as `.partial` and renamed when complete |
| **State** | `status.json` on the `backup-status` volume, with the fields PagerArchive's backup writes for each database (#128). The API mounts it read-only and reads `BACKUP_STATUS_FILE` |
| **For admins** | `GET /api/v1/admin/backup`, and an ArchiveUx `Banner` (tone `error`) on every admin page while backups are failing, in the server's words. No listener page shows it |
| **Failing** | The last run failed, or the last good backup is more than 36 hours old, or (on a new install) there has been none for 36 hours |
| **Metrics** | `radioarchive_backup_failing`, the last good backup's time and size, and the nights kept (ADR 0040), for the "Backup failing" alert |
| **Restoring** | `docker compose run --rm backup restore NIGHT [--unchecked]` into an empty install only, then the restored counts compared with the night's |

## Why the check is in the backup's own container

PagerArchive's check runs in a second service, `backup-check`, with no network. RadioArchive's throwaway PostgreSQL listens on no address at all (`listen_addresses=''`), only its socket inside the container, and is deleted after each check. A second service would add a container and a volume to a host with a 512 MB budget for little more. In the combined deployment PagerArchive's backup takes over (R106), and it has its own check.

## Why the export is written by the API

The backup service can't produce the export: it's the admin API's, and the service has no admin's sign-in. Nothing else should write into the backup folder, so the API writes to a volume of its own, which the backup service mounts read-only, and the backup copies the file in with the night's dumps. Writing it whenever the setup changes, rather than once a night, means a night's copy is never more than a minute behind the dump, whenever the backup runs.

## The combined deployment (M7)

PagerArchive's backup service dumps the `radioarchive` database too (`BACKUP_EXTRA_DATABASES=radioarchive:calls,transmissions,rejected_calls`) and writes its state to `databases/radioarchive.json`, with the same fields. RadioArchive's API then reads that file in place of its own (`BACKUP_STATUS_FILE`), and its own `backup` service is left out (R105).

## How each choice is verified

- `internal/backup`: ok after a good night; failing after a failed run, saying why and when the last good one was; failing once the last good one is over 36 hours old, or a new install has had none for 36 hours; pending before that; off with no file set; text that isn't printable is dropped. The metrics, and none when off.
- `web/tests/backup.spec.ts`: with a good status file (`e2e-web.sh`), no alert. With the admin API saying failing, every admin page shows "Backups are failing." and why, and no listener page does. A good state clears it. Signed out and as a listener the route answers `401` and `403`. axe-core passes in both looks and both themes at 375 and 1,280 px, where the PR's screenshots are taken.
- **A31** (`scripts/ci/e2e-a31.sh`, in CI's image job, once A1, A10, sign-in and the module have run): the backup succeeds, its check says `ok`, the setup export with the fixture's system is beside the dumps, and the counts say the call tables' rows were left out. `/metrics` says it's good. Restored with `restore` into an empty install (a second compose project), it has the systems, talkgroups, units, recorders, settings, accounts and the whole audit log, and no rows of `calls`, `transmissions` or `rejected_calls`. Authentik's restore has its users.
