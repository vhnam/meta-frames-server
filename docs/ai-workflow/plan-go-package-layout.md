# Plan: retire `internal/common/convert`

- Spec: [spec-go-package-layout.md](spec-go-package-layout.md) (locked; this plan does not change it).
- Repository: `meta-frames-server`. Base commit: `03c2a7d8ec8cb61f35a14e5efba33fbf782d42c8`.
- Track: Full-Track, high-risk. The caller list includes auth (`internal/auth/user.go`: recovery expiry, lockout). Executor: **Claude Opus 5.5**. Reviewer: GPT-6.1-Sol.
- Slices: **1**. A split is possible: add the new helpers first, move the callers, then delete the old package. Each step would compile. One slice is still chosen: it avoids a transition state with two definitions of each helper, and it keeps the move atomic for one review and one revert.

## Reuse analysis

- Existing patterns reviewed: `internal/api` holds only `api.gen.go` (oapi-codegen writes that one file, `oapi-codegen.yaml:2`). `internal/db` holds handwritten `store.go` and `migrate.go`; sqlc writes only `internal/db/gen` (`sqlc.yaml`). Tests in `internal/db` use `package db`.
- Chosen approach: **Reuse** — move each helper to the existing package that owns its type. No new package, no new abstraction.
- Duplication risk: Low. Each helper keeps one definition.

## Current state (verified at base commit)

`internal/common/convert/convert.go` defines 5 functions. Importers (13 files, all already import `meta-frames-server/internal/api`):

| File | Uses | Needs new import |
| --- | --- | --- |
| `internal/auth/user.go` | `Timestamp` ×4 | `internal/db` |
| `internal/controllers/audit/audit.go` | `Timestamp` ×1 | `internal/db` |
| `internal/controllers/camera/camera.go` | `Timestamp` ×2, `ToAPIDate` ×1 | `internal/db` |
| `internal/controllers/filmstock/filmstock.go` | `Timestamp` ×2 | `internal/db` |
| `internal/controllers/lab/lab.go` | `Timestamp` ×2 | `internal/db` |
| `internal/controllers/laborder/laborder.go` | `Timestamp` ×3, `APIDate`, `FromAPIDate`, `ToAPIDate` | `internal/db` |
| `internal/controllers/lens/lens.go` | `Timestamp` ×2 | `internal/db` |
| `internal/controllers/scanner/scanner.go` | `Timestamp` ×2 | `internal/db` |
| `internal/controllers/processing/processing.go` | `APIDate`, `FromAPIDate`, `OptionalDate`, `ToAPIDate` | none |
| `internal/controllers/roll/roll.go` | `APIDate`, `FromAPIDate`, `OptionalDate`, `ToAPIDate` | none |
| `internal/controllers/scan/scan.go` | `APIDate` | none |
| `internal/controllers/processing/processing_flow_test.go` | `APIDate` | none |
| `internal/controllers/roll/roll_flow_test.go` | `APIDate` ×2 | none |

Cycle check: `internal/db` imports only `internal/common/requestctx` and `internal/db/gen`. `internal/api` imports no internal package. Neither imports `auth` or `controllers`, so the new edges create no cycle. `internal/auth` already imports `internal/db` in `auth.go`, `storer.go`, `sessions.go`. No importer declares a local identifier `db` that would shadow the package (checked with grep; the executor confirms with the compiler).

## Slice 1: move helpers to their owners and delete the old package

**Allowed files**

- Add: `internal/api/date.go`, `internal/api/date_test.go`, `internal/db/timestamp.go`, `internal/db/timestamp_test.go`.
- Delete: `internal/common/convert/convert.go`, `internal/common/convert/convert_test.go` (use `rm`, not `git rm`).
- Edit (import line and qualifiers only): the 13 files in the table above.
- Workspace log: `../docs/progress.md` (append one entry).

**Changes per file**

1. `internal/api/date.go` (`package api`): move `ToAPIDate`, `FromAPIDate`, `APIDate`, `OptionalDate` with bodies and doc comments unchanged. `OptionalDate` takes `*DateInput` (drop the `api.` qualifier, same package). Imports: `time`, `openapi_types "github.com/oapi-codegen/runtime/types"`.
2. `internal/db/timestamp.go` (`package db`): move `Timestamp` with body and doc comment unchanged. Imports: `time`, `github.com/jackc/pgx/v5/pgtype`.
3. `internal/api/date_test.go` (`package api`): move `TestOptionalDate`, `TestDateConversionsRoundTripAndKeepNil` and the `date` helper unchanged except `api.DateInput` → `DateInput`.
4. `internal/db/timestamp_test.go` (`package db`): move `TestTimestampTreatsNullAsTheZeroTime` unchanged. Add one case: `pgtype.Timestamptz{Time: moment, Valid: false}` returns the zero time (spec: "an invalid timestamp maps to the Go zero time, including when its Time field has a value"). It needs no database; it must pass under `go test -short`.
5. The 13 importers: remove the `internal/common/convert` import. Replace `convert.Timestamp` → `db.Timestamp` and add `"meta-frames-server/internal/db"` where the table says so. Replace `convert.{ToAPIDate,FromAPIDate,APIDate,OptionalDate}` → `api.<same name>`. Change nothing else in these files.
6. Delete both files in `internal/common/convert`; the directory must not exist after the slice.

The symbol names do not change, so this is a package move, not a rename. GitNexus `rename` does not apply; edit the qualifiers by hand and let the compiler confirm every site.

**Acceptance criteria**

1. `grep -rn 'internal/common/convert' --include='*.go' .` returns nothing; `internal/common/convert/` does not exist.
2. Each helper has exactly one definition: `grep -rnE '^func (ToAPIDate|FromAPIDate|APIDate|OptionalDate|Timestamp)\(' internal` returns 5 lines, 4 in `internal/api/date.go`, 1 in `internal/db/timestamp.go`.
3. Function signatures and bodies match the base commit byte for byte, except the `api.` qualifier on `OptionalDate`'s parameter (compare with `git show 03c2a7d:internal/common/convert/convert.go`).
4. `go build -tags=sonic -o /dev/null ./cmd/api` succeeds (an explicit `-o` is required: the default output name `api` collides with the `api/` directory) (Dockerfile flags, `build/package/Dockerfile:6`). `go vet ./...` reports no import cycle.
5. `make gen` leaves `internal/api/date.go` and `internal/db/timestamp.go` unchanged and produces no diff in `internal/api/api.gen.go` or `internal/db/gen/`. If the generators produce an unrelated diff, record it, restore the generated files, and report it; do not commit it.
6. Moved tests pass in their new packages: `go test -short ./internal/api/ ./internal/db/`. Controller flow tests and auth tests pass: `go test ./internal/controllers/... ./internal/auth/...`.
7. Workflow §4 server checks: `gofmt -l` on staged, unstaged and untracked in-scope Go files gives no output; `make lint` passes; `make test` passes with Docker running. Record base SHA plus the changed-path list and content hashes.
8. GitNexus (repository `AGENTS.md`): before the edit, run `impact` upstream on `Timestamp` and `OptionalDate` and record the risk (the spec reports `Timestamp` CRITICAL; 63 symbols, 8 direct dependents, 4 processes). Before any commit, run `detect_changes --scope all`; the changed symbols must be only the 5 moved helpers and their callers. A `partial` or `truncated` result is not clean.
9. Scope check covers staged, unstaged and untracked changes. Run `git status --porcelain --untracked-files=all`, `git diff --cached --name-status` and `git diff --name-status` in `meta-frames-server`. Every path in the output must be an allowed file or a baseline entry. Baseline at plan time: ` M AGENTS.md`, `?? docs/ai-workflow/plan-go-package-layout.md`, `?? docs/ai-workflow/spec-go-package-layout.md`. The slice must not change or stage the baseline entries. Content check: before the first edit, record `shasum -a 256` of all three baseline files; after the slice, the hashes must match. Plan-time SHA-256 (nothing staged at that time): `AGENTS.md` = `2662d4d19bf79635b04b50c5193c8f1a7ed4b506ccae51100ac7558a2f73be54`, `docs/ai-workflow/spec-go-package-layout.md` = `fbb278f912700f7e5d394d09ba95f109cedad29d0e249c50158bd383fe842eb8`. The plan file cannot hold its own hash; the executor records it before the first edit. A mismatch at start means the baseline moved: stop and report. After the slice, `git diff --cached --name-only` must not list any of the three files. `../docs/progress.md` is outside this repository; check it in the workspace by reading the appended entry. No `CHANGELOG.md` entry: the change is internal and has no user-facing effect.

**In scope**

- Moving the 5 helpers and their 3 tests, plus the one invalid-with-Time test case.
- Updating the 13 importers' import lines and qualifiers.
- Deleting `internal/common/convert`.
- One `docs/progress.md` entry.

**Out of scope**

- Any behavior change in the helpers (zero-time handling, nil handling, time zones).
- Route wiring, auth policy, service boundaries, database queries, SQL migrations, generator config (`oapi-codegen.yaml`, `sqlc.yaml`), deployment scripts.
- Other `internal/common/*` packages (`apperror`, `clock`, `pointers`, `requestctx`).
- New helpers, renamed helpers, or call-site refactors beyond the qualifier swap.
- Production database access of any kind. The slice does not need it.
- Fixing the GitNexus block in `meta-frames-server/CLAUDE.md` (see Notes).

**Risk**

- `Timestamp` feeds auth recovery expiry, last attempt, lock expiry and creation time. The move keeps the body unchanged; criteria 3, 4 and 6 confirm that. A silent change would weaken account lockout or recovery, so a reviewer diff of the moved body against the base is required.
- Controllers gain a direct import of `internal/db`. The spec locks this choice.

## Notes for Nam

- `meta-frames-server/CLAUDE.md` contains the GitNexus block. The workspace rule says that file stays `@AGENTS.md` only. This slice does not fix it; it needs a separate one-file docs change.
- `meta-frames-server` has an uncommitted change in `AGENTS.md` that predates this plan. The executor must not stage it.
- Next step after Nam approves: delegate Slice 1 to Opus (executor), then Sol reviews and writes `review-go-package-layout.md`.
