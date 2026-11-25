---
status: proposed
---

# 32. The Audit log page and the audit log's retention

Date: 2026-11-25

The admin frame gets the **Audit log** page (R84): every admin change from [ADR 0029](0029-the-audit-log.md)'s read API, in ArchiveUx's `DataTable` with the server doing the order and the paging, filtered by who, the kind of thing, the thing and the days, and each change put into words. And the audit log gets a **retention period of its own**: the owner decided on issue #118 (2026-11-25) that old entries are deleted, with the coordinator's default, a setting `AUDIT_RETENTION_DAYS` of 730 days, shown and changed on the Settings page and pruned by the nightly run, whose prune writes one entry of its own. That amends ADR 0029's "kept, nothing is ever deleted". `SPEC.md` R54, R84, R88; A26's browser half; issue #112.

The owner's decision is the retention itself and its default; the rest is the implementer's (spec "How to use this spec", item 6), and is proposed. It follows [ADR 0027](0027-the-admin-frame-the-overlap-query-and-a10s-scope.md)'s frame and ArchiveUx 0.5.0's building blocks (its ADR 0004).

## Summary

| Part | Choice |
|---|---|
| **The page** | `/admin/audit`, "Audit log" in the Admin menu between Recorders and Settings (R84's order, Accounts to come before it). A `DataTable` (`sortRows={false}`): When (sortable, newest first), Who (name, email under it), How, What (the kind, then the thing's label), Change (a line each). Cards at 375 px |
| **Order and paging** | The server's. Pressing When asks again with `order=oldest` (new on `GET /audit`), or newest; **Load more** asks for the next page with the answer's cursor, 50 at a time |
| **Filters** | Who (a select from `GET /audit/admins`), Kind of thing (a select), Dates (ArchiveUx's `DateRange`, days on the deployment's clock, made instants with `rangeBounds`), and a row's **History** button for that thing alone. They apply as they change and are the page's URL (`?who=…&kind=…&thing=…&from=…&to=…&order=oldest`), so a view can be linked |
| **Words** | The page's, from the fields (ADR 0029, "The page"): a change "Label `DISP N` → `DISP North`", "State public → hidden"; a creation "Created" then each field set; a deletion "Deleted" then each field it had; an attachment "Bench / `ex` attached to EX", "moved from EX to EX-2", "detached from EX"; ending sessions "Signed out everywhere: 2 sessions ended"; the prune "Deleted 12 entries from before 16 Oct 2024 (kept 730 days)". Typed values (names, labels) as code, chosen ones (a state, a role) as words, null as "none" |
| **Who list** | `GET /api/v1/admin/audit/admins`: everyone with an entry, by the name and email of their latest, sorted by name. An account changed or deleted since keeps its entries, so the list comes from the log, not `accounts` |
| **The period** | The setting `audit_retention_days` (`AUDIT_RETENTION_DAYS` on the page), 90 to 3,650 days, 730 by default. One reading of it, the database function `audit_log_retention_days()`, used by the API, the prune and the trigger, so they never disagree |
| **Setting it** | `GET`/`PUT /api/v1/admin/audit/retention`, as `/retention` is for recordings: `{days, confirm}`. A shorter period that would delete entries now answers `409 confirm_shorter_audit_retention` with how many; one that deletes none saves at once. Audited like any setting: kind `retention`, thing `audit`, label "Audit log retention", `{"days"}` before and after |
| **The prune** | Step 4 of the nightly run: `audit_log_prune()` deletes the entries older than the period by the database's clock and, when any went, writes one entry: who `system` ("System", no account), how `nightly`, kind `audit` ("Audit log"), action `deleted`, changes `entries` (how many), `before` (the cut-off) and `days`. The run's result and log line carry the count, never a name |
| **Only the prune deletes** | Migration 00026 changes ADR 0029's trigger: a `DELETE` goes through only while `audit_log_prune()`'s transaction-local switch `radioarchive.audit_prune` is on, and only for an entry older than the period in force. `UPDATE`, `TRUNCATE` and any other `DELETE` are still refused |
| **The Settings page** | An "Audit log" section under the recordings' two: "Keep audit log entries (days)", the bounds and the default, how many entries the log holds and since when; ArchiveUx's `Confirm` ("Delete 3 audit log entries?", "Shorten to 90 days") when a shorter period deletes some; Cancel changes nothing |

## The page

ArchiveUx's section 4 says a paged admin table hands its order to the server (`sortRows={false}`, fetch on `onsort`). The audit log is the first: it can hold years of entries, so the page never has them all. When is the only sortable column, because the API pages by `(at, id)` and nothing else; sorting by who or kind would need another index and cursor for something the filters already do better. Oldest first is `ListAuditEntriesOldest`, the same query the other way round on the same index.

The filters apply as each one changes, with no Apply button: a select that changes the list below it, not the page, isn't a change of context (WCAG 3.2.2), and a status line says what's shown ("3 changes shown, newest first; there are more."). The thing filter has no box of its own: a thing's key (`1/1001`, `Bench/ex`, a group's ID) isn't something an admin types, so it comes from a row's **History**, which names the row ("History of talkgroup EX 1001 DISP N") and moves the focus to **Show every talkgroup**, the way back, since the row it was on may go. A kind's `thing` of `''` (retention of recordings, the prune) has no History: an empty thing isn't a filter in the API.

Dates are days, not times: an admin looks for "last Tuesday", and the read API's `since` and `until` are instants, so the page turns the deployment's days into them with ArchiveUx's `rangeBounds`, as the Recent calls page does (ADR 0023).

The page and its words are in `web/src/lib/admin/audit.ts`, with nothing about them on the server: the API gives fields (ADR 0029), and the module or another client can say them differently.

## The retention

The owner's words were "yes, old entries are deleted", with the coordinator's default of two years. The choices around it:

- **A setting in the database, not an environment variable.** It's shown and changed on the Settings page, like recordings' retention (R88), and an environment variable can't be changed from a page. `AUDIT_RETENTION_DAYS` is the setting's name in the spec and on the page; it is stored as `audit_retention_days` in `settings`, so it's backed up with them (R89).
- **90 to 3,650 days.** Under three months the log stops answering "who changed this last quarter?"; ten years matches recordings' maximum. Outside those, or unreadable, the reading falls back to 730, the longer side of a mistake.
- **The database's clock.** An entry's `at` is the database's `clock_timestamp()` (ADR 0029, "Paging"), so its age is measured on the same clock, `now()` in the prune. The retention job's own clock, which tests move, doesn't reach it: moving that clock ten years on deletes no entry the period keeps (`TestIntegrationAuditRetention`).
- **One entry per prune that deleted something.** It says what went in numbers, like the counts the nightly run keeps for recordings; the deleted entries' names go with them, so nothing of what was deleted is kept, which is the point of a period. A night that deletes nothing writes nothing, as a no-op change writes nothing (ADR 0029).
- **Asking first only when entries would go.** R88 asks before any shorter recordings period. Here a shorter period that deletes nothing (the log is younger than it) needs no question: nothing can't be brought back. The count is the entries older than the new period now; the next nightly run deletes those and any that age past it by then.

## Only the prune deletes

ADR 0029 made the log append-only with triggers. A prune needs one `DELETE`, and the question was how to let only it through.

The prune is a `plpgsql` function that sets a transaction-local setting (`set_config('radioarchive.audit_prune', 'on', true)`) around its own `DELETE`, and the trigger lets a row go only while that's on **and** the row is older than the period. Anyone who can run SQL as the API's role could set the switch themselves, as they could call the function; what they can't do, with or without it, is delete an entry the period still keeps, or change one. That is the guarantee the log needs: no admin route and no slip in a handler can remove or rewrite history, and what the period lets go, it lets go the same way every time.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Who list | `GET /accounts` (issue #105's) | Deleted and renamed accounts keep entries; the system's prune has no account |
| Sorting | Every column sortable | The API pages by time; a sort by name over a paged log would need its own index and cursor, and the who and kind filters answer the same question |
| Thing filter | A text box for the key | Keys aren't words an admin knows; History from a row is |
| Filters | An Apply button | Each select changes only the list; a status line says what's shown |
| The period | An environment variable | It's changed on the Settings page (the owner's decision) |
| The prune's clock | The retention job's | Entries are dated by the database; the job's clock is moved by tests and would delete what the period keeps |
| Deleting | Dropping the triggers for the prune | Every `DELETE` would go through, a mistaken one too |
| Deleting | A `SECURITY DEFINER` function owned by another role | The API and the migrations use one role here, so it would guard nothing more, and needs a second role in every deployment |
| Deleting | Moving old entries to an archive table | The owner's decision is that they're deleted |
| The prune's entry | One every night | 365 rows a year saying nothing happened |
| The prune's entry | Keeping the deleted entries' labels | It would keep, as names, what the period deleted |

## Open points

1. **The module.** `Get-RadioArchiveAuditLog` reads the log newest first; `order=oldest`, `/audit/admins` and the audit period have no module commands yet. Issue #117 or a later slice can add them; the API is ready.
2. **R84's Accounts page** (issue #105) goes before Audit log in the menu when it comes.

## Consequences

- Admins read every change on a page, filtered and linked, each in words.
- The audit log holds two years of changes by default, and the Settings page changes that; nothing older is kept.
- `GET /api/v1/admin/audit` takes `order`; `GET /audit/admins`, `GET /audit/retention` and `PUT /audit/retention` are new admin routes, the `PUT` with its probe in `TestIntegrationAuditHarness`.
- `audit_log.via` may be `nightly` and `kind` may be `audit`, written only by the prune; `store.Audited` still takes only browser, module and import.
- The nightly run's result has `audit_entries`.

## How each choice is verified

| Choice | Verification |
|---|---|
| The period's one reading: 90 to 3,650, else 730; the prune deletes only entries past it and writes one entry with how many, the cut-off and the period; none past it, nothing written; the switch by hand deletes nothing the period keeps; the nightly "how" isn't anyone's | `internal/store/audit_test.go`, `TestIntegrationAuditPrune`, `TestIntegrationAudited` |
| The API: the default, bounds, a shorter period asking only when entries would go, with the count; saving audited; the nightly run pruning with recordings' retention at 7 days, and nothing more with the job's clock ten years on; `UPDATE` and `DELETE` still refused | `internal/server/audit_test.go`, `TestIntegrationAuditRetention` |
| `order=oldest` pages the other way; `/audit/admins` lists who has entries; reads add nothing | `TestIntegrationAuditOldestFirstAndWho`, `TestIntegrationAuditNotForReads` |
| `PUT /audit/retention` audited through a real sign-in, a refused one adding nothing | `cmd/radioarchive/audit_test.go`, `TestIntegrationAuditHarness` |
| The page: in the menu; who, how, when and each change in words; filtered by who, kind, a row's History and days, from the URL too; oldest first and Load more from the server; nothing matching said so | `web/tests/audit.spec.ts` |
| Settings: the period, a problem under its field, `Confirm` naming the count, Cancel changing nothing | `web/tests/audit.spec.ts`; saving, `web/tests/admin-live.spec.ts` |
| A26's browser half: renaming 1001 and moving Bench's attachment in the browser show with who, how and when | `web/tests/admin-live.spec.ts` |
| axe in both looks and both themes at 375 and 1,280 px: the list, a History, the calendar, nothing matching, Settings asking | `web/tests/audit.spec.ts` |
| Signed out or a listener: "This page is for admins", and the routes refuse | `web/tests/admin.spec.ts` |
