---
status: proposed (the parts marked "owner, 2026-11-25" are the owner's decisions)
---

# 25. Admin accounts, roles and admin sign-in

Date: 2026-11-25

Admins are accounts in RadioArchive's own database, as in PagerArchive. A new `accounts` table holds one row for everyone who has finished a sign-in to the radio, keyed by the OIDC issuer and subject like the profile (ADR 0021) and the sessions (ADR 0022). The row holds the name and email as last signed in, a `role` (`listener` or `admin`) and a `status` (`active` or `disabled`). The first admin comes from `INITIAL_ADMIN_EMAIL` while no admin exists. The last-admin rule is checked in the same transaction as the change. The admin check reads the session and the account's row at every admin request, and the binary serves the admin API behind it for the first time (`SPEC.md` P3, P4, R51, R54, R55, R70, R84, R103, R110, R111, A13).

**The owner's decisions (owner, 2026-11-25):** roles and status live in RadioArchive's database, not in Authentik; the first admin is set up from `INITIAL_ADMIN_EMAIL` while no admin exists; the last-admin rule is checked in the same transaction as the change; being a PagerArchive admin doesn't make someone a radio admin (R55). PagerArchive's ADR 0004, sections 1, 2 and 5, was the reference; nothing PagerArchive-specific is copied. Everything else here is the implementer's choice (spec "How to use this spec", item 6), and is proposed.

This supersedes [ADR 0003](0003-systems-attachments-and-rejected-calls.md)'s section "Admin API and sign-in" where it says admin sign-in doesn't exist, that the only authenticators are test stand-ins, and that the binary mounts no admin API (its routes table and error shape stand). It supersedes [ADR 0009](0009-listener-sign-in-and-the-saved-profile.md)'s bullet "Admins" under "Leaving room for the combined deployment and admins", and its consequence for the owner about `radioarchive-admins`: no Authentik group is checked. In [ADR 0022](0022-listener-sessions-live-in-the-database.md), the admin route for ending a listener's sessions is now mounted by the binary.

## Summary

| Part | Choice |
|---|---|
| **The table** | `accounts` (migration 00020): `issuer`, `subject` (primary key), `name`, `email`, `role` (`listener`, `admin`), `status` (`active`, `disabled`), `first_signed_in_at`, `last_signed_in_at`. A partial index on the active admins. Existing sessions' accounts get a row, as listeners |
| **Made when** | Every finished sign-in upserts its row (name, email, last sign-in). Nobody who hasn't signed in has one (P4) |
| **The first admin** (owner) | While this issuer has no active admin, a sign-in whose email matches `INITIAL_ADMIN_EMAIL`, ignoring case, **and which Authentik says is proven** (`email_verified: true`) makes the account admin, in the same transaction as the check. Two at once wait on a transaction-level advisory lock, so one wins. Once any admin exists the setting does nothing |
| **Proven addresses** | Authentik's own email mapping always says `email_verified: false`. The radio's blueprint brings its own, which says true while the user's address is the one an emailed code proved (`radioarchive_email_proven`, which the sign-up records) |
| **Roles** | `PATCH /api/v1/admin/accounts/{id}` with `{"role": "admin"}` or `{"role": "listener"}`; `GET /api/v1/admin/accounts` lists them, admins first. An admin never changes their own role (`409 own_role`) |
| **The last-admin rule** (owner) | In one transaction, with the active admins locked (`SELECT … FOR UPDATE`): the admin making the change must still be one of them, and demoting the last active admin is refused (`409 last_admin`). Two admins demoting each other at once leave one |
| **The admin check** | `account.Service.Admin`: the session cookie (ADR 0022), then the account's row, role `admin` and status `active`, read with the session in one query at every admin request, nothing cached. `401` signed out, `403` not an admin, `503` if the check can't be made |
| **What handlers get** | The admin's account (`server.AdminFrom`), for issue #112's audit log; logs name the admin by account ID, never name or email |
| **Status** | A `disabled` account's sign-in is refused (`/?signin=disabled`, a plain notice), and its sessions aren't live. Issue #105 sets and clears it |
| **Another site** | State-changing admin requests from another site are refused with `403 other_site` (Go's `http.CrossOriginProtection`, on top of `SameSite=Lax`) |
| **The binary** | Serves the whole admin API under `/api/v1/admin`, behind the check. With sign-in off, nobody is an admin and every admin route answers `401` |
| **P3** | Unchanged and structural: public routes and the live feed get no cookie or `Authorization`, so an admin hears and sees exactly what anyone does (A13's listener half) |
| **The frame** | `GET /api/v1/account` says `"admin": true` for an admin, so the frame (issue #111) can show the Admin menu to admins only. It opens nothing by itself |

## The accounts table

Until now an account was an Authentik identity plus `listener_profiles` and `listener_sessions` rows. Admins need a role kept somewhere the radio decides, and issue #105 needs a list of accounts to find a listener to disable, so the radio now keeps a row for every account that has signed in. The name and email are Authentik's at the last sign-in. They are what admins see, and they are never logged.

The sessions query joins the account's row (`GetListenerSession`), so one indexed read finds the session, the role and the status. A session whose account has no row isn't a session. The migration gives every account with a session a row, as a listener, so nobody is signed out by it. Accounts with only a saved profile get their row at their next sign-in.

Neither `listener_profiles` nor `listener_sessions` has a foreign key to `accounts`. Profiles saved before this change have no account row, and a key would need one made up for them. Issue #105's deletion, if the owner chooses it, deletes the three rows in one transaction.

## The first admin, and what "proven" means

PagerArchive has a "Set up the first admin" button because its users are invited. Here a standalone deployment's enrolment is open, and the emailed code proves the address (ADR 0009), so the promotion happens at sign-in, with no button.

An address is only as good as its proof. With nothing more, anyone who signed up first with the owner's address could become admin, if Authentik let them in without proving it. So the API promotes only a sign-in whose ID token says `email_verified: true`.

**What Authentik says.** Authentik 2026.8's managed mapping for the `email` scope (`goauthentik.io/providers/oauth2/scope-email`, in `/blueprints/system/providers-oauth2.yaml` in the image) returns `"email_verified": False` for everyone, always. Authentik keeps no record of whether an address was proven. So `radioarchive.yaml` brings the radio's own mapping, `radioarchive-email`, in its place on the provider. It says `email_verified: true` while the user's `radioarchive_email_proven` attribute equals their email, ignoring case. The standalone sign-up's activation step, reached only after the emailed code was entered, sets that attribute to the address the code went to. A user whose address changes later is unproven again until it is proven.

**Without sign-up.** With `LISTENER_SIGNUP` off, nobody can enrol, and the operator makes the first admin's user in Authentik's admin interface. Setting `radioarchive_email_proven` to the address says the operator vouches for it (README.md, "Admins").

**The combined deployment.** PagerArchive's invited users don't carry the attribute, so none of them can become the radio's first admin until an Authentik administrator sets it. That's left open for M7 (open point 1).

**Locking.** "No active admin exists" can't be locked row by row while there is no admin's row. The first-admin check therefore takes a transaction-level advisory lock (`pg_advisory_xact_lock`) before it reads the active admins. Two sign-ins with the address at once run one after the other, and the second finds the first's admin. Only a sign-in that could promote takes the lock, so ordinary sign-ins never wait on it.

A disabled account is never promoted, and its sign-in keeps its last sign-in time.

## Roles and the last-admin rule

An admin promotes and demotes other accounts. Being an admin of PagerArchive, or of Authentik, makes nobody a radio admin, because no Authentik group is asked (R55). Nothing an Authentik token can change grants radio admin, which is what makes issue #105's token acceptable (owner decision 3 in issue #118).

`store.SetAccountRole` runs in one transaction:

1. It locks the issuer's active admins, in subject order (`SELECT subject … FOR UPDATE`), so concurrent changes queue and never deadlock.
2. The admin making the change must be among them. One demoted or disabled a moment earlier is refused (`403 admin_required`).
3. It locks the target account's row. An unknown account is `404 account_not_found`.
4. Demoting an active admin when they are the last active admin is refused (`409 last_admin`).

An admin never changes their own role (`409 own_role`), so with step 2 an admin demoting someone always leaves at least themselves. The count still guards the rule, and issue #105's disabling uses the same lock and count. Two admins demoting each other at once: the first commits, the second's step 1 waits and then rereads the set, and step 2 refuses it. One admin is left.

## The admin check

`server.AdminAuthenticator` changes to `Admin(w, r) (account.Admin, error)`. `account.Service` implements it. It reads the session cookie and then, in the same query, the account's role and status. Nothing is cached, so a demotion takes effect at the next request, and a disabling signs the account out at its next request. The session is renewed as at any account request (ADR 0022). The `401`, `403` and `503` answers keep R78's shape: `admin_sign_in_required`, `admin_required` and `admin_check_unavailable`.

The handlers keep their signature. The `who` they log is now the admin's account ID (the OIDC subject), and the whole `account.Admin` (issuer, subject, name, email) is in the request's context, through `server.AdminFrom`, for issue #112's audit log. The tests' stand-in (`X-Test-Admin`) is still only in `_test.go` files.

**Another site.** Every admin route passes Go's `http.CrossOriginProtection` first. A browser's state-changing request whose `Sec-Fetch-Site` or `Origin` names another site is refused with `403 other_site` before the session is read. A request with neither header isn't a browser's, and goes on to the check. That leaves room for issue #116's module, which signs in with its own token. The session cookie's `SameSite=Lax` is the first lock; this is the second.

## What stays as it was

- **P3.** Public routes and the live feed still get no cookie or `Authorization`, so signing in, as a listener or as an admin, changes nothing anyone hears or sees. Hidden talkgroups appear in admin answers only. The A10 harness is unchanged.
- **P4.** Anonymous use adds no row. A sign-in adds its session's row and its account's row; neither holds an address. Signing out deletes the session and keeps the account.
- **R70.** Admins' exemption from heavy-use limits is M5's.
- **Admin playback** isn't audited (R34). Every admin change is audited from issue #112 on (R54).

## The browser tests' fixture

ADRs 0012, 0017 and 0018 said the fixture's admin commands go "when the binary serves the admin API". `testing/webfixture`'s talkgroup changes (`talkgroup <ID> state|delay|alert|led`) and the retention period (`retention <days>`) now go through the binary's admin API. The browser tests' binary signs in against `testing/fakeauthentik`, a made-up OIDC provider that signs everyone in at once as the fixture's admin, `INITIAL_ADMIN_EMAIL`, with the address proven. The fixture signs in through it, as a browser would, then sends the change. The made-up provider is `internal/oidcclient/oidctest` served on its own; only tests use either.

What stays: the fixture's setup itself (systems, talkgroups, units and Bench's attachment, written as SQL before the tests start, as issue #115's import will be), the calls a recorder stores (`calls`, `call`, `call-start`, `call-end`, `call-dated`), retention's deletion (`call-delete`) and the nightly run on a clock of the tests' own (`nightly`). No admin route does those.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Where roles live (owner) | The Authentik group `radioarchive-admins` | The owner's decision: as PagerArchive, in the radio's database. A token with user rights (issue #105) could add someone to a group |
| The first admin (owner) | A "Set up the first admin" button, as PagerArchive | Enrolment here is open, or PagerArchive's invitation in the combined deployment; promotion at sign-in needs no extra step |
| Which address counts | Any sign-in with the address | Whoever signs up with it first would be admin if the address weren't proven |
| Proving it | Trust Authentik's managed mapping | It always says `false` (Authentik 2026.8) |
| Proving it | Treat every active user as proven | A user an Authentik admin made, or one whose address changed, was never proven |
| Locking the first admin | `SELECT … FOR UPDATE` on the admins alone | With no admin's row there is nothing to lock: two sign-ins at once both find none |
| Locking the first admin | A `SERIALIZABLE` transaction | Needs a retry loop at every sign-in |
| Reading the role | Cache it with the session | A demotion or disabling would wait for the cache |
| Reading the role | A second query after the session's | The same answer in two round trips; the join is one |
| What handlers get | Change every handler's signature to take the account | Every admin file changes, and parallel slices' too; the context carries it |
| Another site | `sameOrigin` as the account's routes do | Go's `CrossOriginProtection` is the standard's way, and lets a non-browser client through |
| Sign-in off | Don't mount the admin API | Mounting it always keeps one route table; every route answers `401` |
| Foreign keys from profiles and sessions | Add them | Old profiles have no account row; deletion (issue #105) can delete all three in one transaction |

## Open points

1. **Proven addresses in the combined deployment (for M7).** PagerArchive's Authentik doesn't record `radioarchive_email_proven` when it invites someone, so the radio's first admin there needs an Authentik administrator to set it, as without sign-up. M7 may want PagerArchive's invitation flow to set it, since the invitation proves the address. The reversible choice taken now: promote only what the radio's mapping says is proven.
2. **What admins see of accounts.** `GET /api/v1/admin/accounts` shows admins every account's name and email, because finding someone to promote needs them. This is issue #118's open question 2 for the owner (what the radio keeps about accounts). If the owner decides otherwise, the list narrows without changing the table.
3. **A second factor for radio admins.** Issue #118's open question 4. Nothing here asks Authentik for a stronger sign-in for admins; recommendation (a), as PagerArchive, needs no change.

## Consequences

- The binary serves the admin API. An admin's browser session opens it, at every request, from the account's row.
- Every account that has signed in has a row, holding its name and email as at its last sign-in, which admins can list. A database backup holds them.
- The first admin needs `INITIAL_ADMIN_EMAIL` set, and an address Authentik has proven: by signing up, or by an operator setting `radioarchive_email_proven`.
- Changing Authentik's address for an admin doesn't change their role. It changes the name and email the radio shows at their next sign-in.
- Changing `AUTHENTIK_URL` changes the issuer, so the old admins' rows no longer match. While the new issuer has no admin, `INITIAL_ADMIN_EMAIL` makes one again.
- The browser tests' binary runs with sign-in on, against a made-up provider.

## How each choice is verified

| Choice | Verification |
|---|---|
| First admin: another case, proven only, once one exists nobody else, another address a listener; upsert of name and email | `internal/store/accounts_test.go`, `TestIntegrationFirstAdmin` |
| Two sign-ins with the address at once make exactly one admin | `TestIntegrationFirstAdminAtOnce` (fails with the advisory lock removed) |
| Own role refused; a demoted admin changes nothing; the last active admin can't be demoted; a disabled account's sign-in keeps its role and last sign-in | `TestIntegrationLastAdmin` |
| Two admins demoting each other at once leave one | `TestIntegrationLastAdminAtOnce` (fails with the checks removed) |
| A session is read with its account's role and status; no row, no session | `internal/store/sessions_test.go` |
| The service: the account recorded at sign-in, `admin` in the account's answer, `Admin` from the row at each request, demoted and disabled at the next, a disabled sign-in refused with no session, the promotion logged without the address | `internal/account/admin_test.go`, `TestAdmins` |
| Every admin route the binary serves: `401` signed out, `403` a listener, answered for an admin, a demoted admin `403` and a disabled one `401` at their next request; own role `409`; another site `403` and nothing made; no email in the logs | `cmd/radioarchive/admin_test.go`, `TestIntegrationAdminRoutes`, through a real sign-in |
| An unproven `INITIAL_ADMIN_EMAIL` is no admin | `TestIntegrationFirstAdminUnproven` |
| A13's listener half: as an admin, the public routes and the live feed answer as they do anyone, 1003 absent | `TestIntegrationA13AdminHearsWhatAnyoneHears` (the binary, real admin session); `internal/server`'s `TestIntegrationSignedInSameAnswers` and `TestIntegrationSignedInSameLiveFeed` over every public route, with an admin's credentials |
| P4: anonymous use, the admin API's included, adds no row; a sign-in adds its session's and account's rows, holding no address | `cmd/radioarchive/p4_test.go` |
| The binary's admin routes are the whole admin API | `cmd/radioarchive/main_test.go`, `TestIntegrationBinaryRoutes` |
| Against the real Authentik: `INITIAL_ADMIN_EMAIL`, in another case, signed up and proven by its code, is an admin; the admin API answers it; the public API answers it as anyone; another account is a listener and gets `403` | `web/signin-tests/a33.spec.ts` via `scripts/ci/e2e-signin.sh` |
| The disabled notice, axe in both looks and themes at 375 and 1,280 px | `web/tests/listen.spec.ts` |
| The fixture's admin changes go through the admin API | `web/tests` `@live` tests via `scripts/ci/e2e-web.sh` |
