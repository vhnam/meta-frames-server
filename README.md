# meta-frames-server

Go REST API for Meta-Frame (Gin, OpenAPI + oapi-codegen, pgx, sqlc, goose).

```bash
make db-up      # Postgres on :5432
make run        # applies embedded migrations on start, serves :8080
curl localhost:8080/health
make test-unit  # unit tests only, no Docker
make test       # unit + integration tests (testcontainers, needs Docker)
```

- Edit `api/openapi.yaml` first, then `make gen` (oapi-codegen + sqlc). Every operation carries named request and response examples, grouped by tag; `internal/server/spec_test.go` validates them against the schemas, so keep them in step with schema changes.
- SQL lives in `internal/db/queries/*.sql`; migrations in `migrations/` (goose).
- `POST /cameras|lenses|film-stocks|labs` creates a record and returns it with a server-assigned id. `PUT /{resource}/{uuid}` is the idempotent create-or-update with a client-generated id (safe to retry offline).
- Cameras, lenses, film stocks and labs support full CRUD: `POST` (create), `GET` (list/one), `PUT` (update), `DELETE`. Deleting is refused with 409 while the entity is in use (a camera that held rolls, a lens on a roll, a stock with rolls or derived stocks, a lab with processing history); deactivate cameras and lenses instead.
- Nothing is hard-deleted: `DELETE` sets `deleted_at`, and reads ignore soft-deleted rows. A deleted id stays reserved (re-creating it is a 409). Every table has `created_at`, `updated_at` (kept current by a trigger) and `deleted_at`; the API exposes `createdAt`/`updatedAt` on cameras, lenses, film stocks and labs.
- Deletes beyond the four core entities: `DELETE /rolls/{id}` (only rolls still in stock), `DELETE /processing/{id}` (only jobs without scans; the roll status is recomputed) and `DELETE /scans/{id}` (the image file stays in storage). Also `GET /scans/{id}`, `GET /rolls/{id}/frames[/{number}]` and `GET /audit-logs/{id}`.
- Audit log: database triggers write one `audit_log` row per insert, update and delete on every entity table (and the camera/roll lens links), with the before/after row, the `X-Actor` request header and the request id. Read it with `GET /audit-logs?entityType=&entityId=&action=&limit=&offset=`. Because it is trigger-based, nothing that writes through the API, or `psql`, can skip it.
- Errors are `{code, message}`: 400 spec validation, 404, 409 state conflict, 422 business rule.

## Code layout

```
cmd/api/              entrypoint: config, DB, migrations, wiring
api/openapi.yaml      API contract (source of truth)
internal/
  api/                generated server code (oapi-codegen) - do not edit
  controllers/        aggregates the per-area controllers into the generated server interface
    camera/ lens/ filmstock/ roll/ lab/ processing/ scan/ stats/
                      one package per area: decode request, call a service, map to API types
  services/           business rules; plain inputs/views, no HTTP (services.go wires the areas)
    camera/ lens/ filmstock/ lab/ roll/ processing/ scan/ stats/ audit/
                      one package per area; roll builds on processing + scan, stats on roll + scan
    shared/           small helpers used by several areas
  domain/             vocabulary shared by every layer: roll statuses, job types, scanners
  db/                 Store (queries + transactions), migrations runner
    queries/*.sql     SQL for sqlc
    gen/              generated query code (sqlc) - do not edit
  server/             router, CORS, OpenAPI request validation, HTTP error mapping
  storage/            scan file storage (local disk)
  common/             apperror, pointers, clock, convert (API date/time helpers), requestctx (caller identity)
  testutil/           in-memory fakes and assertions shared by unit tests
  config/             environment configuration
migrations/           goose SQL migrations (embedded in the binary)
configs/              air.toml (live reload), .env.example (documents the env vars; export them yourself)
build/package/        Dockerfile (`make docker-build`)
deployments/          compose.yml for local Postgres (`make db-up`)
test/integration/     end-to-end tests against a real Postgres container
```

Services depend on `db.Store` and `storage.Store` interfaces, so their rules are unit-tested
against in-memory fakes (`internal/services/fakes_test.go`).

## Implemented use cases

| Area | UC | Endpoints |
|---|---|---|
| Camera & lens | 01–09 | `/cameras`, `/cameras/{id}`, `/cameras/{id}/active`, `/cameras/{id}/lenses`, `/lenses`, `/lenses/{id}`, `/lenses/{id}/active` |
| Film stock | 10–14 | `/film-stocks`, `/film-stocks/{id}`, `/inventory` |
| Rolls | 15–22 | `POST /rolls/bulk` (Idempotency-Key), `/rolls`, `/rolls/{id}`, `/rolls/{id}/load`, `/rolls/{id}/lenses`, `/rolls/{id}/finish`, `/expiry` |
| Labs & processing | 23–29 | `/labs`, `/labs/{id}`, `/rolls/{id}/processing/{jobId}`, `/processing/{id}/scans-received`, `/processing/{id}/negatives-returned`, `/negatives-at-lab` |
| Frames & scans | 30–34 | `/processing/{id}/scans` (+`/preview`), `/processing/{id}/frames/{n}/compare`, `/scans/{id}/file`, `/rolls/{id}/frames/{n}` |
| Search & stats | 35–39 | `/search/rolls`, `/stats/gear`, `/stats/film`, `/stats/timeline`, `/stats/spending` |

Scan files are stored on local disk (`STORAGE_DIR`); use object storage before deploying to Render (ephemeral disk).
