---
status: proposed
---

# 6. Talkgroup settings, the catalogue and the fireground lookup

Date: 2026-11-24

A talkgroup with no alert or LED colour of its own takes its **tag's**, then its **groups'** (in the groups' order), then its **system's**; an explicit `none` stops there. Sort order isn't taken from anywhere: each thing's sort order orders it among its own kind. The public catalogue is built from one SQL view, `public_talkgroups`, through `internal/gate`, held in the API's memory and checked against a generation number on every request. The fireground lookup reads the same catalogue. These are the implementer's choices where `SPEC.md` R15, R22-R24, R42, R74, R77, R100, R101 and section 13 leave room (spec "How to use this spec", item 6). Built in [Talkgroup listener fields, the public catalogue and fireground lookup](https://github.com/RIKIKU/RadioArchive/issues/55).

## Summary

| Part | Choice |
|---|---|
| **New fields** (migration 00007) | Talkgroups: `delay_minutes` (0-1,440), `alert`, `led_colour`, `sort_order`, `fireground_channel`. Tags and systems: `alert`, `led_colour`, `sort_order`. A `groups` table (name, alert, LED colour, sort order) and `talkgroup_groups` |
| **LED colours** | `red`, `orange`, `yellow`, `green`, `cyan`, `blue`, `purple`, `pink`, or `none`. Names only; the same names as ArchiveUx 0.3.0's LED tokens (`--led-<name>`), which define what they look like |
| **Alerts** | `beep`, `chime`, `pip`, `tone`, or `none`. Names only; the frontend ships the sounds |
| **Unset (null)** | Taken from the tag, then the groups in order, then the system. `none` means no alert or no colour, and isn't taken from anywhere |
| **Sort order** | A whole number, default 0, ties broken by ID or name. A talkgroup's orders it within its system; a tag's, group's and system's order them among tags, groups and systems. Not inherited |
| **Delay** | The talkgroup's own, in whole minutes, default 0. Not inherited |
| **Fireground channel number** | A whole number from 0, unique across the deployment (a partial unique index), stored as an integer so leading zeros don't count. The admin API takes a JSON number or a string of digits (`"041"`) |
| **Groups** | Named on a talkgroup by name, and created on first use, as tags are (ADR 0003). Also `GET`/`POST /groups`, `PATCH /groups/{id}`; `GET /tags`, `PATCH /tags/{id}` for their settings |
| **The catalogue's data** | The view `public_talkgroups`: talkgroups whose `call_state(system, talkgroup, '{}')` is public (ADR 0003's one rule). The gate's catalogue query reads that view, never `talkgroups` |
| **Freshness** | `catalogue_generation`, one row, moved by deferred triggers at the commit of any change to talkgroups, their groups, groups, tags or systems. The gate reads it on every request and rebuilds when it has moved |
| **Endpoints** | `GET /api/v1/catalogue`, `GET /api/v1/catalogue/search?q=`, `GET /api/v1/fireground/{number}`. All `Cache-Control: no-store` |

## How a talkgroup takes its settings

R22 gives a talkgroup exactly one tag and any number of groups, and gives tags, groups and systems an alert and an LED colour too. The order chosen goes from most to least particular to the talkgroup, with the single answer before the several:

1. **Its own.** What the admin set on the row.
2. **Its tag.** A talkgroup has exactly one, and it says what kind of traffic is on it (dispatch, fireground). An alert or a colour is about the traffic a listener is hearing, so it comes first after the talkgroup's own, and it's never ambiguous.
3. **Its groups, in the groups' order** (sort order, then name). Groups say whose or where a talkgroup is. With several, the first that sets one wins, so the admin decides which group's colour shows by ordering the groups.
4. **Its system.** The broadest default, for a deployment that wants one colour per network.

Each setting is taken on its own: a talkgroup can take its alert from its tag and its colour from a group.

Unset is `NULL`, which takes the next one. `none` is a value of its own, meaning "no alert" or "no colour", which stops the search there. Without it, an admin couldn't turn off the alert a tag gives every one of its talkgroups for one talkgroup.

Considered and not chosen: groups before the tag (a talkgroup in two groups with different colours would be decided by group order before its one unambiguous tag), and combining several colours (R22 shows one mark).

Sort order isn't inherited, because it means something different at each level: a talkgroup's orders it among talkgroups, a group's orders groups. The catalogue lists systems by their sort order, then talkgroups by theirs, then by ID; its tag and group lists go by their own sort order, then name. Delay isn't inherited either: R23 states it as the talkgroup's, and a delay a talkgroup took silently from its tag would hide its calls from the live feed without anyone having set it there.

## Colours and sounds

About 8 named colours (R22), which ArchiveUx will define as tokens measured as marks (section 11.2, item 5). The database stores the name, so ArchiveUx can change what a colour looks like without a migration. The names are common hues a reader can tell apart by name, which matters because the mark is never the only cue. Adding one later is a migration (the checks) and a line in `store.LEDColours`.

The sounds are short, distinct and not alarming: `beep`, `chime`, `pip`, `tone`. The frontend ships them; the server only stores and passes on the name.

## The catalogue goes through the gate

ADR 0004 makes `public_calls` the only way the public reads calls. The catalogue does the same for talkgroups:

1. **The data.** `public_talkgroups` is the talkgroups whose state is public by `call_state`, the same function `public_calls` uses, for a talkgroup on its own. It has no state column. The gate's one catalogue query (`GateCatalogue`) reads it and joins the talkgroup's own tag, groups and system. Tags, groups and systems are listed only when a public talkgroup names them, so a group whose talkgroups are all hidden isn't named (P1: nothing that reveals one exists).
2. **The code.** `internal/public` asks `gate.Catalogue` and gets a `*gate.Catalogue`, which holds only public talkgroups. Its search and fireground lookup work on that, so they can't find what isn't there.
3. **The routes.** The three routes are `public.API`'s, so `server.API` mounts them as public routes and the binary's test checks it serves them.
4. **The tests.** The A10 harness has probes for all three: the whole catalogue (`GET` and `HEAD`), search by 1003's ID, name, label, tag and group and by ignored 1005's name, an empty query and none, and the fireground lookup, where a public call's probe asks for 41 (and `041`), a deleted call's for 99, and every hidden or ignored call's for 1003's or 1005's number. Every answer must be free of 1003, 1005, their names, labels, tag and group, and the hidden and ignored numbers' answers must equal 99's byte for byte.

## Held in memory, fresh at every request

R74 asks for the catalogue in server memory, rebuilt on every talkgroup edit, so hiding takes effect at once. A rebuild called from the admin API's handlers would miss every other writer: auto-create in ingest, the import (M4), and `psql`, which the end-to-end check uses; and it would miss the other API servers of a deployment that runs several.

So the database says when the catalogue has changed. `catalogue_generation` is one row. Triggers on `talkgroups`, `talkgroup_groups`, `groups`, `tags` and `systems` add one to it, and the gate reads it (one row by primary key) before every catalogue answer and rebuilds when it differs from the one it holds. It's read before the talkgroups, so a catalogue built just after an edit commits is never kept under an older number than its data. It's a table and not a sequence: a sequence moves before its transaction commits, and a catalogue built in that moment would keep the old rows under the new number until the next edit.

The triggers are row-level constraint triggers deferred to the commit, so the generation's row is the last lock a transaction takes. With statement triggers an admin's edit and an ingest transaction could each hold a row the other needs while waiting for the generation's row. The cost is one update of the same row per changed row at commit: 1,000 for an import of 1,000 talkgroups, a few milliseconds.

The public package keeps the encoded catalogue for the catalogue it was built from, so the same 1,000 talkgroups aren't encoded again for every request. About 1,000 talkgroups are about 150 KB, in one response with no paging (R77).

## Search (R42, R77)

`GET /api/v1/catalogue/search?q=` matches the query, ignoring case, against each public talkgroup's name (as R24 shows it), label, tag and decimal ID. Talkgroups it names exactly come first, then those with a field or a word that starts with it, then those that contain it, each in the catalogue's order, so a voice command's "Dispatch North" finds Dispatch North first. An empty or missing query gives every talkgroup. A query over 100 bytes is refused (`400 query_too_long`), so a long one can't cost 1,000 long comparisons. Groups aren't searched: R42 browses by group and searches by name, label, ID or tag.

## The fireground lookup (R100, R101, R33)

`GET /api/v1/fireground/{number}` reads the number as decimal digits only, leading zeros not counting, up to 16 digits. A number a public talkgroup has answers `200` with the talkgroup as the catalogue shows it. Anything else (no talkgroup has it, its talkgroup is hidden or ignored, or it isn't a number) answers one `404 {"error": "channel_unavailable", "message": "This channel isn't available."}` with the same headers. Every case is one lookup in the same in-memory map, which never holds a hidden talkgroup's number, so there's no second query or file for a hidden number to cost. The frontend's `?fireground=` link (R101, issue #56) uses it.

A duplicate number is refused in the admin API with `409 fireground_channel_taken`, naming the talkgroup that has it: `Fireground channel 41 is already talkgroup 1002 "Fireground 41" in system EX.` The check runs in the edit's transaction; a race between two edits is caught by the unique index and answered the same way.

## What a listener sees of systems (R15)

The catalogue, search and fireground answers carry `systems` and each talkgroup's `system` ID only when more than one system has public talkgroups. A system whose talkgroups are all hidden doesn't count, so it isn't revealed by a second system's name appearing.

## Consequences

- The live feed (#59) and recent calls must take a talkgroup's name, group, LED colour, alert and delay from the gate too; the catalogue's effective settings are the ones to use, so the rules live in one place (`gate.catalogueOf`).
- The admin table (M4) shows each talkgroup's own settings, with null meaning "taken from elsewhere". Showing the effective value beside it is the admin UI's to decide.
- The test fixture (section 8) now has group "North" on 1001 and fireground channel number 41 on 1002. Server tests each get a database of their own (`storetest.FreshURL`), because the catalogue and fireground numbers are the whole deployment's.
- A14 needs only a talkgroup's `delay_minutes`, which the admin API sets; the delay itself is the live feed's (#59).
