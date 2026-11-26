# RadioArchive
Live fireground radio streaming from trunk-recorder; companion to PagerArchive

## Development

Needs Go (the version in `go.mod`) and Docker. The recorder client and its
tests also need cgo and libopus (`apt install libopus-dev pkg-config`); the API
doesn't, and stays cgo-free.

```sh
sh scripts/dev-env.sh > .env       # once: made-up secrets for the development stack
docker compose up --build --wait   # the API, PostgreSQL 18, Authentik and Mailpit; migrations run at start
curl http://127.0.0.1:8080/healthz # ok

# The database tests skip unless RADIOARCHIVE_TEST_DATABASE_URL is set.
. ./.env && RADIOARCHIVE_TEST_DATABASE_URL="postgres://radioarchive:$POSTGRES_PASSWORD@127.0.0.1:5432/radioarchive?sslmode=disable" go test ./...
```

Listeners sign in with Authentik, only to save their preferences, which are kept
in RadioArchive's database with their sessions (`docs/adr/0009-listener-sign-in-and-the-saved-profile.md`,
`docs/adr/0021-the-saved-profile-lives-in-radioarchives-database.md`,
`docs/adr/0022-listener-sessions-live-in-the-database.md`). In the
development stack, Authentik is at http://localhost:9000 and the sign-up codes
it emails are at http://127.0.0.1:8025 (Mailpit). A database volume from
before Authentik was added needs dropping once: `docker compose down --volumes`.
`sh scripts/ci/e2e-signin.sh` runs the sign-in browser tests (`web/signin-tests`)
against the whole stack.

### Admins

An admin is an account whose role is admin in RadioArchive's own database,
never an Authentik group (`docs/adr/0025-admin-accounts-roles-and-admin-sign-in.md`).
Set `INITIAL_ADMIN_EMAIL` in `.env` to the first admin's address. While no
admin exists, the account that signs in with that address (in any case)
becomes one, as long as Authentik has proven the address: a standalone
deployment's sign-up proves it with the emailed code. Once an admin exists
the setting does nothing; admins promote and demote others with
`PATCH /api/v1/admin/accounts/{id}` (`{"role": "admin"}` or `{"role": "listener"}`).

Admins disable, enable and delete accounts on the Accounts page (Admin,
Accounts), with `PATCH /api/v1/admin/accounts/{id}` (`{"status": "disabled"}`
or `{"status": "active"}`) and `DELETE /api/v1/admin/accounts/{id}`
(`docs/adr/0033-disabling-enabling-and-deleting-accounts.md`). Disabling
signs the account out of the radio at once, then switches its Authentik user
off with the accounts service account's token, `AUTHENTIK_API_TOKEN` in
`.env` (the blueprint makes the account, `radioarchive-accounts`, with that
key). If Authentik can't be reached, the radio's change stands, the admin is
told, and doing it again tries Authentik again. Deleting removes the
account, its saved settings and its sessions, and its Authentik user. A
development `.env` from before this needs the line once:
`echo "AUTHENTIK_API_TOKEN=$(head -c 48 /dev/urandom | base64 | tr -d '/+=\n')" >> .env`.

Every admin change is in the audit log, with who made it, how (browser,
module or import), when, and each field before and after
(`docs/adr/0029-the-audit-log.md`). Admins read it on the **Audit log**
page, filtered by who, kind, thing and days, or with
`GET /api/v1/admin/audit` (newest first, or `order=oldest`; `who`, `kind`,
`thing`, `since`, `until`, `limit`, and `cursor` from the answer's `next`).
Recordings' retention doesn't touch it; it has a period of its own,
`AUDIT_RETENTION_DAYS`, 730 days unless an admin changes it (30 to 3,650) on
the Settings page (`PUT /api/v1/admin/audit/retention`), after which the nightly run
deletes older entries and records how many went
(`docs/adr/0032-the-audit-log-page-and-its-retention.md`).

With `LISTENER_SIGNUP=false` nobody can sign up, so the operator makes the
first admin's user in Authentik's admin interface (http://localhost:9000/if/admin/,
as `akadmin`): Directory, Users, Create, with the address as both username
and email, path `users/radioarchive`, and the attribute
`email_proven: <the same address>`, which says the operator vouches for it
(the attribute PagerArchive's invitations set too). Then set a password for the user (Set password) and sign in
to the radio with it.

### The PowerShell module

Admins can do everything the admin API does from PowerShell 7.6 or later
with the `RadioArchive` module in `powershell/RadioArchive`
(`docs/adr/0028-the-powershell-module-its-sign-in-and-its-commands.md`,
`docs/powershell-guidelines.md`). Each release publishes it to the
PowerShell Gallery (`Install-PSResource -Name RadioArchive`); from a
clone, import it as below. It signs in as the admin, through the
radio's Authentik, with its own public client (`radioarchive-powershell`,
in `radioarchive.yaml`): sign in to the radio with the browser once first,
then

```powershell
Import-Module ./powershell/RadioArchive/RadioArchive.psd1
Connect-RadioArchive -Uri http://localhost:8080 -AllowUnencryptedAuthentication   # or -UseDeviceCode
Get-RadioArchiveTalkgroup -Group North | Set-RadioArchiveTalkgroup -State Hidden
Get-Help about_RadioArchive
```

`-AllowUnencryptedAuthentication` is for a plain-HTTP test copy only. The
sign-in is saved: on Windows encrypted for your account (DPAPI), elsewhere
in your default SecretManagement vault, or for the session only without
one. Its tests: `pwsh -File powershell/tests/Invoke-Tests.ps1` (Pester
5.9.1, PSScriptAnalyzer 1.25.0), and against the stack
`scripts/ci/e2e-module.sh`. `scripts/oauth-probe.sh` checks what the
module relies on in the stack's Authentik. The API takes the module's
issuer from `AUTHENTIK_URL` and `MODULE_CLIENT_ID` (default
`radioarchive-powershell`).

`sqlc generate` regenerates `internal/store/db` from `internal/store/queries.sql`; CI fails if it's out of date.

### The browser app

`web/` is the browser app: Svelte 5, SvelteKit (`adapter-static`) and
TypeScript on [ArchiveUx](https://github.com/RIKIKU/ArchiveUx)'s tokens. Its
build is embedded in the binary (`web/web.go`) and served at `/` and every
other path outside `/api`. Needs Node 24.

```sh
cd web && npm ci
npm run check          # svelte-check, no warnings
npm run check:tokens   # every style value is an ArchiveUx token (R80)
npm run build          # into web/build, which go build embeds
npx playwright install --with-deps   # once: Chromium, Firefox and WebKit
cd .. && sh scripts/ci/e2e-web.sh    # build, start the binary on a fresh database with the test fixture, run web/tests
```

The frame (side menu, bottom bar, account menu) is ArchiveUx's `Frame`. The
listening set, its share link and fireground links are in `web/src/lib/listening/`
(`docs/adr/0008-the-listening-set-and-its-share-link.md`).

## Recorders

Recorders are managed only from the server shell (`SPEC.md` R10):

```sh
docker compose exec api radioarchive recorder add "North hill"   # prints the client key once
docker compose exec api radioarchive recorder list
docker compose exec api radioarchive recorder rotate-key "North hill"
docker compose exec api radioarchive recorder retire "North hill"
```

The recorder client (`cmd/radioarchive-client`, image from
`recorder/client/Dockerfile`, amd64 and arm64) runs beside trunk-recorder with
`SERVER_URL` and `CLIENT_KEY` set. `scripts/ci/e2e-a1.sh` runs the fake
recorder through the client image into the compose stack.

## The public API and the hidden-talkgroup gate

Everything a listener reaches without signing in reads through one gate
(`internal/gate`), so no hidden or ignored talkgroup's ID, name or audio
leaves the server, and its calls answer exactly as calls that never existed
(`SPEC.md` P1, R30, R32; `docs/adr/0004-the-hidden-talkgroup-gate.md`):

```sh
# Call details for up to 500 call IDs; unavailable ones are left out.
curl -X QUERY -H 'Content-Type: application/json' --data '{"ids":["<call ID>"]}' http://127.0.0.1:8080/api/v1/calls
# A recording, with range requests.
curl -H 'Range: bytes=0-999' http://127.0.0.1:8080/api/v1/calls/<call ID>/recording
# The same file to save, named for the call in the deployment's TZ.
curl -OJ http://127.0.0.1:8080/api/v1/calls/<call ID>/download
# Up to 500 calls, loose and in conversations, as one zip, the same bytes
# every time (docs/adr/0015-zips-their-url-packing-parts-and-resuming.md).
# The browser's GET packs the list into the URL, so a download resumes.
curl -OJ -X POST -H 'Content-Type: application/json' \
  --data '{"calls":["<call ID>"],"conversations":[{"name":"Structure fire","note":"","calls":["<call ID>"]}]}' \
  http://127.0.0.1:8080/api/v1/zip
```

The live feed is one WebSocket per listener, `/api/v1/live`: the listener
sends `{"type": "set", "talkgroups": [{"id": 1001}, {"id": 1002}]}` and gets the call events
and Opus frames of those talkgroups' public calls, as they happen
(`docs/adr/0007-the-live-feed-to-listeners.md`).

A new public endpoint goes in `internal/public`, reads through the gate, and
adds itself to the A10 harness (`internal/server/a10_test.go`), which fails
until it does. `scripts/ci/e2e-a10.sh` checks the same through the images.

## Systems and attachments

The server takes a call only when its recorder + `shortName` is attached to a
system, and its talkgroup is in that system's list (or the system's
auto-create is on) and isn't ignored (`SPEC.md` R11, R12, R16-R18, R21;
`docs/adr/0003-systems-attachments-and-rejected-calls.md`). Systems,
talkgroups and attachments are managed through the admin API
(`/api/v1/admin`), which answers a signed-in admin's browser session (see
Admins, above).
