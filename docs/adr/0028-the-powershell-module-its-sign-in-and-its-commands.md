---
status: proposed
---

# 28. The PowerShell module: its sign-in and its commands

Date: 2026-11-25

The PowerShell module, `RadioArchive` (`powershell/RadioArchive/`), wraps the admin API for the radio's admins (`SPEC.md` P3, R10, R34, R51, R54, R55, R70, R111, A26; issue #116). It signs in as a person through the radio's Authentik with its own public OIDC client: in the system browser with PKCE and a loopback redirect, or with a device code. The API takes its access tokens as `Authorization: Bearer` on admin routes only, and maps the token's subject onto the account the browser's sign-in made (ADR 0025). The sign-in is saved encrypted (DPAPI on Windows, a SecretManagement vault elsewhere), and tokens are `SecureString`s throughout. Its rules are `docs/powershell-guidelines.md`.

SPEC says only "sign-in as in PagerArchive". PagerArchive's ADR 0009 and its PowerShell guidelines were the reference for the shape wherever SPEC leaves room (issue #118, owner decision 4); nothing PagerArchive-specific is copied. Everything here is the implementer's choice (spec "How to use this spec", item 6), and is proposed.

## Summary

| Part | Choice |
|---|---|
| **The client** | Its own provider and application in `radioarchive.yaml`, `radioarchive-powershell`: public, no secret, a loopback redirect on any port (`^http://127\.0\.0\.1:[0-9]{1,5}/$`), the authorization code, refresh token and device code grants, `offline_access`, `sub_mode: user_id`, 5-minute access tokens, refresh tokens for 30 days from their last use and replaced at each use (threshold 0) |
| **PKCE** | Required by the radio's existing PKCE policy, bound to the module's application too: Authentik 2026.8.5 lets a public client skip it (evidence below) |
| **Device code** | The standalone brand's `flow_device_code`: `radioarchive-device`, no stages, signed-in only, so Authentik sends a signed-out visitor through the brand's sign-in first. The combined deployment's is M7's |
| **Discovery** | `GET /auth/module` (no sign-in, an account route): `{"issuer", "client_id", "scope"}`. The module reads the rest from the issuer's discovery document |
| **The API** | `Authorization: Bearer` on admin routes only: signature (RS256, the issuer's JWKS), issuer, audience, expiry; an access token (`azp` the module's client and a `scope`), not an ID token; then the account, role `admin` and status `active`, read at every request. Never with a session cookie (`400 two_credentials`) |
| **Two issuers, one account** | The account is (the browser client's issuer, the token's subject). Both clients are on one Authentik with `sub_mode: user_id`, so the subject is the same Authentik user ID. An admin must have signed in with the browser once, which promotion needs anyway |
| **The audit log's "how"** | `account.Admin.Via` is `module` for a bearer token and `browser` for a session cookie, set by the authenticator, never the client (ADR 0029). `GET /api/v1/admin/me` says who signed in, and how |
| **P3** | Unchanged and structural: public routes and pages get no `Authorization`, so a token unlocks nothing there |
| **Staying signed in** | The refresh token: on Windows a DPAPI-encrypted file under local application data; elsewhere the default SecretManagement vault, or `-Vault`; with neither, this session only, with a warning |
| **Tokens** | `SecureString`s from end to end; Authentik's token, device and revocation requests built and read as bytes; never in output, `-Verbose`, `-Debug`, `$Error`, a transcript or a file in plain text |
| **Names** | `Verb-RadioArchiveNoun`, each with an `Ra` alias (`Get-RaTalkgroup`). R51's `Get-Talkgroup` is an example of the pipeline, not a name |
| **Commands** | 35, for everything the admin API does: below (the three for disabling, enabling and deleting accounts added with ADR 0033) |
| **Tests** | Pester 5.9.1 meta-tests, unit tests with the API mocked, sign-in tests and PSScriptAnalyzer with zero findings in CI on Windows and Linux, on 7.6.0 and the newest stable release; behaviour tests against the compose stack with Authentik |

## The module's client in Authentik

The browser's client is confidential: the API holds its secret. A script on someone's machine can't keep a secret, so the module has its own public client, as RFC 8252 says a native app should. Its application's slug is its client ID, so its issuer is `AUTHENTIK_URL/application/o/radioarchive-powershell/`; the API derives it from `AUTHENTIK_URL` and `MODULE_CLIENT_ID` (default `radioarchive-powershell`), and refuses a `MODULE_CLIENT_ID` equal to `OIDC_CLIENT_ID`.

| Setting | Value | Why |
|---|---|---|
| `client_type` | `public`, no secret | A script can't keep one |
| `redirect_uris` | regex `^http://127\.0\.0\.1:[0-9]{1,5}/$` | RFC 8252 section 7.3: a loopback redirect on any port; `localhost` is refused (section 8.3) |
| `grant_types` | `authorization_code`, `refresh_token`, device code | Authentik refuses a grant not listed |
| Scopes | `openid`, the radio's `email` mapping, `profile`, `offline_access` | `offline_access` gives the refresh token; the radio's email mapping, as the browser's (ADR 0025) |
| `sub_mode` | `user_id` | The subject the browser's sign-in recorded |
| `access_token_validity` | 5 minutes | The API reads the account at every request anyway |
| `refresh_token_validity`, `refresh_token_threshold` | 30 days, 0 seconds | A new refresh token at every renewal, the old one revoked, so 30 days count from the last use |
| `access_code_validity` | 10 minutes | Also the device code's lifetime: time to type it on another device |
| Signing key | Authentik's self-signed certificate | As the browser's provider; issuer and audience keep the two apart |
| Authorization flow | implicit consent | Straight back after the sign-in, as the browser |

### Evidence: Authentik 2026.8.5, before building on it

`scripts/oauth-probe.sh` (the Go program `testing/oauthprobe`) signs in two made-up users through Authentik's flow executor API, as a browser's pages would, and checks what the module relies on. It prints claim names and checks, never a token. Run on cld01 on 2026-11-25 against the compose stack (`ghcr.io/goauthentik/server:2026.8`, image version 2026.8.5):

```
== E1 PKCE with a loopback redirect on a free port (RFC 8252)
PASS a code is issued to http://127.0.0.1:43226/
PASS the exchange with the wrong verifier is refused  (400 invalid_grant)
PASS the exchange with the right verifier gives tokens  (200, expires_in=300, a refresh token: true)
PASS the same code a second time is refused  (400 invalid_grant)
PASS another port (53480) is accepted too
PASS redirect http://localhost:43226/ is refused  (STOPPED 400)
PASS redirect http://127.0.0.1.example.test:43226/ is refused  (STOPPED 400)
PASS redirect https://example.test/ is refused  (STOPPED 400)
PASS redirect http://127.0.0.1:43226/x is refused  (STOPPED 400)
PASS authorize without a code_challenge is refused (the blueprint's policy)  (STOPPED 200)
PASS authorize with the plain method is refused  (STOPPED 200)

== E2 the access token and the ID token
  access token claims: [acr amr aud auth_time azp email email_verified exp given_name groups iat iss jti name nickname preferred_username scope sid sub uid]
  id token claims:     [acr amr aud auth_time email email_verified exp given_name groups iat iss jti name nickname preferred_username sid sub]
  access token: iss=http://localhost:19716/application/o/radioarchive-powershell/ aud=radioarchive-powershell azp=radioarchive-powershell scope="offline_access email openid profile" exp-iat=300
  id token:     iss=http://localhost:19716/application/o/radioarchive-powershell/ aud=radioarchive-powershell azp=<nil> scope=<nil>
PASS the access token's issuer is the module's application
PASS its audience is the module's client
PASS its azp is the module's client, and it has a scope
PASS the ID token has no scope (how the API tells them apart)
PASS its lifetime is 5 minutes

== E3 a refresh replaces the refresh token (threshold 0)
PASS refresh gives a new access token and a new refresh token  (200)
PASS the old refresh token is refused after it was replaced  (400 invalid_grant)

== E6 revocation (RFC 7009), a public client with no secret
PASS revoke answers 200  (200)
PASS a revoked refresh token is refused  (400 invalid_grant)

== E7 device code (RFC 8628)
PASS device authorization gives a user code and a verification URI  (verification_uri=http://localhost:19716/device, expires_in=600, interval=5)
PASS polling before the code is entered: authorization_pending  (400 authorization_pending)
  the other device's browser ended at: AT ak-provider-oauth2-device-code-finish <nil>
PASS after the sign-in and the code: tokens, with a refresh token  (200)
PASS the device sign-in's subject is that user's ID
PASS the device code can't be used twice  (400 invalid_grant)

0 failed
```

**Without the policy.** With the PKCE policy's binding to the module's application deleted from Authentik's database, the same probe (`-expect-pkce-policy=false`) got:

```
WITHOUT THE POLICY: authorize without a code_challenge: code issued true (); exchange without a verifier: 200 , tokens true
```

So Authentik 2026.8.5 still lets a public client skip PKCE, and the policy is needed. The binding was then put back by applying the blueprint again (`ak apply_blueprint`), and the probe passed as above. The policy is the radio's existing `radioarchive-pkce`: on the authorize path it passes only an S256 challenge, and it passes everything else, the device code included.

## How the API takes the module's tokens

`account.Service.Admin` is the admin API's authenticator (ADR 0025). A request with an `Authorization` header is the module's:

1. **Not with a session cookie.** A request carrying both is refused, `400 two_credentials`, so who is acting is never unclear.
2. **The token.** `oidcclient.AccessVerifier`, go-oidc's verifier for the module's issuer and client ID: an RS256 signature by a key in the issuer's JWKS (found by discovery on first use, so the API starts while Authentik is starting, and fetched again when an unknown key ID appears), the issuer, the audience and the expiry. Then it must be an **access token**: `azp` is the module's client and `scope` is a non-empty string, which Authentik's ID tokens don't have (E2). Anything else is `401 invalid_token`, with `WWW-Authenticate: Bearer error="invalid_token"`, and the module renews its token once and tries again. The log says a token was refused and why in a word (`expired`, `audience`, `issuer`, `signature`), never the token or its claims.
3. **The account,** read now, nothing cached: the row for (the browser client's issuer, the token's subject). No row, or not an admin: `403 admin_required`. Disabled: `401 account_disabled`. So a demotion or a disabling takes effect at the module's next command, as in the browser.

The admin is then `account.Admin` with `Via: module`, through `server.AdminFrom`, so the handlers, their checks and the audit log (ADR 0029) are the browser's. Cross-site protection (ADR 0025) lets the module through: its requests carry neither `Origin` nor `Sec-Fetch-Site`.

**Two issuers, one account.** Accounts are keyed by the issuer and subject the browser's sign-in has (ADR 0025), and the module's tokens name another issuer. Both clients are applications of the same Authentik with `sub_mode: user_id`, so the subject is the Authentik user ID in both. The API checks the token against the module's issuer, then looks the account up under the browser's. Keying the account by the user ID alone, or giving the module's sign-ins rows of their own, were rejected: the first would change ADR 0025's table for every row; the second would let someone promoted in the browser be a listener in the module.

**Someone who has never signed in with the browser** has no account row and so isn't an admin. That loses nothing: an admin is made by another admin promoting an account that has signed in, or by the first admin's sign-in with the browser (ADR 0025).

**Public routes** still see no `Authorization` (ADR 0009, P3): the server takes it off every public route and page before its handler runs, so a bearer token answers exactly as none. The account's routes (`/api/v1/account`) are the browser's and don't read it.

**Heavy-use limits** and admins' exemption from them, the module's included (R70), are M5's.

## What the module asks for first

`GET /auth/module` answers anyone, with sign-in on: `{"issuer": "<AUTHENTIK_URL>/application/o/radioarchive-powershell/", "client_id": "radioarchive-powershell", "scope": "openid email profile offline_access"}`. With sign-in off it's `404 module_sign_in_off`. It lives among the sign-in hops (an account route), not under `/api/v1`, because it isn't the admin API and no public route may come from outside `internal/public`. The module then reads the issuer's discovery document, and refuses any endpoint not on the issuer's own origin: it talks only to the radio and its Authentik.

`GET /api/v1/admin/me` is the admin making the request: account ID, name, email, role and `via`. `Connect-RadioArchive` asks it to show who signed in, and to learn that the radio takes them as an admin.

## Signing in, renewing, signing out

`Connect-RadioArchive -Uri <radio>`:

1. `GET /auth/module` and the issuer's discovery.
2. A saved sign-in for that radio is renewed and used, without the browser.
3. Otherwise **the browser**: a `TcpListener` on 127.0.0.1, port 0, a PKCE verifier and S256 challenge and a random state; the system browser opened at the authorize URL (`Start-Process` on Windows, `open` on macOS, `xdg-open` on Linux, which the behaviour tests stand in for); one request read, its state checked, its code read straight into a `SecureString`; the code exchanged with the verifier. Up to 5 minutes.
4. **`-UseDeviceCode`**: Authentik's device endpoint, and a warning "To sign in, open <AUTHENTIK_URL>/device on any device and enter the code ...", then polling at Authentik's interval until signed in, refused or expired (10 minutes).
5. `GET /api/v1/admin/me`. Refused (a listener, a disabled account), the refresh token is revoked and nothing is saved.
6. The refresh token is saved, and a `RadioArchive.Connection` is output: `Uri`, `AccountId`, `Name`, `Email`, `Role`, `SavedIn`, `ExpiresAt`.

Every command renews the access token when it has under 60 seconds left. Authentik replaces the refresh token at each renewal (E3), so the new one is saved at once, in the same place. Windows of one user share one saved sign-in: each reads the saved token before renewing, and if Authentik refuses it because another window renewed meanwhile, reads the newer one and tries again. A sign-in Authentik refuses has ended: the saved copy is deleted, and the command says to run `Connect-RadioArchive`.

`Disconnect-RadioArchive` revokes the refresh token (and this window's access token) in Authentik (E6) and deletes the saved copy wherever it is. If Authentik can't be reached, the copy is still deleted, with a warning that the sign-in ends by itself after 30 days unused. The API checks access tokens itself, so one already issued works until it expires (5 minutes), but no copy of it exists outside that window's memory.

## Staying signed in

| Where | Store | Set-up needed |
|---|---|---|
| Windows | A file under `LocalApplicationData/RadioArchive/SignIns`, the refresh token encrypted with DPAPI (`ConvertFrom-SecureString` with no key), so only that Windows account on that machine reads it | None |
| Linux and macOS | The default SecretManagement vault, secret `RadioArchive:<origin>`; the file beside it holds no token, only which vault | A registered default vault |
| Neither | This session only, with a warning saying how to register a vault | None |
| `-Vault <name>`, anywhere | That vault | That vault; without SecretManagement, `secretmanagement_missing` |

Local, not roaming, application data: a refresh token is replaced at every use, and two machines sharing one would sign each other out. One file per radio, named from the SHA-256 of its origin, written under a temporary name and moved over the old one, so another window never reads half a file. `ConvertFrom-SecureString` with no key encrypts only on Windows; elsewhere it writes the text's UTF-16 bytes as hex, so the module's two DPAPI functions refuse to run off Windows. SecretManagement isn't a required module: it's loaded only when used, and the module never registers or configures a vault itself, because a vault holds the user's other secrets too.

## Tokens in the module

- `SecureString`s throughout, sent with `Invoke-RestMethod -Authentication Bearer -Token`.
- Authentik's token, device and revocation endpoints are the only requests whose bodies hold a token. One private file sends them with `HttpClient`, building the form as bytes copied straight out of the `SecureString`s, and copying the reply's token values straight into new `SecureString`s, blanking them in the reply before the rest is read as JSON. The buffers are zeroed. The PKCE verifier is a plain string: random, made here, for one sign-in, and useless without the code.
- HTTP errors never throw inside `Invoke-RestMethod` (`-SkipHttpErrorCheck`), because its exception keeps the request, `Authorization` header and all, and would land in `$Error`. An error from below that isn't the module's own is wrapped in one that is.
- `-Verbose` shows each request's method and path; `-Debug` its status. Neither shows a token, and nor does any warning or error.
- Plain HTTP needs `-AllowUnencryptedAuthentication`, with a warning, remembered by the saved sign-in.

## The commands

| Noun | Commands | Notes |
|---|---|---|
| (sign-in) | `Connect-RadioArchive`, `Disconnect-RadioArchive` | |
| System | `Get-`, `New-`, `Set-`, `Remove-RadioArchiveSystem` | |
| Talkgroup | `Get-`, `New-`, `Set-RadioArchiveTalkgroup` | `Get` reads the admin table (ADR 0026), hidden and ignored ones too, and filters by system, ID, name, group, tag, state and unreviewed. `Set -State` asks first |
| Call, recording | `Get-RadioArchiveCall`, `Save-RadioArchiveRecording` | A talkgroup's calls, any state (R34), paged as the pipeline asks; a recording saved byte for byte |
| Unit | `Get-`, `Set-`, `Remove-RadioArchiveUnit` | `Set` adds a unit that isn't there, as the API's `PUT` |
| Group, tag | `Get-`, `New-`, `Set-`, `Rename-RadioArchiveGroup`; `Get-`, `Set-`, `Rename-RadioArchiveTag` | |
| Attachment | `Get-`, `Add-`, `Remove-RadioArchiveAttachment` | Rejections pipe into `Add-` |
| Rejection | `Get-RadioArchiveRejection` | |
| Recorder | `Get-`, `Set-RadioArchiveRecorder` | State and the quiet threshold only: **never keys** (R10) |
| Retention | `Get-`, `Set-RadioArchiveRetention` | Shortening asks first, saying how many recordings go |
| Account | `Get-`, `Set-`, `Disable-`, `Enable-`, `Remove-RadioArchiveAccount` | Roles (ADR 0025); disabling, enabling and deleting (ADR 0033) |
| Audit log | `Get-RadioArchiveAuditLog` | ADR 0029's read API, newest first, by who, kind, thing and time |

Objects' properties are named as the parameters that take them, so a `Get-` pipes into its `Set-`: a talkgroup is `SystemId` and `Id`, and `Get-RadioArchiveTalkgroup -Group North | Set-RadioArchiveTalkgroup -State Hidden` hides every talkgroup in North. `Set-`, `Rename-` and `Remove-` bind only identity from the pipeline; `New-` binds every field, so an export made with `Export-Csv` adds again with `Import-Csv`. Deletions, state changes and role changes ask first (ConfirmImpact `High`); everything that changes supports `-WhatIf`. The setup import and export are issue #117's.

## Considered options

| Decision | Rejected | Why |
|---|---|---|
| The module's client | The browser's confidential client | Its secret would have to be on admins' machines |
| Mapping the module onto accounts | Keying accounts by Authentik user ID alone | Changes ADR 0025's table and every row's key |
| Mapping the module onto accounts | A row per issuer | Someone promoted in the browser would be a listener in the module |
| Telling access tokens from ID tokens | The `typ` header | Authentik's are both `JWT`; `azp` and `scope` differ (E2) |
| Telling access tokens from ID tokens | Token introspection | A round trip to Authentik at every request; the signature, issuer, audience and expiry are checked locally, the account in the database |
| Where the module learns the issuer | A setting the admin types | The radio's URL alone is enough (`/auth/module`) |
| Who the module is signed in as | `/api/v1/account` with a bearer token | That route is the browser's session; `/api/v1/admin/me` is the admin API's own |
| Names | `Get-Talkgroup`, as R51's example | Too generic beside other modules; a prefix and short aliases, as PagerArchive (issue #118) |
| A token beside a cookie | Prefer one of them | Who is acting would be a guess |
| The loopback listener | `HttpListener` | On Windows it needs a URL reservation for 127.0.0.1 |
| The device code's prompt | `Write-Host` | Not allowed by the guidelines; a warning is shown by default, as `Connect-AzAccount` does |

## Open points

1. **The combined deployment** loads `radioarchive.yaml`, so it gets the module's client too, but its device-code flow is PagerArchive's brand's, M7's to set.
2. **Publishing** the module to the PowerShell Gallery joins the release workflow in M6.
3. **The setup import and export** commands are issue #117's.
4. **Disabling and deleting accounts** from the module come with issue #105's API.

## Consequences

- A second OAuth client exists in Authentik. Its refresh tokens are long-lived credentials (30 days from their last use), kept encrypted or for one session only; Authentik deletes a user's tokens when the user is disabled, and the API's per-request account check doesn't depend on them.
- The API trusts the module's issuer's keys for admin requests. Changing `AUTHENTIK_URL` changes both issuers.
- Authentik logs refused refresh tokens in its own log. They no longer work.
- The behaviour tests run on Linux only, in the image job: GitHub's Windows runners can't run the stack's Linux containers. The Windows half (DPAPI) is the `powershell` job's.

## How each choice is verified

| Choice | Verification |
|---|---|
| Authentik's behaviour, the policy needed for PKCE | `scripts/oauth-probe.sh` (above) |
| The API's checks: expired, another key, another audience, the browser's audience alone, the browser's issuer alone, another issuer, another `azp`, no scope, ID tokens, unsigned, malformed, a listener's, never signed in, demoted, disabled, with a cookie, Basic; every admin route answers the module's admin; own role; P3 on the public routes; no token in the logs | `cmd/radioarchive/module_test.go`, `TestIntegrationModuleTokens`, `TestIntegrationModuleSignInOff` |
| `Via: module`, the account by the browser's issuer | `internal/account/admin_test.go`, `TestModuleAdmin`; `GET /api/v1/admin/me` and the audit log's entry in `TestIntegrationModuleTokens` |
| The guidelines' rules | `powershell/tests/Meta.Tests.ps1`, PSScriptAnalyzer per file |
| The pipeline, what each change sends, `-WhatIf` and `-Confirm`, errors, paging, saving, the token reader, the loopback listener | `powershell/tests/Module.Tests.ps1` |
| Where the sign-in is saved, a second process reading it, no vault, `-Vault` without SecretManagement, a listener refused and nothing saved | `powershell/tests/SignIn.Tests.ps1` |
| Against the real stack: browser sign-in, a second window, device code, no token anywhere, Disconnect revoking, a listener refused, a demoted admin refused, `Get -Group North | Set -State Hidden` and the catalogue, the audit entry from the module (A26), a 1003 recording byte for byte, no vault | `web/module-tests/module.spec.ts` via `scripts/ci/e2e-module.sh` |
