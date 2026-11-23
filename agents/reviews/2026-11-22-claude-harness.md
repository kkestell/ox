# Claude harness parking, exit reasons, and permission mode checks

## Scope and coverage

Uncommitted changes on `claude-harness` against base `394daa4`, checked against `agents/plans/2026-11-22-claude-harness.md`: `internal/claude/agent.go`, `internal/claude/claude.go`, and their tests in `park_test.go`, `prompt_test.go`, and `cmd/oxd/main_test.go`. The change resolves issue #30 (audit findings F10, F11, F14).

Gaps:

- No live probe of a plain-text failure after `initialize`, at the parent session's request. The harness doc records such lines only on failure, and the reader already delivers them before the exit.

## Summary

No findings.

The change does what the plan says. `CronDelete` no longer sets `mayHaveSchedules`. `Agent.plainText` is written only by `Handle` on the session goroutine, outside `starting`, and read once by `exit`. The six modes in `permissionModes` match the list in `docs/harnesses/claude-code.md`. `checkPermissionMode` is shared by startup's `checkConfig`, session creation, settings changes, and MCP `create_session`, so a misspelled mode is now refused everywhere through one rule. The built-in configuration's Claude modes (`manual`, `auto`, `acceptEdits`, `plan`) pass. No requirement or document changes, as the plan says.

## Checks run

- `go test ./internal/claude/ ./cmd/oxd/`: passed.
- Removing the `a.plainText` append from `exit`: `TestUnexpectedExitIsReported` fails.
- Restoring `"CronDelete"` to the scheduling tool names: `TestTurnWithoutStopHookKeepsSchedules` fails.
- Removing the `permissionModes` check: `TestConfigRefusesPermissionModesClaudeRefuses` fails.
- Every mutation was reverted; the tree matches the reviewed diff.
- From the implementer: `make test` passed; `make e2e` passed 124/124 on a rerun after unrelated timeouts under machine load. `make live` not run: the change does not alter how Ox drives Claude's protocol, so the Verification rules do not require it.

## Verdict

`Ready to commit`. The pull request carries `Fixes #30`.
