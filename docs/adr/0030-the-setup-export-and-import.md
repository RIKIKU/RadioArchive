---
status: proposed
---

# 30. The setup's export and import

Date: 2026-11-25

The whole setup goes out as one versioned JSON document, `GET /api/v1/admin/setup`, in a fixed order, so the same setup always exports the same bytes. The import, `POST /api/v1/admin/import`, takes that shape: it merges on system ID, system + talkgroup ID and system + unit; adds what's new with every field the file gives, state included; changes only R52's fields of a talkgroup that exists, never its state; and deletes nothing. A dry run answers every row's result and a plan, a fingerprint of the results, and changes nothing. Applying sends the same setup with the plan; if the results are no longer the plan's, it answers `409 import_changed` with the results as they are now and applies nothing. Each change is an audit log entry made by the signed-in admin, how `import` (`SPEC.md` section 4, R11, R21, R22, R26, R52-R54, R89, R100, A24, A25; issue #115).

**Whose choices.** What the export carries and what an import may change are the **owner's** (issue #118, question 3, answered 2026-11-25 as recommended): the export carries the whole setup, every R22 field and the groups', tags' and systems' settings and the units; an import creates new talkgroups with every field in the file, state and unreviewed included; for talkgroups that exist it changes only R52's fields (label, name, tag, groups, fireground channel number, and sort order when given), never state. The format, the routes, the plan, the merge's details and the audit's shape are the **coordinator's delegation** to this slice (issue #118's coordinator notes: a versioned format, a fixed order, the import's "how" is `import`), and are proposed. PagerArchive's ADR 0009, section 8 (one batch, a dry run, a plan fingerprint, `import_changed`) is the reference for the shape. It builds on [ADR 0003](0003-systems-attachments-and-rejected-calls.md) (systems and their IDs), [ADR 0006](0006-talkgroup-settings-and-the-catalogue.md) (the settings, the fireground rule, the catalogue's generation), [ADR 0019](0019-units-aliases-and-unit-search.md) (units and ranges), [ADR 0025](0025-admin-accounts-roles-and-admin-sign-in.md) (admin routes) and [ADR 0029](0029-the-audit-log.md) (the audit log).

## Summary

| Part | Choice |
|---|---|
| **Export** | `GET /api/v1/admin/setup`, admins only, `no-store`. `{"format": "radioarchive-setup", "version": 1, "systems": [...], "groups": [...], "tags": [...]}`, indented two spaces, with a final newline. Read in one snapshot (repeatable read) |
| **What's in it** | Each system's ID, label, name, auto-create, alert, LED colour and sort order, with its `talkgroups[]` and `units[]`. Each talkgroup's every R22 field: ID, label, name, tag, groups, state, unreviewed, type, frequency, delay, alert, LED colour, sort order, fireground channel number. Each unit's `from`, `to` and alias. Each group's and tag's name, alert, LED colour and sort order. Hidden and ignored talkgroups too: it's the admin's |
| **What isn't** | Recorders and attachments (R10, R12: the shell's and the pair's), accounts, calls, the audit log, and anything with a time: no export time, no `created_at`. Group and tag IDs: they go by name |
| **Order** | Systems, talkgroups and units by ID (units by `from`, then `to`); groups and tags by name in bytes, not the database's collation; a talkgroup's groups in their order (sort order, then name), which is the order its settings are taken from (ADR 0006) |
| **Type and frequency** | New in migration 00024: `type`, text up to 32 bytes, `''` for nothing said (trunk-recorder's mode letter, `D`, `T`, `E` …); `frequency`, hertz, or null. R22 lists both and the database didn't hold them; the round trip needs them. The admin API's talkgroup shows them, and PUT and PATCH take them. Never a listener's |
| **Import** | `POST /api/v1/admin/import`, admins only: `{"setup": {…the export's shape…}, "dry_run": true}`, then `{"setup": {…the same…}, "plan": "<hex>"}`. An apply without a plan is refused (`400 plan_required`), so nothing is applied unpreviewed |
| **Rows** | The export's shape, where a field left out is "not said": it leaves what exists as it is, and a new thing takes the default. `null` says "none" (alert, LED colour, fireground channel number, frequency). The module (issue #117) turns trunk-recorder's talkgroup CSV and the unit CSV into this shape |
| **Merge** | Systems on ID; talkgroups on system + ID; units on system + `from` + `to` (an exact unit or a range, ADR 0019); groups and tags on name. Nothing missing from the file is deleted |
| **New** | Created with every field the file gives: a talkgroup's state and unreviewed included (state public when the file has none, R22). A new talkgroup needs a tag unless it's unreviewed; a new system needs a label. Groups and tags named on a talkgroup and not in the file's lists are created on first use, with no settings |
| **Existing talkgroup** | Changes only label, name, tag, groups, fireground channel number, and sort order when given. Never state, unreviewed, delay, alert, LED colour, type or frequency. Where the file gives those otherwise, the result lists them as `not_changed` |
| **Existing unit** | Its alias changes |
| **Existing system, group, tag** | Unchanged: an import doesn't change their settings (the owner's rule for talkgroups, applied to what they take settings from). Differences are listed as `not_changed` |
| **Skipped** | A talkgroup row whose `type` is `A` or `analog` (`analog`) or `conventional` (`conventional`), any case; a row whose label is just its talkgroup ID, leading zeros allowed (`placeholder`). Skipped rows are reported and change nothing, whether or not the talkgroup exists |
| **Results** | One per row and per thing the file leaves out: `{kind, system?, id, label, result, reason?, changes?, not_changed?}`, `result` one of `new`, `changed`, `unchanged`, `kept` (exists, not in the file), `skipped`. `changes` are the audit entry's own (`store.Changes`): a new thing's every field, a changed thing's changed fields. A summary counts each kind's results |
| **Kept** | Talkgroups and units of the file's systems that the file leaves out; systems, groups and tags the file leaves out |
| **Plan** | SHA-256 (hex) of the summary and results as JSON, in their fixed order |
| **The dry run** | The import itself, in one transaction, rolled back with its audit entries: the preview is what the apply does, field for field, because it is the same code reading what it wrote |
| **Apply** | The same, committed only if its plan is the one sent. Else `409 import_changed` with the results now and their plan; nothing applied. A row created meanwhile, or a fireground number taken meanwhile (a unique index), is the same `409`, with a fresh dry run's results |
| **Fireground** | Checked before any write, on the numbers every talkgroup would have: a number on two talkgroups, in the file or against one the file doesn't change, is `409 fireground_channel_taken`, naming every talkgroup with it ("talkgroup 1001 "Dispatch North" in system EX and talkgroup 1002 "Fireground 41" in system EX"), dry run or not. Numbers that move are freed first, so two talkgroups can swap theirs |
| **One transaction** | The whole import. The catalogue's generation moves at its commit (migration 00007's triggers), as for any edit, so the next public request sees it |
| **One at a time** | An advisory lock (`115, 0`) for the import; the audit log's lock (ADR 0029) on each existing talkgroup and unit it changes, taken in order before it reads them again |
| **Limits** | 8 MB of body (`413 body_too_large`) and 10,000 rows, systems, talkgroups, units, groups and tags together (`413 too_many_rows`), each refusal naming its limit. Every row is checked before anything is read; a bad row is a `400` naming it ("System 1, talkgroup 1001: …"), and a row twice is `400 duplicate_row` |
| **Audited** | One entry per change, as every admin change (ADR 0029): kind, thing, label and each changed field; who is the signed-in admin; how is `import`, whatever the request signed in with. A creation of a group or tag from the file's lists has its own entry; one made by naming it on a talkgroup is in that talkgroup's |
| **Logs** | The admin's ID and the counts; never a name or alias (A32) |

## The format

The document names itself (`"format": "radioarchive-setup"`) and its version (`1`). The import refuses any other (`400 unsupported_format`), so a file from a later server, or something that isn't a setup, is turned away rather than half-read. A later version that adds fields can be read by a later server, and the version tells it which rules a file was written under.

Each object is the admin API's own shape for the thing, minus its times and generated IDs, so an export reads in the API's words and a field added to the API is easy to add here. Groups and tags go by name, because their IDs are a deployment's own and an import into another deployment would make them up again; the plan would change with them, and a round trip couldn't be byte-identical.

Indented, with a final newline, so a saved export is readable and two exports compare with any diff tool. The fixed order and the absence of times make the bytes a function of the setup alone, which A24's round trip needs and R89's nightly copy (M6) benefits from: an unchanged setup writes an unchanged file.

## How an import reads a row

R52 names what an import changes on a talkgroup that exists; the owner's answer adds that a new one takes every field in the file. Fields left out of a row are "not said", so a CSV that knows only label, name, tag and group changes only those, and a hand-written file can name just the field it means (`{"id": 1001, "fireground_channel": 41}`). `null` is a value: "none". A new thing takes the admin API's defaults for what isn't said (state public, R22; no delay; sort order 0; no settings of its own).

A talkgroup that exists keeps its state whatever the file says: an import can't make a hidden talkgroup public, nor hide a public one. The file's other values for the fields an import never changes (state, unreviewed, delay, alert, LED colour, type, frequency) are listed as `not_changed`, so the preview says "1003 is hidden; the file says public; not changed", which is A24's "1003 unchanged in state". The same goes for a system's, group's or tag's own settings: they're what talkgroups take their alert and colour from (ADR 0006), and changing them would change talkgroups' settings that an import may not change directly.

Unreviewed stays as it is on a talkgroup that exists. Naming an auto-created talkgroup by import doesn't mark it reviewed; an admin does, as before.

## Skipped rows

R52 and section 4 skip conventional and analog rows, and placeholders. How a row is known to be one is this slice's:

- **Analog or conventional**: by its `type`. trunk-recorder's talkgroup file has a mode column (`D` digital, `T` TDMA, `E` encrypted, `A` analog …), which the module puts in `type`; `A` or `analog` is analog. `conventional` is the module's word for a row from a conventional system's channel file. Nothing else is inferred: a frequency alone doesn't make a row conventional, since a trunked talkgroup may carry one too.
- **Placeholder**: the label is just its talkgroup ID, as files carry for talkgroups nobody named (`1006`, `01006`). A talkgroup an admin really did label with its own number would be skipped too; the label adds nothing the ID doesn't, so that's accepted.

Skipped rows are skipped whether or not the talkgroup exists, so a placeholder row never renames a named talkgroup back to its number.

## The plan, and why the dry run is the import

The preview has to be what the apply will do, field for field: A24 says nothing changes until confirmed, and R52 shows the changed fields first. Computing the results apart from the writes means two pieces of code that must agree, down to the order the database sorts a talkgroup's groups in (its collation, not Go's). So the dry run *is* the import, in a transaction rolled back with its audit entries, and its results are read back from what it wrote. The apply runs the same code and commits only if its fingerprint is the one sent.

The plan covers the summary and every result, including `not_changed` and `kept`, so any change between the two (an admin's edit, a recorder's auto-create, another import) changes the plan and turns the apply away with the new results, which the module shows the admin again. A rolled-back dry run uses up sequence numbers (group, tag and audit entry IDs), which is harmless: no result holds one.

## Locks

Two imports run one at a time (an advisory lock). An admin editing a talkgroup the import changes, at the same moment, waits for it, because the import takes the audit log's lock on each existing talkgroup and unit it changes (ADR 0029) before reading it again, so each entry's "before" is what the other left. Things the import creates aren't locked: a talkgroup or unit created meanwhile makes the import's plain insert fail (not an upsert), which is `import_changed`. The locks are bounded by what an import changes, not its size: a round trip into an empty deployment takes none. PostgreSQL's lock table holds about 10,000 at its default settings, which is why the row limit is 10,000.

## Audit

Each change is an entry of its own (kind, thing, label, fields), exactly as the admin API's would be, with `via` `import` (ADR 0029's third "how"). One entry per change, rather than one per import holding a list, keeps the audit log's filters working: a talkgroup's history (`kind=talkgroup&thing=1/1001`) shows its import alongside its browser edits. The import's results are the entries' own changes, so the preview the admin confirmed is what the log keeps. A dry run writes none (they roll back), nor does an import that changes nothing.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Format | The admin API's shapes as they are, with times | A round trip can't be byte-identical with `created_at` in it, nor with group and tag IDs |
| Format | No version | A file from a later server would be half-read |
| Import body | The setup at the top level with `dry_run` beside it | Mixes the file's fields with the request's; `{"setup", "dry_run", "plan"}` keeps them apart |
| Preview | Computed in Go, apart from the writes | Two pieces of code that must agree, down to the database's collation; the results would drift from the entries |
| Existing talkgroups | Overwrite every field the file gives | The owner's rule: only R52's, never state. A restored file could otherwise unhide a talkgroup |
| Existing systems, groups, tags | Update their settings | They feed every talkgroup's alert and colour; that would be changing talkgroups' settings an import may not change |
| Skipping | A frequency means conventional | Trunked talkgroups may carry one |
| Fireground swaps | Refuse | One import fixing two talkgroups' numbers is a normal thing to want; freeing the moved numbers first costs one update each |
| Audit | One entry per import, with every change | The talkgroup's history filter wouldn't find it |
| Locks | The audit lock on every thing in the file | A 10,000-row file would fill PostgreSQL's lock table; only the things changed need it |
| A duplicate row | Squash identical ones | A file with one talkgroup twice is a mistake to fix, not to guess at |

## Open points

1. **A system's type.** Issue #12 lists a system type (P25, conventional …). v1 is P25 trunked only (section 4) and a system has no type yet, so conventional rows are known by the talkgroup's type only. If systems get one, an import could skip a conventional system whole.
2. **Updating what exists, other than talkgroups and units.** An import never changes a system's, group's or tag's own fields. If the owner wants an import to rename a system or set a tag's colour, that's an option to add (as PagerArchive's `update`), and the plan would cover it.

## Consequences

- The admin API's talkgroup carries `type` and `frequency`. A PUT without them clears them, as for every other field a PUT leaves out; the Talkgroups page (#113) edits with PATCH.
- The module (#117) builds its import on these two routes: one dry run, `ShouldProcess` on its summary, then the apply with its plan; `import_changed` tells the admin to run it again. It turns the CSVs into this shape, putting trunk-recorder's mode in `type`.
- The browser's and the module's export (#117) save this route's bytes as they are; both say a saved export holds hidden talkgroups.
- R89's nightly copy (M6) writes the same bytes, from `Admin.readSetup` or the route.
- An import's entries are in the audit log as `import`, and the audit harness probes the import route.

## How each choice is verified

| Choice | Verification |
|---|---|
| **A24**, server half: the fixture, a file renaming 1001, adding 1005, listing 1003 as public, leaving out 1002 and 1004, and a row labelled 1006, previews 1 changed (label and name), 1 new, 1 unchanged (1003, state not changed), 2 kept, 1 skipped as a placeholder; nothing changes (export, audit log, catalogue) until applied with the plan; applied, 1003 is still hidden, the catalogue shows the change and nothing of 1003, and two `import` entries name the admin; the same file again changes nothing | `internal/server/setup_test.go`, `TestIntegrationImportA24` |
| **A24**, round trip: the fixture with every field set somewhere (a system's, a tag's and a group's settings, a talkgroup's delay, alert, colour, type, frequency and three groups, an unused group, an unreviewed talkgroup with no tag), exported, imported into an empty deployment, exports identically, byte for byte; 1003 comes back hidden; re-imported where it came from, nothing changes | `TestIntegrationImportRoundTrip` |
| A change between the dry run and the apply answers `import_changed` with the results now, applies nothing, and the new plan applies | `TestIntegrationImportChanged` |
| Analog, conventional and placeholder rows are skipped and reported, and a placeholder doesn't rename a talkgroup that exists; a TDMA row is imported with its type and frequency | `TestIntegrationImportSkips` |
| Two talkgroups given 41 in the file, or 1001 given the 41 that 1002 keeps, stop the import, dry run or not, naming the talkgroups, changing nothing; two talkgroups swap numbers; null takes one away | `TestIntegrationImportFireground` |
| A new system; units added, an alias changed, units left out kept; groups by name, a new one with the file's settings; what exists keeps its settings, listed as not changed | `TestIntegrationImportMerge` |
| The format, a field it doesn't know, a duplicate row, a bad value, a new talkgroup without a tag, a new system without a label, the row and body limits, no setup, no plan; admins only | `TestIntegrationImportRefusals`, `TestIntegrationAdminSignIn` |
| The import's change is in the audit log with who, how (`import`) and when, through a real sign-in; a stale plan adds nothing; the export is a read that adds nothing; no log line holds a name the entries hold (A32) | `cmd/radioarchive/audit_test.go`, `TestIntegrationAuditHarness`; `TestIntegrationAuditNotForReads` |
| The binary serves both routes | `cmd/radioarchive/main_test.go` |
| Public answers are unchanged (A10) | the A10 harness and `TestIntegrationSignedInSameAnswers`, unchanged |
