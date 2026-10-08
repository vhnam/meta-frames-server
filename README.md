# meta-frames-server

REST API for **Meta-Frame**, a catalog for film photography. It tracks gear, film stock, each roll from purchase to scan, lab processing, and the scanned frames.

**Stack:** Go · Gin · PostgreSQL (pgx, sqlc, goose) · OpenAPI 3 (oapi-codegen)

## What it covers

| Area | What you can do |
|---|---|
| Accounts | Register, log in and out with a password or with Google, recover a password by email, read the current user |
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

Settings come from environment variables. See [configs/.env.example](configs/.env.example). Copy it to `.env` at the repository root: `make run` and `make dev` load that file, and its values override the current environment. A plain `go run ./cmd/api` does not load it.

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
  auth/             accounts with authboss: /auth routes, sessions, lockout, user storer
  server/           router, CORS, validation, error mapping
  storage/          scan file storage (local disk)
migrations/         goose migrations, embedded in the binary
test/integration/   end-to-end tests against real Postgres
```

## API conventions

- **Ids:** `POST` assigns an id on the server. `PUT /{resource}/{id}` creates or updates with an id from the client, so offline retries are safe.
- **Soft delete:** `DELETE` only marks a row as deleted, and the id stays reserved. A delete returns 409 while the record is still in use.
- **Errors:** responses use `{code, message}`. 400 means the request failed validation, 404 means not found, 409 means a state conflict, and 422 means a business rule failed.
- **Your records only:** every record belongs to the account that created it, and each query is limited to the signed-in account (`owner_id`). Another account's record answers 404. Composite foreign keys stop a record from pointing at another account's record. Records created before accounts existed go to the first account.
- **Sessions:** every endpoint except `/health` and `/auth/*` needs a session and returns 401 without one. Register or log in to get an HTTP-only session cookie, and send requests with credentials. The spec decides which operations are public (`security: []`).
- **Audit:** each entry records the email of the signed-in user who made the change.
- **Accounts:** `/auth/*` is served by authboss and documented in the spec like any other route. Sessions are stored in Postgres, so logout ends a session right away and a password reset ends all of an account's sessions. Five wrong passwords within 15 minutes lock the account for 15 minutes.
- **Google sign-in:** set `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` (a "Web application" OAuth client in the Google Cloud console) and register `{API_URL}/auth/oauth2/callback/google` as its redirect URI. The front end sends the browser to `/auth/oauth2/google?redir=/some/path`. It comes back to `{APP_URL}/some/path` signed in, or to `{APP_URL}/login?error=<code>`. A Google account whose verified email matches an existing account is linked to that account.
- **CSRF:** the cookie is `SameSite=Lax` by default, and CORS rejects writes from origins outside `CORS_ORIGINS` with a 403. A front end on another site needs `SESSION_SAMESITE=none`.

> Scan files are stored on local disk (`STORAGE_DIR`). Move them to object storage before deploying to a host with an ephemeral disk.
