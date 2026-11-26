# Review: Run Ur as its own process

## Scope and coverage

Reviewed the uncommitted changes on branch `run-ur-as-process`, base
`7c933525d89f19aa0f677496c45fd3b3d0528cfa` (the merge base with `origin/main`,
which is also `HEAD`). The working tree holds the modified files in `git status`
and the untracked `cmd/ur`, `internal/process`, `internal/ur/agent.go`,
`internal/ur/ask.go`, `internal/ur/harness.go`, `internal/ur/engine/engine_test.go`,
and `internal/ur/wire`. Checked against
`agents/plans/2026-11-25-run-ur-as-process.md` (issue #132). `AGENTS.md` is
deliberately untouched; its edits land on `main` separately.

The requirements diff carries the plan's two changes, *Ur sessions* and
*Unexpected exits*, word for word. The change departs from the plan in three
places, each reported by the implementer and each keeping the approved
requirements:

- On a lost conversation `ur` reports `resumable ""` and keeps reading stdin
  until stopped, instead of exiting with status 1, so the send that started it
  never fails on a closed pipe.
- `Approve` writes `approve` before it removes the approval, so a failed write
  keeps the approval.
- `Configure` writes only when the settings change.

Open `bug` issues #33, #104, #108, #109, and #118 cover none of the code
reviewed here, and the change resolves none of them.

Gaps:

- None material. The concern reviewers ran probes against a built `ur` with a
  stand-in OpenRouter, and mutation runs of `go test ./internal/ur/...`.

## Summary

| ID | Severity | Category    | Action   | Status | Title                                                        |
| -- | -------- | ----------- | -------- | ------ | ------------------------------------------------------------ |
| F1 | High     | Correctness | Fix      | Resolved | Ur's shell commands run with SIGPIPE and SIGINT ignored      |
| F2 | Medium   | Process I/O | Fix      | Resolved | A crashed `ur` leaves its running command alive              |
| F3 | Medium   | Testing     | Fix      | Resolved | The engine's stale-event guards have no test                 |
| F4 | Low      | Settings    | Fix      | Resolved | A relative `-ur` puts conversation files in the workspace    |
| F5 | Low      | Testing     | Fix      | Resolved | Suppressing a cancel for an answered approval has no test    |
| F6 | Low      | Testing     | Fix      | Resolved | `ur_test.go` still records `OnError`, which nothing calls    |
| F7 | Low      | Simplicity  | Rejected | —      | `Send`, `Interrupt`, `Configure` check `exited`              |
| F8 | Low      | Conventions | Rejected | —      | `ur`'s flag is `-permission-mode`, not `permission_mode`     |
| F9 | Low      | Construction | Fix     | Resolved | The adapter kills process group 0 if `ur` names no group     |

## F1. Ur's shell commands run with SIGPIPE and SIGINT ignored

High · Correctness · Fix · `internal/ur/engine/engine.go:50`

`Main` calls `signal.Ignore(syscall.SIGINT, syscall.SIGPIPE)`. An ignored
signal stays ignored across fork and exec, and bash cannot reset a signal that
was ignored when it started, so every command `ur` runs, and everything it
starts, has both signals ignored. Before the change, `oxd` caught SIGINT with
`signal.Notify` and the Go runtime handled SIGPIPE, and handled signals reset
to their defaults in an exec'd child.

The effect is in everyday commands. A producer piped into `head` is no longer
killed when `head` exits: it prints `Broken pipe` errors and keeps running, and
an endless one runs until the command's timeout. Pipeline exit statuses change
(141 becomes 1 or 0). `kill -INT` no longer stops a command's children. This
breaks the plan's "Ur keeps its shell tool … as they are" and *Ur's shell
commands*.

### Evidence

- A Go program that calls the same `signal.Ignore` and runs
  `bash -c 'yes | head -1 >/dev/null; echo "pipestatus=${PIPESTATUS[*]}"'`
  prints `yes: stdout: Broken pipe` and `pipestatus=1 0`. Plain bash prints
  `pipestatus=141 0`.
- Two concern reviewers reproduced it through a built `ur` in auto mode: the
  tool result showed the broken-pipe errors, `PIPESTATUS` of 1 or 0, and
  Python's `signal.getsignal(SIGINT)` reporting ignored. `sleep 2 & kill -INT $!; wait $!`
  exits 0 after the full two seconds instead of 130.

### Action

Install handlers instead of ignoring: `signal.Notify` SIGINT and SIGPIPE onto a
channel `ur` never acts on. A handled SIGPIPE still makes a write to a closed
stdout return `EPIPE` instead of ending `ur`, and handled signals reset to
their defaults in each command.

Test: in `internal/ur/ur_test.go`, through the real `ur` that `TestMain` runs
(since `run` does not install the signal setup), an auto-mode command such as
`yes | head -1 >/dev/null; echo "${PIPESTATUS[0]}"; trap -p` whose tool result
shows `141` and no ignored traps. `TestShellAuto` can carry it if its canned
command is easy to swap; otherwise a new case beside it.

## F2. A crashed `ur` leaves its running command alive

Medium · Process I/O · Fix · `internal/ur/engine/shell.go:99`, `internal/ur/agent.go:344`

Each command runs in its own process group, and only `ur` kills those groups:
on a stopped turn, at the command's timeout, and in `stop()` on stdin EOF or
SIGTERM. When `ur` dies any other way (a panic, a race report under
`GORACE=halt_on_error=1` in `make dev`, `kill -9`, or the `SIGKILL` ten seconds
after an ignored `SIGTERM`), its command keeps running with no timeout.

The plan makes this crash a path the user recovers from: the session shows the
exit, parks, and the next send resumes in a new `ur`, while the old command is
still running in the same workspace. `Agent.Reaped` closes when `ur` is reaped,
so deleting the session removes its worktree under the live command and
`children.WaitAll` stops waiting for it. This breaks *Ur's shell commands*
("A command ends when it exits, at its timeout …") and *Deleting a session*
("waits for it to exit") on the path the change exists to make survivable. The
implementer listed it as an open choice.

### Evidence

Two concern reviewers reproduced it with a built `ur` and a stand-in OpenRouter
that returns one auto-mode call (`sleep 97` with `timeout_seconds: 5`, and
`echo $$ > pid; sleep 30`). After `kill -9` of `ur`, the transcript showed
`Ur exited: signal: killed`, `Reaped()` was closed, and the command was still
alive seven seconds later, past its five-second timeout.

### Action

Make the command groups die with `ur`. `oxd` cannot find them today, so `ur`
must tell it: `ur` reports each command's process group when it starts it and
again once it has killed and reaped it (a `command` event with the group ID and
its end, or fields on the existing `tool_use` and `tool_result` updates), the
adapter keeps the live groups, and on `process.Exit` it kills each one left
before it ends the agent. The adapter clears the set on each report and at the
end. The commit body names the behavior, the state, and its cleanup, as
`AGENTS.md` requires. A different design is fine if it leaves no command
running after `ur` exits and before the session parks.

Test: in `internal/ur/ur_test.go`, an auto-mode command
`echo $PPID > ur.pid; echo $$ > pid; sleep 60`; once both files exist, send
`SIGKILL` to the PID in `ur.pid`, wait for `exited`, and check that the PID in
`pid` is gone within a bound.

## F3. The engine's stale-event guards have no test

Medium · Testing · Fix · `internal/ur/engine/engine.go:388`, `:406`, `:426`

`handle` drops a `delta`, `ended`, or `ran` that belongs to a turn no longer
running. Without these checks, a delta from an interrupted request would be
saved into the next turn's conversation file, and a killed command's outcome
would be answered into the next turn, corrupting what Ur sends OpenRouter. The
base tests that reached these cases lost them in this change: `TestInterrupt`
no longer sends a chunk mid-interrupt, and `TestShellInterrupt`'s executing
case no longer waits for the killed command's late result. In the remaining
tests the stale event arrives while no turn runs, so the `e.turn == nil` half
of each check is enough.

### Evidence

The construction reviewer replaced each check with a bare `e.turn == nil`
test, one at a time, and ran `go test ./internal/ur/...` three times for each.
Every run passed.

### Action

Add engine-level tests in `internal/ur/engine/engine_test.go`, which already
drives `run` in-process on pipes with a canned OpenRouter, where the stale
event provably arrives during the next turn: hold the first request's stream
open, interrupt, send a second input, then release the first stream's late
chunk and check that the second turn's file and updates do not contain it.
Do the same for a killed command whose outcome lands during the next turn. One
test per guard; each must fail with its guard reduced to the nil check.

## F4. A relative `-ur` puts conversation files in the workspace

Low · Settings · Fix · `cmd/oxd/main.go:69`, `internal/ur/agent.go:52`

`oxd` passes `-ur` to the harness as given, and `ur` now runs with its working
directory set to the session's workspace, so a relative `-ur` resolves there.
Conversation files then land in the user's repository or worktree and show as
changes. `-socket` and `-worktrees` already go through `absolute()` for the
same reason. The default and every script pass absolute paths, so only a
user-supplied relative flag reaches it.

### Evidence

The boundaries reviewer built a harness with dir `urdata` after changing the
test's working directory to a temporary one. After one turn, `<cwd>/urdata`
held no files and `<workspace>/urdata` held the conversation.

### Action

Make the path absolute when `ur.Harness` receives it, relative to `oxd`'s
working directory, so the harness's boundary owns it. Keep the reviewer's probe
as a test in `internal/ur/ur_test.go`: a harness with a relative dir writes its
conversation under the test's working directory, not the workspace.

## F5. Suppressing a cancel for an answered approval has no test

Low · Testing · Fix · `internal/ur/agent.go:185`

When `ur` cancels an approval the adapter no longer holds, because the user
answered it meanwhile, the adapter publishes nothing. That happens when an
interrupt and an answer cross: `Interrupt` writes `interrupt`, the page's
`Approve` follows before `ur` reads it, and `ur` cancels an approval already
answered. Without the guard the transcript shows the answered approval as
cancelled.

### Evidence

The construction reviewer made the adapter publish the cancel anyway, and
`go test ./internal/ur/` passed.

### Action

Add a test in `internal/ur/ur_test.go` beside the manual-mode shell tests:
with an approval pending, call `Interrupt` and then `Approve` from the test
goroutine before any event is handled, and check that no `approval_cancelled`
for that request is published after `approval_answered`. Both writes reach
`ur` in that order, so the test is deterministic.

## F6. `ur_test.go` still records `OnError`, which nothing calls

Low · Testing · Fix · `internal/ur/ur_test.go:88`, `:128`

The adapter never calls `OnError`, and since `TestResumeLost` stopped reading
`run.errors`, no test reads it either. The field and its callback are dead test
setup that suggests a behavior the harness no longer has.

### Evidence

`grep -n OnError internal/ur/*.go` finds only the test's callback, and no test
reads `r.errors`.

### Action

Remove the `errors` field and the `OnError` callback from the test's `run`
helper.

## F7. `Send`, `Interrupt`, `Configure` check `exited`

Low · Simplicity · Rejected · `internal/ur/agent.go:216`, `:255`, `:275`

The session parks an agent as soon as `Exited()` is true and never calls it
again, so these checks are unreachable from the session.

### Action

Rejected: `agent.ErrExited` is the interface's answer for a call to an ended
agent, and Claude's `Send` makes the same check (`internal/claude/prompt.go:47`).
The checks cost three lines and keep the two harnesses to one contract.

## F8. `ur`'s flag is `-permission-mode`, not `permission_mode`

Low · Conventions · Rejected · `internal/ur/engine/engine.go:62`, `internal/ur/agent.go:52`

### Action

Rejected: the plan settled the command line, and the one-name rule covers
configuration, protocol, store, Go, and the web client. A command-line flag
follows Go's hyphenated flag style, as `oxd`'s `-park-after` does.

## F9. The adapter kills process group 0 if `ur` names no group

Low · Construction · Fix · `internal/ur/agent.go:168`, `:118`

The fix for F2 adds `command_started` and `command_ended`, whose `group` the
adapter stores as `ur` sends it. A `command_started` without `group` decodes to
0 (the field is `omitempty`), and on `ur`'s exit the adapter runs
`syscall.Kill(-0, SIGKILL)`, which is `kill(0, …)`: it kills `oxd`'s own
process group, which in `make dev` includes `oxd`, `ox`, and the dev script. A
negative group would kill a single unrelated process. The adapter already
checks every other field of `ur`'s events at this boundary (state values,
update types, the order of `taken` and `dropped`); `group` is the one it
trusts, and it is the one that reaches `kill`.

### Evidence

Traced: `wire.Event.Group` is `int` with `omitempty`, `handle` appends
`e.Group` without a check (`agent.go:169`), and `Handle` passes `-group` to
`syscall.Kill` (`agent.go:119`). `kill(2)` with pid 0 signals the caller's
process group.

### Action

Treat a `command_started` whose `group` is not positive as a protocol error in
`handle`, so only a real group reaches the stored list. Test: a case in
`TestUnexpectedExit`'s protocol-error table where the stand-in prints
`{"type":"command_started"}`, and the agent ends with the protocol reason.

## Checks run

- From the implementer's report: `make test`, `make e2e` (203 passed, 5
  skipped), `make live`, and a `make dev` check in headless Chrome all passed
  on the final tree. Each check `AGENTS.md` ("Verification") requires for this
  change ran.
- The implementer's Tests proved list (`TestResumeLost`, `TestUnexpectedExit`,
  `TestInterrupt`, `TestKillAllEndsCommands`, `TestEndOfInputEndsCommands`,
  `TestRefusesToStartWithoutUr`) was not re-run by mutation. The spot checks
  below cover the adapter and engine.
- `go test -race -count=1 ./internal/ur/... ./internal/process/... ./internal/claude/...`
  passes (lifecycle and boundaries reviewers).
- Spot-checked removals, each restored:
  - `Stop`'s `stopped` reason: a test fails.
  - `end`'s fallback to the latest untaken input: a test fails.
  - `ur`'s SIGTERM handler: `TestKillAllEndsCommands` fails.
  - The out-of-order `taken` check: `TestUnexpectedExit` fails.
  - The engine's three stale-event guards: no test fails (F3).
  - The answered-approval cancel guard: no test fails (F5).
  - The `exited` checks in `Interrupt` and `Configure`: no test fails (F7).
- Probes with a built `ur` and a stand-in OpenRouter: F1, F2, F4.
- A standalone Go program confirming that `signal.Ignore` reaches `bash -c`
  children: F1.

## Verification 1

Checked the uncommitted working tree on base `7c93352` after `/ox-work fix`.
Each fix was reverted on its own, its test run, and the file restored.

| ID | Status   | Evidence                                                                                                                                                       |
| -- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | `Main` uses `signal.Notify`. `TestShellSignals` expects `pipe=141` and `int=130`; with `signal.Ignore` restored it fails with `pipe=1`, `int=0`.               |
| F2 | Resolved | `ur` reports each group on `command_started` and `command_ended`; the adapter kills the rest on `process.Exit`. `TestCrashEndsCommands` fails without the kill. |
| F3 | Resolved | `TestStaleOutput` in `engine_test.go` fails with each of the three guards reduced to `e.turn == nil`. It drives `handle` directly, the one layer where a stale event's arrival during the next turn is deterministic. |
| F4 | Resolved | `startAgent` makes `dir` absolute. `TestRelativeConversationDirectory` fails without it.                                                                         |
| F5 | Resolved | `TestAnswerCrossesInterrupt` fails when the adapter publishes the cancel for an answered approval.                                                              |
| F6 | Resolved | `ur_test.go` no longer has the `errors` field or the `OnError` callback.                                                                                         |

New findings: F9.

Checks run: `go test -count=1 ./internal/ur/...` and `go vet` on the changed
packages pass; each revert above failed its test and was restored. The
implementer reports `make test` and `TestLiveUr` passing after the fixes. The
fixes change no page behavior, so `make e2e` from the first round still holds.

## Verification 2

Checked the uncommitted working tree on base `7c93352` after the fix for F9.

| ID | Status   | Evidence                                                                                                                                                  |
| -- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F9 | Resolved | `handle` refuses a `command_started` whose group is not positive. `TestUnexpectedExit/command_without_a_group` fails with the check removed and passes with it. |

New findings: none. The fix touches only the adapter's `command_started` case
and its test.

Checks run: `go test -count=1 ./internal/ur/... ./internal/process/...` and
`go vet ./internal/ur/...` pass; the check's removal failed its test and was
restored. The implementer reports `make test` passing.

## Verdict

Ready to commit. F1 through F6 and F9 are resolved; F7 and F8 are rejected.
