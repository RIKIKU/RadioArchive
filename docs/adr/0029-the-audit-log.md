---
status: proposed
---

# 29. The audit log

Date: 2026-11-25

Every admin change is recorded in one table, `audit_log`, in the same transaction as the change, through one store path, `store.Audited`. An entry says who made the change (the admin's account, with their name and email as they were), how (browser, module or import, from how the request was signed in), when, and what: the kind of thing, which one, and each changed field before and after. A harness lists every state-changing admin route from the route table and fails while any has no test that finds its entry. The read API is `GET /api/v1/admin/audit` (`SPEC.md` R10, R12, R34, R54, R84, R89, R90, A26, A32; issue #112).

Everything here is the implementer's choice (spec "How to use this spec", item 6), and is proposed. It builds on [ADR 0025](0025-admin-accounts-roles-and-admin-sign-in.md), whose `server.AdminFrom` carries the admin to the handlers for this. It completes [ADR 0003](0003-systems-attachments-and-rejected-calls.md)'s note that the audit log comes in M4.

## Summary

| Part | Choice |
|---|---|
| **The table** | `audit_log` (migration 00023): `id`, `at`, `issuer`, `subject`, `admin_name`, `admin_email`, `via`, `kind`, `thing`, `label`, `action` (`created`, `changed`, `deleted`), `changes` (a JSON array of `{field, before, after}`). No foreign key to `accounts` |
| **Who** | The admin's account by issuer and subject, with the name and email of the session that made the change, copied into the entry, so it still reads after the account changes or is deleted |
| **How** | `browser` (a session cookie), `module` (a bearer token, issue #116), `import` (the import route, issue #115). The admin authenticator says it (`account.Admin.Via`); the client never does. An entry without one can't be written, and the change with it fails |
| **What** | `kind` (system, talkgroup, unit, group, tag, attachment, recorder, retention, account), `thing` (a stable key: `1`, `1/1001`, `1/5100-5199`, a group's ID, `Bench/ex`, a recorder's name, an account's subject; empty for retention), `label` (the name it went by: `EX 1001 DISP North`, `Bench / ex`) and each changed field, in the admin API's field order, as the admin API shows it, timestamps left out |
| **Creations and deletions** | A creation records every field after (before `null`); a deletion every field before (after `null`), so what was deleted is kept |
| **One store path** | `store.Audited(ctx, actor, func(q, au) error)`: one transaction; the handler changes with `q` and records with `au.Record`. The change's error, or a failure writing the entry, rolls back both. Every admin write goes through it, the role change and ending sessions too (`store.SetAccountRole`, `store.EndAccountListenerSessions`) |
| **Before** | `au.Lock(kind, thing)` takes a transaction-level advisory lock on the thing before its "before" is read, so two admins changing one thing at once each record what the other left |
| **Nothing changed** | A request that changes nothing (a refusal, a PATCH to the values already there, ending the sessions of someone with none) writes no entry |
| **Not audited** | Reads, the audit log's included, and playback (R34, R54) |
| **Kept** | Recordings' retention never touches it (R54). Triggers refuse `UPDATE`, `DELETE` and `TRUNCATE` on it, so nothing rewrites an entry. Amended 2026-11-25 (owner): entries older than the audit log's own period (730 days by default) are deleted by the nightly run, the one `DELETE` the trigger lets through; [ADR 0032](0032-the-audit-log-page-and-its-retention.md) |
| **Not telemetry** | Entries hold names, because only admins read them. Logs, metrics and traces still never do (R90, A32): handlers log the admin's account ID and the things' IDs |
| **The read API** | `GET /api/v1/admin/audit`: newest first, `limit` (1-200, default 50), `cursor` (the answer's `next`), and the filters `who` (the account's ID), `kind`, `thing`, `since` and `until` (RFC 3339, `until` exclusive) |
| **The harness** | `cmd/radioarchive`'s `TestIntegrationAuditHarness`: through a real sign-in, a probe for every state-changing admin route of the binary, each a change it finds in the log with who, how and when, and a refused request that adds nothing. It fails while a route has no probe |

## How a change was made

The issue's rule is that "how" comes from how the request was signed in. The admin check already knows: `account.Service.Admin` reads the session cookie, so it answers `Via: browser`. Issue #116's bearer token check answers `module`; issue #115's import route sets `import` for the changes it makes. A header, a query or a body field naming the client is never read. The store refuses an actor with none of the three (`store.ErrNoActor`), before the change runs, so a new authenticator that forgets to say how can't make an unaudited change.

The module signs in with its own OIDC client and so its own issuer. Issue #116 maps the module's subject onto the browser issuer's account (issue #118's coordinator notes), so an admin's entries carry one account whichever way they came in, and the `who` filter finds them all.

The server shell's recorder commands (`recorder add`, `rotate-key`, `retire`, R10) aren't admin API routes and have no signed-in admin. The issue makes them optional ("if the slice includes them"); they're left out (open point 1).

## What an entry holds

The fields are the admin API's own shapes (`server.System`, `server.Talkgroup`, `server.Unit`, `server.Label`, `server.Attachment`), marshalled and compared field by field. So an entry reads in the same words as the API, a field added to a shape is audited without more code, and the page can write each change in words ("Label `DISP N` → `DISP North`", "State public → hidden"). Timestamps (`created_at`, `updated_at`, `attached_at`) are left out: they change at every write and say nothing the entry's `at` doesn't. A talkgroup's entry holds its groups and tag by name; a tag or group created by naming it on a talkgroup is in that talkgroup's entry, not one of its own.

Settings with no shape of their own get a small object: retention `{"days"}`, a recorder `{"quiet_after_minutes"}`, an account's role `{"role"}`, and ending an account's sessions `{"sessions": n}` → `{"sessions": 0}`.

The `label` is for people, and is the thing's name at the change (after it, or before a deletion). The `thing` key is for filters and never changes with a rename.

## Why one transaction, and the harness

A change and its entry are one transaction, so neither exists without the other: a change refused halfway (a fireground channel number taken, a system in use) rolls back with its entry, and an entry the table refuses rolls back its change. The alternative, writing the entry after the change commits, can lose an entry to a crash between the two.

What the transaction can't do is make a new handler use it. As A10's harness does for the gate, `TestIntegrationAuditHarness` reads the binary's route table: every admin route whose method isn't `GET` must have a probe in `auditProbes`, and each probe's change must be the newest entry, with the signed-in admin's account, name and email, `browser`, and a time within the request. Slices merged after this one add their probes (issue #118); a route without one fails CI.

## Kept a long time

R54 keeps the audit log "a long time (not subject to recording retention)". Retention deletes calls, transmissions and rejected-call counts only, and the table refuses `UPDATE`, `DELETE` and `TRUNCATE` from any role, the API's included. Nothing is pruned in M4. R89's nightly `pg_dump` (M6) dumps the `radioarchive` database with only call data excluded, so the audit log is in it with the setup.

**Amended 2026-11-25.** The owner decided on issue #118 that old entries are deleted, and the prune came in M4 with the page rather than M6: the setting `AUDIT_RETENTION_DAYS` (730 days by default, 90 to 3,650, on the Settings page), and the nightly run's `audit_log_prune()`, which deletes the entries older than it and writes one entry of the system's saying how many went and from before when. Migration 00026 changes the trigger so that only that function's `DELETE` goes through, and only for entries past the period; `UPDATE`, `TRUNCATE` and every other `DELETE` are still refused. Recordings' retention still never touches the log. M6's backup is unchanged: the log is in the nightly dump with what the period has kept. See [ADR 0032](0032-the-audit-log-page-and-its-retention.md).

An account deleted later (issue #118's open question 2, option a) keeps its entries, with the name and email as they were.

## Paging

Newest first by `(at, id)`, with the cursor the last entry's pair, so a page never repeats or skips an entry already committed when the first page was read. `at` is the database's `clock_timestamp()` at the insert, near the commit, not the API's clock: the log says when the change happened. A change committed while an admin is paging, with an `at` older than their cursor, appears on a later read of the first page rather than the next page; for a log read by people, that's acceptable.

## The page

The Audit log page (R84), with these filters and each change in words, needs the admin frame (issue #111), and follows it in this issue. The read API gives the data, not the sentences; the page writes them.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Where entries are written | A database trigger on each table | It can't know who or how: the API connects as one role. Session variables could carry it, but a write that forgot to set them would still commit |
| Where entries are written | After the change commits, or in a queue | A crash between the two loses the entry |
| How | A header the module sends | The client would say how it signed in; the rule is that it never does |
| What | Whole rows before and after | Hard to read, and every write's timestamps would be a change |
| What | A sentence per change, made by the server | The page and the module would need parsing to filter or show it differently; the fields are the data, the words are the page's |
| Before | `SELECT … FOR UPDATE` on each table | A creation has no row to lock; one advisory lock per thing covers both |
| Who | A foreign key to `accounts` | An entry must outlive its account, and keep its name and email as they were |
| Keeping it | Nothing stopping `DELETE` | R54's "read-only"; a trigger costs nothing and makes it structural |
| No-op requests | An entry anyway | Nothing changed; the log would fill with saves of unchanged forms |
| Paging | Offset and limit | Pages shift while new entries arrive |

## Open points

1. **The server shell's recorder commands.** `radioarchive recorder add`, `rotate-key` and `retire` change recorders but aren't admin API routes and have no signed-in admin. Auditing them as "server shell" needs a `via` of `shell` and an actor with no account (the host's user, say). Left for a later slice; the table's checks would gain the value then.
2. **How long "a long time" is.** ~~Nothing is ever deleted. If the owner wants a limit (years), it's a nightly prune in M6 and a change to the triggers.~~ Decided 2026-11-25 (owner, issue #118): old entries are deleted, after 730 days by default; built in M4 with the page, not M6 ([ADR 0032](0032-the-audit-log-page-and-its-retention.md)).

## Consequences

- Every admin change has an entry, and every entry a change.
- Admins can read who changed what, how and when, through the API now and the page next.
- A new state-changing admin route fails CI until it has an audit probe.
- The database grows by one small row an admin change, ~~never shrinking~~ until the entry is past the audit log's period (amended 2026-11-25, ADR 0032).
- `account.Admin` carries `Via`; an admin authenticator must say how a request was signed in, or its changes fail.
- `retention.Job.SetDays`, `store.SetAccountRole` and `store.EndAccountListenerSessions` take the admin making the change.

## How each choice is verified

| Choice | Verification |
|---|---|
| The diff: every field for a creation or deletion, only what changed otherwise, in order; nothing for no change | `internal/store/audit_diff_test.go`, `TestAuditDiff` |
| A change and its entry commit together; a failed change leaves no entry; an entry the table refuses leaves no change; no admin or no "how", nothing runs; `UPDATE`, `DELETE` and `TRUNCATE` refused | `internal/store/audit_test.go`, `TestIntegrationAudited` |
| Newest first, paged by the cursor, filtered by who, kind, thing and time | `TestIntegrationAuditEntries`; through the API, `internal/server`'s `TestIntegrationAuditReadAPI` |
| A role change's entry, with the account's name; refused ones and no-ops write nothing | `TestIntegrationRoleChangesAudited` |
| **A26**, server half: renaming 1001 with a session, hiding it with a bearer request (the module's stand-in until issue #116) and changing an attachment make three entries, each with who, how and when | `internal/server/audit_test.go`, `TestIntegrationA26Audit` |
| Playing a recording, reading the talkgroup table, every admin read and the audit log add nothing; nor do refusals and no-ops | `TestIntegrationAuditNotForReads`; the harness, for every admin `GET` the binary serves |
| ~~The nightly retention run, ten years on, leaves the audit log as it was~~ Recordings' retention leaves the audit log alone; the audit log's own period prunes it (amended 2026-11-25, ADR 0032) | `TestIntegrationAuditRetention` (was `TestIntegrationAuditKeptByRetention`) |
| Every state-changing admin route of the binary has a probe; each change through a real browser sign-in is found with who, how and when; each refused request adds nothing | `cmd/radioarchive/audit_test.go`, `TestIntegrationAuditHarness` (fails with a probe removed, or a handler's `Record` skipped) |
| No log line holds a name an entry holds (A32) | `TestIntegrationAuditHarness` |
| Anonymous use, the admin API's included, adds no row (P4) | `cmd/radioarchive/p4_test.go`, unchanged |
| Admin data doesn't change public answers (A10, A13) | the A10 harness and `TestIntegrationSignedInSameAnswers`, unchanged |
