# meta-frames-server

REST API for **Meta-Frame**, a catalog for film photography. It tracks gear, film stock, each roll from purchase to scan, lab processing, and the scanned frames.

**Stack:** Go · Gin · PostgreSQL (pgx, sqlc, goose) · OpenAPI 3 (oapi-codegen)

## What it covers

| Area | What you can do |
|---|---|
| Cameras & lenses | Manage gear, mount lenses, deactivate retired items |
| Film stock | Catalog stocks, see inventory and expiry |
| Rolls | Buy in bulk, load, shoot, finish |
| Labs & processing | Send rolls to a lab, order scans, track scans and negatives coming back |
| Frames & scans | Import scans, preview, compare frames, download files |
| Search & stats | Search rolls; gear, film, timeline, and spending stats |
| Audit log | Every insert, update, and delete, written by database triggers |

The full contract is in [api/openapi.yaml](api/openapi.yaml). User-visible changes are in [CHANGELOG.md](CHANGELOG.md).

## Quick start

```bash
make db-up                   # Postgres on :5432 (Docker)
make run                     # runs migrations, serves :8080
curl localhost:8080/health
```

Settings come from environment variables. See [configs/.env.example](configs/.env.example).

## Development

| Command | Purpose |
|---|---|
| `make dev` | Run with live reload (air) |
| `make gen` | Regenerate server and query code after editing the spec or SQL |
| `make test-unit` | Unit tests, no Docker |
| `make test` | Unit and integration tests (needs Docker) |
| `make cover` | Coverage of hand-written code |
| `make lint` | golangci-lint |
| `make db-reset` | Wipe and restart the local database |

Workflow: edit `api/openapi.yaml` first, then run `make gen`. Keep the spec examples valid, because `internal/server/spec_test.go` checks them.

## Architecture

```
HTTP ─▶ server/ ─▶ controllers/ ─▶ services/ ─▶ db/ (Postgres)
        routing,    request ↔ API    business      sqlc queries,
        validation  type mapping     rules         transactions
                                       └──────▶ storage/ (scan files)
```

```
cmd/api/            entrypoint and wiring
api/openapi.yaml    API contract (source of truth)
internal/
  api/              generated server code (do not edit)
  controllers/      one package per area
  services/         one package per area, unit-tested against in-memory fakes
  domain/           shared vocabulary: roll statuses, job types, scanners
  db/               Store, queries/*.sql, gen/ (sqlc, do not edit)
  server/           router, CORS, validation, error mapping
  storage/          scan file storage (local disk)
migrations/         goose migrations, embedded in the binary
test/integration/   end-to-end tests against real Postgres
```

## API conventions

- **Ids:** `POST` assigns an id on the server. `PUT /{resource}/{id}` creates or updates with an id from the client, so offline retries are safe.
- **Soft delete:** `DELETE` only marks a row as deleted, and the id stays reserved. A delete returns 409 while the record is still in use.
- **Errors:** responses use `{code, message}`. 400 means the request failed validation, 404 means not found, 409 means a state conflict, and 422 means a business rule failed.
- **Audit:** send `X-Actor` to put a name on audit entries.

> Scan files are stored on local disk (`STORAGE_DIR`). Move them to object storage before deploying to a host with an ephemeral disk.
