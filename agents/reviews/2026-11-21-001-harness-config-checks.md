# Harness configuration checks on `agent.Harness`

## Scope and coverage

Reviewed the uncommitted changes on base `96678de` against
`agents/plans/2026-11-21-003-harness-config-checks.md`: `cmd/oxd/main.go`,
`cmd/oxd/main_test.go`, `docs/architecture.md`, `internal/agent/agent.go`,
`internal/claude/claude.go`, `internal/claude/prompt.go`, and
`internal/ur/ur.go`. The untracked plans 2026-11-21-001 and 002 belong to
other work and were not reviewed. Callers checked: the harness map in
`cmd/oxd/main.go`, and the request checks in `internal/server` that use
`config.Harness.CheckPermissionMode`, which this change leaves alone.

Gaps: none.

## Summary

| ID | Severity | Category    | Action | Status | Title                                        |
| -- | -------- | ----------- | ------ | ------ | -------------------------------------------- |
| F1 | Low      | Conventions | Fix    | Resolved | Ur's `agent.Harness` literal hides `Start` at the end of one line |

## F1. Ur's `agent.Harness` literal hides `Start` at the end of one line

Low · Conventions · Fix · `internal/ur/ur.go:46`

`ur.Harness` now returns
`agent.Harness{CheckEffort: checkEffort, CheckPermissionMode: checkPermissionMode, TakesProviders: true, Start: func(...) {`
on one line, with the multi-line `Start` closure as the last field. A reader
has to scan past three fields to find where the closure starts, and the
literal reads differently from `claude.Harness`, which puts one field per
line.

### Evidence

Read from the diff.

### Action

Write the literal one field per line, `Start` first, as `claude.Harness` in
`internal/claude/prompt.go` does. No test: the change has no behavior.

## Checks run

- `make test`: passed (Go tests with the race detector, golangci-lint, web
  lint, stack tests).
- Condition removals, each reverted after:
  - `TakesProviders: true` on `ur.Harness`:
    `TestConfigOffersOnlyWhatHarnessesTake` fails, the built-in
    configuration is refused.
  - `CheckPermissionMode` on `claude.Harness`:
    `TestConfigRefusesClaudesDefaultPermissionModeAlias` fails.
  - `CheckEffort` on `ur.Harness`: the Ur effort case is accepted.
  - The nil `CheckEffort` skip in `checkConfig`: the `plain` harness case
    panics.
- The new `picky` cases cannot compile on the base revision, which has no
  `CheckEffort` or `CheckPermissionMode` field on `agent.Harness`; on the
  base's logic an unnamed harness gets no checks, so both would be accepted.
- `make e2e`: taken from the implementer's report, passed (40 passed, 3
  skipped). `make live` and the headless Chrome check are not required: the
  change does not alter how Ox drives Claude Code or how the page reaches
  `oxd`.

## Verification 1

Checked the working tree on base `96678de` after the fix. `git diff -w` of
`internal/ur/ur.go` shows only the literal's layout changed beyond the
reviewed diff. `make test` passed. `make e2e` was not rerun: the fix changes
no behavior.

| ID | Status   | Evidence                                                              |
| -- | -------- | --------------------------------------------------------------------- |
| F1 | Resolved | `ur.Harness` writes one field per line, `Start` first; no other change |

No new findings.

## Verdict

Ready to commit.
