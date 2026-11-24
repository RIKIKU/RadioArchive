---
status: accepted (2026-11-25, by the owner; the session parts superseded by ADR 0022)
---

# 21. The saved profile lives in RadioArchive's database

Date: 2026-11-25

> **Partly superseded by [ADR 0022](0022-listener-sessions-live-in-the-database.md) (2026-11-25).** The session is no longer a sealed cookie: it is a row of `listener_sessions`, and the cookie holds only a random ID. Where this ADR says the sealed session cookie carries the issuer, that the account menu's name and email come from it, and (under Deletion) that a switched-off user stays signed in until the cookie ends, read ADR 0022. An admin can now end a listener's sessions at once. The profile, its table and its key stand. The text below is as it was decided.

A signed-in listener's saved profile (the listening set with its priority marks, the playback settings, the look, and the download basket with its conversations) moves out of Authentik and into RadioArchive's own PostgreSQL. It is one row of `listener_profiles`, keyed by the OIDC issuer and subject the listener signs in with. Authentik keeps only who the listener is. The API no longer holds an Authentik API token, and the blueprint no longer creates the API's account or the policy that gave it `view_user` and `change_user` on each listener. The browser-facing API and its behaviour stay as they were. The owner decided this on 2026-11-25: "Shouldn't we keep just the user info there and store things like download basket in radio archive and just reference the user ID?" (`SPEC.md` P4, R41, R48, R64, R89, R110). The rest are the implementer's choices (spec "How to use this spec", item 6).

This supersedes the "Where the profile lives, and the token" part of [ADR 0009](0009-listener-sign-in-and-the-saved-profile.md), and the rows and consequences there that follow from it. Sign-in itself, the sealed session cookie, the first-sign-in rule and the routes are unchanged.

## Summary

| Part | Choice |
|---|---|
| **Where the profile lives** | `listener_profiles` in RadioArchive's database (migration 00015): `issuer`, `subject`, `profile` (jsonb, the same JSON as before: `{"v":1,"talkgroups":[…],"look":…,"theme":…,"alerts":…,"basket":{…}}`), `updated_at`. Primary key `(issuer, subject)` |
| **The key** | The OIDC issuer (`AUTHENTIK_URL/application/o/<client>/`) and the ID token's `sub`, which is Authentik's user ID (`sub_mode: user_id`). The session cookie now carries the issuer too |
| **Reading and writing** | `internal/store` (`ListenerProfile`, `SaveListenerProfile`, `ClearListenerProfile`), behind `account.Profiles`. One upsert a save; no read-modify-write of anyone else's data |
| **Clearing** (R110) | **Clear saved settings** deletes the row |
| **Size** | As before: a request body of at most 1 MiB, 5,000 talkgroups, 1,000 basket calls, 50 conversations, 2,000 places in conversations, names and notes bounded (ADR 0018). The server checks the whole profile before writing; the table also refuses anything that isn't a JSON object |
| **The token** | Gone: no `AUTHENTIK_API_TOKEN` or `_FILE`, no `RA_AUTHENTIK_API_TOKEN`. The blueprint deletes the API's account, its token, group and role, and the `radioarchive-profile-access` policy from an Authentik that loaded the earlier version (`state: absent`) |
| **Anonymous listeners** (P4) | Unchanged: nothing is stored for a listener who hasn't signed in. A save without a session is refused before anything is read |
| **Backups** (R89) | The profiles are in RadioArchive's nightly `pg_dump` with the rest of its setup, not in Authentik's |
| **Existing data** | None to move. No deployment runs in production yet, so no profile was ever saved in an Authentik attribute that matters; a development stack's old `radioarchive` attributes are simply no longer read |

## Why

ADR 0009 kept the profile in an Authentik user attribute because R110 said "in their Authentik profile and nowhere else". That cost a standing token that could change users. Authentik has nothing narrower than `change_user` for attributes, so the token could also rename a listener, switch them off, and change their groups. ADR 0009 narrowed it with a policy that gave the token object permissions only on listeners' own users. Even so, it could still add a listener to `radioarchive-admins`. That was the open question in [issue #73](https://github.com/RIKIKU/RadioArchive/issues/73).

The owner would rather Authentik hold identity and nothing of the radio's. With the profile in RadioArchive's database, the API needs nothing from Authentik but sign-in. The token, the role, the policy and their risk all go.

## The table and its key

```sql
CREATE TABLE listener_profiles (
    issuer     text        NOT NULL CHECK (issuer <> ''),
    subject    text        NOT NULL CHECK (subject <> ''),
    profile    jsonb       NOT NULL CHECK (jsonb_typeof(profile) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject)
);
```

**Why the issuer as well as the subject.** `sub` is Authentik's numeric user ID, unique only within one Authentik. A deployment that moves from its own Authentik to PagerArchive's (standalone to combined, R103) would otherwise read user 5's profile for a different user 5. Keying by the issuer too keeps two Authentiks' users apart. The session cookie carries the issuer it was made under. A cookie from another issuer (the deployment's `AUTHENTIK_URL` changed) isn't a live session, so its subject is never read against the new issuer's accounts.

The cost: changing `AUTHENTIK_URL` changes the issuer, so listeners start with an empty account and sign in again. Their old rows stay in the table until removed by hand. These are preferences, and a URL change is rare, so this is accepted.

**Why jsonb, one row.** The profile is the browser's own format (ADR 0008, ADR 0018), read and written whole, and never queried by field. One jsonb value keeps it exactly as the server checked it and readable in a backup. A save is one `INSERT … ON CONFLICT DO UPDATE`, so two tabs saving at once leave the later one's profile, as before. The hidden-talkgroup rule is unchanged: a save keeps the saved entries that aren't public now, read from the row first (ADR 0009).

**What it holds.** Only the profile. No email, name, address or token: the account menu's name and email still come from the sealed session cookie. `updated_at` is when the listener last saved.

## Deletion

- **Clear saved settings** deletes the row, everything in it, hidden talkgroups' entries too. The account stays empty until the listener changes something (R110).
- **A user deleted or switched off in Authentik.** RadioArchive no longer asks Authentik about users, so it doesn't learn of either. A deleted user's row stays until it is removed. A switched-off user's session lasts until its cookie ends (30 days from last use), and in that time they can still save their own preferences. Before this ADR, a switched-off user was signed out at the next account read. A session only ever reached the listener's own preferences, so this is accepted for now. M4's admin tools can list and delete rows by subject. A retention rule for rows not saved in a long time is left for later, and `updated_at` is there for it.

## Backups

R89 now finds the profiles in RadioArchive's own database. The nightly `pg_dump` of the `radioarchive` database (calls and transmissions excluded) includes `listener_profiles`, and restoring it restores the listeners' settings. Authentik's dump holds accounts and admins only. The combined deployment's R106 backup dumps the `radioarchive` database the same way.

## What changed elsewhere

- `internal/authentik` keeps only `Transport`, which the OIDC client still uses to reach Authentik on the compose network. The API client and its errors are gone.
- `GET /api/v1/account` still returns `saves`, now always `true` for a signed-in listener, so the browser app is unchanged. The `account_not_saved` refusal is gone, because every signed-in account can save, Authentik's own administrators included.
- `compose.yaml`, `scripts/dev-env.sh` and `scripts/ci/e2e-signin.sh` drop the token. An existing development `.env` may keep its `AUTHENTIK_API_TOKEN` line, which nothing reads.
- Issue #73's risk goes with the token: nothing the radio holds can change an Authentik group. M4 can decide admins by the `radioarchive-admins` group (R55) without a token of the radio's being able to add someone to it. M4 still makes that decision.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| Where the profile lives | Authentik's user attributes, as ADR 0009 had it | Needs a standing token that can change users and their groups (issue #73); the owner wants Authentik to hold identity only |
| The key | The subject alone | Two Authentiks' numeric IDs collide when a deployment moves to the combined one |
| The key | The email address | It can change, and it's personal data the row needn't hold |
| The form | A table each for the set, the marks and the basket | The profile is read and written whole in the browser's own format; nothing queries it by field |
| Revocation | Asking Authentik about the user at each account read | Needs a token again, which is what this removes |

## Consequences

- No Authentik API token to keep, rotate or leak, and no policy that changes Authentik's permissions at each sign-in.
- Signed-in listeners' settings are in RadioArchive's database and backups, and each signed-in listener adds one row. P4 still holds for anonymous listeners. `cmd/radioarchive/p4_test.go` checks both sides through the binary's handler.
- A user switched off or deleted in Authentik is no longer signed out of the radio at once, and a deleted user's row isn't removed automatically (Deletion, above).
- Changing `AUTHENTIK_URL` starts every listener with an empty account.

## How each choice is verified

| Choice | Verification |
|---|---|
| The table: saved, replaced, kept apart by issuer and subject, cleared, objects only | `internal/store/profiles_test.go` |
| The account's routes against the store: save, read back, hidden entries kept, clear, size limits, the database down | `internal/account/account_test.go` |
| A session from another issuer isn't live; another issuer's row is someone else's | `internal/account/account_test.go`, `TestProfileKeyedByIssuer` |
| P4: anonymous use keeps nothing; a signed-in save is exactly one `listener_profiles` row holding no address, name or email; clearing leaves the database as it was | `cmd/radioarchive/p4_test.go`, through the binary's handler with a made-up OIDC provider |
| A33, clearing and sign-out against a real Authentik, with no token | `web/signin-tests/a33.spec.ts` via `scripts/ci/e2e-signin.sh` |
| The blueprint removes the old token from an Authentik that had it | Checked by hand on a test stack: started from the previous blueprint, the token answered 200; after this one was applied it was refused |
