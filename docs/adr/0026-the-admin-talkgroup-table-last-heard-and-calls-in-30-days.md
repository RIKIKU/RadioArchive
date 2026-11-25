---
status: proposed
---

# 26. The admin talkgroup table: last heard, calls in the last 30 days, and a talkgroup's calls

Date: 2026-11-25

The admin API answers every talkgroup of every system in one request, `GET /api/v1/admin/talkgroups`, each with every R22 field as the talkgroup holds it, its **last heard** and its **calls in the last 30 days**. Last heard is kept by ingest in a table of its own, so it survives retention; the 30-day count is counted from the calls and rejected calls kept, at each request. A talkgroup's calls, hidden and ignored ones included, are listed for admins newest first by `GET /api/v1/admin/systems/{system}/talkgroups/{talkgroup}/calls`, each playable through ADR 0004's admin playback. Neither route is audited or logged per request. These are the implementer's choices where `SPEC.md` R17, R22, R34, R50, R54 and R90 leave room (spec "How to use this spec", item 6). Built in [Admin talkgroup table data](https://github.com/RIKIKU/RadioArchive/issues/110).

## Summary

| Part | Choice |
|---|---|
| **The table** | `GET /api/v1/admin/talkgroups`: `{"calls_since": <time>, "talkgroups": [...]}`, every talkgroup of every system, by system ID then talkgroup ID, no paging and no parameters (R50, A23). Each row is the talkgroup as `GET /systems/{id}/talkgroups/{tg}` gives it, plus `last_heard_at` (null when never heard) and `calls_30_days` |
| **Heard** | A talkgroup is heard on a call when the call is **stored** with it as its own talkgroup or in its patch (R16), or when the call is **refused because the talkgroup is ignored** (the talkgroup ADR 0003's `rejected_calls` row names). A call refused as not attached or as an unknown talkgroup hears nothing: those are R12's and R17's counts |
| **Last heard** | The latest start time of a call it was heard on, never later than the server's clock. A row in `talkgroup_last_heard` (migration 00019), moved by ingest after each call is decided and never back. It survives retention |
| **Calls in the last 30 days** | The calls it was heard on that started in the 30 days up to the request (`calls_since` is where they begin), counted from `calls` and `rejected_calls` at each request. It counts what's kept, so it goes down as retention deletes |
| **Effective alert and LED colour** | Not in the row. The row has the talkgroup's own (null: taken from elsewhere, ADR 0006); the admin UI has the tags, groups and systems to show where it comes from |
| **Rejected calls** (R17) | `GET /rejections` as it is: per system and talkgroup, with ADR 0003's wording ("TG 4711: 38 calls rejected, not in the list") |
| **A talkgroup's calls** (R34) | `GET /systems/{id}/talkgroups/{tg}/calls[?limit=1-500&cursor=…]`: `{"calls": [...], "next": <cursor> \| null}`, newest first by `(started_at, id)`, 100 a page by default. Its own calls and those patched with it, in any state, each with its state now, and its recording's admin URL once it has one |
| **Audit and logs** | Neither route is audited (R34, R54: reading changes nothing, playback isn't audited) and neither logs per request; a failure logs the route's pattern and the database's SQLSTATE only, and ingest's failure to record last heard logs the system ID (R90) |

## What counts as heard

R50 asks for the two columns and the "not heard in N days" filter, to find talkgroups that have gone quiet and the busy ones. The issue named three cases to settle.

**A call heard only through a patch.** A patched call is stored once, under the talkgroup trunk-recorder recorded it on (R16), but a listener of any talkgroup in the patch hears it. So it counts for every listed talkgroup in its patch, as the public talkgroup filter matches it (ADR 0013). A patched ID the system doesn't list counts for nothing; if it's added later, it's heard from its next call.

**An ignored talkgroup.** Its calls are refused, not stored (R18), but they're on the air: an admin deciding whether to keep ignoring a talkgroup, or to stop trunk-recorder recording it, wants to know it's busy. So a call refused because a talkgroup is ignored counts for that talkgroup. When the call's own talkgroup is public and the ignored one is in its patch, `rejected_calls` names only the ignored one, so the call counts for it alone, and both columns agree. Calls an ignored talkgroup had stored before it was ignored count too, while they're kept.

**After retention.** A `max()` over `calls` would forget a talkgroup as soon as retention deleted its calls, and "not heard in 40 days" could never be true with 30 days' retention. So last heard is stored, as `recorders.last_call_at` is (ADR 0010), and moved at ingest. The 30-day count is counted from what's kept: with the default 30 days' retention that's the same thing, and with a shorter period it's the calls the admin can still list and play.

### Where last heard is moved

After the call's transaction commits, as `TouchRecorderCall` is: `store.Admit` says which talkgroups the call was heard on (`Admission.HeardTalkgroups`), and the live `call_start` and the recording's upload each pass them to one upsert, `TouchTalkgroupsHeard`, with the call's start time (the recorder's clock, or the server's if that's earlier). It takes the later of the two times, so a recording uploaded from a client's queue a day late doesn't move it back, and the live start and the recording of one call write the same time. A call already decided before (a resent `call_start`, a duplicate upload) moves nothing.

It's a table of its own rather than a column on `talkgroups`, because every change to a `talkgroups` row moves the catalogue's generation (ADR 0006) and the catalogue would be rebuilt at every call. Outside the transaction, so the upload doesn't hold a talkgroup's row while it stores a recording, and in talkgroup ID order, so two calls touching the same talkgroups can't deadlock. A failure is logged and the call is kept: last heard is an admin's hint, the call matters more. The migration fills it from the calls and rejected calls already kept, by the same rule (a second on a month).

A talkgroup patched in during a live call (a `transmission` or `call_end` that adds to the patch) is heard when the recording arrives with that patch, not at the event; its 30-day count includes the call as soon as the live row has it.

## The talkgroup's calls (R34)

An admin plays a hidden talkgroup's recordings from its row (R34, A13). The list is the admin API's: no public route reads it, and it doesn't go through the gate, which exists to keep these calls out. It lists the talkgroup's own calls and those patched with it, like the public filter, with each call's state now (`call_state`), so an admin can see that a public talkgroup's call is hidden because 1003 was patched in. Ignored talkgroups' calls stored before they were ignored are listed until retention deletes them (R30, R35). Calls in progress are listed with no recording.

The cursor is ADR 0013's: 32 characters of base64url, the last call's start time to the microsecond and its ID, a position that's never looked up. The query reads the talkgroup's own calls from `calls (talkgroup, started_at)` and the patched ones from the GIN index on `patched_talkgroups`, each half stopping at the page, and runs with custom plans in a read-only transaction, as the recent calls do: the generic plan read the patched half by start time across the month.

## Speed

On cld01, PostgreSQL 18 with its defaults, a database migrated from empty and then filled with ADR 0013's month: 600,000 calls over 31 days in start-time order (5 % patched, the busiest talkgroup 60,000 calls), 1.8 million transmissions, 1,000 talkgroups (50 hidden, 5 ignored), 20,000 calls refused as ignored, statistics as autovacuum left them. Timings through the real handler (`server.Admin` behind `server.API`, query and JSON). Cold is PostgreSQL stopped, `drop_caches`, started, then the one request.

| Request | Warm p50 | Warm p95 | Cold | Answer |
|---|---|---|---|---|
| The table, empty database (migrated, no talkgroups) | 0.2 ms | 1.0 ms | | 55 bytes |
| The table, 1,000 talkgroups, no calls | 6.1 ms | 8.3 ms | | 351 KB |
| The table, 1,000 talkgroups and the month | 122 ms | 138 ms | 1.38-1.42 s (10 rounds) | 384 KB |
| A talkgroup's calls, busiest, first page of 100 | 15 ms | 16 ms | 71-81 ms | 30 KB |
| A talkgroup's calls, rarest, first page of 100 | 15 ms | 16 ms | 90-94 ms | 30 KB |
| A talkgroup's calls, busiest, a page of 500 | 70 ms | 74 ms | | 152 KB |

Warm is 200 requests each. The answer isn't compressed.

The table's cost is the 30-day count: a month of calls read once (the plan is a parallel sequential scan of `calls`, then a second for the 5 % patched, each aggregated to 1,000 rows before they're added). Counting each kind on its own and adding the counts let the planner aggregate in parallel; a single `UNION ALL` of every row, then one `GROUP BY`, took 225 ms warm. Cold, it's reading the 142 MB table from disk. Two ways to make the cold case faster were measured and not taken:

- **A covering index** (`calls (system_id, talkgroup, started_at)`, 23 MB a month, and a partial one for patched calls). The month counted from it in 0.36 s cold, but only while the visibility map was current: with 1 % of the table not yet vacuumed (a few hours of calls between autovacuum's runs) the planner went back to the sequential scan, and written as one index probe per talkgroup, the heap fetches for the unvacuumed 10 % made it 2.1-3.8 s cold. The partial index alone saved 20 ms warm and nothing cold.
- **Counts kept by ingest**, per talkgroup and day. Fast either way, but a call's patch grows during a live call and is replaced by its recording's, the live call and its recording are separate writes, and a day's granularity blurs "the last 30 days". Exact counts would take triggers on `calls` in ingest's transactions.

So the table costs about a tenth of a second warm, and its first opening after the database starts costs under a second and a half on this disk. Ingest pays one small upsert per call for last heard.

## Consequences

- #73 serves these routes from the binary behind its admin check; until then they're tested in process with the test authenticator, as M1-M3's admin routes are. `Admin.WithClock` gives the 30 days the deployment's clock; without it, `time.Now`.
- The Talkgroups page (#113) searches, filters and sorts the table in the browser (A23); "not heard in N days" compares `last_heard_at` with now. It plays a call through `recording.url`.
- If the cold opening matters more than ingest's write cost and an index's space, a covering index with autovacuum tuned to keep the visibility map current (`autovacuum_vacuum_insert_scale_factor` on `calls`) is the next thing to try, with the measurements above repeated.
- An ignored talkgroup's refused calls count in `calls_30_days` and also in `GET /rejections`; the page can show either.

## Open points for the owner

1. **Calls in the last 30 days after a shorter retention.** It counts the calls kept, so with 7 days' retention it's at most 7 days of calls. Counting the full 30 days whatever the retention needs counts kept by ingest (above). The reversible choice is counting what's kept.
2. **The cold opening.** 1.4 s the first time after the database starts, on cld01's disk; about 0.1 s after. Accepted here as the warm costs of ADR 0013 were; the index above is the change if not.
