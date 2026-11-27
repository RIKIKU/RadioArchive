---
status: proposed
---

# 52. The Android app's bearer token on the account

Date: 2026-11-28

The Android app (RIKIKU/RadioArchive-Android) reaches milestone 5, sign-in and sync. It signs in with Authentik as a native app should (RFC 8252): its own **public** client, `radioarchive-android`, through the system browser with PKCE, and a private-use redirect back to the app. It keeps a refresh token. An app can't hold the browser's session cookie, so on the account's routes (`GET /api/v1/account`, `PUT` and `DELETE /api/v1/account/profile`) the API now also takes the app's access token as `Authorization: Bearer`. It's the same account and the same profile as the browser's, keyed by the browser client's issuer and the token's subject, as ADR 0028 does for the PowerShell module. Public routes still never see the header (ADR 0009, P3). These are the implementer's choices where `SPEC.md` R41, R110 and section 6.12 leave room (spec "How to use this spec", item 6). The app's side is its ADR 0005.

## Summary

| Part | Choice |
|---|---|
| **The client** | `radioarchive-android` in `radioarchive.yaml`: public, no secret, redirect `io.github.rikiku.radioarchive:/oauth2redirect` (strict), authorization code and refresh token grants, `offline_access`, `sub_mode: user_id`, 10-minute access tokens, refresh tokens for 30 days from their last use and replaced at each use. The radio's PKCE policy is bound to it, as to the module's |
| **The API** | `APP_CLIENT_ID` (default `radioarchive-android`; it must differ from `OIDC_CLIENT_ID` and `MODULE_CLIENT_ID`). Its issuer is `AUTHENTIK_URL/application/o/<APP_CLIENT_ID>/`. The token is checked as the module's is (`oidcclient.AccessVerifier`): RS256 by the issuer's JWKS, the issuer, the audience and the expiry, then that it's an access token (`azp` the app's client, a `scope`) |
| **Which routes** | The account's three routes, and only when the request has no session cookie. A cookie wins: the browser's routes behave as before. Any other bearer token there, the module's included, is now `401 invalid_token` (it was ignored, reading as signed out) |
| **The account** | (the browser client's issuer, the token's subject). Seen for the first time, it's made from the token's name and email, as a browser's first sign-in makes it, but **never made the first admin**: the app has no admin work. A disabled account is signed out |
| **A refused token** | `401 invalid_token` with `WWW-Authenticate: Bearer error="invalid_token"`, so the app renews its token once and tries again. The log says why in a word, never the token |
| **Same site** | Waived for a request with the token and no cookie: no credential a browser sends by itself, so no cross-site forgery. A request with the cookie is checked as before, token or not |
| **Discovery** | `GET /auth/app` (no sign-in, an account route): `{"issuer", "client_id", "scope"}`, as `/auth/module` is for the module. `404 app_sign_in_off` with sign-in off |
| **Migration** | None |

## Why not the module's client

The module's client allows only a loopback redirect (`http://127.0.0.1:<port>/`). An Android app can listen on loopback, but RFC 8252 prefers a private-use scheme on a phone, and AppAuth's Android library is built around one. A redirect added to the module's client would let either one sign in as the other. A client of its own also lets an operator switch the app off, or bind policies to it, alone.

## Why not a session for the app

The app could trade its token once for a cookie session. But that's a second credential to keep and expire, and the gate in front of a deployment (below) would still need the token. The token alone works on every request.

## Behind a forward-auth gate

A deployment behind an Authentik proxy-provider gate (the preview gate on snup) passes the app's requests when the gate's proxy provider lists the app's provider under **JWT federation providers**, with **intercept header auth** on. The outpost then takes the app's access token as `Authorization: Bearer`. `/auth/app` should skip the gate as `/auth/module` does, so the app can find out where to sign in before it has a token. That's deployment configuration, not this repo's.

## How each choice is verified

| Choice | Verification |
|---|---|
| The app's token reads and saves the profile with no cookie and no Origin; the account is made on first use, under the browser's issuer; the browser then sees the same profile; the app clears it | `TestAppSignsInWithItsToken`, `TestAppClearsItsProfile` (`internal/account/app_test.go`) |
| Refused: another key, expired, no scope, another `azp`, the module's token, the browser's ID token, junk, each `401 invalid_token` on GET and PUT, and no account made | `TestAppTokenRefused` |
| Never the first admin, even with `INITIAL_ADMIN_EMAIL`, proven | `TestAppNeverMakesTheFirstAdmin` |
| A disabled account is signed out | `TestAppDisabledAccountIsSignedOut` |
| `/auth/app` | `TestAppSettings` |
| A cookie request from another site is still refused, with a token beside it | `TestAppTokenDoesNotWaiveSameSiteForCookies` |
| Public routes never see the token | Unchanged: `TestIntegrationSignedInSameAnswers` and `TestIntegrationSignedInSameLiveFeed` (ADR 0009) already send a bearer token |

## Consequences

- An Authentik that loads `radioarchive.yaml` gets the app's client. On snup, PagerArchive's Authentik, it's applied by hand with the gate's federation (RadioArchive-Android ADR 0005).
- The app's first sign-in can make an account, as the browser's can. Admins see it in Admin → Accounts like any other.
