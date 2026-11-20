# Review: starting and stopping Claude without holding the session

## Scope and coverage

Reviewed the uncommitted changes to the 19 tracked files under `cmd/oxd`,
`docs`, and `internal` on base `2ce2769`, against
`agents/plans/2026-11-20-005-start-stop-off-session-goroutine.md` (audit
finding F1), with the plan's settled product answer: a send to a parked
session replies at once, and a failed start drops the prompt and returns its
text to the composer. The plan's two requirement changes, **Failed startup**
and **Lost conversation**, appear in `docs/functional-requirements.md` word
for word, and no other requirement changed.

Traced `claude.Start`, the `starting` phase through `Send`, `Interrupt`,
`Configure`, `Parkable`, `Snapshot`, `Handle`, `Stop`, and `end`; the single
stdout reader and `deliverOutput`'s discard after `done`; `Terminate` and
`WaitAll`; the session's `startForInput`, `stop`, `parked`, `deliver`, the park
timer, and `destroySession`; `cmd/oxd`'s shutdown; and the web client's
`finishPrompt` and `reconcilePrompt`, which return a dropped prompt's text
unchanged.

Gaps:

- Whether Claude can report a self-started turn as `running` before its
  `initialize` response, and still be running when that response arrives, is
  not reproduced. If it can, `started` publishes `idle` while Claude runs. The
  base discarded all such output, so this is no regression, and it is not
  raised as a finding.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                           |
| -- | -------- | ------------- | -------- | ------ | --------------------------------------------------------------- |
| F1 | Low      | Testing       | Fix      | Resolved | Live resume step does not check the overlap it documents        |
| F2 | Low      | Documentation | Fix      | Resolved | `session.queued` comment still says the agent's input is written |
| F3 | Low      | Testing       | Rejected | —      | `Terminate`'s immediate kill has no test                        |

## F1. Live resume step does not check the overlap it documents

Low · Testing · Fix · Resolved · `internal/server/live_test.go:248`

The new `TestLive` step "a conversation resumes at once after its process is
stopped" backs the new fact in `docs/claude-code.md` (Stopping): a new process
can `--resume` a conversation while the SIGTERMed process is still exiting.
The step computes `exiting` with `pgrep` but only logs it. A run in which the
first process had already exited when the second started passes, so the step
can pass without checking the case it claims to test.

### Evidence

`internal/server/live_test.go:248` sets `exiting`; line 258 passes it to
`t.Logf` only. In this review's `make live` run it logged `true`, so the
documented fact holds for Claude Code 2.1.329, but nothing would fail if it
did not.

### Action

Fail the step when `exiting` is false (for example `t.Errorf("the stopped
process had exited before the next one started")`), so the step fails unless
the overlap it documents occurred. The existing step carries it.

## F2. `session.queued` comment still says the agent's input is written

Low · Documentation · Fix · Resolved · `internal/server/session.go:113`

The comment on `session.queued` reads "the one written to the harness, then
the inbox". A starting agent's `Snapshot().Queued` now also reports an input it
holds and has not written, as `docs/protocol.md` (`session/get`, `queued`)
already says.

### Evidence

`internal/claude/prompt.go` `queued` returns `starting.input`;
`internal/server/session.go:113` describes only the written input.

### Action

Reword to "the one the agent holds or has written, then the inbox, oldest
first". No test.

## F3. `Terminate`'s immediate kill has no test

Low · Testing · Rejected · `internal/claude/claude.go:331`

Removing the branch that kills Claude at once when SIGTERM fails with an
error other than `os.ErrProcessDone` leaves every test passing.

### Evidence

Mutation run: deleting `case !errors.Is(err, os.ErrProcessDone): p.cmd.Process.Kill()`
survived `go test ./internal/claude ./internal/server`.

### Action

Rejected. The plan's **Stop** decision specifies this branch. Reaching it
needs a signal failure other than a finished process, which Ox cannot cause
for its own unreaped child. A test would have to inject that failure, which
costs more than the two lines it protects.

## Checks run

- `make test`: passed.
- `make e2e`: 35 passed, 3 skipped.
- `make live`: `TestLive` passed (149 s), including the lost-conversation and
  misspelled-mode steps through the server and the resume-while-exiting step
  (logged `true`); the web client's approval specs passed (3).
- Headless Chrome through `make dev`: not applicable. The change alters no web
  client code and nothing in how the page reaches `oxd`.
- `TestStartAndStopHoldUpNoOtherSession` at base `2ce2769` (in a detached
  worktree, since removed): fails, because the send to the starting session
  does not reply. The other new tests use interfaces the change adds
  (`Stop()` without a result, `WaitAll`, the `starting` phase,
  `deliverOutput`'s `done`), so they cannot run at base.
- Removed conditions, one at a time, in a scratch worktree (since removed):
  `ErrBusy` for a second held input, `Interrupt` in `starting`, ignoring
  Claude's `idle` while starting, plain text clearing `noConversation`, giving
  up on a success after `noConversation`, `OnResumable("")`, the stderr reason,
  `Claude was stopped`, dropping the held input at the end, `deliverOutput`'s
  `done` case, `WaitAll` in `cmd/oxd`, `started` publishing `idle`, dropping a
  held input whose write fails, `WaitAll`'s wait, the startup limit, `giveUp`'s
  `Terminate`, `queued` reporting the held input, `Stop`'s `Terminate`, the
  unreported `timedOut` exit, and the `exited` naming the stopped prompt. Each
  made a test fail, except `Terminate`'s immediate kill (F3).

## Verification 1

Checked the working tree on base `2ce2769` after `/ox-work fix`. Since the
review, only `internal/server/live_test.go` and `internal/server/session.go`
changed. Checks: `make test` passed; `make e2e` 35 passed, 3 skipped;
`make live` `TestLive` passed (145 s) with the new assertion, and the approval
specs passed (3).

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `live_test.go:259` fails the step with "the stopped process had exited before the next one started" when `pgrep` finds no stopped process; it passed in this run, so the overlap occurred. |
| F2 | Resolved | `session.go:113` now reads "the one the agent holds or has written, then the inbox, oldest first". |

No new findings.

## Verdict

Ready to commit. F1 and F2 are resolved; F3 is rejected.
