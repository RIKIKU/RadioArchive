# RadioArchive
Live fireground radio streaming from trunk-recorder; companion to PagerArchive

## Development

Needs Go (the version in `go.mod`) and Docker. The recorder client and its
tests also need cgo and libopus (`apt install libopus-dev pkg-config`); the API
doesn't, and stays cgo-free.

```sh
docker compose up --build --wait   # the API and PostgreSQL 18; migrations run at start
curl http://127.0.0.1:8080/healthz # ok

# The database tests skip unless RADIOARCHIVE_TEST_DATABASE_URL is set.
RADIOARCHIVE_TEST_DATABASE_URL='postgres://radioarchive:radioarchive@127.0.0.1:5432/radioarchive?sslmode=disable' go test ./...
```

`sqlc generate` regenerates `internal/store/db` from `internal/store/queries.sql`; CI fails if it's out of date.

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
