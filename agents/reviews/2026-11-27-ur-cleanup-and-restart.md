# Review: End Ur's commands before deletion settles, refuse a child cleanly, and restart Ur on rebuild

## Scope and coverage

Uncommitted changes on `ur-cleanup-and-restart` over base `1e7b9c1`, reviewed against `agents/plans/2026-11-27-ur-cleanup-and-restart.md` (issues #147 and #154), with the user's approved assumption that a rebuild changing `bin/ur` or `bin/ox-mcp` restarts `oxd` under `make dev`. The tree held the 14 modified files in `git status` plus the plan.

The review covered the reader goroutine and `exited` hook in `internal/process`, the `commands` parser and kill in `internal/ur`, `addChild`'s new publication order, `session.update`'s membership check, the `starts` hash in `stack.mjs`, the docs, and the tests. It also traced every caller that can emit an update to check that none holds `Server.mu`, how the web client handles `session/created` (it adds the session unloaded and reads history from `session/get`), and the Claude and Ur snapshot state at publication (`running`, from the queued input).

Gaps:

- `/ox-work`'s final response (Tests proved, Checks) was not passed on. I ran the required checks and spot-checks myself.
- `make e2e` was not run. The page's code is unchanged, and the page reads a new child's history from `session/get`.

## Summary

| ID | Severity | Category | Action   | Status | Title                                                      |
| -- | -------- | -------- | -------- | ------ | ---------------------------------------------------------- |
| F1 | Low      | Testing  | Fix      | Open   | Ur's `send input to Ur` wrap has no test                   |
| F2 | Low      | Testing  | Rejected | —      | `TestCrashEndsCommands` cannot tell kill-before-Reaped     |

## F1. Ur's `send input to Ur` wrap has no test

Low · Testing · Fix · `internal/ur/agent.go:269`

The plan's Decisions make Ur's write error name Ur, so the parent sees what failed when a child's first message is refused. No test reaches `ur.Agent.Send`'s failed write. `TestRefusedFirstMessageLeavesNoChild` builds the string `send input to Ur: broken pipe` itself in its gate agent, so it passes whether or not the wrap exists.

### Evidence

`grep -rn "send input to Ur" internal` finds only the wrap and the hand-written error in `mcp_test.go`. No Ur test calls `Send` after `ur` has exited, so no test observes the wrap.

### Action

Keep the wrap. Add a test in `internal/ur/ur_test.go`, beside `TestUnexpectedExit`: a stand-in `ur` script that exits at once, wait on `Reaped()` without handling events, then `Send` an input and assert the error starts with `send input to Ur:`. The write then fails with a broken pipe because `ur` has gone.

## F2. `TestCrashEndsCommands` cannot tell kill-before-Reaped

Low · Testing · Rejected · `internal/ur/ur_test.go:1715`

Moving `exited()` after `close(p.reaped)` would still pass the "stopped before the exit" case, because `gone` polls for up to two seconds.

### Action

Rejected. A killed command is reaped by init asynchronously, so no test can assert it is gone the moment `Reaped` closes. The order is fixed by `process.read`'s structure. The test does catch the regression that matters: with `exited` not called, both cases fail.

## Checks run

- `make test` (lint, `go test -race ./...`, Node suites): passed.
- `make live` (`TestLive`, `TestLiveUr`, `approval.spec.ts`): passed.
- Removing the `exited()` call in `process.read`: `TestCrashEndsCommands` fails.
- Replacing the membership check in `session.update` with `true`: `TestCreateSession` fails ("the page heard session/update about child … before session/created").
- Restoring the old `addChild` order (publish, then first `Send`): `TestChildStartHoldsUpNoOtherSession` and `TestRefusedFirstMessageLeavesNoChild` fail.
- Hashing only `command` in `stack.mjs`: "a changed executable a service starts restarts it" fails.
- Every mutation was reverted from a copy, and the diff stat matched before and after.

## Verification 1

Checked the uncommitted tree over `1e7b9c1`. Since the review, only `internal/ur/ur_test.go` changed, adding `TestSendToGoneUr`. `make test` passed on the final tree. `make live` was not rerun: no code it exercises changed after it passed.

| ID | Status   | Evidence                                                                                                                                  |
| -- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `TestSendToGoneUr` launches a stand-in `ur` that exits, waits on `Reaped`, and checks `Send`'s error prefix. It fails with the wrap removed and passes with it (3 runs). |

No new findings.

## Verdict

`Ready to commit`. F1 is resolved and F2 is rejected.
