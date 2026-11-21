# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Trivy security scan. `make scan` checks the Go dependencies for known vulnerabilities and the repository for secrets and misconfigurations (`trivy.yaml`). After `make hooks`, the pre-push hook runs it and blocks the push on a HIGH or CRITICAL finding.
- Gitleaks secret scan. `make secrets` checks the git history for secrets. The pre-commit hook checks the staged changes and blocks the commit when it finds a secret.
- `GOOGLE_ANDROID_CLIENT_ID` and `GOOGLE_IOS_CLIENT_ID` name the Android and iOS OAuth clients that `POST /auth/google/token` accepts, besides `GOOGLE_CLIENT_ID`. Each holds one client id.

### Changed

- Internal: the date and timestamp helpers moved from `internal/common/convert` to `internal/api` and `internal/db`. The API does not change.
- The container image states `USER nonroot:nonroot`. The distroless base already ran as that user, so runtime behavior does not change.
- Internal: the release steps and the naming rule in `AGENTS.md` no longer name `package.json`, `vp lint`, or valibot, which the server does not use. The GitNexus rules use plain wording, and the impact check no longer applies to comment, docs, and formatting edits.

### Removed

- **Breaking:** `GOOGLE_MOBILE_CLIENT_IDS`. The server ignores it. Set `GOOGLE_ANDROID_CLIENT_ID` and `GOOGLE_IOS_CLIENT_ID` before you deploy. Until you do, `POST /auth/google/token` rejects ID tokens issued to the Android or iOS client. Tokens issued to `GOOGLE_CLIENT_ID` still work.

### Security

- `golang.org/x/net` 0.61.0 to 0.62.0 (CVE-2026-78661, denial of service through crafted HTML input).
- `github.com/moby/go-archive` 0.2.0 to 0.3.0 (CVE-2026-17106, file write through link following in tar extraction). Only the integration tests use it, through testcontainers.

## [4.0.0] - 2026-11-15

### Added

- `RollSummary` reports the roll's jobs: `developed` (a develop or develop_scan job has a developed date, negatives or scans back), `scanned` (a job has scans received), `openJobTypes` (the types of jobs still waiting for scans or negatives, each type once) and `openAtLab` (an open job is at a lab). `openAtLab` is false while the only open jobs are processed at home, even though the status is then `at_lab`.

- Lab orders record what a lab bills: `GET /lab-orders`, `GET|PUT|DELETE /lab-orders/{id}`. An order has a code (unique per lab, `409 order_code_taken`), an order date, a due date, VAT and discount as amounts, and lines with a product, options, quantity and unit price. An order with jobs cannot be deleted or change lab (`409 order_has_jobs`). A line with jobs cannot be removed (`409 order_item_in_use`) or take fewer rolls than it has (`409 order_item_over_quantity`). A lab with orders counts as in use. Migration `00016` adds the `lab_order` and `lab_order_item` tables.
- A job goes on one or more lines of an order, such as one for developing and one for scanning, with `orderItemIds` on `PUT /rolls/{id}/processing/{jobId}`, or with `PUT /processing/{id}/order-items`. The lines must be on one order (`422 order_items_on_two_orders`) from the job's lab (`422 order_lab_mismatch`), and each must have room (`409 order_item_full`). `GET /lab-orders/{id}/linkable-jobs` lists the jobs on no order or only on this one, with the lines they already have. Migration `00017` moves the link to the `processing_order_item` table.
- `RollSummary.version` grows by one on every change to a roll, whichever endpoint makes it (edit, load, finish, notes, status changes from its jobs, delete). `PUT /rolls/{id}` takes `If-Match: "<version>"` (several comma-separated, or `*`): a different version gets `412 roll_changed` and nothing is written; a weak or malformed tag gets `400 invalid_precondition`. Without the header the edit overwrites as before. Migration `00018` adds the column and the trigger that moves it.
- `PUT /rolls/{id}/notes` sets a roll's notes (`description`) and nothing else. With `baseDescription` it is a compare-and-set: already holding the notes is a no-op, still holding the base saves, anything else is `409 roll_notes_conflict`.
- `FrameInput.baseNotes` makes `PUT /rolls/{id}/frames/{number}` the same compare-and-set (`409 frame_notes_conflict`). Without it the last write wins, as before.
- `If-None-Match: *` makes `PUT /cameras/{id}`, `/lenses/{id}`, `/film-stocks/{id}` and `/labs/{id}` create-only: an id already taken, by any record of any account, deleted or not, gets `412 already_exists`, and nothing is changed or audited. Any other value gets `400 invalid_precondition`. CORS allows the `If-Match` and `If-None-Match` headers.
- A roll has a plan: `PUT /rolls/{id}/plan` sets `target` (`developed` or `scanned`), `developBy` and `scanBy` (`self` or `lab`), and `RollSummary.plan` returns it. `scanBy` without target `scanned` is `422 scan_not_planned`. Roll edits keep the plan. Migration `00019` adds the plan columns, `roll.scan_requested_at`, `processing.developed_at`, `processing.handoff_id` and the `lab_handoff` table (audited as `lab_handoff`).
- `RollSummary.stage` is the roll's lifecycle tab: `in_stock`, `in_camera`, `done_shooting`, `processing` or `completed`. `stageDetail` is the badge inside `processing` (for example `lab_developing`, `home_scanning`, `rescanning`, `waiting_lab_scan`, `printing`). The jobs decide them; the plan decides only the "waiting" badge. `needsScan` says the roll is marked for a scan. The raw `status` and its rules do not change.
- `POST /lab-handoffs` hands several rolls to one lab at once ("Giao lab"), in one transaction with `Idempotency-Key` for retries. Each roll gets a job and a handoff (its stay at the lab); the rolls share a batch. A roll can go if it is done shooting (`develop`, `develop_scan`), developed and waiting for its planned scan, or completed and marked for a scan (`scan`). `scanOrders` go to the jobs that produce scans. One roll that cannot go refuses the whole request, and the message names it (`409 roll_not_waiting`, `409 negatives_at_lab`, `422 invalid_job_type`, `422 scan_orders_required`, `422 duplicate_roll`). A scan job clears the roll's scan mark.
- A lab job joins the roll's open handoff at that lab, or opens one. `PUT /lab-handoffs/{id}/returned` records the roll back from the lab, also when every job of the handoff was cancelled; a handoff already returned changes nothing, and a date before the handoff is `422 returned_before_handed`. `RollSummary.openHandoffId` names the open handoff.
- `User.avatarUrl` is the linked Google account's profile picture, an https URL, and is left out otherwise. Google sign-in stores it and replaces it on the next sign-in. A value that is not an absolute https URL is dropped. Migration `00020` adds `app_user.avatar_url`.
- `make deploy` tests, builds and deploys the committed code to Cloud Run. `make migrate-prod` and `make migrate-prod-status` apply and list migrations on the production database. Each refuses uncommitted changes where they would reach production.

### Changed

- `make lint` runs golangci-lint v2 from this module (`go tool golangci-lint`).
- The spec names the developed date and the scan mark in English: "Developed" and "Needs scan". The endpoints are unchanged.
- `GET /rolls` lists the newest start date first. Rolls with no start date come last, then the newest added date, then id.
- `negativesAtLab` is true while a lab holds the roll, whatever the roll's status: it has an open handoff, or an old lab job with no handoff whose negatives are not recorded back. `GET /negatives-at-lab` follows the same rule per job.
- Recording negatives returned on a job in a handoff closes the handoff and gives every live job of it the date. A job already dated, or a closed handoff, changes nothing, so a repeated old request cannot close a newer handoff. Old jobs with no handoff work as before.
- While a lab holds the roll, `PUT /rolls/{id}/processing/{jobId}` refuses a home job and a job at another lab (`409 negatives_at_lab`). The lab that holds the roll can scan it next while it is still `at_lab`, once its develop job is developed. A job that holds the roll keeps its lab (`409 lab_fixed_by_handoff`); an edit that leaves out `labId` no longer moves such a job home. A new job at the lab of an old unreturned job takes the old job into its handoff.
- Finishing a roll that is already done shooting on the same date is a no-op (200) instead of `409 roll_not_in_camera`. Another date, or a roll that has moved on, is still a 409.
- Loading a roll again into the camera that holds it is a no-op only when the start date (today when left out), the shot ISO as stored and the lens set (the built-in lens on a fixed-lens camera) all match. Any difference is `409 roll_loaded_differently`; it used to answer 200 and keep the stored values.
- A job on an order takes its price from its lines added up, with the order's VAT and discount shared out by price, rounded to the dong. Its own `price` is cleared and cannot be sent with `orderItemIds` (`422 price_from_order`). `Processing.price`, a roll's totals and `GET /stats/spending` use this price. `Processing.order` names the order and the job's lines.

## [3.0.0] - 2026-11-03

### Added

- `GET /auth/google/config` returns the web client id (`GOOGLE_CLIENT_ID`) that a mobile app names when it requests an ID token.
- `POST /auth/google/token` signs in a mobile app with a Google ID token. It finds or creates the account as the browser flow does and sets the session cookie. `GOOGLE_MOBILE_CLIENT_IDS` lists the iOS and Android OAuth clients it accepts, besides `GOOGLE_CLIENT_ID`.
- `make run-prod` runs the local server with `DATABASE_URL` from `.env.prod`. Other settings still come from `.env`.
- Film stock `description` is documented as Markdown text in the API spec (`format: markdown`). The server stores it unchanged.
- Scanners have their own table and endpoints: `GET /scanners`, `POST /scanners`, `GET|PUT|DELETE /scanners/{id}`. Two built-in scanners, Noritsu HS-1800 and Frontier SP-3000, are visible to every account and read-only (`409 built_in_scanner`). Each account adds its own (another lab's machine, a flatbed, a camera rig) with `brand`, `model`, an optional `kind` and `notes`. A scanner used by a job or a scan cannot be deleted (`409 scanner_in_use`).
- Scan options depend on the scanner's kind: `GET /scanner-kinds` lists each kind's options (a minilab's resolution and format; a flatbed's dpi, bit depth, dust removal and multi-exposure; a camera rig's file format, pixel shift and stitched shots; and on every kind the framing: cropped, with the film border, or with border and sprockets), with a set for scanners of unknown kind. A scan order keeps its choices in `settings`, and a scanner keeps `defaultSettings` that a new order starts from. The server checks settings against the scanner's kind (`422 invalid_scan_settings`); an order's settings are only checked again when they change. Migration `00012` moves `hi_res` into `settings.resolution`.
- Labs name the scanners they run: `LabInput.scannerIds` sets them in order and `Lab.scannerIds` returns them. `defaultScannerId` optionally names the one a job sent there starts with; it must be one of `scannerIds` (`422 unknown_default_scanner`) and is saved with them, so sending `scannerIds` without it clears the default. Leaving `scannerIds` out keeps the lab's scanners; an empty list clears them. Each must be a built-in scanner or one of the account's own (`422 unknown_scanner`). Deleting a scanner removes it from the labs that ran it. Migration `00013` adds the `lab_scanner` table, and `00014` adds `is_default` with each lab's first scanner as its default.
- Scan orders, scans, scan refs and frame comparisons carry `scannerId` and `scannerName`. Requests name a scanner by `scannerId`: in `scanOrders`, in the `scannerId` multipart field of a scan import, and in the `scannerId` filter of `GET /processing/{id}/scans`. `GET /processing/{id}/frames/{number}/compare` returns every scan of the frame in `scans`.

### Changed

- A film stock's `boxIso` is now a string: a single ISO (`"400"`) or a rising range (`"100-400"`). Other values fail with `400` or `422 invalid_box_iso`. This breaks clients that send or read it as a number (breaking, API 0.6.0). A roll's `shotIso` stays an integer: one equal to a single box ISO is stored as null, and one within a range is kept. `LoadedRoll.shotIso` is left out when the roll has no shot ISO and its stock has a range. `GET /inventory?iso=` also matches stocks whose range includes the speed. Migration `00015` changes the column to text.
- The server checks its settings at startup and exits with one message listing every invalid value. A bad `PORT`, `DB_MAX_CONNS`, URL, `MAIL_FROM`, `SMTP_ADDR` or `SESSION_SAMESITE`, or only one of `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`, used to be ignored or fail later. Passwords in a URL are hidden in the message.
- The server accepts unencrypted HTTP/2 (h2c) as well as HTTP/1.1, so Cloud Run can forward requests over 32 MiB, such as large scan uploads, when deployed with `--use-http2`.
- Compose and the integration tests use Postgres 18, the major version production runs. Compose keeps its data in a new volume (`pgdata18`), so `make db-up` starts with an empty database; dump a Postgres 17 database and restore it to keep its data.
- `.dockerignore` keeps `.env` files, `/data` and other local files out of the Docker build context.
- A scanner's `model` is optional; `brand` stays required. A lab may run a Noritsu whose model nobody knows: such a scanner is named by its brand alone, and an account has at most one per brand. Migration `00013` drops the `NOT NULL` on `scanner.model`.
- The fixed scanner list (`noritsu`, `frontier`, `other`) is gone from the database. Migration `00011` moves existing data to the scanner table: `other` becomes an "Unknown scanner" owned by each account that used it, to rename to the real machine.

### Deprecated

- `hiRes` on scan orders: it reads and writes `settings.resolution` (`hi_res` or `standard`), and is ignored when `settings` is sent.
- The `scanner` code (`ScannerCode`: `noritsu`, `frontier`, `other`) in requests and responses, and the `noritsu`, `frontier` and `missing` fields of a frame comparison. They still work until clients move to `scannerId`: as input, `other` means the account's "Unknown scanner" (created on first use in a scan order); in responses, every scanner other than the two built-in ones reads as `other`.

### Fixed

- `PUT /rolls/{id}/processing/{jobId}` on an existing job saves a changed `process`. It was ignored before; leaving `process` out keeps the job's current one.
- `PUT /processing/{id}/negatives-returned` accepts a job processed at home and records the day its negatives were developed, which closes a home develop job. It returned `409 home_processing` before, so home develop jobs stayed open.

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

[Unreleased]: https://github.com/vhnam/meta-frames/compare/v4.0.0...HEAD
[4.0.0]: https://github.com/vhnam/meta-frames/compare/v3.0.0...v4.0.0
[3.0.0]: https://github.com/vhnam/meta-frames/compare/v2.0.0...v3.0.0
[2.0.0]: https://github.com/vhnam/meta-frames/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/vhnam/meta-frames/releases/tag/v1.0.0
