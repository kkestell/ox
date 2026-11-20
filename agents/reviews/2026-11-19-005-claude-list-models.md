# Review: Claude calls `list_models`

## Scope and coverage

Uncommitted changes on base `7ffb38f` (16 modified files, nothing staged), checked against `agents/plans/2026-11-19-005-claude-list-models.md` (Task 2 of `agents/scratch/agent-tools.md`). Reviewed the diff and what it reaches: `session.start` (the only builder of `agent.Options`), `startProcess` and its callers, `cmd/oxd` startup, `handleMCP`/`attach` in `internal/server/mcp.go`, `cmd/ox-mcp`, the test client helpers behind `TestLive`, and the build scripts that place `ox-mcp` beside `oxd` (`Makefile`, `e2e/global-setup.ts`, `dev.mjs`).

The requirement edit matches the plan's text word for word and sits after **Questions from Claude**. The documentation edits match the plan's Documentation updates.

Gaps:

- `make live` was not rerun; the implementer reports it passing, and no finding depends on it.
- Removing the `MCP` options in `session.start` is caught only by `TestLive`, which the plan chose as the layer for it; not rerun here.

## Summary

| ID | Severity | Category | Action   | Status | Title                                                        |
| -- | -------- | -------- | -------- | ------ | ------------------------------------------------------------ |
| F1 | Low      | Testing  | Fix      | Resolved | Forwarder check's test hangs and orphans `oxd` on regression |
| F2 | Low      | Testing  | Rejected | —      | No test protects the absolute socket path                    |

## F1. Forwarder check's test hangs and orphans `oxd` on regression

Low · Testing · Fix · Resolved · `cmd/oxd/main_test.go:136`

`TestRefusesToStartWithoutForwarder` runs `oxd` with `CombinedOutput` and no bound. When the check it guards is missing, `oxd` starts, listens, and never exits, so the test blocks until `go test`'s timeout panics (10 minutes under `make test`). The panic does not kill the child: the `oxd` test binary keeps running, holding a socket in a temporary directory, after the test run ends.

### Evidence

Run against the base revision in a detached worktree with `-timeout 30s`: `panic: test timed out`, and `pgrep` then showed the orphaned `oxd.test -socket=…/oxd206707086/oxd.sock …` process (killed by hand). Removing the `findForwarder` error check in the changed tree gave the same hang and orphan.

### Action

Bound the run so a regression fails at once and the child is reaped: build the command with `exec.CommandContext` on a context with a short deadline (a few seconds; the refusal happens before the database opens), and fail with "oxd started without ox-mcp" when the deadline, not an exit, ended it. The test stays in `cmd/oxd/main_test.go`; it must fail within its deadline, leaving no process, when the `findForwarder` check is removed.

## F2. No test protects the absolute socket path

Low · Testing · Rejected · `cmd/oxd/main.go:38`

Removing `filepath.Abs` on the socket path fails no test. It matters only for a relative `-socket`, which no caller passes: the defaults from `internal/paths` are absolute, and `make dev`, e2e, and the live test all use absolute paths.

### Action

Rejected: a test would need `oxd` with a relative socket, a workspace in another directory, and a stand-in Claude that runs the forwarder, which costs more than the one-line guard it protects.

## Checks run

- `make test`: passed.
- `make e2e`: passed (16 tests).
- `make live`: not rerun; reported passing by the implementer.
- New tests against base `7ffb38f` in a detached worktree (removed afterwards): `TestForwarder` fails (no `readOnlyHint`); `TestRefusesToStartWithoutForwarder` hangs until the test timeout and orphans `oxd` (F1); `TestClaudeRegistersOxMCP` does not compile against the base `startProcess`.
- Removed conditions, one at a time, in a scratch worktree (removed afterwards): `readOnlyHint` annotation → `TestForwarder` fails; `--allowedTools` → `TestClaudeRegistersOxMCP` fails; the `findForwarder` error → `TestRefusesToStartWithoutForwarder` hangs (F1); `filepath.Abs` → nothing fails (F2).
- Headless Chrome through `make dev`: not needed; the change does not alter how the page reaches `oxd`.

## Verification 1

Uncommitted changes on base `7ffb38f`; since the review, only `cmd/oxd/main_test.go` changed.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `TestRefusesToStartWithoutForwarder` now runs `oxd` under `exec.CommandContext` with a 5 s deadline and fails with "oxd started without ox-mcp" when the deadline ends it. With the `findForwarder` error removed in a scratch worktree, it failed in 5.5 s under `-race` and left no `oxd` process; with the fix in place it passes. |

No new findings.

Checks run: `make test` passed; `make e2e` passed (16 tests); `TestRefusesToStartWithoutForwarder` and `TestSecondInterruptStopsShutdown` pass under `-race`. `make live` not rerun: the fix changes only a startup test, not how Ox drives Claude Code.

## Verdict

`Ready to commit`.
