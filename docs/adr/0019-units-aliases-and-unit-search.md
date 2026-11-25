---
status: accepted (2026-11-25, by the owner)
---

# 19. Units: aliases, where they're held, and what unit search may find

Date: 2026-11-25

> **Amended 2026-11-25 by the owner's ask** that unit search work as PagerArchive's selection field does ([#119](https://github.com/RIKIKU/RadioArchive/issues/119)). A radio that only a range alias covers is offered by its radio ID with the range beside it ("5150 in Portables"), never by the range's alias. A range alias is offered as itself when its alias or its radio IDs are typed ("Portables", "5100-5199", "5100-"); a number alone never offers one. Picking it filters recent calls to the calls with any radio in it: `unit` takes `[system.]first-last`. A range is found only when one of its radios is heard on a call the public may have. Where this ADR says a unit filter names one radio, or that a range's alias names the radios it covers in unit search, that no longer holds; [Ranges in unit search and the filter](#ranges-in-unit-search-and-the-filter) has the rest, and the rest stands.

Units come into M3 (owner, 2026-11-25, #75's question 1). A unit is a row of `units` (migration 00016): a system, a first and last radio ID (the same for one radio) and an alias. An exact unit beats a range that covers it, and of two ranges the narrower wins. The gate holds every alias in memory with the catalogue, at its generation, and names the units of the calls it gives: in the call details, recent calls, `index.csv` and the live events. Unit search (`GET /api/v1/units/search?q=`) finds units by alias or radio ID, but only radios heard on a call the public may have, so a radio heard only on a hidden talkgroup is never found. Aliases are set through the admin API only until the web admin (M4). These are the implementer's choices where `SPEC.md` P1, R26, R27, R32, R36, R46, R65 and A10 leave room (spec "How to use this spec", item 6). Built in [Units in recent calls: unit search and aliases](https://github.com/RIKIKU/RadioArchive/issues/83).

## Summary

| Part | Choice |
|---|---|
| **Table** | `units (system_id, first_id, last_id, alias)`, key all three ID columns, `last_id >= first_id`, `first_id > 0`. Alias up to 100 bytes, `''` for none, never `;`. Deleted with its system |
| **Which alias** | A radio's own row; else the narrowest range covering it (then the lowest); else none. An exact row with `''` has none, so its ID shows even inside a named range |
| **Shown as** | The alias, else the radio ID (R27). The call details' transmissions carry `"alias"` (null for none); the live events' `unit.name` is the alias, else the ID (ADR 0007's field); `index.csv`'s `unit_aliases` the alias or empty. In the browser, `unitsOf` (ADR 0017) is the one place a call's units become text |
| **Held** | In the gate's catalogue, rebuilt when the catalogue's generation moves; a trigger on `units` moves it (migration 00007's function). Never in the catalogue's answer |
| **Admin API** | `GET /api/v1/admin/systems/{system}/units`, and `GET`, `PUT` (`{"alias": "…"}`, `201` new or `200`) and `DELETE` on `…/units/{unit}`, where `{unit}` is `5001` or `5100-5199`. Logged without the alias (A32) |
| **Unit search** | `GET /api/v1/units/search?q=` (and `HEAD`), in `internal/public`, through `gate.SearchUnits`. `{"units": [...]}`: a radio `{"system"?, "id", "alias", "in"?}`, a range `{"system"?, "first", "last", "alias"}`. At most 20, `no-store`; `system` only when there are several (R15). No talkgroup, no call. `q` over 100 bytes: `400 query_too_long`; empty: no units |
| **What it finds** | Radios heard on a call the call details would give: in `public_calls`, its talkgroup in the catalogue, and past any delay (R23). Matched by their own alias (whole, start of it or of a word, anywhere in it, ignoring case) or by radio ID (whole, or its start, when `q` is a number), best first; a radio with no unit of its own carries `in`, its range's alias. Range aliases with a radio so heard, by alias or by their radio IDs (`5100-5199`, `5100-`), never by a number alone (amended 2026-11-25) |
| **The filter** | The Recent calls page's unit box is ArchiveUx's `Suggest` over unit search. Picking a unit puts its `[system.]id` in the address (`unit=5001`), a range its `[system.]first-last` (`unit=5100-5199`); the box and the summary show "Engine 1 (5001)", "5150 in Portables" or "Portables (5100-5199)". A radio ID or a range typed works too |

## Which units unit search may find (open point)

The owner answered that aliases come into M3, not which units unit search may find. The two readings: every unit in the table, or only units heard on public calls. Until the owner says otherwise, it's the second, which tells the least:

- A unit list is the admin's setup, and it names radios on every talkgroup, hidden ones too. "Covert radio" in a public answer says something exists that the public can't hear: P1 forbids "anything that reveals one exists".
- A listener filters recent calls by unit to find calls; a unit never heard on a public call finds none, so suggesting it helps no one.
- It's the more reversible choice: widening it later shows more, and takes nothing back that's been seen.

So a radio heard only on hidden 1003, ignored 1004, on a call patched with either, or on a delayed call within its delay, answers exactly as a radio never heard, by its alias and by its ID; A10's probes check that byte for byte. **Open point for the owner: may unit search list every unit with an alias, heard or not?**

## How unit search reads

It costs a bounded amount, whatever the archive holds:

1. The aliases that match `q` (from memory) give spans of radio IDs: an exact unit's one, a range's its span; a number gives the spans whose decimal form starts with it (`5`, `50-59`, `500-599`, …). A range matches by its alias, or by its radio IDs when `q` is a range typed; never when `q` is a number.
2. `GateUnitsIn` reads the radio IDs heard in each span by a skip scan of `transmissions_unit` (one index probe per radio, so a radio heard 10,000 times costs what one heard once does), at most 60 in all, at most 20 of them for one range.
3. `GateUnitsHeard` reads, for each radio, at most 10 of its public calls from at most 100 of its transmissions, and the gate keeps a radio with one the call details would give (`Gate.held`'s rule: the catalogue and the delay, checked in Go, as ADR 0013 keeps it).
4. Each radio found is matched again on its own alias (its unit's, never a range's) in its own system, so a range's alias never finds a radio, and another system's alias never names one. A range is found when one of the radios read for it is kept in step 3, in the range's own system.

The bounds fail towards finding less, never more: a radio among its system's first 60 matches heard mostly on hidden calls may be missed, and so may a range whose first 20 radios heard are heard mostly there. Timing: a radio heard only on hidden calls costs a few index probes more than one never heard, the kind of difference ADR 0004 and ADR 0013 accept.

## Ranges in unit search and the filter

Added 2026-11-25 ([#119](https://github.com/RIKIKU/RadioArchive/issues/119)): the owner asked that unit search work as PagerArchive's selection field does. There a capcode that only a range alias covers is offered as the capcode, "in" the range, and a range alias is offered when its name or pattern is typed, which is choosing the range on purpose; typing a capcode never offers a range. Here:

- **A radio only a range covers** (no unit of its own) is found by its radio ID, as `{"id": 5150, "alias": null, "in": "Portables"}`, and suggested as "5150 in Portables". Its calls still name it "Portables" (R27): `in` is that same alias, the narrowest range's. Its range's alias doesn't find it. Before this, typing "Portables" listed each radio heard in it, named "Portables", which the listener couldn't tell apart; now it offers the one range they meant.
- **A range alias** is found by its alias, matched as a radio's is, or by its radio IDs typed as a range: `5100-5199` exactly, or the start of it (`5100-`, `5100-51`), spaces ignored. A number alone (`5100`, `51`) is a radio ID and never offers a range, so a listener after one radio isn't handed a fleet. A range with no alias is no range alias: it's never offered, though its span may still be typed.
- **An exact alias beats a range**, as before: 5001 "Engine 1" inside 5000-5009 "Engines" is still "Engine 1 (5001)", and typing "engine" offers both, the radio and the range.
- **The filter.** `unit` takes `[system.]first-last` (first < last, both radio IDs), in GET and in the QUERY and POST body, refused otherwise with `invalid_unit` as a malformed radio ID is. A call matches when any radio in the span is heard on it, in its own system when one is named. The span is the range's numbers, not "the radios that show as Portables": a radio inside with its own alias is still in it, because a listener choosing a fleet means every radio in it, and a span is what the address can say.
- **Its cost** is bounded as one radio's is (ADR 0013). `GateUnitCalls` reads the span's transmissions from `transmissions_unit` in its order, at most 20,000 rows, and the gate drops the repeats; up to 2,000 calls from fewer than that many rows go to the search as call IDs, and anything more is asked call by call (`EXISTS` with the span). So a span of `1-999999999999` costs what a busy radio does: it isn't capped by width, and needn't be. One radio is the span of one ID, so the route has one path. A busy radio's list was 2,001 distinct calls; it's now also at most 20,000 transmissions, which only a radio heard 10 times a call on 2,000 calls reaches, and then it's filtered call by call, as a busy radio is.

What the public may learn doesn't change. A range is offered only when one of its radios is heard on a call the call details would give, which is step 3's rule for radios; a range whose radios are heard only on hidden 1003, ignored 1004, a call patched with either, or a delayed call within its delay, answers by its alias and by its radio IDs exactly as a range never heard. Filtering by such a range runs the same two queries as filtering by an empty one, with a few more call IDs that `public_calls` then drops, the difference ADR 0013 accepts for one radio; the answer is byte for byte an empty range's. A10's probes check both.

Considered and not chosen: offering a range when a radio ID inside it is typed (the owner's rule: typing a radio ID never offers a range), and filtering a range by the radios whose alias is the range's (it would leave out a radio given its own alias inside a fleet, and the address couldn't say it).

## Why aliases are held with the catalogue

The live feed names a unit at every transmission, and recent calls name a page's units at once; a query per unit, or a join in the search (which ADR 0013 took out for the planner's sake), would cost more than a map. Units change rarely and number a few thousand at most. Keeping them at the catalogue's generation reuses the check every request already makes (R74), so a changed alias is seen at the next request on every server, as a renamed talkgroup is. Considered: a generation of their own (one more read per request, for nothing), and joining in SQL (above).

## Why ranges may overlap

R26 says only that an exact ID beats a range. Letting the narrower of two ranges win extends that rule (an exact unit is the narrowest range), lets an admin name a sub-fleet inside a fleet, and needs no check across rows, so a PUT can't be refused for a row it doesn't name. Two ranges of the same width that overlap: the lower first wins, so the answer is always the same.

## How each choice is verified

| Choice | Verification |
|---|---|
| R26's ranges (5001 "Engine 1", 5150 "Portables", 5200 its ID), the narrower range, an empty exact alias, another system's alias, a change seen at the next request; the filter by 5001 | `TestIntegrationUnitAliases` |
| What unit search finds and its order; hidden, patched, delayed (and after the delay), hiding a talkgroup; a long query | `TestIntegrationUnitSearch`, `TestIntegrationUnitSearchSystems` |
| Ranges in unit search: by alias and radio IDs, never by a number; "in" for a radio only a range names; a range kept while one radio is public, gone when none is, and one heard only on hidden, patched or delayed calls not found | `TestIntegrationUnitSearch` |
| The filter by a range (alone, by system, another system), call by call when busy, and its refusals | `TestIntegrationUnitAliases`, `TestIntegrationRecentCallsBusyUnit`, `TestIntegrationRecentCallsBounds` |
| The admin API and its refusals | `TestIntegrationAdminUnits` |
| A10: unit search by a unit's ID, alias and the start of it, a word and a digit every unit shares, `HEAD`; a range heard only on the call by its alias, its radio IDs and their start, a radio only it names, a word every range shares; recent calls by that range, alone and by system; signed in too | `a10Probes["GET /api/v1/units/search"]`, `recentProbes`, `TestIntegrationSignedInSameAnswers`; `e2e-a10.sh` in the binary ("Covert pair" against "Quiet fleet") |
| Live events name the alias | `TestIntegrationLiveA5` |
| `index.csv`'s aliases | `TestIntegrationZipA18` |
| The filter picked from unit search (a radio, a range, a radio "in" a range), the address, the rows, the card, nothing of 1003's units or "Covert fleet" suggested; axe at 375 and 1,280 px in both looks and themes | `web/tests/units.spec.ts` |

## Consequences

- The web admin's unit table, import and export (R50-R53) are M4's, on this table and these routes.
- A unit filter names one radio or one range of them (amended 2026-11-25; it named one radio only).
- The browser test fixture's 1002 calls carry the fixture's units; 9003 "Covert radio" is heard only on 1003, and 9000-9009 "Covert fleet" holds only radios heard where the public may not hear them.
- ArchiveUx's `Suggest` shows a value as one line of text, so "in Portables" is part of the suggestion's text, not a quieter hint beside it as PagerArchive's field shows it. A hint for values would be ArchiveUx's to add.
