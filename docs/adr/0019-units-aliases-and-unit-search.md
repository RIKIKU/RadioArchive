---
status: proposed
---

# 19. Units: aliases, where they're held, and what unit search may find

Date: 2026-11-25

Units come into M3 (owner, 2026-11-25, #75's question 1). A unit is a row of `units` (migration 00016): a system, a first and last radio ID (the same for one radio) and an alias. An exact unit beats a range that covers it, and of two ranges the narrower wins. The gate holds every alias in memory with the catalogue, at its generation, and names the units of the calls it gives: in the call details, recent calls, `index.csv` and the live events. Unit search (`GET /api/v1/units/search?q=`) finds units by alias or radio ID, but only radios heard on a call the public may have, so a radio heard only on a hidden talkgroup is never found. Aliases are set through the admin API only until the web admin (M4). These are the implementer's choices where `SPEC.md` P1, R26, R27, R32, R36, R46, R65 and A10 leave room (spec "How to use this spec", item 6). Built in [Units in recent calls: unit search and aliases](https://github.com/RIKIKU/RadioArchive/issues/83).

## Summary

| Part | Choice |
|---|---|
| **Table** | `units (system_id, first_id, last_id, alias)`, key all three ID columns, `last_id >= first_id`, `first_id > 0`. Alias up to 100 bytes, `''` for none, never `;`. Deleted with its system |
| **Which alias** | A radio's own row; else the narrowest range covering it (then the lowest); else none. An exact row with `''` has none, so its ID shows even inside a named range |
| **Shown as** | The alias, else the radio ID (R27). The call details' transmissions carry `"alias"` (null for none); the live events' `unit.name` is the alias, else the ID (ADR 0007's field); `index.csv`'s `unit_aliases` the alias or empty. In the browser, `unitsOf` (ADR 0017) is the one place a call's units become text |
| **Held** | In the gate's catalogue, rebuilt when the catalogue's generation moves; a trigger on `units` moves it (migration 00007's function). Never in the catalogue's answer |
| **Admin API** | `GET /api/v1/admin/systems/{system}/units`, and `GET`, `PUT` (`{"alias": "…"}`, `201` new or `200`) and `DELETE` on `…/units/{unit}`, where `{unit}` is `5001` or `5100-5199`. Logged without the alias (A32) |
| **Unit search** | `GET /api/v1/units/search?q=` (and `HEAD`), in `internal/public`, through `gate.SearchUnits`. `{"units": [{"system"?, "id", "alias"}]}`, at most 20, `no-store`; `system` only when there are several (R15). No talkgroup, no call. `q` over 100 bytes: `400 query_too_long`; empty: no units |
| **What it finds** | Radios heard on a call the call details would give: in `public_calls`, its talkgroup in the catalogue, and past any delay (R23). Matched by alias (whole, start of it or of a word, anywhere in it, ignoring case) or by radio ID (whole, or its start, when `q` is a number), best first |
| **The filter** | The Recent calls page's unit box is ArchiveUx's `Suggest` over unit search. Picking a unit puts its `[system.]id` in the address (`unit=5001`); the box and the summary show "Engine 1 (5001)". A radio ID typed works as before |

## Which units unit search may find (open point)

The owner answered that aliases come into M3, not which units unit search may find. The two readings: every unit in the table, or only units heard on public calls. Until the owner says otherwise, it's the second, which tells the least:

- A unit list is the admin's setup, and it names radios on every talkgroup, hidden ones too. "Covert radio" in a public answer says something exists that the public can't hear: P1 forbids "anything that reveals one exists".
- A listener filters recent calls by unit to find calls; a unit never heard on a public call finds none, so suggesting it helps no one.
- It's the more reversible choice: widening it later shows more, and takes nothing back that's been seen.

So a radio heard only on hidden 1003, ignored 1004, on a call patched with either, or on a delayed call within its delay, answers exactly as a radio never heard, by its alias and by its ID; A10's probes check that byte for byte. **Open point for the owner: may unit search list every unit with an alias, heard or not?**

## How unit search reads

It costs a bounded amount, whatever the archive holds:

1. The aliases that match `q` (from memory) give spans of radio IDs: an exact unit's one, a range's its span; a number gives the spans whose decimal form starts with it (`5`, `50-59`, `500-599`, …).
2. `GateUnitsIn` reads the radio IDs heard in each span by a skip scan of `transmissions_unit` (one index probe per radio, so a radio heard 10,000 times costs what one heard once does), at most 60 in all.
3. `GateUnitsHeard` reads, for each radio, at most 10 of its public calls from at most 100 of its transmissions, and the gate keeps a radio with one the call details would give (`Gate.held`'s rule: the catalogue and the delay, checked in Go, as ADR 0013 keeps it).
4. Each radio found is matched again on its own alias in its own system, so a range's alias never names a radio that an exact unit, or another system, names otherwise.

The bounds fail towards finding less, never more: a radio among its system's first 60 matches heard mostly on hidden calls may be missed. Timing: a radio heard only on hidden calls costs a few index probes more than one never heard, the kind of difference ADR 0004 and ADR 0013 accept.

## Why aliases are held with the catalogue

The live feed names a unit at every transmission, and recent calls name a page's units at once; a query per unit, or a join in the search (which ADR 0013 took out for the planner's sake), would cost more than a map. Units change rarely and number a few thousand at most. Keeping them at the catalogue's generation reuses the check every request already makes (R74), so a changed alias is seen at the next request on every server, as a renamed talkgroup is. Considered: a generation of their own (one more read per request, for nothing), and joining in SQL (above).

## Why ranges may overlap

R26 says only that an exact ID beats a range. Letting the narrower of two ranges win extends that rule (an exact unit is the narrowest range), lets an admin name a sub-fleet inside a fleet, and needs no check across rows, so a PUT can't be refused for a row it doesn't name. Two ranges of the same width that overlap: the lower first wins, so the answer is always the same.

## How each choice is verified

| Choice | Verification |
|---|---|
| R26's ranges (5001 "Engine 1", 5150 "Portables", 5200 its ID), the narrower range, an empty exact alias, another system's alias, a change seen at the next request; the filter by 5001 | `TestIntegrationUnitAliases` |
| What unit search finds and its order; hidden, patched, delayed (and after the delay), hiding a talkgroup; a long query | `TestIntegrationUnitSearch`, `TestIntegrationUnitSearchSystems` |
| The admin API and its refusals | `TestIntegrationAdminUnits` |
| A10: unit search by a unit's ID, alias and the start of it, a word and a digit every unit shares, `HEAD`; signed in too | `a10Probes["GET /api/v1/units/search"]`, `TestIntegrationSignedInSameAnswers`; `e2e-a10.sh` in the binary |
| Live events name the alias | `TestIntegrationLiveA5` |
| `index.csv`'s aliases | `TestIntegrationZipA18` |
| The filter picked from unit search, the rows, the card, nothing of 1003's units suggested; axe at 375 and 1,280 px in both looks and themes | `web/tests/units.spec.ts` |

## Consequences

- The web admin's unit table, import and export (R50-R53) are M4's, on this table and these routes.
- A unit filter names one radio. Filtering by a range's alias (every "Portables") isn't offered.
- The browser test fixture's 1002 calls carry the fixture's units; 9003 "Covert radio" is heard only on 1003.
