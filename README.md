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
(`/api/v1/admin`), which the binary serves once admin sign-in (Authentik)
arrives; until then only the tests use it.
