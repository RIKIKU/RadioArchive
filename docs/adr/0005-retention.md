---
status: accepted (2026-11-24, by the owner)
---

# 5. Retention: the nightly job, orphan files and low disk

Date: 2026-11-24

One package, `internal/retention`, deletes what's past the retention period. A goroutine in every API server checks the recordings volume's free space every minute and does the nightly run when it's due; a PostgreSQL advisory lock lets only one server do either at a time, and a `jobs` table records each run, as PagerArchive's nightly job does. A call's row is deleted and committed before its file is removed. These are the implementer's choices where `SPEC.md` P5, R30, R35 and R88 leave room (spec "How to use this spec", item 6). Built in [Retention and early deletion on low disk](https://github.com/RIKIKU/RadioArchive/issues/37).

## Summary

| Part | Choice |
|---|---|
| **The setting** | `retention_days` in a new `settings` table (migration 00006), 1 to 3,650, missing means 30. Changed with `PUT /api/v1/admin/retention`, behind the admin authenticator (ADR 0003), so not in the binary until M4 |
| **Shortening** | Without `"confirm": true`, `409 confirm_shorter_retention` with `recordings`: how many recordings, hidden ones included, started before the new cut-off now. Lengthening just saves |
| **What's past the period** | A call that started more than `days × 24 h` before the run, by `started_at`, whatever its state (R30, R35), with its transmissions. Rejected calls go by `rejected_at` at the same cut-off (ADR 0003) |
| **When** | `NIGHTLY_AT` (default 03:00) in `TZ`. A run missed while no server was up is made up at the next start, as PagerArchive's |
| **One server** | `pg_try_advisory_lock` on a session held for the run. The run checks again, holding it, that it's still due. The `jobs` row (`nightly`, `low_disk`) records what each run was for, what it did and whether it failed |
| **Order** | Rows first, in batches of 1,000, each committed; then the batch's files. A file that can't be removed has no row, so the orphan sweep removes it later |
| **Orphan files** | `<id[:2]>/<id>.opus` with no row naming it, last written more than 24 h before the run. Files in `.incoming` the same. Nothing else on the volume is touched |
| **Low disk** | Free below `RECORDINGS_MIN_FREE` (default 5 %): delete the oldest call, hidden ones alike, and its file, measure again, until it isn't below or no call is left. Logged and counted as "retention shortened", recorded as a `low_disk` run |
| **Warning** | `GET /api/v1/admin/retention` gives the volume's free space and `state`: `ok`, `warning` below 15 %, `low` below the minimum, with a message for the last two |
| **Tests** | `retention.Config` takes the clock (`Now`) and the free-space measure (`FreeSpace`). Tests call `RunNightly` and `CheckDisk`; the binary only runs `Run`, on its schedule |

## Why rows before files

The gate (ADR 0004) opens a recording only by the path a `public_calls` row gives it. With the row gone, every public route answers as for a call that never existed, whatever happens to the file. The other order, files first, leaves a row pointing at nothing if the run stops between the two: R88's "deleted" call would still be listed with its metadata, past its period (P5), until the next run. A row deleted with its file left behind is harmless: nothing names the file, and the sweep removes it.

A recording being served while its row is deleted keeps playing to the end: on Linux an open file outlives its name. The next request answers 404.

## Uploads in progress

An upload (ADR 0002) writes its file in `.incoming`, renames it to `<id[:2]>/<id>.opus`, and commits its row, in that order, in one request. Retention never deletes a file because it's new, only because it's old:

- **In `.incoming`** a file is an upload being received (each write moves its modification time on) or one a server dropped mid-request. Only the second can be a day old. If a stalled upload's file were removed, the rename would fail, the client would get an error, and it would retry: it deletes a recording only once the server acknowledges it.
- **Renamed into place, row not committed yet**: the file has the time of its last write, moments before. A day's grace is far beyond the transaction. Before removing an orphan the sweep checks the file is still the one it found, as old, so an upload renamed over it since isn't taken with it.
- **A row being written while the job deletes it**: the job's `DELETE` waits for the upload's row lock and then removes the call with the path the upload committed, and its file.
- **A file dated after the job's clock** (the clock moved back) is kept.

## Low disk

R88 says the oldest recordings go "until it's above". The check deletes one call at a time and measures again, so it deletes no more than it must. It deletes the oldest call whether or not it has a recording: one without has no file to free, but it's older than anything kept, so it's past retention as shortened. The log line gives the free space before and after and when the oldest call kept started, which is the retention in force.

The check runs every minute, not only at night, because a disk fills by day. It can't tell recordings from whatever else is on the volume: if something else fills it, recordings go until none is left, and the server then logs an error each check. That's the spec's rule; the sizing formula in R88's docs, the 15 % warning and the alert (R91) are there so it doesn't come to it.

`statfs` is Linux's; elsewhere (where the server doesn't run, ADR 0001) the check logs once that it can't measure and does nothing.

## The test-only trigger

The issue asks for a controllable clock and a test-only trigger, fenced like the admin stub. `RunNightly` and `CheckDisk` are methods on the job, which tests call with their own clock and free-space measure. The binary builds the job with `time.Now` and `statfs` and only calls `Run`; there's no route, environment variable or signal that runs it, and the binary's test checks the admin API, including `/retention`, answers 404. Tests run in an era of their own (calls from 2001, the clock set then), because every package's tests share one database and retention deletes by age across it.

## Consequences

- Retention is the period from a call's start to the nightly run after it ends, so a call is kept up to a day past it: the 30th day's calls go at the 31st night's run. A30 checks 29 and 31 days.
- Counters (`Expired`, `Orphans`, `Shortened`) are in memory until `/metrics` exists (R90); the alert on "retention shortened" (R91) will read `Shortened`, and the `low_disk` row says when it last happened.
- The Settings page (R84) is M4's, on this API.
- The compose-based checks set retention to 3,650 days, because the fixture's calls are dated November 2025.
