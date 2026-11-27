# Diagnostics, tests, and metrics

## Scope and coverage

Uncommitted changes on `diagnostics-tests-metrics` against base `4c32af4`
(`git merge-base origin/main HEAD`), checked against
`agents/plans/2026-11-27-diagnostics-tests-metrics.md` (issue #156, audit
findings F24, F29, F32, F33, F35, F37). The working tree held the eleven
modified files and the untracked plan.

The change touches no state owner, protocol, persistence schema, or harness
driving beyond Ur's stream parser, so the review ran in this session with no
children.

Gaps:

- `/ox-work`'s final response was not passed on, so its Tests proved and Checks
  lists were not available. The checks below were run here instead.

## Summary

| ID | Severity | Category      | Action | Status | Title                                                |
| -- | -------- | ------------- | ------ | ------ | ---------------------------------------------------- |
| F1 | Low      | Documentation | Fix    | Resolved | `TestStreamErrors`'s doc comment now heads `finishing` |

## F1. `TestStreamErrors`'s doc comment now heads `finishing`

Low · Documentation · Fix · `internal/ur/ur_test.go:420`

The `finishing` helper was inserted between `TestStreamErrors` and its doc
comment. The comment "A failed turn ends with its error. The next request
carries the reply the user saw…" now runs straight into the helper's own
comment and documents `finishing`, and `TestStreamErrors` has no comment.

### Evidence

`internal/ur/ur_test.go:420-428`: the two comment blocks are contiguous above
`func finishing`, and `func TestStreamErrors` follows with none.

### Action

Move `finishing` and its one-line comment above the "A failed turn ends with
its error" comment, so that comment sits directly on `TestStreamErrors`. No
test needed.

## Checks run

- `go test -race -count=1 ./internal/ur/... ./internal/server/...`: pass.
  `go vet ./...`: clean. `gofmt -l internal/`: clean. `dprint check`: clean.
- Spot-check: with `finishDelete`'s new log line removed,
  `TestWorktreeDeleteRepeated` fails.
- Spot-check: with every nonempty finish reason forced to `stop`, the three new
  `TestStreamErrors` cases and `TestAsk` fail.
- `make metrics` with the base and the changed script: Go production files drop
  from 46 to 43 (8,272 to 8,161 code lines) and tests from 12,878 to 12,861; the
  rest of the output is identical. `git ls-files '*/go.mod'` finds only
  `docs/screenshots/repos/field-sync/go.mod`.
- `TestLiveUr` with the key from the main checkout's `.env`: pass, so the
  built-in model's normal replies still end with an accepted finish reason.
- `TestLive$/plan-mode` live: Haiku declined, and the section reports SKIP while
  `TestLive` passes.
- The rest of `make live` (other Claude sections and `approval.spec.ts`) was
  not run; the change cannot reach them.
- `docs/harnesses/claude-code.md` §12's new bullet matches §2's recorded
  `initialize`, `--model`, and `--effort` behavior.

## Verification 1

Working tree on base `4c32af4` after the F1 fix; the only change since the
review is the move in `internal/ur/ur_test.go`. Ran
`go test -count=1 ./internal/ur` (pass) and `gofmt -l internal/` (clean).

| ID | Status   | Evidence                                                                                  |
| -- | -------- | ----------------------------------------------------------------------------------------- |
| F1 | Resolved | `finishing` and its comment now precede `TestStreamErrors`'s comment, which heads the test |

No new findings.

## Verdict

Ready to commit.
