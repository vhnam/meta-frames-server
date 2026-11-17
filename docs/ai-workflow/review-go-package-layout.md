# Review: go package layout, Slice 1

What this change does: It moves four HTTP date helpers to `internal/api` and `Timestamp` to `internal/db`. The 13 callers use the new owners, and `internal/common/convert` is removed. The helper bodies match base commit `03c2a7d`, except for `api.DateInput` becoming `DateInput` inside `api`.

## [CRITICAL]

None. The only out-of-scope repository files in the worktree are the three unchanged baseline files named in the plan. Nothing is staged.

## [WARN]

1. **The recorded build command cannot pass from this repository root** (`docs/ai-workflow/plan-go-package-layout.md:61`; `../docs/progress.md:1257`). `go build -tags=sonic ./cmd/api` fails with `go: build output "api" already exists and is a directory`. The slice report records that command as passing. Add an output path to the acceptance command and correct the build evidence. `go build -tags=sonic -o /private/tmp/meta-frames-sol-api ./cmd/api` passed in this review. This is a verification defect; the corrected build found no code defect.

2. **GitNexus does not see the four new files in this worktree** (`docs/ai-workflow/plan-go-package-layout.md:65`; `../docs/progress.md:1257`). `detect-changes --scope all` reports 16 files and lists the deleted definitions in `internal/common/convert`, but `git status` also shows four untracked helper and test files. The reported graph result cannot establish that the new definitions are the only definitions. Record this limit and use the source, caller, and compile checks for the four untracked files; do not describe the graph result as a complete change check. The graph also counts the pre-existing `AGENTS.md` change.

## [NIT]

None.

## Acceptance checks

1. **Pass:** No Go import of `internal/common/convert` remains; its directory is absent.
2. **Pass:** The five helper names each have one definition, in the planned files. All 13 caller diffs are import or qualifier changes.
3. **Pass:** All five function definitions match `git show 03c2a7d:internal/common/convert/convert.go` after the one permitted `api.DateInput` substitution. `Timestamp` returns zero when `Valid` is false, even when `Time` has a value; the moved test covers it. Auth recovery and lock expiry still call that helper.
4. **Needs correction:** The exact build command fails as described in finding 1. The build with an explicit output path and `go vet ./...` passed.
5. **Pass:** `make gen` passed. `api.gen.go` and `internal/db/gen` have no changes; the handwritten helpers kept their content.
6. **Pass:** `go test ./internal/api/ ./internal/db/` and `make test` passed after the reviewer test was added. The controller and auth packages passed in the full run.
7. **Known failure:** `gofmt -l` returned no paths. `make lint` reported 40 issues (9 errcheck, 5 staticcheck, 26 unused), the same counts reported from the base commit. These issues are outside this slice. `make test` passed.
8. **Needs correction:** GitNexus reported high risk, 16 tracked files, and 38 symbols, but omitted the four untracked files. See finding 2. The source and compile checks cover the new files.
9. **Pass:** `git status --porcelain --untracked-files=all`, staged and unstaged diffs, and the four untracked file contents were reviewed. The baseline SHA-256 values for `AGENTS.md`, the spec, and the plan match the slice report; no file is staged. The workspace progress entry exists.

The reviewer added `TestDateConversionsPreserveTimeValue` in `internal/api/date_test.go:36`. The moved date tests cover nil and UTC dates but did not check that a supplied time with a non-UTC location stays unchanged. The new test checks all four date helpers without testing HTTP or framework behavior. No new timestamp test was needed because the executor added the invalid-with-Time case.

The executor log gives abbreviated SHA-256 values for untracked files and no per-file hashes for edited files. I did not reuse its passing checks on that evidence. I reran the required Go tests, build, vet, generator, format, and lint checks on the current worktree. The full test run needed local socket access; it passed with that access. The timestamp and date production files still match the executor's reported hash prefixes and suffixes. The reviewer test changed `internal/api/date_test.go` after the executor run.

Lean: -0 lines possible.

Verdict: request changes

## Round 2

What this change does: This round corrects the build command in the plan and the Slice 1 progress entry. It records the GitNexus limit for untracked files and checks the reindexed helper symbols. It changes no production code.

Findings: none. WARN 1 and WARN 2 from round 1 are resolved.

- WARN 1: Plan criterion 4 and the Slice 1 progress entry now name `go build -tags=sonic -o /dev/null ./cmd/api`. I ran this exact command once; it passed.
- WARN 2: The fix report states that `detect-changes` does not see untracked files. It records `analyze --index-only` and the five upstream impact checks. I reran all five impact commands: each resolves to `internal/api/date.go` or `internal/db/timestamp.go`, with the risk and counts in the fix report. I reran `context Timestamp`; its symbol and callers point to `internal/db/timestamp.go`, with no reference to `internal/common/convert`. The reindexed graph also sees the timestamp test caller. The date and timestamp test files remain covered by the passing round 1 tests.
- Scope: All 17 file SHA-256 values in the fix report match the current helper, test, auth, and controller files. The plan hash matches the report. `AGENTS.md` is `2662d4d19bf79635b04b50c5193c8f1a7ed4b506ccae51100ac7558a2f73be54`; `git status --porcelain -- CLAUDE.md` has no output. No file is staged.

Lean: -0 lines possible.

Verdict: approve
