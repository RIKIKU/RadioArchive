---
status: proposed
---

# 35. The import's update option, and the module's import and export

Date: 2026-11-25

The PowerShell module imports and exports the setup the way PagerArchive's module does (its ADR 0009, sections 8-10): `Import-RadioArchive<Thing>` takes rows from the pipeline by property name, the whole pipeline is **one** import through `POST /api/v1/admin/import` (a dry run, one `ShouldProcess` on its summary, then the apply with its plan), `-WhatIf` outputs every row's result with `Applied` `$false`, and `Export-RadioArchive<Thing>` writes a CSV that imports back as all unchanged. `-Update` (asking first, as `ConfirmImpact` High does) sends the import's new `update` option: what exists takes **every other field** the file gives otherwise. **State never changes through an import, `-Update` or not.** The admin pages' Settings page downloads the setup. This amends [ADR 0030](0030-the-setup-export-and-import.md), closing its open point 2 (`SPEC.md` R51-R53, A24, A26, section 9 step 2; issue #117).

**Whose choices.** The owner asked for "a way to bulk update in the ps module like we have in PagerArchive" (issue #117, 2026-11-25). That an update changes every other field, and never state, is the owner's rule as the coordinator recorded it on #117 ("an old file can't make hidden talkgroups public"). The commands' names and shapes, the CSV columns, the server option's form, keeping `unreviewed` with state, and where the browser's download lives are this slice's, proposed here.

## Summary

| Part | Choice |
|---|---|
| **The option** | `{"setup": …, "dry_run": true, "update": true}`, then `{"setup": …, "plan": …, "update": true}`. Absent or `false`: ADR 0030's import, unchanged |
| **With update: a talkgroup that exists** | Also takes the file's delay, alert, LED colour, type and frequency where they differ, besides R52's fields (label, name, tag, groups, fireground channel number, sort order). Never its **state** or **unreviewed**: the file's values for those are listed as `not_changed`, as before |
| **With update: a system, group or tag that exists** | Takes the file's label, name, auto-create, alert, LED colour and sort order (a system), or alert, LED colour and sort order (a group or tag), where they differ: `changed`, with its changes, and an audit entry. Without update they're `unchanged` with `not_changed`, as before |
| **Units** | Unchanged: an import already changes a unit's alias, its only field |
| **The plan** | Covers the option: SHA-256 of `{update, summary, results}`. The report says `"update"`. A plan previewed without update doesn't apply with it (`409 import_changed`) |
| **Locks** | The audit log's lock on each system, group and tag the update changes too, taken with the talkgroups' and units', in order |
| **A taken system label** | Checked before any write, dry run or not: two systems with one label is `409 label_taken`, naming both ("Systems 1 and 2 would both be labelled "EX""), rather than a unique index failing mid-import as `import_changed` |
| **Audited** | As ADR 0030: one entry per change, how `import`, who the signed-in admin; a system's, group's or tag's change is its own entry |
| **The module's commands** | `Import-RadioArchiveTalkgroup`, `-Unit`, `-System`, `-Group`, `-Tag` (rows by property name) and `Import-RadioArchiveSetup -Path` (a whole-setup JSON, one file one import); `Export-RadioArchiveTalkgroup`, `-Unit`, `-System`, `-Group`, `-Tag -Path` (CSV) and `Export-RadioArchiveSetup -Path` (the route's bytes as they are). `-Update` on each import but units' |
| **One batch** | `process` turns each row into the setup's shape, `end` sends one dry run, asks `ShouldProcess` once ("Import (1 new, 1 changed, 1 unchanged, 1 skipped)" on "3 talkgroup rows"), then applies with the plan. Nothing to change: only the dry run, no question. A bad row (a number that isn't one) stops the batch before anything is sent |
| **Output** | `RadioArchive.ImportResult` per row: Kind, SystemId, Id, Label, Result, Reason, Fields (the changed fields' names), Changes and NotChanged (Field, Before, After), Applied. Kept things only from `Import-RadioArchiveSetup`; a row import shows its own rows |
| **"Not said"** | A row says the fields it has as properties, plus what the command line gives (`-SystemId 1` for trunk-recorder's file). Not `$PSBoundParameters`, which keeps a parameter an earlier pipeline object bound |
| **CSV cells** | Empty leaves Tag, State, Unreviewed, DelayMinutes and SortOrder (and a system's Label and AutoCreate) as they are; empty is none for Alert, LedColour, Frequency and FiregroundChannel; empty is `''` for Label, Name and Type. Group is names separated by semicolons. True and False for yes and no |
| **trunk-recorder's CSV** | Aliases bind its columns: Decimal (Id), Alpha Tag (Label), Mode (Type), Description (Name), Tag, Category or Group; Hex and the rest are ignored. The unit CSV's ID binds Unit |
| **The talkgroup CSV** | SystemId, Id, Label, Name, Tag, Group, State, Unreviewed, Type, Frequency, DelayMinutes, Alert, LedColour, SortOrder, FiregroundChannel, read from `GET /admin/setup`, so it's the export's values. Every talkgroup, or those piped in, or a system's |
| **The browser's export** | The Settings page's **Setup export** section: what the file holds and doesn't, that it holds hidden and ignored talkgroups, and **Download the setup**, a link to `GET /api/v1/admin/setup` saved as `radioarchive-setup-<date>.json` (the deployment's date) |
| **Warnings** | `Export-RadioArchiveSetup` always, and `Export-RadioArchiveTalkgroup` when it writes a hidden or ignored one, warn that the file holds them |

## Why state stays, and unreviewed with it

ADR 0030's reason holds with update: a file is a picture of a setup at some time, and restoring an old one must not make a talkgroup public that an admin has since hidden, nor hide one. Hiding and showing are `Set-RadioArchiveTalkgroup -State`, one talkgroup at a time or piped, asking first. Unreviewed is kept with state: an auto-created talkgroup is reviewed by an admin looking at it (ADR 0030, "Unreviewed stays as it is"), and a file can't do that for them. Every other field is a setting a file may well be edited to change in bulk, which is what the owner asked for.

## Why one option for every kind

One `update` covers talkgroups, systems, groups and tags: an admin editing a CSV of one kind and one editing a whole setup mean the same thing by "update what exists". A per-kind option would let a setup import update tags but not talkgroups, which nobody asked for. A row import's file only says one kind, so the option reaches only that kind in practice.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Update | Change state too, as PagerArchive's update changes everything | The owner's rule: an old file can't unhide a talkgroup |
| Update | Change unreviewed | Reviewing is an admin's look at a talkgroup, not a file's |
| The plan | Leave the option out of the fingerprint | A preview without update could then be applied with it whenever the results happened to match |
| Exports | `Get-RadioArchiveTalkgroup \| Export-Csv` | Export-Csv writes a list of groups as `System.String[]`, and the talkgroup table lacks nothing the setup has but costs its 30-day counts; the setup is the import's own words |
| "Not said" | `$PSBoundParameters` | It keeps parameters an earlier pipeline object bound, so a row would say fields it doesn't have |
| Numbers | `[Nullable[int]]` parameters | A CSV's empty cell binds as 0, not "none" |
| The browser's export | A button that fetches and saves a blob | A link is what a browser already downloads, with the session cookie; nothing to build or test but the link |
| Where | Its own admin page | One link and two sentences; Settings is where the deployment's own things are |
| A taken label | Leave it to the unique index | It would read as `import_changed`, and the fresh dry run would fail the same way |

## Consequences

- ADR 0030's open point 2 is answered by this option; its rule for an import without update is unchanged.
- `RadioArchive.Talkgroup` gains Type and Frequency.
- The guidelines' verbs gain Import and Export (`docs/powershell-guidelines.md` section 2), and the meta-tests treat Import as a change and Export as writing a file.
- `ImportUpdateTalkgroup` writes delay, alert, LED colour, type and frequency, the caller passing what it keeps.

## How each choice is verified

| Choice | Verification |
|---|---|
| Without update, a file's other fields are `not_changed`; with update every field is taken, state and unreviewed never, on a public, a hidden and an ignored unreviewed talkgroup; a system's, a group's and a tag's fields change; each change is an `import` entry; the same file again changes nothing; a plain plan doesn't apply an update; a taken label is refused before anything is written | `internal/server/setup_update_test.go`, `TestIntegrationImportUpdate` |
| One batch, the fields a row says and no others, trunk-recorder's columns, CSV cells, a bad number, `-WhatIf`, nothing to change, `-Update` asking by default, `import_changed`, `Get-` objects, each Export's file importing as all unchanged, the setup's bytes | `powershell/tests/Import.Tests.ps1` |
| A24 through the module against the stack: the preview, nothing until confirmed, 1003 hidden after an import and an `-Update`, its own export and its talkgroup CSV round-trip; A26: renamed in the browser, hidden with the module, an attachment added, each on the Audit log page with who and when; section 9 step 2: the first admin signs in with the module, creates a system, imports `testing/setup/fixture.json` and attaches the shell's recorder (as system 9 "EX9", since the stack's tests already made EX; the clean host is M6's run of section 9) | `web/module-tests/module.spec.ts` (`scripts/ci/e2e-module.sh`) |
| The Settings page's download is the setup, hidden 1003 included, named by date; axe and screenshots in both looks and themes | `web/tests/admin.spec.ts` |
| The import is still audited, through a real sign-in | `cmd/radioarchive/audit_test.go`, `TestIntegrationAuditHarness` |
