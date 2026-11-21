# Review: a child holds its first message before it is published

## Scope and coverage

Uncommitted changes on base `43a8b5f`, checked against
[the plan](../plans/2026-11-21-007-child-owns-first-message.md): `internal/server`
(`mcp.go`, `methods.go`, `server.go`, `session.go`, `client_test.go`,
`mcp_test.go`), `docs/architecture.md`, `docs/protocol.md`, and `docs/todo.md`.
The review traced `addSession` and `addChild` through the Claude, Ur, and fake
harnesses' `Start` and `Send`, and through `Close`, `archive`, and `destroy`.
`docs/functional-requirements.md` is unchanged, as the plan's Requirement changes
say.

Gaps: none.

## Summary

| ID | Severity | Category | Action   | Status   | Title                                                        |
| -- | -------- | -------- | -------- | -------- | ------------------------------------------------------------ |
| F1 | Low      | Testing  | Fix      | Resolved | No test guards holding `Server.mu` across the child's launch |
| F2 | Low      | Testing  | Rejected | —        | The shutdown refusal in `addChild` is untested               |

## F1. No test guards holding `Server.mu` across the child's launch

Low · Testing · Fix · `internal/server/mcp_test.go:585`

The plan holds `Server.mu` across the child's `start` so that `Close` cannot set
`closed` and snapshot the members between the shutdown check and publication.
`TestCreateSessionDuringShutdown` starts `Close` in a goroutine during the
launch, but it only asserts that the child was created and survives a restart.
Both hold even when `Close` misses the child, so the test passes with the lock
released during the launch. Under that regression, a child created as shutdown
begins keeps its goroutine and process after `Close` returns.

### Evidence

In a scratch worktree, `addSession` was changed to unlock `s.mu` around
`sess.start()`. `go test -race -count=5 -run TestCreateSessionDuringShutdown`
passed.

### Action

After `<-closed`, assert that `Close` ended the child:
`c.update(late.SessionID, "parked")`. In the scratch worktree, the test with
that line passed 20 of 20 runs on the change, and failed 5 of 10 runs with the
lock released during the launch, each time with `no matching message within 5s`.
It catches the regression whenever `Close` reaches the lock during the launch,
which the test cannot order. Update the test's comment to say it checks that
shutdown ends the child.

## F2. The shutdown refusal in `addChild` is untested

Low · Testing · Rejected · `internal/server/mcp.go:389`

Removing the `s.closed` check from `addChild` fails no test, since the rewritten
`TestCreateSessionDuringShutdown` no longer reaches it.

### Evidence

Read from the source; the old test reached the refusal only after a 100 ms sleep
put the call in the parent's queue ahead of `Close`.

### Action

Rejected. The plan removes that scenario on purpose: the call must reach the
parent's goroutine after `Close` sets `closed` and before `Close` ends the
parent, and no observable event orders it. The check is unchanged from the base
revision, where only the sleep reached it.

## Checks run

- `make test`: passed.
- `make live`: passed. `TestLive`, including "Claude starts a child session
  without asking", `TestLiveUr`, and the three `approval.spec.ts` tests passed.
  `make live` covers `create_session` with a real Claude child, so it was
  required for this change.
- The plan's tests on the base revision: the launch-failure part of
  `TestCreateSession` and the "not published while launching" assertion in
  `TestCreateSessionDuringShutdown` both observe `session/created` before
  `Start` on the base code, so both fail there.
- Removed conditions, each in a scratch worktree with `-race`:
  - The `DeleteSession` after a failed launch: `TestCreateSession` fails
    (`UNIQUE constraint failed: children.parent_id, children.name`).
  - `session/created` broadcast after `Send`: `TestCreateSessionDuringShutdown`
    fails (`heard session/update about the child before session/created`).
  - `go sess.run()` before `start`: the race detector fails the tests.
  - The failed-delete log: `TestFailedLaunchKeepsItsErrorWhenDeleteFails` fails.
  - `Server.mu` released during `start`: no test fails (F1).

## Verification 1

Checked the uncommitted tree on base `43a8b5f` after the F1 fix, which changes
only `internal/server/mcp_test.go`. `make test` passed. In a scratch worktree,
`TestCreateSessionDuringShutdown` passed 20 of 20 runs with `-race`, and failed
6 of 10 runs with `Server.mu` released around the child's `start`. `make live`
was not rerun: the fix changes a test only.

| ID | Status   | Evidence                                                                                                               |
| -- | -------- | ---------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `TestCreateSessionDuringShutdown` waits for the child's `parked` update after `Close`, and fails with the lock removed |

No new findings.

## Verdict

`Ready to commit`.
