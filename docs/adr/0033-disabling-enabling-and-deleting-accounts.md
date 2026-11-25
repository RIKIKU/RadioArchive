---
status: proposed (the parts marked "owner, 2026-11-25" are the owner's decisions)
---

# 33. Disabling, enabling and deleting accounts

Date: 2026-11-25

An admin disables, enables and deletes other accounts on the new **Accounts** page and through the admin API. Disabling is a database change first: in one transaction the account's status becomes `disabled`, every session of the account is deleted, the last-admin rule is checked and the audit entry is written. Then the radio switches the user off in Authentik, through Authentik's API, with a token of its own new **accounts service account**. If Authentik can't be reached or refuses, the account stays locked out of the radio, the admin is told so, the page shows the account as "Disabled here, not yet in Authentik", and repeating the request tries Authentik again. Enabling mirrors it. **Delete account** removes the account's row, saved profile and sessions, and its Authentik user, all or nothing. The proven-email attribute becomes the shared `email_proven` (`SPEC.md` P3, P4, R54, R55, R84, R103, R107, R110, R111; issue #105).

**The owner's decisions (owner, 2026-11-25):** disabling the PagerArchive way, database first and then Authentik through its API, with a user-admin token the radio holds again; if Authentik can't be reached the user is still locked out of the radio, the admin is told, and repeating it retries Authentik; enabling as PagerArchive reactivates; a disabled account keeps its row and saved profile; **Delete account** as recommended (the account row, profile and sessions, and in the standalone deployment the Authentik user); admins see names and emails; in the combined deployment disabling in either app disables in both and PagerArchive's users automatically have radio accounts (R107, issue #118's open questions 1 and 2). PagerArchive's ADR 0004, sections 1, 2 and 6, was the reference; nothing PagerArchive-specific is copied. The proven-email attribute was agreed with PagerArchive's coordinator ([issue #128](https://github.com/RIKIKU/RadioArchive/issues/128), item 2). Everything else here is the implementer's choice (spec "How to use this spec", item 6), and is proposed.

This supersedes [ADR 0022](0022-listener-sessions-live-in-the-database.md)'s open part of "Disabling a listener" (how the radio disables the user in Authentik, which was left to M4 without a standing token) and [ADR 0021](0021-the-saved-profile-lives-in-radioarchives-database.md)'s consequence that the API holds no Authentik API token. ADR 0021's reasons stand for the profile: it stays in the radio's database, and the old token's account, role and policy stay removed. It completes [ADR 0025](0025-admin-accounts-roles-and-admin-sign-in.md)'s "Issue #105 sets and clears it" (status) and its open point 2 (what admins see), and changes its proven-address attribute's name.

## Summary

| Part | Choice |
|---|---|
| **Disable, enable** | `PATCH /api/v1/admin/accounts/{id}` with `{"status": "disabled"}` or `{"status": "active"}`: the role's route, one field at a time (`{"role"}` or `{"status"}`, never both: `400 invalid_body`) |
| **1. The radio** | `store.SetAccountStatus`, one audited transaction: the active admins locked (`SELECT … FOR UPDATE`), the admin making it still one of them, never their own account (`409 own_account`), never the last active admin (`409 last_admin`), the target row locked, the status set, every session deleted when disabling (the same deletion as `EndAccountListenerSessions`), the entry `status` and `sessions` before and after, and `authentik_pending` set |
| **2. Authentik** | `authentik.Users.SetActive`: `GET /api/v3/core/users/{id}/`, refused if a superuser or not a person's account, then `PATCH {"is_active": false|true}`. Authentik ends a switched-off user's sessions itself. Tried at every such request, even when the radio had nothing to change, so repeating it repairs Authentik |
| **When Authentik misses** | The radio's change stands. `502 authentik_unreachable` (or `authentik_off` without a token; `409 authentik_protected` for a superuser or service account), with the message and the account as the radio has it. `authentik_pending` stays true; the page says "Disabled here, not yet in Authentik" and offers **Try Authentik again** |
| **`authentik_pending`** | A column of `accounts` (migration 00027): set with each status change, cleared when Authentik takes it, written only for the status Authentik was asked about. Recording Authentik's answer runs through `store.Audited` and writes no entry: it isn't a change of the radio's data |
| **Delete account** (owner) | `DELETE /api/v1/admin/accounts/{id}`. `store.DeleteAccount`, one audited transaction under disabling's rules: the sessions, the saved profile and the row deleted, the entry keeping `name`, `email`, `role`, `status`, `sessions` and `saved_profile` as they were; last, inside the transaction, the Authentik user deleted (standalone). If Authentik misses, everything rolls back and nothing is deleted anywhere; repeating it tries again. A user Authentik no longer has counts as deleted |
| **The token** (owner) | `AUTHENTIK_API_TOKEN`, or `AUTHENTIK_API_TOKEN_FILE`; optional. Sent only to Authentik in `Authorization`, never logged, in no answer or error. Without it, disabling and enabling change the radio and answer `502 authentik_off`; deleting changes nothing |
| **The service account** | The blueprint's `radioarchive-accounts`: a service account in the group `RadioArchive accounts`, which isn't a superuser group, with the role `RadioArchive accounts`: `view_user`, `change_user`, `delete_user`, all three needed. Its token `radioarchive-accounts`, key `RA_AUTHENTIK_API_TOKEN`. New names: the old `radioarchive-api` account and `RadioArchive API` group and role are still removed (`state: absent`) |
| **Protected users** | The radio never switches off or deletes an Authentik superuser or any account that isn't a person's (`type` other than `internal` and `external`) |
| **What a disabled account keeps** (owner) | Everything: its row, role, saved profile and audit entries. Its sign-in is refused (`/?signin=disabled`), and it keeps its last sign-in time |
| **P3** | Unchanged: the live feed is anonymous and never sees a cookie, so a disabled listener goes on hearing what anyone hears; there are no live connections to close |
| **The Accounts page** (R84) | `/admin/accounts` in the admin frame: ArchiveUx 0.5.0's `DataTable` (name, email, role, status, first and last sign-in; sortable; searched in the browser by name or email; a card each on phones), `StateBadge` for the status, a Disable or Enable button and `MoreActions` (role, Try Authentik again, Delete account…) on every row but the admin's own ("You"), and `Confirm` before disabling, a role change and deleting, a dialog on phones. Admins see names and emails (owner) |
| **The module** | `Disable-RadioArchiveAccount`, `Enable-RadioArchiveAccount`, `Remove-RadioArchiveAccount` (ADR 0028's pattern; all ask first) |
| **The proven address** | The user attribute `email_proven`, shared with PagerArchive (issue #128), in place of `radioarchive_email_proven`: the radio's email mapping reads it, the standalone sign-up's activation step sets it |
| **The combined deployment** (owner) | `account.Service.SetStatus` is the one path every disabling and enabling takes, for M7 to reuse. M7 adds how a disabling in PagerArchive reaches the radio's sessions (R107) and leaves PagerArchive's users alone when deleting (`Config.DeleteAuthentikUsers`) |

## Disabling: the database first

PagerArchive deactivates a user in its own database first, then in Authentik, and tells the admin to repeat it if Authentik can't be reached (its ADR 0004, section 1). The radio does the same, for the same reason: the radio's lock-out must not depend on Authentik being up. The session check reads the account's status with the session at every account and admin request (ADR 0022, ADR 0025), so deleting the sessions in the same transaction as the status signs every browser out at its next request, and a sign-in while Authentik still lets the user in is refused by the radio. Disabling and the status are never cached.

The audit entry is written in the same transaction (ADR 0029): `status` `active` → `disabled` and `sessions` n → 0. Enabling records `status` alone. Repeating a disabling the radio already has writes no entry, as any request that changes nothing; it only reaches Authentik again.

**The shared function.** `store.SetAccountStatus` is the radio's half and `account.Service.SetStatus` the whole, and every disabling goes through them: the Accounts page, the module and, in M7, a disabling that comes from PagerArchive. `store.EndAccountListenerSessions` and disabling share the deletion (`endSessions`), so "ending a listener's sessions" means one thing.

**The last-admin rule.** The same lock and count as the role (ADR 0025): the active admins locked in subject order, the admin making the change among them, and disabling the last active admin refused. An admin never disables their own account, so with the lock an admin disabling another always leaves at least themselves; two admins disabling each other at once leave one (`TestIntegrationDisableAdminsAtOnce`).

## Authentik, and when it misses

The radio's change has committed before Authentik is asked. `authentik.Users.SetActive` reads the user, refuses a protected one, and patches `is_active` only when it differs. Authentik 2026.8 ends a switched-off user's Authentik sessions, so they can't sign in to anything it serves. Each call has a 10 s limit. If Authentik can't be reached, answers an error, or the user is protected, the account keeps its new status, `authentik_pending` stays true, and the API answers:

- `502 authentik_unreachable`: "Disabled in the radio: they're signed out of it and can't sign in to it. Authentik couldn't be reached just now. Try again: repeating it tries Authentik again."
- `502 authentik_off`: the same, saying the radio has no Authentik API token.
- `409 authentik_protected`: Authentik's superusers and service accounts are for Authentik's own administrators, in Authentik.

The answer carries the account as the radio has it, so the page and the module show the right state. A 2xx would hide that Authentik missed it; PagerArchive answers `502 directory_partial` for the same case.

**Why a column.** PagerArchive repairs Authentik at every repeat but doesn't show which accounts need it. Issue #105 asks the page to show an account disabled here but not yet in Authentik, so `authentik_pending` records it. It's cleared only for the status Authentik was asked about: an enabling that races a failed disabling's answer isn't marked done by it. Recording Authentik's answer isn't an admin's change of the radio's data, so it writes no audit entry; it still runs through `store.Audited`, so every admin write takes that one path.

**A user Authentik no longer has** is off already (disabling succeeds) and deleted already (deleting succeeds). Enabling one fails, since nobody can sign in as them.

## Deleting

Issue #118's open question 2, as recommended (owner): keep account rows; an admin's **Delete account** removes the row, the saved profile and the sessions, and in the standalone deployment the Authentik user. The three rows have no foreign keys to each other (ADR 0025), so the transaction deletes each. The audit entry is a deletion with every field as it was, so the log still says who the account was after the row has gone (ADR 0029).

**Authentik inside the transaction.** Deleting can't be "database first, then repeat": once the row is gone, the same request would find no account to retry. So the radio deletes the Authentik user last inside its own transaction: if Authentik misses, the transaction rolls back and nothing is deleted anywhere; if the commit fails after Authentik deleted the user, the row is still there, and repeating it finds Authentik's user gone (a `404`, which counts as deleted) and commits. The transaction holds the admins' and the account's row locks for at most Authentik's 10 s; deletions are rare.

Deleting the last active admin or one's own account is refused, as disabling is. A deleted listener who signs in again (their Authentik user deleted, so only by signing up again) is a new listener with an empty profile.

## The token and its service account

The owner brought the token back (issue #118, decision 3). Its risk is smaller than ADR 0009's was because **admin status no longer comes from Authentik** (ADR 0025): roles live in the radio's database, and no Authentik group is asked, so nothing this token can change, groups included, makes anyone a radio admin.

What it can do, checked against the compose stack's Authentik 2026.8 on 2026-11-25 (`scripts/ci/e2e-signin.sh` checks the account and role at every run):

- **Needed, and the least that works.** Authentik's model permissions are all-users: `view_user` (without it every call is `403`, since a user is found through the list it can see), `change_user` (switching off and on) and `delete_user` (deleting). Removing any one breaks the call that needs it. Nothing narrower exists for `is_active`; per-user object permissions would need a grant on every user, the policy ADR 0021 removed.
- **Refused.** Adding anyone, itself included, to a superuser group (`400`, "User does not have permission to add members to a superuser group"), adding to a group through the group's API (`403`) and setting a password (`403`).
- **Allowed, and accepted.** Renaming a user, changing their attributes and putting them in an ordinary group. So the token can set `email_proven` on a user, which makes a sign-in with that address proven; that matters only to `INITIAL_ADMIN_EMAIL` while no radio admin exists, a deployment's first minutes. In the combined deployment it could add a user to `pagerarchive-users`, the group that admits PagerArchive's users to the radio (issue #128), which PagerArchive's own token can do too. Neither makes anyone an admin of either app.

The token is read like the OIDC client secret: from the environment or a file, never both, trimmed, never logged, never in an answer, sent only to Authentik in `Authorization` through the compose network (`authentik.Transport`). Errors name an operation and an HTTP status, never a body or a URL. The service account is in a group whose `is_superuser` is false, with a role holding the three permissions. Its names (`radioarchive-accounts`, `RadioArchive accounts`) are new, so the old account's removal (`state: absent` on `radioarchive-api`, `RadioArchive API`) still means what it said.

**Protected users.** The token can switch off any user, so the radio checks before it does: an Authentik superuser, or an account whose `type` isn't a person's (`service_account`, `internal_service_account`), is refused before anything changes in Authentik. Authentik's `akadmin` who has signed in to the radio can still be disabled in the radio; only Authentik's own admin interface switches them off there.

## The Accounts page

R84's Accounts page, inside the admin frame (ADR 0027), its entry after Recorders. It reads `GET /api/v1/admin/accounts` and `GET /api/v1/admin/me` (to know which row is the admin's own) and follows the answers as every admin page does. ArchiveUx 0.5.0's building blocks:

- **`DataTable`**: Name (the row's header), Email, Role, Status, First signed in, Last signed in (the deployment's clock), every column sortable; a search field above it filters by name or email in the browser, with the count said politely. Under 40rem each row is a card.
- **`StateBadge`**: Active (ok), Disabled (neutral), and "Disabled here, not yet in Authentik" or "Enabled here, not yet in Authentik" (warning).
- **Actions**: on every row but the admin's own, which says "You", a **Disable…** or **Enable** button and **`MoreActions`**: **Make an admin…** or **Make a listener…**, **Try Authentik again** while pending, and **Delete account…** (danger).
- **`Confirm`** before disabling, a role change and deleting, inline under the table on desktops and a modal dialog on phones; its heading names the account, its words say what happens and that disabling keeps the saved settings, deleting can't be undone. Enabling needs no question. Cancelling says nothing changed.
- What happened is said in a status line; Authentik's miss, or a refusal, in an error banner with the API's words.

Admins see names and emails (owner, 2026-11-25; ADR 0025's open point 2). They're never logged.

## The proven address: `email_proven`

ADR 0025's email mapping says `email_verified: true` while the user's attribute `radioarchive_email_proven` equals their address. PagerArchive's invitation enrolment proves an address too, and in the combined deployment the radio's first admin can come from it. The two coordinators agreed a neutral name both apps set (issue #128): **`email_proven`**, the proven address. The radio's mapping reads it, the standalone sign-up's activation step sets it, and an operator who makes a user by hand vouches for the address by setting it (README.md). No deployment runs in production yet, so no user's old attribute needs moving; a development stack's users who signed up before this are unproven until they sign up again or an operator sets it, which matters only to a first admin.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Order (owner) | Authentik first, then the radio | A disabled user would stay in the radio while Authentik is down |
| Order (owner) | The radio only, with a link to Authentik's admin pages | The owner chose PagerArchive's way: the radio does both |
| The routes | `POST /accounts/{id}/disable` and `/enable` | The role is already a `PATCH` of the account; a status is one more field, and repeating it is the same request |
| When Authentik misses | `200` with a warning field | A client reading only the status would think Authentik took it; PagerArchive answers `502` |
| Pending | Repair at every repeat, record nothing (PagerArchive) | Issue #105 asks the page to show accounts not yet disabled in Authentik |
| Pending's write | An audit entry for Authentik's answer | It's not a change of the radio's data; each disabling already has its entry |
| Deleting | The radio first, then Authentik | A failure would leave an Authentik user whose radio account is gone, and nothing to repeat |
| Deleting | Authentik first, outside the transaction | A refusal of the radio's rules after Authentik deleted the user would leave the row |
| The role's permissions | The old ADR 0009 policy: object permissions per listener | It needed every user granted, and was what ADR 0021 removed; the radio's own check refuses protected users instead |
| The role's permissions | `change_user` alone | Authentik refuses every call without `view_user` (checked) |
| The account's names | Reusing `radioarchive-api` and `RadioArchive API` | The blueprint deletes those (`state: absent`); reusing them would make the removal delete the new account |
| The page's question | Inline on phones too | The owner's brief: a dialog on phones, where an inline panel can be off the screen |

## Open points

1. **The combined deployment (M7).** R107: a disabling in PagerArchive must reach the radio's sessions promptly; issue #128 agreed OIDC back-channel logout, and PagerArchive is checking whether Authentik 2026.8 sends it on deactivation. The radio's half is `account.Service.SetStatus` (and its session deletion), reused as it is. M7 also sets `DeleteAuthentikUsers` off, since PagerArchive's users are PagerArchive's, makes PagerArchive's users radio accounts automatically, and loads the accounts service account's token into PagerArchive's Authentik (the blueprint is shared, so `RA_AUTHENTIK_API_TOKEN` is needed there).
2. **A protected user's radio account.** An Authentik superuser who signs in to the radio can be disabled in the radio (Authentik stays as it is, and the page says so) but can't be deleted, since the radio won't delete their Authentik user and a deletion is all or nothing. Disabling is enough to lock them out of the radio; if the owner wants such rows deletable, deleting could skip Authentik for protected users.
3. **Roles in the module.** `Set-RadioArchiveAccount -Role` stays; disabling has its own commands rather than a `-Status` there, as PowerShell's verbs say what they do.

## Consequences

- An admin can lock a listener out of the radio at once, and out of Authentik too when it can be reached; the Accounts page says which.
- The API holds an Authentik token again, able to switch any person's user on or off, delete them, rename them, change their attributes and put them in ordinary groups, never a superuser group. Admin status doesn't depend on any of it.
- A deployment needs `AUTHENTIK_API_TOKEN` in `.env` (`scripts/dev-env.sh` writes one; the compose file requires it) for Authentik to be told. Without it the radio's half still works, and the admin is told Authentik wasn't.
- Every disabling, enabling and deletion is in the audit log with who, how and when; a deleted account's entry keeps who it was.
- `accounts` has one more column, `authentik_pending`.
- Users who signed up before this change have `radioarchive_email_proven`, which nothing reads any more.

## How each choice is verified

| Choice | Verification |
|---|---|
| Disabling: the status, every session ended, the entry, Authentik pending; again, no entry; Authentik's answer recorded for its status only, with no entry; enabling keeps the profile; own account, a listener, an unknown account and no "how" refused | `internal/store/account_status_test.go`, `TestIntegrationDisableAccount` |
| Two admins disabling each other at once leave one | `TestIntegrationDisableAdminsAtOnce` |
| Deleting: row, profile and sessions gone with an entry keeping every field; `before`'s error rolls everything back and comes back as it is; the refusals; deleting again is `404`; signing in again is a new listener | `TestIntegrationDeleteAccount` |
| The service: Authentik switched off with the token; down, the radio's change stands and is pending, and repeating repairs it; superusers and service accounts left alone; no token, Authentik not told; deleting with Authentik down deletes nothing; the combined deployment's deletion leaves Authentik alone; refusals never reach Authentik; the token in no log | `internal/account/admin_test.go`, `TestSetStatusReachesAuthentik` |
| Through the binary, issue #105's acceptance against the made-up Authentik: both browsers signed out at their next request, the sign-in refused, Authentik switched off; Authentik down: locked out, `502`, listed as pending, repeating reaches Authentik; enabling with the profile intact; own account `409`, a listener `403`; the audit log's entries; deleting the account, its profile and its Authentik user; the token in no log line or answer | `cmd/radioarchive/accounts_test.go`, `TestIntegrationDisableAccount` |
| Every new state-changing route has an audit probe: disabling, enabling (the `PATCH`'s second and third probes) and deleting, each found with who, how and when, and a refusal adding nothing | `cmd/radioarchive/audit_test.go`, `TestIntegrationAuditHarness` |
| The token's settings: optional, from a file, never both, never shown | `cmd/radioarchive/signin_test.go`, `TestSignInSettings` |
| P4: anonymous use adds no row | `cmd/radioarchive/p4_test.go`, unchanged |
| A10 and A13: admin data never in public answers; a disabled listener's live feed is anyone's | the A10 harness and `TestIntegrationSignedInSameLiveFeed`, unchanged |
| The page: the list, search, "You" without actions; disabling asks, signs them out and Authentik refuses them; enabling; roles; deleting asks naming what goes, a dialog on phones; Authentik unreachable: the alert, "not yet in Authentik", the radio's refusal, **Try Authentik again**; axe in both looks and themes at 375 and 1,280 px | `web/tests/accounts.spec.ts` via `scripts/ci/e2e-web.sh`, in Chromium, Firefox and WebKit |
| Against the real Authentik: an admin disables a signed-up listener on the Accounts page, their browser is signed out and Authentik says `is_active: false`; enabling, they sign in through Authentik's own pages with their profile; the service account and role as the blueprint says; the old account, group, role and token absent; the token in no API log line | `web/signin-tests/a33.spec.ts` and `scripts/ci/e2e-signin.sh` |
| The module's commands | `powershell/tests/Module.Tests.ps1`, `Meta.Tests.ps1` |
