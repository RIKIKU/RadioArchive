---
status: accepted (2026-11-25, by the owner)
---

# 22. Listener sessions live in the database

Date: 2026-11-25

> **Partly superseded by [ADR 0033](0033-disabling-enabling-and-deleting-accounts.md) (2026-11-25).** The open part of "Disabling a listener", how the radio disables the user in Authentik, is decided there: the radio's change first (status, sessions, audit entry), then Authentik's API with a token of the radio's own accounts service account (owner, 2026-11-25). Ending sessions and disabling share one deletion. The text below is as it was decided.

A signed-in listener's session moves out of the sealed cookie and into RadioArchive's database, as PagerArchive keeps its sessions. The browser's host-only cookie holds only a random 256-bit session ID. The database holds the ID's SHA-256 in `listener_sessions`, with the account, the name and email for the account menu, and the ID token for signing out. Every account request reads that row, so deleting it ends the session at the next request. Signing out deletes it at once. An admin can end every session of a listener, which is the first half of disabling them. The owner asked for this on 2026-11-25: a user switched off in Authentik wasn't signed out straight away, and should be handled as PagerArchive handles it (`SPEC.md` P3, P4, R110, R111). The rest are the implementer's choices (spec "How to use this spec", item 6).

This supersedes the **Session** row of [ADR 0009](0009-listener-sign-in-and-the-saved-profile.md)'s summary, its section "The session, and why nothing is stored" except the logging and P4 test paragraph, the **Sessions** row of its considered options, and its consequence that rotating the OIDC client secret signs every listener out. In [ADR 0021](0021-the-saved-profile-lives-in-radioarchives-database.md) it supersedes the wording that the session is a sealed cookie carrying the issuer, that the menu's name and email come from it, and the "switched off in Authentik" item under Deletion. Sign-in itself, the routes, the profile and the rule that public routes see no cookie are unchanged.

## Summary

| Part | Choice |
|---|---|
| **The cookie** | `ra_session`: 32 random bytes, base64url (43 characters). Host-only, `HttpOnly`, `SameSite=Lax`, `Secure` on https, `Max-Age` 30 days, set again when the session is renewed |
| **The row** | `listener_sessions` (migration 00017): `id_hash` (SHA-256 of the ID, primary key), `issuer`, `subject`, `name`, `email`, `id_token`, `created_at`, `last_used_at`, `expires_at`. Indexes on `(issuer, subject)` and `expires_at` |
| **Each request** | `GET /api/v1/account`, `PUT` and `DELETE /api/v1/account/profile` read the session by its hash, live at the API's clock: one primary-key lookup. A cookie that doesn't decode to 32 bytes is never looked up |
| **Lifetime** | 30 days from last use. Renewed (row and cookie) at most once an hour, by any account request. Expired rows are deleted when the API starts and hourly after |
| **Signing out** | `POST /auth/signout` deletes the row and takes its ID token, clears the cookie, and holds the token in memory for 2 minutes under a one-time handle. The handle goes in `ra_signout`, on `/auth/end-session` only |
| **Ending a listener's sessions** | `store.EndAccountListenerSessions(issuer, subject)`, `account.Service.EndAccountSessions(subject)` (this deployment's issuer), and `DELETE /api/v1/admin/listeners/{subject}/sessions`, mounted only with the admin API (M4). It answers `{"ended": n}` |
| **Live connections** | None to close. The live feed is anonymous and never sees a cookie (P3, R111, ADR 0009), so no socket belongs to a session |
| **The sign-in cookie** | Unchanged: `ra_signin`, AES-256-GCM sealed (state, nonce, PKCE verifier, return path), `/auth/` only, 10 minutes |
| **Anonymous listeners** (P4) | Unchanged: only a finished sign-in adds a row |

## Why

ADR 0009 made the session a sealed cookie so that the server kept nothing about a listener, signed in or not. ADR 0021 already put signed-in listeners' profiles in the database, so that reason is gone for them. A sealed cookie also can't be ended early. Signing out only cleared the browser's copy, so a copy taken before sign-out stayed valid for 30 days. A user switched off in Authentik stayed signed in to the radio until the cookie ended, because since ADR 0021 the radio no longer asks Authentik about users. A row the API reads at every request can be deleted, and the next request finds nothing.

## How it works

**The ID and its hash.** `crypto/rand` makes 32 bytes, and the cookie carries them base64url-encoded. The database keeps only their SHA-256, so a copy of the table, or a backup, signs nobody in. The ID has 256 bits of entropy, so a plain SHA-256 (no salt, no slow hash) is enough. A guess has to find the ID itself.

**Reading it.** Each account request decodes the cookie and refuses anything that isn't 32 bytes without asking the database. That includes a cookie sealed before this change, which signs that browser out once. No deployment runs in production yet. A valid ID is looked up with `id_hash = $1 AND expires_at > now`, where `now` is the API's clock, so the API decides a session's life and the tests can move time. A row from another issuer than this deployment's isn't a session, as ADR 0021 had it for the cookie. If the database fails, the account answers `503 account_unavailable` and doesn't clear the cookie. That is not the same as being signed out.

**Renewal.** A session last used more than an hour ago gets a new `last_used_at` and `expires_at` (now plus 30 days), and the cookie is set again with a fresh `Max-Age`. So most requests are one read and no write. A failed renewal is logged and the request goes on.

**The sweep.** `account.Service.Run` deletes rows whose `expires_at` has passed, once at start and then hourly. `serve` runs it beside the retention job.

**What the row holds.** The issuer and subject are the account, as its profile is keyed. The name and email are for the account menu, and they come from the ID token's claims at sign-in. The ID token is kept only for the sign-out hop. It is never in a cookie, never in an answer the app's script can read, and never logged. It holds nothing the row doesn't already hold. The row holds no address or user agent.

## Signing out

`POST /auth/signout` keeps its same-site check. It deletes the session's row and gets the ID token back from the same statement (`DELETE … RETURNING id_token`). Then it clears the cookie. If the database fails, the cookie is still cleared, a warning is logged, and the row expires unused. Nobody holds its ID any longer.

Before this change, the ID token for Authentik's end-session endpoint travelled in a sealed cookie, `ra_signout`. Now it isn't in a cookie at all. As PagerArchive does, it is held in memory for at most 2 minutes under a random one-time handle. The handle goes in `ra_signout` (HttpOnly, `SameSite=Lax`, path `/auth/end-session` only). `GET /auth/end-session` takes the token once, clears the cookie, and redirects to Authentik with `id_token_hint` as before. An API restart between the two hops loses the handle. The radio's session has already ended, so the browser lands on `/?signed_out=1`, and its Authentik session stays until it ends or the listener signs out there. That is rare enough not to keep handles in the database.

PagerArchive also asks Authentik's discovery document before setting the handle, to warn early when Authentik can't be reached. RadioArchive already warns at the second hop (`&authentik=unreachable`), so that is left as it was.

## Disabling a listener

In PagerArchive an admin deactivates a user in the app. That deletes their sessions and closes their live connections at once, and then disables them in Authentik. RadioArchive's admin pages come with M4, so this change builds the first half:

- `store.EndAccountListenerSessions(issuer, subject)` deletes every session of the account and says how many there were.
- `account.Service.EndAccountSessions(subject)` does that for this deployment's issuer.
- `DELETE /api/v1/admin/listeners/{subject}/sessions` calls it, behind the admin authenticator, when the admin API is given it with `Admin.WithListenerSessions`. The binary still mounts no admin API (`TestIntegrationNoAdminAPI`). The log names the admin and the count, not the listener.

The live feed has no connections to close: it is anonymous by design (ADR 0009), so a listener whose sessions end goes on hearing what anyone hears. Disabling the user in Authentik, so that they can't sign in again, is left to M4 ([issue #105](https://github.com/RIKIKU/RadioArchive/issues/105)). It must not bring back a standing Authentik API token with `change_user` on listeners, which ADR 0021 removed ([issue #73](https://github.com/RIKIKU/RadioArchive/issues/73)). M4 decides how, for example with the admin's own token.

Deleting a listener's profile (**Clear saved settings**) doesn't end their session. The two are separate rows.

## Cookies kept as they were

The sign-in cookie, `ra_signin`, stays sealed and short-lived. It holds what a sign-in under way needs (state, nonce, PKCE verifier, return path) for 10 minutes, on `/auth/` only. PagerArchive keeps the same in a short-lived cookie too. Moving it into the database would add a row for every sign-in that is started, including ones never finished, and those come from listeners who aren't signed in. That goes against P4 for no gain. Its key is still HKDF-SHA256 of the OIDC client secret. Rotating the secret now fails only the sign-ins under way at that moment; sessions stay.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Revocation | Keep the sealed cookie, plus a list of revoked sessions | A list to keep and sweep anyway, and it still can't end a cookie it never saw |
| Revocation | Ask Authentik about the user at each request | Needs a standing token again (ADR 0021, issue #73), and an Authentik round trip each time |
| What the cookie holds | The session ID itself as the row's key | A copy of the table or a backup would sign people in |
| The hash | A salted or slow hash | The ID is 256 random bits; SHA-256 can't be reversed by guessing |
| Renewal | Every request | A write per read for no gain; hourly is PagerArchive's |
| Expiry | Database time, `now()` | The API's clock keeps the session's life in one place and testable |
| The sign-out token | Keep it sealed in `ra_signout` | The token would leave the server; the handle keeps it in |
| The sign-in cookie | A row per sign-in started | Stores something for listeners who haven't signed in (P4) |

## Consequences

- Signing out ends the session for every copy of the cookie, at once.
- An admin can end a listener's sessions at once (from M4's pages). Disabling them in Authentik is M4's.
- Each signed-in browser adds one row, until it signs out or the session expires. P4 still holds for anonymous listeners, and `cmd/radioarchive/p4_test.go` checks both sides.
- Each account request costs one primary-key read, and at most one write an hour.
- Browsers signed in before this change are signed out once.
- An API restart between the two sign-out hops leaves that browser's Authentik session to end on its own.
- A database backup holds signed-in listeners' names, emails and ID tokens until their sessions end. The ID token is no credential here. The API accepts it nowhere, and Authentik takes it only as a hint.

## How each choice is verified

| Choice | Verification |
|---|---|
| The table: kept under a hash, live until it expires, renewed, ended with its ID token, ended for an account, swept | `internal/store/sessions_test.go` |
| The cookie holds only the ID; the row has its SHA-256, the account and the ID token; the ID token is in no cookie, answer or log | `internal/account/account_test.go`, `TestSignIn` and `TestSignOut` |
| Sign-out deletes the row; the old cookie replayed is no session; the hop sends the token once | `TestSignOut` |
| Ending an account's sessions signs its browsers out at their next request and leaves others alone | `TestEndAccountSessions` |
| 30 days from last use, renewed at most hourly (row and cookie), the sweep | `TestSessionLife` |
| The database down: unavailable, not signed out | `TestSessionStoreDown` |
| The admin route: admins only, mounted only when given the sessions | `internal/server/admin_listeners_test.go`; the binary serves no admin route (`cmd/radioarchive/main_test.go`) |
| P4: a sign-in adds exactly the session's row, holding no address and not the cookie's value; sign-out removes it; anonymous use adds nothing | `cmd/radioarchive/p4_test.go` |
| Sign-in, save, clear and sign-out against a real Authentik | `web/signin-tests/a33.spec.ts` via `scripts/ci/e2e-signin.sh` |
