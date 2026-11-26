# Review: Show a tool Stop ended as stopped, and keep Claude's stderr in a startup timeout

## Scope and coverage

Reviewed the uncommitted changes on branch `claude-stopped-tools-and-startup-stderr` against base `5948152` (`git merge-base origin/main HEAD`, also `HEAD`): `internal/claude/agent.go`, `internal/claude/prompt.go`, `internal/claude/claude_test.go`, `internal/claude/prompt_test.go`, `internal/server/live_test.go`, `docs/architecture.md`, and `docs/protocol.md`. The plan is `agents/plans/2026-11-26-claude-stopped-tools-and-startup-stderr.md`, which resolves #109 (sweep findings F5 and F12). The plan makes no requirement changes, and `docs/functional-requirements.md` is unchanged.

I traced the interrupt path (`Interrupt`, `a.stops`, `settleStops` at `idle`, the next input taken, and `end`), the page's `stopTools` rule in `cmd/ox/web/src/ox.tsx`, the new `startTimedOut` phase through every phase switch (`Handle`, `exit`, both switches in `end`, `Send`, `Interrupt`), and how `process.read` delivers `Exit.Stderr` under its read deadline.

Gaps:

- The request did not pass on `/ox-work`'s Tests proved and Checks, so I ran the checks and the removals myself (see Checks run).

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                          |
| -- | -------- | ------------- | -------- | ------ | -------------------------------------------------------------- |
| F1 | Low      | Testing       | Fix      | Resolved | Live Bash stop case waits a fixed time for the approval        |
| F2 | Low      | Simplicity    | Fix      | Resolved | Startup failure test skips events after the end that never come |
| F3 | Low      | Documentation | Rejected | —      | `starting` row still lists the startup limit                   |

## F1. Live Bash stop case waits a fixed time for the approval

Low · Testing · Fix · `internal/server/live_test.go:626`

The new live case sleeps two seconds, then approves any approval it has received by then. The session runs in `manual` mode, so Claude always asks before running `ping`. If the approval arrives after the two seconds, the test never approves it. Stop then lands while the call waits for approval, not while the command runs. The test still passes, so it would stop covering the case it names (a running Bash call that Stop interrupts) without anyone noticing.

### Evidence

From the source: `time.Sleep(2 * time.Second)` and a scan of `l.c.received(b)` are the only things that wait for the approval. A temporary probe logging the updates before Stop showed that in two runs the approval arrived inside the window: `requires_action`, `approval`, `approval_answered`, `running`, then `result` and `idle` after Stop. So the case passes today, but only because the approval happens to arrive in time.

### Action

Wait for the approval with `l.c.update(b, "approval")`, approve it with `allow`, then keep the short pause that lets the command start. Remove the loop over `received` that approves whatever has arrived. The existing check that no `tool_result` arrived before Stop stays.

## F2. Startup failure test skips events after the end that never come

Low · Simplicity · Fix · `internal/claude/claude_test.go:119`

`TestStartupFailureKeepsClaudesReason` now handles each event only `if !a.Exited()`, with the comment "An ended agent's events are not delivered". No case sends an event after the agent has ended, so the guard never skips anything. A reader would assume some case needs it.

### Evidence

With the guard removed and `a.Handle(e)` called on every event, `go test -run TestStartupFailureKeepsClaudesReason ./internal/claude/` passes.

### Action

Restore the plain `a.Handle(e)` loop and delete the comment.

## F3. `starting` row still lists the startup limit

Low · Documentation · Rejected · `docs/architecture.md:348`

The plan said to remove "startup limit" from the `starting` row's "Leaves on" column. The implementation kept it.

### Action

Rejected. The startup limit does move the agent out of `starting`, into `startTimedOut`, as the diagram shows. The row is correct as written.

## Checks run

- `make test`: passed (Go with the race detector, and the Node suites).
- `golangci-lint run ./internal/claude/...` (includes `gochecksumtype`): 0 issues.
- `make docs`: no formatting changes.
- `OX_LIVE=1 go test -race -run 'TestLive/interrupt$' ./internal/server`: passed. A temporary probe confirmed that the new case stops a Bash call that is running after its approval, and that no `tool_result` arrives for it. The probe was reverted.
- Removals, each reverted:
  - The `r.IsError && len(a.stops) > 0` guard disabled: `TestTopLevelToolCallsArePublished` fails ("the stopped call got a tool_result").
  - `exit.Stderr` left out of the `startTimedOut` reason: `TestStartupFailureKeepsClaudesReason/initialize_response_after_startup_timeout` and `TestStartupLimit` fail.
  - `a.proc.Terminate(killAfter)` removed from the startup timeout: `TestStartupLimit` fails ("the start did not end within 5s").
  - The `!a.Exited()` guard in the test removed: the test still passes (F2).
- `make e2e` not run: the page's code is unchanged and the page already shows a call with no result as stopped.

## Verification 1

Checked the uncommitted tree on base `5948152` after the fixes. Since the review, only `internal/claude/claude_test.go` and `internal/server/live_test.go` changed. Checks: `make test` passed, `go vet ./internal/...` was clean, and `OX_LIVE=1 go test -race -run 'TestLive/interrupt$' ./internal/server` passed.

| ID | Status   | Evidence                                                                                                                                                            |
| -- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | The live case now waits for the approval with `l.c.update(b, "approval")`, approves it, and then pauses. The loop that approved whatever had arrived is gone. The live interrupt section passes. |
| F2 | Resolved | The `!a.Exited()` guard and its comment are gone, and `TestStartupFailureKeepsClaudesReason` passes with the plain `a.Handle(e)` loop.                                 |

No new findings.

## Verdict

Ready to commit. F1 and F2 are resolved, and F3 was rejected.
