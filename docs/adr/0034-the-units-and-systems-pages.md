---
status: proposed
---

# 34. The Units and Systems pages

Date: 2026-11-25

The web admin's **Units** and **Systems** pages, second and third in the Admin menu (R84), follow the Talkgroups page ([ADR 0031](0031-the-talkgroups-page.md)): one ArchiveUx `DataTable` per kind of thing, searched, filtered and sorted in the browser, one thing open at a time, saved as only what changed, and a destructive action asking first. They use the admin API as it stands ([ADR 0003](0003-systems-attachments-and-rejected-calls.md), [ADR 0006](0006-talkgroup-settings-and-the-catalogue.md), [ADR 0019](0019-units-aliases-and-unit-search.md)): no route, migration or audit kind is added, and every change they make goes through routes already audited ([ADR 0029](0029-the-audit-log.md)). Groups and tags are sections of the Systems page. Units get no last heard. These are the implementer's choices where `SPEC.md` R11, R12, R15, R21, R22, R26, R27, R50, R80, R83 and R84 leave room (spec "How to use this spec", item 6). Built in [The Units and Systems pages, with groups and tags](https://github.com/RIKIKU/RadioArchive/issues/114).

## Summary

| Part | Choice |
|---|---|
| **The menu** (R84) | Talkgroups, Units, Systems, Recorders, Accounts, Audit log, Settings. Units' icon is an ID card, Systems' a network (both after Tabler, in `icons.ts`) |
| **Units: its data** | `GET /systems`, then `GET /systems/{id}/units` for every system at once, when the page opens; nothing more until a change. Every unit of every system in one table |
| **Units: the table** | Unit (the row's name: `5001` or `5100-5199`), System (only with several), Radios (how many it covers), Alias ("No alias" for `''`). Each sorts. Drawn a hundred at a time, with Show 100 more and Show all, as ADR 0031's "Speed" has it |
| **Units: search and filter** | A part of the radio ID or range, or of the alias, ignoring case; a whole radio ID typed also finds the ranges covering it ("5150" finds 5100-5199). System, with several. "Showing 3 of 1,204 units" |
| **Units: adding** | A form above the table: System (with several), Radio ID or range (Required; `5001`, `5100-5199`, spaces around the dash allowed), Alias. `PUT …/units/{unit}`. A unit that already exists is refused on the page, under the field ("5001 already has a unit: edit its alias in the table"), since `PUT` would replace it silently |
| **Units: editing** (R26 "inline") | Edit turns the row's alias into a text box in its own cell, the row marked; Enter or Save sends `PUT` with the alias, Escape or Cancel puts it back. One row at a time. The focus goes back to the row's Edit |
| **Units: deleting** | More actions, Delete…, a modal `Confirm` saying what its radios show as from then on |
| **Which alias** | "Which alias does a radio get?": a radio ID (and system) typed gives ADR 0019's answer as the gate gives it: "5150 shows as Portables, from the range 5100-5199", "5200 has no alias: listeners see 5200", "5001 shows as Engine 1: its own unit". `resolve` in `units.ts` is `gate.UnitAlias`'s rule |
| **Systems: the sections** | Systems, Attachments, Not attached, Tags, Groups, with links to each at the top |
| **Systems** (R11, R21) | ID, Label, Name, Auto-create (On or Off), Alert, LED colour, Sort order. Add a system (ID, fixed once made; label, Required; name; the rest); Edit; Delete… after asking. A system with talkgroups or calls is refused in the dialog with the server's words: "System EX still has talkgroups or calls, so it can't be deleted." |
| **Auto-create** (R21) | A checkbox in the system's form, off by default. Switching it on asks first (inline, as the talkgroup form's state does): the talkgroups it makes are public at once |
| **Attachments** (R12) | Recorder / shortName, System, Attached. More actions moves one to each other system, at once, or removes it after asking. "A change affects only future calls" is said above the table, and in each change's status line |
| **Not attached** (R12) | `GET /rejections`' pairs that aren't attached now, in R12's words ("Bench / other: 1 call rejected, not attached to a system"), each with a system and Attach. A pair attached since keeps its count in the API, but isn't listed |
| **Tags and groups** (R22) | Name, Alert, LED colour (its mark and its name), Sort order; Edit. Add a group (`POST /groups`). Tags are made where a talkgroup names one (ADR 0003), so there's no Add a tag |
| **The form** | `SettingsEditor.svelte`, for a system, a tag or a group: a panel above its table, its heading focused, the row marked; ArchiveUx section 5's problems under fields, the server's by code (`system_exists` under ID, `label_taken` under Label, `group_exists` under Name). New: every field (`POST`). Edit: only what differs (`PATCH`) |
| **Unset** (ADR 0006) | Alert and LED colour's first choice is null, "Not set"; the hint under each says what that means for its talkgroups (a tag's: "Its talkgroups with none of their own take it. Not set, they take their groups' or system's."). `None` is a setting of its own |

## Groups and tags on the Systems page

The issue leaves where groups and tags go to the slice: a section of Systems, or a place of their own under Talkgroups. Only R84's pages may be in the menu, so a page of their own would be a page the menu can't reach. They're sections of Systems because their settings are the same kind of thing as a system's: an alert, an LED colour and a sort order, which a talkgroup with none of its own takes from its tag, then its groups, then its system (ADR 0006). Seeing the three together is seeing where a talkgroup's colour comes from. The Talkgroups page already says, in its form, which of them a talkgroup's alert or colour is taken from.

## Editing a unit in its row

R26 gives units "the same table" as talkgroups, and R50's editing is inline. A talkgroup has twelve fields, which didn't fit a row (ADR 0031); a unit has one that changes, its alias, which does. So the alias becomes a text box in its own cell, and the row's actions become Save and Cancel. The text box is in a `form` in the alias's cell, and Save names it (`form="unit-edit"`), so Enter in the box saves. The unit's ID or range isn't edited: it's the unit's key, so a different range is a new unit, added above, and the old one deleted.

## No last heard for units

The issue leaves it to the slice. Units get none in M4:

- A unit's calls are a click away: Recent calls filtered by the unit (ADR 0019), for any radio or range.
- A range's last heard is the latest of every radio in it. Kept by ingest, it would be a write per transmission's unit per call into a table keyed by unit rows, which change; read at page load, it would be one skip scan per unit row, 1,000s of probes for a deployment's whole list. Neither is needed for anything R26 asks.
- It's reversible: a `unit_last_heard` table moved by ingest, as ADR 0026's is for talkgroups, can come later if the owner wants it.

## Asking first

As ADR 0031: what can't be taken back asks, what's undone in a click doesn't.

- **Deleting a unit or a system** asks (a modal `Confirm`, since the row's question would otherwise be off-screen in a long table). A deleted system's attachments and rejected-call counts go with it.
- **Removing an attachment** asks: until it's attached again the pair's calls are refused, and what's said meanwhile can't be got back.
- **Moving an attachment** doesn't: it's undone by moving it back, and it changes only where future calls are filed.
- **Switching auto-create on** asks, inline under the form: every unknown talkgroup the system hears from then on is public at once (R21), which, as making a talkgroup public, can't be unheard. Switching it off doesn't.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Units' data | A new `GET /admin/units` for every system's units | One request per system answers the same; deployments have a few systems |
| Units: adding over an existing unit | Letting `PUT` replace it | Adding should never silently rename a unit; editing is in its row |
| Units: last heard | Kept by ingest per unit row, or read at page load | Above |
| Groups and tags | A page of their own | Only R84's pages are in the menu |
| Tags: Add a tag | `POST /tags` | Tags are made where they're used (ADR 0003); a tag no talkgroup has is nothing to a listener |
| Moving an attachment | Asking first | Undone in a click; only future calls |
| Auto-create on | Not asking | Its talkgroups are public at once |

## Open points

1. **Deleting a group or a tag.** The admin API has no route for it, so the page has none. A group no talkgroup is in isn't shown to listeners (ADR 0006), so it's only clutter on this page.
2. **Talkgroup counts beside tags and groups.** The page doesn't say how many talkgroups each has; the Talkgroups page's filters do.

## How each choice is verified

| Choice | Verification |
|---|---|
| R26 through the page: 5001 "Engine 1" and 5100-5199 "Portables" in the table; 5150 shown as Portables, 5200 as its ID; the search by alias and by a radio ID inside a range | `web/tests/units-admin.spec.ts` |
| Adding a radio and a range through the page (and their problems: a range backwards, a unit that exists, `;` in an alias); the alias edited in its row (Escape, Enter, empty); deleting after asking, cancelled and confirmed | `web/tests/units-admin.spec.ts` |
| The menu's order (R84); EX, its attachments, tags and group on the page | `web/tests/systems.spec.ts` |
| Deleting EX while it has talkgroups is refused with the server's message | `web/tests/systems.spec.ts` |
| A system's form: its problems, `system_exists` under ID; auto-create asks first and, cancelled, sends nothing; a group's name required and `group_exists` under it | `web/tests/systems.spec.ts` |
| A2: Bench / other attached to EX from Not attached files its next call under EX, the refused one stays refused; removing asks | `web/tests/systems-live.spec.ts` |
| A2: auto-create switched on for EX on the page; the next call on 4711 creates it, public and unreviewed | `web/tests/systems-live.spec.ts` |
| A tag's LED colour set on the page shows for 1002, which has none of its own, in the catalogue and on Recent calls | `web/tests/systems-live.spec.ts` |
| The pages refuse signed out and a listener | `web/tests/units-admin.spec.ts`, `web/tests/systems.spec.ts`, `web/tests/admin.spec.ts` |
| A36: axe in both looks and themes at 375 px and 1,280 px: Units with a range, an alias being edited with its problem, More actions and the dialog; Systems with attachments, a pair not attached, a tag being edited with its problem, auto-create's question, More actions and the dialog | `web/tests/units-admin.spec.ts`, `web/tests/systems.spec.ts` |
| Every change audited: the routes' probes, unchanged | `cmd/radioarchive`, `TestIntegrationAuditHarness` |
| Tokens only (R80) | `npm run check:tokens` |

## Consequences

- The web admin now sets everything ADR 0003, 0006 and 0019's admin API sets, except a talkgroup's `PUT` (the Talkgroups page uses `PATCH`).
- The browser test fixture (`testing/webfixture`) now moves `talkgroup_last_heard` for the calls it writes, as ingest does (ADR 0026), so the admin pages show the fixture's talkgroups as heard. It wrote calls without going through ingest, so they showed "Never".
