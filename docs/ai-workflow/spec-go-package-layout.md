# Go package layout review

## Decision

Keep the server's module, command, and feature packages. Retire one mixed-purpose package, `internal/common/convert`. Its HTTP date helpers and PostgreSQL timestamp helper have different owners. Give each group to an existing package. This removes one package and gives callers a direct dependency on the code that defines the relevant boundary.

This review covers repository structure at commit `03c2a7d8ec8cb61f35a14e5efba33fbf782d42c8`. The GitNexus index for `meta-frames-server` matches that commit. The Go team recommends placing server commands in `cmd` and server implementation packages in `internal`. It leaves room for different server architectures. [Organizing a Go module](https://go.dev/doc/modules/layout#server-project)

## Existing boundaries and reuse

| Location | Evidence and decision |
| --- | --- |
| `cmd/api` | `main.go` loads configuration, runs migrations, constructs dependencies and starts the HTTP server. The Makefile, Air and the Docker build target this command. Keep its path. |
| `internal/server`, `internal/controllers`, `internal/services` | The router handles HTTP. The controller aggregate implements the generated strict interface. Feature controllers translate HTTP types, and services run business operations. Keep both feature trees. For example, processing and roll share operations within a transaction. Directory symmetry alone gives no reason to merge the layers. |
| `internal/db`, `internal/db/gen`, `internal/db/queries` | The store owns PostgreSQL access and transactions. sqlc owns the generated types. Reuse the handwritten db package for timestamp conversion. |
| `api`, `internal/api` | The root package embeds the full OpenAPI YAML, including auth routes excluded from server generation. oapi-codegen writes one named file in the internal package. Place handwritten HTTP date helpers beside that generated file. Keep the embed and generator paths. |
| `migrations` | Goose uses embedded SQL. sqlc reads the same directory as its schema input. Keep the SQL files and their embed declaration together. |
| `internal/testutil`, `test/integration` | These directories support multiple features. Keep the shared fixtures and cross-package integration tests at their current paths. |

## Scope and dependency change

Move `ToAPIDate`, `FromAPIDate`, `APIDate` and `OptionalDate` to `internal/api`. Move `Timestamp` to `internal/db`. Preserve their signatures and implementations, except for the package qualification required by each move. Move their existing tests to the corresponding owners. Keep handwritten code in separate files from generated output.

The current import path occurs in 11 production files and two controller flow tests. The production sites are auth user mapping and the audit, camera, filmstock, lab, laborder, lens, processing, roll, scan and scanner controllers. Update the callers in the same compilable change. Remove the old package after the last import moves. Duplication risk is low because each helper retains one definition.

Exclude changes to route wiring, authentication policy, service boundaries, timestamp normalization, database queries, SQL migrations, generator settings and deployment scripts. Other shared packages have separate responsibilities and are outside this change.

## Data flow and compatibility contract

- An optional HTTP date enters through the generated request type. The controller passes the underlying date to its service. Preserve a nil request body, absent date and nil date pointer as absent. Preserve the supplied time value in either conversion direction.
- A PostgreSQL row carries a pgx timestamp and a validity flag. An invalid timestamp maps to the Go zero time, including when its Time field has a value. A valid timestamp passes through as stored. Apply the same rule to HTTP responses and auth user fields.
- Auth uses the helper for recovery expiry, last attempt, lock expiry and creation time. A change in zero-time handling could affect account recovery or lockout. Preserve the reverse conversion in the auth store.
- The helpers have no persistent state or side effects. Preserve request identity, transaction boundaries, errors, rollback and commit behavior. The HTTP schema, response fields, database schema and stored rows retain their existing contracts.

## Impact and acceptance

GitNexus rates the upstream impact of `Timestamp` as CRITICAL. It reported 63 impacted symbols, eight direct dependents and four affected processes. The results include session writes, handler construction and Google token sign-in. This measures reach. The proposed change moves code across package boundaries and requires verification of those callers.

1. Verify that each helper has one definition, every caller uses its owner and the old package has no imports. Check the Go package graph for cycles.
2. Run the existing date and timestamp tests in their new packages. Cover absent input, explicit dates, invalid timestamps and valid timestamps. Keep the controller flow tests and auth mapping tests passing.
3. Build the existing command with the Dockerfile's build tags. Confirm the OpenAPI and sqlc generators still target their existing files and preserve the handwritten helpers.
4. Apply the server formatting checks, run `make lint` and run `make test` with Docker available. Record the checked tree and results in the slice report. Production access is unnecessary.

## Handoff

Claude Opus 5.5 prepares the file-level plan in `docs/ai-workflow/plan-go-package-layout.md`. Plan one independently compilable move. Assign execution to Opus because the caller list includes authentication. Submit the plan to Nam before implementation.
