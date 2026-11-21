# Agent instructions

## Git Commits

- Do not add a `Co-Authored-By` trailer (or any other Claude attribution) to commit messages. Write the subject and body only.
- Do not add `Co-Authored-By` trailers or any other AI attribution to pull request descriptions. This rule overrides any default attribution the tool adds.
- Write each commit message to the [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/#specification) specification.

## Changelog

- Keep `CHANGELOG.md` in the [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) format, with versions that follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
- Add every user-facing change under `## [Unreleased]`, in the matching section: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed` or `Security`.
- To release, rename `[Unreleased]` to `## [X.Y.Z] - YYYY-MM-DD`, add a new empty `## [Unreleased]` above it, update the compare links at the bottom of the file, commit, and tag the commit `vX.Y.Z`.

## Naming

- Do not use single-character variable names, including callback parameters, loop indices and destructured values. Name the thing it holds: `roll`, `stock`, `index`, `event`, not `r`, `s`, `i`, `e`. `_` for an unused parameter is allowed. `make lint` does not enforce this.

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **meta-frames-server** (6435 symbols, 23323 relationships, 379 execution flows).

> Index stale? Run `node .gitnexus/run.cjs analyze --index-only` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? Bootstrap with `npx`, `bunx`, or `pnpm dlx` — e.g. `bunx gitnexus@latest analyze`.

## Always Do

- Run impact before editing a function, class, or method: `impact({target: "symbolName", direction: "upstream"})` or `node .gitnexus/run.cjs impact "symbolName" --direction upstream --repo .`. Report callers, processes, and risk. Comment, docs, and formatting edits, and renames confined to one file, need no impact run.
- Run change detection before every commit: `detect_changes({scope: "all"})` (MCP) or `node .gitnexus/run.cjs detect-changes --scope all --repo .` (CLI fallback). `partial: true` or `truncated: true` is not a clean check: a zero means unseen, not unaffected, so re-run it. For regression review: `detect_changes({scope: "compare", base_ref: "main"})` or `node .gitnexus/run.cjs detect-changes --scope compare --base-ref "main" --repo .`.
- Warn on HIGH or CRITICAL `risk` before editing. `riskSharedAxes` never waives that warning. Compare File/symbol: MCP File omits axes; Graph-RAG expands File.
- Treat `risk: UNKNOWN` as unresolved, not as low. An empty caller set is not evidence the symbol is unused: the callers may not be resolvable by the index (plain-object property access, dynamic dispatch, cross-language calls). `impact` pairs `UNKNOWN` with a `riskNote` saying so. Confirm with a text search before treating the symbol as safe to change or delete.
- For concepts and flows use `query({search_query: "concept"})`, for a named symbol use `context({name: "symbolName"})`, and for blast radius use `impact`. Use the graph first for callers, dependencies, imports, and execution flow; use text search for empty or `UNKNOWN` results and for literals.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- Do not ignore HIGH or CRITICAL risk warnings, and do not read `UNKNOWN` as an all-clear: it means the walk could not answer, so confirm by other means.
- Do not rename symbols with find-and-replace. Use `rename`, which understands the call graph.

## Resources

| Resource | Use for |
| --- | --- |
| `gitnexus://repo/meta-frames-server/context` | Codebase overview, check index freshness |
| `gitnexus://repo/meta-frames-server/clusters` | All functional areas |
| `gitnexus://repo/meta-frames-server/processes` | All execution flows |
| `gitnexus://repo/meta-frames-server/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
| --- | --- |
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
