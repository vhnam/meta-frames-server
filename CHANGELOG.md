# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.0.0] - 2026-10-08

### Added

- Accounts with authboss: `POST /auth/register`, `POST /auth/login`, `POST /auth/logout`, password recovery by email (`POST /auth/recover`, then `POST /auth/recover/end` with the emailed token), and `GET /auth/me` for the signed-in user. Sessions are stored in Postgres behind an HTTP-only cookie (`metaframes_session`). Logout ends the session on the server, and a password reset ends every session of the account and lifts a lockout. Five wrong passwords within 15 minutes lock the account for 15 minutes (`429 account_locked`). Recovery emails are sent in the background.
- Sign in with Google (`GET /auth/oauth2/google`, then Google's redirect to `/auth/oauth2/callback/google`), enabled by `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`. New Google users get an account without a password. An existing account with the same verified email is linked. Google access tokens are not stored.
- Settings for accounts: `APP_URL` (front end, for email links and the return after Google sign-in), `API_URL` (this server as browsers reach it), `SESSION_SAMESITE` (`lax` by default, `strict`, or `none` for a front end on another site, which forces a Secure cookie), and `MAIL_FROM`, `SMTP_ADDR`, `SMTP_USERNAME`, `SMTP_PASSWORD` for recovery emails. Without `SMTP_ADDR`, emails are printed to stdout.
- `make run` and `make dev` load `.env` from the repository root. Its values override the current environment.

### Changed

- Every endpoint except `/health` and `/auth/*` requires a session and returns `401 unauthorized` without one (breaking, API 0.5.0).
- Every record belongs to the account that created it, and each account only sees and changes its own: lists, search, stats, inventory, and the audit log are per account. Another account's record answers `404`. A `PUT` with an id another account already uses is `409 already_exists`. Records created before accounts existed go to the first account. Idempotency keys are per account (breaking).
- Audit entries name the signed-in user's email as the actor. The `X-Actor` header is no longer read (breaking).
- CORS allows only the `Content-Type` and `Idempotency-Key` request headers. `Authorization` and `X-Actor` are no longer allowed (breaking).

## [1.0.0] - 2026-10-06

### Added

- HTTP API for a film catalog: cameras, lenses, film stocks, rolls, labs, processing, scans, search, and statistics.
- Server-assigned ids on create, and idempotent create-or-update when the client supplies the id.
- Bulk roll creation guarded by an `Idempotency-Key`.
- Roll lifecycle from in stock through loaded, finished, lab, developed, and scanned, including expiry listing.
- Processing transitions for scans received and negatives returned, and a list of negatives still at the lab.
- Scan import with preview, per-frame compare, and file download. A soft-deleted scan no longer blocks a new file in the same slot.
- Search over rolls, plus gear, film, timeline, and spending statistics.
- Soft delete on every entity. The id stays reserved, reads skip deleted rows, and delete is refused while the record is still in use.
- An audit log written by database triggers on insert, update, and delete, including the `X-Actor` header and the request id.
- Scan orders on processing jobs. Each job records the scanners that were ordered, whether each is hi-res, and how many scans have been imported for it. `Processing.scanners` is replaced by `scanOrders` (breaking, API 0.4.0). Scan and develop-and-scan jobs need at least one order; develop and print jobs need none. A scanner that already has scans cannot be dropped from the order, and a scan can only be imported for an ordered scanner.
- Expected return dates on processing jobs, both optional and neither earlier than `sentAt`. `scansExpectedAt` is when the lab expects to deliver the scans, for scan and develop-and-scan jobs. `negativesExpectedAt` is when it expects to return the negatives, for develop and develop-and-scan jobs. Other job types are rejected with `scan_not_applicable` or `negatives_expected_not_applicable`. Reminders will use them later.
- Local disk storage for scan files, embedded database migrations on startup, and a Compose Postgres for local development.

### Changed

- `PUT /rolls/{id}` and `POST /rolls/bulk` take expiry as `{ year, month }`, the same object returned on roll reads.
- `GET /rolls` sorts by creation time, newest first. It no longer puts rolls with a start date first.

### Fixed

- `GET /inventory` (and any query that passes a list of ids) failed when the list was empty, because the simple query protocol could not encode a `uuid[]` with no elements.
- `POST /rolls/bulk` with an `Idempotency-Key` failed while saving the response, because the JSON was sent as bytea hex.
- `GET /cameras` returned 500 when a loaded roll had no start date. The camera now reports `daysLoaded` as 0 in that case.

[Unreleased]: https://github.com/vhnam/meta-frames/compare/v2.0.0...HEAD
[2.0.0]: https://github.com/vhnam/meta-frames/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/vhnam/meta-frames/releases/tag/v1.0.0
