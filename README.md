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

Settings come from environment variables. See [.env.example](.env.example). Copy it to `.env` at the repository root: `make run` and `make dev` load that file, and its values override the current environment. A plain `go run ./cmd/api` does not load it. `make run-prod` is the same local server, with `DATABASE_URL` taken from `.env.prod`. Startup still runs migrations, so that command migrates the production database.

## Development

| Command | Purpose |
|---|---|
| `make dev` | Run with live reload (air) |
| `make run-prod` | Run locally against the database in `.env.prod` |
| `make migrate-prod-status` | List applied and pending migrations on the production database |
| `make migrate-prod` | Apply pending migrations to the production database, after a confirmation |
| `make deploy` | Test, build, push and deploy the committed code to Cloud Run, after a confirmation |
| `make gen` | Regenerate server and query code after editing the spec or SQL |
| `make test-unit` | Unit tests, no Docker |
| `make test` | Unit and integration tests (needs Docker) |
| `make cover` | Coverage of hand-written code |
| `make lint` | golangci-lint |
| `make scan` | Trivy scan: vulnerabilities, secrets and misconfigurations |
| `make secrets` | Gitleaks scan: secrets in the full git history |
| `make hooks` | Use the git hooks in `.githooks/` (run once per clone) |
| `make db-reset` | Wipe and restart the local database |

Security scan: install [Trivy](https://trivy.dev/) (`brew install trivy`) and run `make hooks` once. The pre-push hook then runs the scan and blocks the push on a HIGH or CRITICAL secret or misconfiguration, or a HIGH or CRITICAL vulnerability that has a fix. Settings are in `trivy.yaml`; local `.env` and `google_sign_in.json` files are not scanned. To accept a finding, add its ID to `.trivyignore` with a comment that says why.

Secret scan: install [Gitleaks](https://github.com/gitleaks/gitleaks) (`brew install gitleaks`) and run `make hooks` once. The pre-commit hook then scans the staged changes and blocks the commit when it finds a secret. `make secrets` scans the full git history. To accept a false positive, add its fingerprint to `.gitleaksignore`.

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
- **Retries and concurrent edits:** `PUT /{resource}/{id}` with `If-None-Match: *` only creates (`412 already_exists` when the id is taken), so a retried create never updates a later edit. `PUT /rolls/{id}` takes `If-Match: "<RollSummary.version>"` and answers `412 roll_changed` when the roll changed since it was read. Frame and roll notes take a base value (`baseNotes`, `baseDescription`) and answer `409` when the stored text is neither the base nor the new text. Repeating a finish or a load with the same details is a no-op.
- **Soft delete:** `DELETE` only marks a row as deleted, and the id stays reserved. A delete returns 409 while the record is still in use.
- **Errors:** responses use `{code, message}`. 400 means the request failed validation, 404 means not found, 409 means a state conflict, 412 means a precondition header did not hold, and 422 means a business rule failed.
- **Your records only:** every record belongs to the account that created it, and each query is limited to the signed-in account (`owner_id`). Another account's record answers 404. Composite foreign keys stop a record from pointing at another account's record. Records created before accounts existed go to the first account.
- **Sessions:** every endpoint except `/health` and `/auth/*` needs a session and returns 401 without one. Register or log in to get an HTTP-only session cookie, and send requests with credentials. The spec decides which operations are public (`security: []`).
- **Audit:** each entry records the email of the signed-in user who made the change.
- **Accounts:** `/auth/*` is served by authboss and documented in the spec like any other route. Sessions are stored in Postgres, so logout ends a session right away and a password reset ends all of an account's sessions. Five wrong passwords within 15 minutes lock the account for 15 minutes.
- **Email verification:** `EMAIL_VERIFICATION` is `off` (default), `send` or `enforce`; any other value stops the server at start-up. In production, `send` and `enforce` need `SMTP_ADDR`, `SMTP_USERNAME`, `SMTP_PASSWORD`, a `MAIL_FROM` that is a bare address (no display name) other than the default `no-reply@localhost`, and an `API_URL` that is an `https` URL with a public host (not `localhost` or a loopback address).
- **Email verification flow:** a password registration mails a link, `{API_URL}/auth/verify-email#token=...`. The page there posts the token (and the account password) to `POST /auth/verify-email`, which records the proof. `POST /auth/verify-email/resend` mails a new link. Google accounts record proof at sign-up. The three values of `EMAIL_VERIFICATION` do this:
  - `off`: nothing is issued or required.
  - `send`: registration mails the link, and the account signs in at once as before. Nothing is refused.
  - `enforce`: as `send`, and an account without proof cannot use the API. `POST /auth/register` answers `201` with `verificationRequired: true` and starts no session. `POST /auth/login` with the right password answers `403 email_verification_required` and sets no cookie (a locked account still answers `429`, and a wrong password `401`). Sessions that already exist answer `401` until the account proves its address, then work again. Only the approved legacy Google-linked accounts are exempt. Existing password accounts need proof too. Verify, resend and password recovery stay public, so such an account can prove its address.
- **SMTP in production:** with `ENV=production` and `SMTP_ADDR` set, `SMTP_USERNAME` and `SMTP_PASSWORD` are required in every mode, `EMAIL_VERIFICATION=off` included. The server sends mail only over STARTTLS (use the submission port, for example 587) with a verified certificate, and only after the relay accepts authentication. Otherwise the send fails before any message goes out. Outside production the server uses STARTTLS and authentication when the relay offers them, so a local mail catcher works. Each send stops at a 1-minute deadline, and at most 8 sends run at once; extra mail is dropped and logged.
- **Proxy hops:** `TRUSTED_PROXY_HOPS` (default `0`) is the number of proxies in front of the server that append to `X-Forwarded-For`. `0` uses the connection's remote address; `N` uses the Nth entry from the right of `X-Forwarded-For`.
- **Google sign-in:** set `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` (a "Web application" OAuth client in the Google Cloud console) and register `{API_URL}/auth/oauth2/callback/google` as its redirect URI. The front end sends the browser to `/auth/oauth2/google?redir=/some/path`. It comes back to `{APP_URL}/some/path` signed in, or to `{APP_URL}/login?error=<code>`. A Google sign-in for a new Google subject whose verified email belongs to another account is refused with `account_conflict`. A returning Google subject signs in under the usual rules.
- **Google sign-in from a mobile app:** the app reads the web client id from `GET /auth/google/config`, then sends the ID token from the device's account picker to `POST /auth/google/token`, and the answer sets the session cookie. The token's audience must be `GOOGLE_CLIENT_ID`, `GOOGLE_ANDROID_CLIENT_ID` (the Android OAuth client), or `GOOGLE_IOS_CLIENT_ID` (the iOS OAuth client).
- **CSRF:** the cookie is `SameSite=Lax` by default, and CORS rejects writes from origins outside `CORS_ORIGINS` with a 403. A front end on another site needs `SESSION_SAMESITE=none`.

> Scan files are stored on local disk (`STORAGE_DIR`). Move them to object storage before deploying to a host with an ephemeral disk.
