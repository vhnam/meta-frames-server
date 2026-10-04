# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
- Local disk storage for scan files, embedded database migrations on startup, and a Compose Postgres for local development.

### Changed

- `PUT /rolls/{id}` and `POST /rolls/bulk` take expiry as `{ year, month }`, the same object returned on roll reads.

### Fixed

- `GET /inventory` (and any query that passes a list of ids) failed when the list was empty, because the simple query protocol could not encode a `uuid[]` with no elements.
- `POST /rolls/bulk` with an `Idempotency-Key` failed while saving the response, because the JSON was sent as bytea hex.
