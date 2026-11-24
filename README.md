# RadioArchive
Live fireground radio streaming from trunk-recorder; companion to PagerArchive

## Development

Needs Go (the version in `go.mod`) and Docker.

```sh
docker compose up --build --wait   # the API and PostgreSQL 18; migrations run at start
curl http://127.0.0.1:8080/healthz # ok

# The database tests skip unless RADIOARCHIVE_TEST_DATABASE_URL is set.
RADIOARCHIVE_TEST_DATABASE_URL='postgres://radioarchive:radioarchive@127.0.0.1:5432/radioarchive?sslmode=disable' go test ./...
```

`sqlc generate` regenerates `internal/store/db` from `internal/store/queries.sql`; CI fails if it's out of date.
