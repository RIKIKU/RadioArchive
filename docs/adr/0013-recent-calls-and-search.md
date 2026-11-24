---
status: proposed
---

# 13. Recent calls and search: the route, its paging cursor, the default and calls in progress

Date: 2026-11-24

Recent calls and search are one public route, `GET /api/v1/recent-calls`, reading through a gate method (`gate.RecentCalls`) over `public_calls`. It pages by start time with an opaque cursor that names a position, not a call. When no talkgroup is named it searches every public talkgroup; the browser names its listening set when it wants that default (R46, P4). A call in progress is listed, as the call details list it; a delayed talkgroup's call is not, until its delay has passed since it ended. To make a month's search fast, migration 00012 rewrites `public_calls`'s condition, still as call_state's one rule, and adds the indexes the filters need. These are the implementer's choices where `SPEC.md` P1, R16, R23, R30, R32, R46, R60, R73, R74, R78 and section 13 leave room (spec "How to use this spec", item 6). Built in [Recent calls and search behind the gate](https://github.com/RIKIKU/RadioArchive/issues/76).

## Summary

| Part | Choice |
|---|---|
| **Route** | `GET /api/v1/recent-calls` (and `HEAD`), in `internal/public`, through `gate.RecentCalls`, which reads `public_calls` only (ADR 0004). `Cache-Control: no-store` |
| **Parameters** | `talkgroups` (comma-separated `[system.]id`, at most 1,000), `from` and `to` (RFC 3339; `from` included, `to` not), `unit` (`[system.]radio ID`), `order` (`newest`, the default, or `oldest`), `cursor`, `limit` (1-100, default 100). Any other parameter is refused |
| **Answer** | `{"calls": [...], "next": "<cursor>" \| null}`. Each call is exactly the call details' call (ADR 0004): one shape for the browser to render |
| **The default** | No talkgroup named: every public talkgroup. No range: the 31 days up to now; `from` alone runs 31 days on from it; `to` alone, 31 days up to it |
| **Talkgroup filter** | A call matches when its own talkgroup or any in its patch is named (R16, ADR 0007). Names are read against the catalogue: only public talkgroups match anything, so a hidden or ignored talkgroup's ID is a talkgroup that doesn't exist |
| **Paging** | By `(started_at, id)`, newest or oldest first. The cursor is the last row's start time (microseconds) and call ID, 24 bytes in base64url: 32 characters. It's never looked up |
| **Calls in progress** | Listed, with `in_progress: true` and no recording, as the call details give them. A delayed talkgroup's call is held until its delay has passed since it **ended** (R23), so never while it's in progress |
| **Bounds (R73)** | A range over 31 days: `400 range_too_long`, "Search at most 31 days at once. Try a narrower time range." At most 100 rows a page. The whole search (catalogue and every query) has 5 s, as a context deadline and as the transaction's `statement_timeout`; running out is `503 search_too_slow`, "That search took too long. Try a narrower time range." |
| **Errors** | R78's `{"error", "message"}`, `400` with a stable code per parameter. Every refusal depends on the request's form only, never on what the deployment holds |
| **Speed** | Migration 00012: `public_calls`'s condition as two `NOT EXISTS`, the same rule as `call_state`; indexes on `calls (started_at, id)`, `calls (talkgroup, started_at)`, a GIN on `calls (patched_talkgroups)`, `transmissions (unit)`, and the non-public talkgroups. The search's queries join nothing (names come from the catalogue) and run in a read-only transaction with custom plans |

## Why one route, and why GET

R46 has one list with filters, and A10 lists "search" and "recent calls" as surfaces; with no free text (R46), a search is the recent-calls list with filters, so they're one route with one set of probes. It's a `GET` with a query string: nothing in it is private (talkgroup IDs and times), a browser can retry it, and load more is the same URL with a cursor. R67's QUERY and POST are for lists of call IDs; this takes none.

The 1,000-talkgroup cap is the deployment's size (section 2). A full listening set of 4-digit IDs is about 5 KB of query string, inside nginx's default 8 KB header buffers (R85); with a system prefix on each, a listening set of 1,000 talkgroups in several systems could pass it. That's the page's to watch (#79): a listening set that large is better sent as no filter, which is every public talkgroup. (ADR 0016 chose otherwise: `QUERY` and `POST` take the same search as a JSON body, through the same code, and the page sends one when the GET would be too long.)

## The default (R46, P4)

R46's default is the listening set, which only the browser holds (P4), so the browser names its talkgroups. Naming none means every public talkgroup: it's what "no filter" means everywhere else in R46, A4 asks for a patched call "listed under no filter", and it's what a listener with an empty set, or a call link's "recent calls" for a talkgroup they don't follow, needs. Refusing it would have made the browser send all 1,000 IDs to say the same thing. A filter that names talkgroups, none of them public, answers an empty page at once without a query, so a hidden ID costs exactly what an unknown one does.

A patched ID that the system doesn't list has no state (ADR 0003), so it can be in a public call's patch; it isn't in the catalogue, so naming it matches nothing. The filter is over public talkgroups, as R46 says.

## Paging by start time

R73 pages by start time, and A22 asks that load more over calls sharing a start time neither repeats nor skips one. Start times alone can't do that: two recorders, or two calls on one, may start in the same microsecond. So the order is `(started_at, id)`, and the cursor is that pair for the last row given; the next page is the rows strictly after it in the page's order. Call IDs are random UUIDs (R60), so they break ties in no meaningful order, but in the same order every time.

The cursor is a position, not a call. The server never looks the named call up, so a cursor made from a hidden call's start time and ID gives the same page as one made from a deleted call's at the same moment, and a cursor tells nothing the list didn't. It carries no filters: the browser sends the same filters with it. A cursor that doesn't decode is `400 invalid_cursor`.

Consequences of paging by start time, all ordinary for a list that's being written to:
- A call that starts after the first page was read appears on a fresh search, not in load more of a newest-first list.
- A delayed call is released (R23) into the list at its start time, which may be on a page already read; it appears on a fresh search.
- A call hidden or deleted between pages is simply not on the next page; nothing shifts, because the cursor is a position and not an offset.

## Calls in progress, and the delay

The route lists what the call details give (ADR 0004): every public call, recorded or not. A call in progress is listed with `in_progress: true`, `ended_at`, `duration_ms` and `recording` null, as its call link will show it ("In progress", R61). Holding calls in progress back would have made the list disagree with the call details and the live feed, which both have them, and a call that's live is the one a listener most wants to find. Its row has no recording yet, so the page offers live listening for it, not playback (#79).

The gate holds back what the call details hold back (`Gate.held`): a talkgroup the catalogue doesn't have yet, and a delay in the call's patch that hasn't passed since the call ended (R23, `delayHolds`). That's checked in Go, on the catalogue's delays, so the rule stays in one place (ADR 0006). Rows it holds back are skipped and the page is filled from the rows after them, in batches of a page plus one, so a held call never shortens a page or ends the list early, and `next` is given only when another public call exists. Hiding takes effect at the next request (R74): nothing is cached, and the catalogue is checked against its generation on every search.

## Speed: a month of calls

ADR 0004 asked for `public_calls` to be checked over a month of calls, because it evaluated `call_state` per row. Checked on a month at the spec's scale (two recorders at issue #21's middle row: 600,000 calls over 31 days, 1.8 million transmissions, 1,000 talkgroups of which 50 hidden and 5 ignored, 10 delayed), with PostgreSQL 18's defaults:

- **`call_state` per row was the cost.** It's a SQL function the planner can't inline (its body reads a table), so each row ran a query of its own: 50-130 µs. Reading the month's calls through the old view took 28 s (82 s with no parallel workers). A page that stops after a hundred rows is cheap either way, but any search that has to read many calls (a filter that matches few, or many hidden calls in a row) pays that per call, and the planner, which can't estimate a function's result, guesses 0.5 % of calls are public and plans for that.
- **The rewrite.** The view's condition is now "no talkgroup of the patch is listed as other than public", as two `NOT EXISTS`: the call's own talkgroup against the non-public talkgroups, which the planner can hash or probe by index; and, only for a patched call, its patch. That's `call_state`'s rule (the strictest state is public exactly when no listed talkgroup is hidden or ignored), and `TestPublicCallsIsCallState` checks it agrees with `call_state` for every combination of states, patches and systems. Reading the whole month through the new view takes 0.2-0.7 s.
- **The indexes.** `(started_at, id)` serves the page order and the cursor (and retention, which used `started_at`). A talkgroup filter is a bitmap OR of `(talkgroup, started_at)` and the GIN on `patched_talkgroups`. A unit filter is `transmissions (unit, call_id)`. On this month they're about 120 MB together, 90 MB of it the unit index (1.8 million transmissions); disk, not memory, and about 200 bytes a call beside R88's sizing formula's 2.1 KB.
- **No joins in the search.** The call details' query joins the call's system, talkgroup and tag for their names. In a search those joins' row estimates (about 1 call in 440 surviving the talkgroup join) made the planner think a page of 20 busy talkgroups was rare, so it gathered and sorted their whole month (160,000 calls, 0.7-1 s) instead of reading the newest hundred of the `started_at` index (6 ms). The search's queries read `public_calls` alone, and the gate names each call's system and talkgroup from the catalogue, which every listed call's talkgroup must be in anyway (ADR 0006's consequence: recent calls take talkgroup settings from the catalogue).
- **The unit filter** reads the unit's calls from `transmissions (unit, call_id)` first, an index-only read of up to 2,001 IDs. Up to 2,000 (none included): the search takes them as `id = ANY (...)`, so it costs what the unit has, not what the range has. More: the unit is common (a third of a percent of a month's calls or more), so the search asks `EXISTS` call by call while it reads the range in order, and finds a page soon. Left to the planner, an `EXISTS` alone read the whole month for a rare or never-heard unit (3-5 s cold, past the timeout once), and the list of IDs alone fetched all 11,700 of the busiest unit's calls (4-5 s cold).
- **Custom plans.** pgx prepares statements, and PostgreSQL may switch to a generic plan after five runs, which must allow for every filter at once: a unit search planned generically took 3.7 s. Each search sets `plan_cache_mode = force_custom_plan` for its transaction, so a filter left out is a NULL the planner folds away. Planning costs about a millisecond.

The plans and timings, cold and warm, from an empty database migrated and then filled, are in the pull request.

## A10

The harness probes the route around each call (`recentProbes`): a range holding only that call (both orders), every talkgroup, filters naming its talkgroup alone, beside 1001 and by system (1003 or 1005 for the hidden and ignored calls, including 1001 patched with 1003; 4711, which doesn't exist, for the deleted call), a unit heard only on it (alone and by system), cursors made from it (both orders), a page of one, and `HEAD`. Every unavailable call's answer must equal the deleted call's, status, headers and body, and none may contain 1003, 1005, their names, their calls' IDs or audio. `TestIntegrationSignedInSameAnswers` replays them signed in. `e2e-a10.sh` checks the binary too: the public call listed, none of 1003's, and a filter naming 1003 answering as one naming 4711.

Timing: a unit filter reads the unit's call IDs whatever their talkgroups, then runs the search with them; a unit never heard runs it with none, so it costs the same two queries as a unit heard only on hidden calls. Only the number of IDs differs (a few hidden ones cost microseconds more), the kind of difference ADR 0004 accepts. Whether a unit's existence may be found at all is #75's open question 1, for unit search.

## Left out

- **Unit aliases and unit search** are #83's, waiting on #75's open question 1. The unit filter takes a radio ID only.
- **Play from here** is the page's (#79), waiting on #75's open question 3; the route already gives the following calls in either order.
- **Request limits** (R73's 120 a minute and its `429`) are M5's. Only the bounds A22 needs are here.

## Consequences

- A new public read of calls should use `public_calls`, as before; its condition is now the rewritten one. `call_state` stays for ingest and the live feed, which ask about one call and need to know which state (ignored, to refuse it).
- The page (#79) sends its listening set as `talkgroups`, the filters as they are, and `next` as `cursor` for load more.
- Ingest writes three more index entries per call and one per transmission; at a few calls a second that's nothing.
