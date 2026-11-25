---
status: proposed
---

# 40. Metrics and their allowlist

Date: 2026-11-26

The API exports metrics for the monitoring stack (`SPEC.md` R90), as PagerArchive does: Prometheus text at `/metrics`, behind `METRICS_TOKEN`, scraped by Alloy. These are the implementer's choices where R90 leaves room (spec "How to use this spec", item 6). Built in [Telemetry: /metrics behind METRICS_TOKEN](https://github.com/RIKIKU/RadioArchive/issues/149).

## Summary

| Part | Choice |
|---|---|
| **Where** | `GET /metrics`, a route kind of its own (`MetricsRoute`): never public, never limited, outside the API's paths and the app's. The reverse proxy denies it (`docs/deploy.md`); Alloy scrapes it on the `monitoring` network |
| **Who** | `Authorization: Bearer <METRICS_TOKEN>`, compared in constant time; `401` otherwise. With no `METRICS_TOKEN` (or `METRICS_TOKEN_FILE`), `404`: metrics are off. A token under 16 characters stops the API at start |
| **Format** | Prometheus text 0.0.4, written by `internal/telemetry`, with no client library: about 45 metrics don't need one, and the binary stays small (section 7's budget) |
| **The allowlist** | Every metric is a `Desc` in `internal/telemetry/names.go`, with its name (`radioarchive_...`), type, help and label keys. Every label key is in `LabelKeys`. A collector writes only through a `Writer`, which drops (and counts in `radioarchive_telemetry_dropped_total`) an undeclared metric, the wrong number of labels, or a value its key doesn't allow |
| **Label values** | `system` and `talkgroup` are numbers. `recorder`, `other_recorder` and `short_name` are the names admins and trunk-recorder gave them: printable, at most 100 bytes. `version` is a version. Every other key takes a lower-case fixed word. No key names a talkgroup or a unit, or holds an address, a key or a token |
| **Collectors** | Each package writes its own: the recorder endpoints (signals, the client's queue, versions, uploads, rejected calls, missing recordings, overlaps, the live delay), the live feed, the limits, the retention job and the recordings volume, and the process |
| **Database reads** | The rejections and the missing recordings are read at most once a minute, the overlaps once in 5 minutes: a 15-second scrape doesn't repeat them |
| **No traces, no OTLP** | RadioArchive sends no traces and no logs anywhere. Its logs stay the container's JSON lines on stderr |

## What's exported

- **Recorders** (R6, R14), one series per active recorder: connected; each signal (disconnected, deaf, quiet) tripped; deaf by why (`no_heartbeat`, `decode_rate`); the quiet threshold where set; last call, last connection and last heartbeat; the client's version and whether it's out of date; the decode rate per system its plugin reports; and the client's queue (R5), entries and the oldest's age, from its last heartbeat. Counters: clients refused as too old (A29) and connections out of date, signals tripped, protocol events, and uploads by their answer.
- **Calls**: calls rejected since the start, by reason (R12, R17, R18); over the retention period, the rejections by recorder and `shortName` (R12) and by system and talkgroup (R17, R18), from `rejected_calls`; recordings missing (R19); overlapping calls in the last 24 hours (R13).
- **Live** (R36-R38, R71): the live delay as a histogram, frames sent and dropped, the feed's events, listener sockets open and the cap, whether the radio is full, zips open (R72), and limit hits per limit (R70-R73).
- **The volume** (R88): its size and free space, its state (ok, warning under 15 %, low, full), `RECORDINGS_MIN_FREE`, recordings deleted (expired, orphans, shortened), times "recordings volume full" was raised, and when the nightly run last finished.
- **The process**: the version, the Go runtime's heap and its memory from the system, goroutines, and the start time.

## Why recorders are named

Alerts say which recorder has stopped ("Recorder North hill disconnected", R91), and admins know recorders by their names, which the logs already carry. A recorder's name is set by the deployment's own admin, from the shell (R10). It is shown only to admins, and Grafana is for admins only, as PagerArchive's is. P1 forbids talkgroup and unit names, and none is exported.

## The live delay

R38's delay runs from the recorder plugin's first PCM packet to the browser's first decoded frame. The server can't see the browser, so the metric measures what it can: from the call's `first_pcm_ms`, which the client takes from the plugin, to the server passing the call's first frame to the live feed, on the two hosts' wall clocks. A6 measures the whole path in the browser. A call resent after the client reconnects counts its real delay, which listeners also had.

## Missing recordings

R19 counts a recording that hasn't arrived 10 minutes after its call ended. The gauge counts calls that ended between 10 minutes and 24 hours ago with no recording. It drops as soon as a recording arrives, and a call whose recording never comes stops counting after a day, so the alert clears without waiting for retention. The client keeps recordings for up to `QUEUE_MAX_AGE` (24 h, R5), so the window matches the longest a recording can still arrive.

## Why no traces

R90 asks for telemetry "as in PagerArchive", with no listener addresses in logs, traces or metrics. PagerArchive's traces follow a page from its receiver to the database. RadioArchive's work is a few calls at once, and metrics and logs show where it stands. OTLP would add the OpenTelemetry SDK to a binary under a 256 MB budget. A later release can add traces behind the same allowlist if the owner wants them.

## How each choice is verified

- `internal/telemetry`: a `Desc` with a key off the allowlist, a bad name or no help can't be registered; the `Writer` keeps a good sample and drops (and counts) a name as an ID, a name as a code, an address, a control character, too few labels and an undeclared metric; the histogram's buckets; and the handler's `404`, `401` and `200`, in Prometheus's text format.
- `internal/server`: A29's metric (`TestIntegrationA29Metrics`): a 1.4 server counts the refused 1.2 client and shows the 1.3 one as out of date, with its heartbeat's queue and decode rate, and as disconnected once it goes. A missing recording counts and clears when it arrives, and the live delay is observed (`TestIntegrationMissingRecording`).
- `internal/limits`: hits per limit, and the radio full while every socket is open.
- A32 (`testing/e2e`, `TestA32NoNamesInTelemetry`): the fixture's calls on every talkgroup and unit through the real client, listeners and the public API from a listener's address behind the proxy, admin edits and refusals; then no log line and no metric holds a talkgroup or unit name or the address. The audit harness (`cmd/radioarchive`) checks the admin routes' logs for every name an audit entry holds. CI's image job checks `/metrics` on the compose stack, and that neither it nor the API's log names a fixture talkgroup or unit after A1 and A10.
