---
status: accepted (2026-11-25, by the owner; where the profile lives superseded by ADR 0021)
---

# 9. Listener sign-in and the saved profile

Date: 2026-11-24

> **Partly superseded by [ADR 0021](0021-the-saved-profile-lives-in-radioarchives-database.md) (2026-11-25).** The profile now lives in RadioArchive's database, keyed by the account's issuer and subject, and the API holds no Authentik token. The summary rows **Where the profile lives**, **How the server writes it** and **The token**, the section "Where the profile lives, and the token", and what follows from it (an account the token can't reach saving nothing, a switched-off user signed out at the next read, the first consequence for M4, **Migration: none**) are superseded. The rest stands. The text below is as it was decided.

A listener signs in with Authentik, which RadioArchive uses as an OIDC client with PKCE, as PagerArchive does. Signing in only saves preferences. The listening set with its priority marks, the playback settings and the look are kept in one attribute of the listener's Authentik user, `radioarchive`, and nowhere else. The API writes that attribute with a standing Authentik API token. The token's role holds "view user" and "change user" only on the users of listeners who have signed in to the radio, as object permissions. The session is a sealed, host-only cookie, so the server stores nothing about any listener. Public routes are handed no cookie or credential, so being signed in can't change anything a listener hears. The owner chose the standing token and enrolment by emailed code in [the parent's open question 3](https://github.com/RIKIKU/RadioArchive/issues/53) (`SPEC.md` P3, P4, R41, R48, R82, R103, R110, R111, A33). The rest are the implementer's choices (spec "How to use this spec", item 6). Built in [Listener sign-in and the saved profile](https://github.com/RIKIKU/RadioArchive/issues/61).

## Summary

| Part | Choice |
|---|---|
| **Sign-in** | OIDC authorization code flow with PKCE (S256), state and nonce, verified by `coreos/go-oidc` (`internal/oidcclient`). A confidential client, `radioarchive`. The application refuses a sign-in without an S256 challenge |
| **Where the profile lives** | The listener's Authentik user, attribute `radioarchive`: `{"v":1,"talkgroups":[…],"look":"console","theme":"dark","alerts":false}`. The set is in the browser's own format (ADR 0008). The download basket joins it in M3 |
| **How the server writes it** | `GET` then `PATCH /api/v3/core/users/{id}/` with the API's token (`internal/authentik`), changing that one attribute and keeping the others |
| **The token** | `AUTHENTIK_API_TOKEN` or `AUTHENTIK_API_TOKEN_FILE`, read at start, sent only in the `Authorization` header to Authentik, and never logged. A blueprint makes it for a service account whose role holds no global permission (below) |
| **Session** | `ra_session`: AES-256-GCM sealed JSON (subject, name, email, ID token, times), host-only, `HttpOnly`, `SameSite=Lax`, `Secure` on https. 30 days from last use. The key is derived with HKDF from the OIDC client secret |
| **Routes** | `/auth/signin`, `/auth/callback`, `POST /auth/signout`, `/auth/end-session` (top-level hops). `GET /api/v1/account`, `PUT` and `DELETE /api/v1/account/profile` (the browser app's). A new route kind, `account`, in the server's table |
| **Sign-in never changes listening** | The server strips `Cookie` and `Authorization` from every request before a public route sees it. A test compares every public route's answer signed in and out |
| **First sign-in** (R41) | The browser keeps the ID of the account it was last matched with. No match yet: an account with no set gets the browser's; one with the same set is matched; one with another set brings one prompt: **Use my account's set** or **Keep this browser's set**. Nothing is merged |
| **Signed in and matched** | The account's settings are the browser's when the page opens, and changes are saved to it, debounced. Hold and the session's additions are never saved |
| **Hidden talkgroups** | `GET /api/v1/account` shows only public talkgroups (P1). A saved entry that is hidden now stays in the account, unseen, when the browser saves next |
| **Clearing** (R110) | **Clear saved settings** in the account menu, confirmed, removes the attribute. The browser keeps its own. The account stays empty until the listener changes something |
| **Enrolment** (R103) | **Sign up** on Authentik's sign-in page while `LISTENER_SIGNUP` is `true` (the default): email, name and password, then a 6-digit code emailed to that address, at most 5 an hour per address. Off: no **Sign up**, and the flow refuses even at its own address |
| **Blueprints** | `radioarchive.yaml`, the client (provider, application, sign-out, the API's account), which the combined deployment can load into PagerArchive's Authentik. `radioarchive-standalone.yaml`, the sign-in, enrolment, password reset and brand, which only a standalone deployment loads |
| **Compose** | Authentik 2026.8 server and worker beside the API, its own database in RadioArchive's PostgreSQL, and Mailpit for development and tests. A hash label recreates Authentik when a mounted file changes, as PagerArchive's does |
| **Migration** | None. The server keeps nothing new |

## Where the profile lives, and the token

> **Superseded by [ADR 0021](0021-the-saved-profile-lives-in-radioarchives-database.md).**

R110 puts a listener's settings in their Authentik profile and nowhere else. Authentik offers one place a client can write per user: the user's `attributes`. A user's OAuth access token can't write them, because the user API needs model permissions. So the owner approved a standing token on the server (question 3a, 2026-11-24).

**What the token can do.** Authentik has no permission narrower than `authentik_core.change_user` for attributes. On the dev stack (Authentik 2026.8.5), a token with global `view_user` and `change_user` could:

- write any user's attributes, rename them, switch them on or off, and change their path;
- add any user to any group that isn't a superuser group.

It couldn't create users, set passwords, impersonate, start recovery, or call a group's `add_user`.

So the role, `RadioArchive API`, holds **no global permission**. An expression policy on the `radioarchive` application, `radioarchive-profile-access`, runs when someone signs in to the radio. It gives the role `view_user` and `change_user` on that one user, as object permissions. It never gives them for Authentik's superusers, members of `radioarchive-admins` or service accounts, and it takes them away from such a user at their next sign-in. The token can reach only people who have signed in to the radio as listeners. It can't reach `akadmin`, PagerArchive's other users, or the radio's admins. An account it can't reach is signed in with nothing saved (`"saves": false`; the menu says "This account's settings aren't saved here").

**What remains for the owner** (see Consequences). For a listener's own user, `change_user` still lets the token's holder add that user to `radioarchive-admins`. Until M4 that group unlocks nothing. Once admin sign-in exists, a stolen token could make a listener account an admin, unless M4 decides admins some other way.

**Protecting it.** The token comes from the environment or a file (`_FILE`, for Docker or systemd secrets). It is sent only to Authentik, in a header. Errors from Authentik name the operation and status, never a body or URL, and nothing logs the token. Rotating it means changing `AUTHENTIK_API_TOKEN` in `.env`. The blueprint (`RA_AUTHENTIK_API_TOKEN`) and the API read the same value.

**Read, then write.** Authentik's `PATCH` replaces `attributes` whole, so the API reads the user and writes back every other attribute unchanged. Another app changing its own attribute of the same user at the same moment could lose that change. At this scale that is accepted.

## The session, and why nothing is stored

P4 and R110 together mean the server keeps nothing about a listener, signed in or not. PagerArchive keeps sessions in its database. Here the session is the cookie itself, sealed with AES-256-GCM, with the cookie's name bound in as additional data. It holds the Authentik subject, the name and email for the account menu, and the ID token for signing out. A cookie that would pass 3,800 bytes drops the ID token; signing out then still ends Authentik's session but doesn't come back. A sign-in under way (`ra_signin`: state, nonce, PKCE verifier, return path) and a sign-out's ID token (`ra_signout`) are sealed cookies too, on their own paths for minutes.

The key is HKDF-SHA256 of the OIDC client secret with its own label, so the operator keeps no extra secret. Rotating the client secret signs every listener out, which costs them nothing but signing in. A session lasts 30 days from its last use. It is renewed at most once a day, when the account is read. A user switched off in Authentik is signed out at the next read.

Revocation is by Authentik: a deactivated user is signed out. A deleted user reads as one the token can't reach, so they stay signed in with nothing saved until the cookie ends. A stolen cookie gives only that listener's preferences, which is what a session is for.

What isn't logged: no address, email, name, subject, token or query string, on success or failure. A test drives every public route and the sign-in and account routes, as a listener who hasn't signed in, through the binary's handler. It checks that no table gains or changes a row and that no log line holds the listener's address, connecting or forwarded (`cmd/radioarchive/p4_test.go`).

## Sign-in never changes what a listener hears

P3 and R111 are kept by structure, not by care. The account package is the only code that reads the session. The server's route table hands each public route (`internal/public`, behind the gate) a copy of the request with `Cookie` and `Authorization` removed. So a public route can't tell a signed-in listener from anyone else, and that holds for routes yet to be written too. `TestIntegrationSignedInSameAnswers` replays every HTTP public route's A10 probes with and without a session and requires the same status, headers and body. The A10 harness fails while any public route lacks probes, so a new route is compared as soon as it is mounted. `TestIntegrationSignedInSameLiveFeed` opens two live sockets with the same set, one carrying a session cookie and a bearer token, and requires the same messages, byte for byte, through two live calls. In the browser, `a33.spec.ts` compares the catalogue, search and fireground answers for one listener signed in and out.

The listener's session is not an `AdminAuthenticator`, and the binary still serves no admin API. A test checks both, with sign-in on. Listening needs no Authentik: the API starts and serves listeners while Authentik is down or still starting. Discovery is lazy, and every failure ends on the Listen page with a notice, never a block.

## First sign-in and after (R41)

The browser keeps `localStorage["radioarchive.account"]`: the ID of the account its set was last matched with, and nothing else. When the page finds its listener signed in:

1. **Matched before**: the account's settings (set, marks, look, theme, alerts) replace the browser's, when the account holds any. Then each change is saved to the account, 400 ms after the last one.
2. **Not matched, and the account holds no set**: the browser's settings are saved to it.
3. **Not matched, and the account holds the same**: matched, with nothing asked.
4. **Not matched, and the account holds another set**: one prompt, naming the account's talkgroups: **Use my account's set** (the browser's is replaced) or **Keep this browser's set** (the account's is replaced). Nothing is merged. The answer matches the browser, so the prompt doesn't come again.

The look, theme and playback settings go with the set, as one profile. Signing out unmatches the browser and leaves it its set. Hold and the talkgroups added for the session live apart from the saved set (ADR 0008), so they are never saved.

## Enrolment by emailed code (R103)

The standalone blueprint's `radioarchive-signup` flow asks for email, name and password, twice. The address must not be an account's already. A sign-up left unfinished before the code is carried on with, not refused. The user is created inactive under `users/radioarchive`, a 6-digit code is emailed (15 minutes), and the user is activated, signed in and sent to `/auth/signin`, which signs in to the radio. At most 5 code emails an hour go to one address. Each is counted as an Authentik event naming a hash of the address, never the address. `LISTENER_SIGNUP` (`true`, the default; anything else is off) decides whether the sign-in page offers **Sign up**, and a policy refuses the flow itself when it's off. Policies read it from Authentik's environment as each sign-in starts. They don't read it when the blueprint is applied, because Authentik reapplies a blueprint only when the file changes, not when a setting does. Changing it in `.env` and running `docker compose up -d` (which recreates Authentik) is enough; this was checked by hand on the dev stack, both ways. Password reset is by emailed code, as PagerArchive's. Authentik's own administrators need a second factor on this sign-in.

PagerArchive also locks an account after 5 wrong passwords or codes. This blueprint doesn't yet: Authentik's code device throttles wrong codes, and the password stage cancels after 5. An account here only holds preferences, so the lock is left for later.

## Leaving room for the combined deployment and admins

- **Combined (R103, R105, M7).** `radioarchive.yaml` names nothing from the standalone file, and its variables start `RA_` beside PagerArchive's `PA_`. PagerArchive's Authentik can load it as one more blueprint (SPEC 11.1, item 4) while keeping its own sign-in, brand and invitation-only enrolment. The radio's API then takes PagerArchive's `AUTHENTIK_URL` in its override. Each app keeps its own host-only cookie. Silent sign-in (`prompt=none`) is one more `oauth2.AuthCodeOption` on `oidcclient.AuthURL`. Shared sign-out uses the same end-session endpoint.
- **Admins (R51, R55, M4).** `internal/oidcclient` knows nothing of listeners. Admin sign-in can use the same client and provider, with its own session check for `radioarchive-admins` behind the existing `AdminAuthenticator` fence. The profile-access policy already keeps the token away from that group's members.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Where the profile lives | A table in RadioArchive's database, keyed by subject | R110 says the profile and nowhere else; P4 and the backups (R89) expect Authentik to hold it |
| Writing it | The listener's own access token against Authentik's API | Authentik's user API needs model permissions, which listeners don't have |
| Writing it | A user-settings flow driven by the browser | Authentik's pages, not the radio's, and no way to send structured data |
| The token's scope | Global `view_user` + `change_user`, as PagerArchive's role has | Reaches every user, `akadmin`'s groups and PagerArchive's users included |
| Sessions | Server-side rows, as PagerArchive | Stores something about each listener; a sealed cookie needs no row |
| The cookie key | A `SESSION_KEY` setting of its own | One more secret to keep; HKDF of the client secret keeps the uses apart |
| Sign-in never changes listening | Care in each public handler | The next handler's author might forget; stripping credentials at the route table can't be forgotten |
| First sign-in | Merging the two sets | R41: no merge |

## Consequences

- **For the owner (M4):** a stolen token can still add a listener's account to `radioarchive-admins`, because Authentik's `change_user` includes group membership. M4 should decide admins in a way the token can't change. Options include a list kept by the radio itself (`INITIAL_ADMIN_EMAIL` plus the shell), or checking who made the group change. Until then the group unlocks nothing.
- The account's details are visible to Authentik's own administrators, and Authentik's database backup holds them (R89), as SPEC expects.
- An account the token can't reach (an admin's) saves nothing. Admins listen with the browser's own settings.
- `compose.yaml` now needs a `.env` (`scripts/dev-env.sh` writes one with fresh random secrets), and an old development database needs its volume dropped once so Authentik's database is created.
- Rotating the OIDC client secret signs every listener out.

## How each choice is verified

| Choice | Verification |
|---|---|
| PKCE, state, nonce, the session cookie's flags, sign-out, hidden entries, same-site changes | `internal/account/account_test.go`, against a made-up OIDC provider and directory |
| P3/R111: same answers signed in and out | `internal/server/account_test.go` (every public route, and the live feed), `web/signin-tests/a33.spec.ts` (catalogue, search, fireground) |
| P4: no rows, no addresses in logs | `cmd/radioarchive/p4_test.go`; the account tests check their own logs too |
| No admin API from listener sign-in | `cmd/radioarchive/main_test.go` (sign-in on), `internal/server/account_test.go` |
| A33, clearing, sign-out, enrolment by code | `web/signin-tests/a33.spec.ts` in CI against the compose stack with Authentik and Mailpit (`scripts/ci/e2e-signin.sh`) |
| Compose recreates Authentik when a blueprint changes | `deploy/compose_test.go` |
| The token reaches only listeners | Checked by hand on the dev stack (above); the e2e saves a listener's profile with it |
