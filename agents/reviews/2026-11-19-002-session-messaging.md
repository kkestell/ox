# Review: messaging between sessions

## Scope and coverage

The uncommitted changes on `main` over base `ecb15ff` (24 modified files, no
untracked files), against `agents/plans/2026-11-19-001-session-messaging.md`.
The user took the plan's recommendation on both "Check your assumptions"
questions and approved its Requirement changes word for word.
`docs/functional-requirements.md` carries exactly those changes.

The implementer reported three deviations. F1 judges the untested inbox clear
on a process exiting by itself, F5 the plan-mode live case, and F6 the fake's
queued input.

Gaps:

- New tests were not run against the base revision. Each Go test uses
  `agent.Input` or the `attach` helper and does not compile there, and at the
  base `send_message` is an unknown tool (`-32602`), so the e2e helper rejects.

## Summary

| ID | Severity | Category | Action   | Status | Title                                                    |
| -- | -------- | -------- | -------- | ------ | -------------------------------------------------------- |
| F1 | Medium   | Testing  | Fix      | Resolved | No test keeps a crashed Claude from restarting for mail  |
| F2 | Medium   | Testing  | Fix      | Resolved | No test keeps a failed start from retrying               |
| F3 | Low      | Testing  | Rejected | —      | The fake's echo of a taken input's content is untested   |
| F4 | Low      | Testing  | Rejected | —      | `deliver`'s `ended` check is untested                    |
| F5 | Low      | Testing  | Rejected | —      | The plan-mode live case passes when Haiku makes no call  |
| F6 | Low      | Intent   | Rejected | —      | The fake keeps its queued input in its own struct        |

## F1. No test keeps a crashed Claude from restarting for mail

Medium · Testing · Fix · Resolved · `internal/server/session.go:174`

`parked()` clears the inbox. That clear is the only thing that keeps a Claude
process that exits by itself, with messages waiting, from being started again
to deliver them: after `parked()` sets `agent` to nil, `deliver` runs in the
same loop iteration and would call `start()` for the next message.
**Message delivery** says messages Claude has not taken are dropped when
Claude exits. No test fails when the clear is removed. The implementer left
it untested because the fake cannot crash, but the server tests already drive
stand-in `claude` scripts (`TestResumeTokenKeptWhenNotStored`).

### Evidence

Removing `sess.inbox = nil` from `parked()` leaves `make test` passing. A probe
test with a stand-in `claude` that answers `initialize`, reads one line, and
exits, given two messages from an attached connection, records one start with
the clear and a second start without it.

### Action

Add a test to `internal/server/methods_test.go`, in the style of
`TestResumeTokenKeptWhenNotStored`: a stand-in `claude` that records its
arguments, answers `initialize`, reads the first message, and exits. Store two
`claude` sessions, attach to one, and send the other two messages. The first
stays queued in the harness because the script never reports
`command_lifecycle` started, and the second waits in the inbox. Wait for
`parked`, run one more operation on the recipient (`session/get`), and assert
that the arguments file holds one start and that no `prompt` names the second
message.

## F2. No test keeps a failed start from retrying

Medium · Testing · Fix · Resolved · `internal/server/session.go:93`

When `deliver` cannot start a parked session, it clears the inbox and records
one `exited` notice. Without the clear, every later operation or event on the
session would retry the start and record another `exited`. When the
conversation was lost, the retry after `forgetConversation` would start a new
conversation and deliver the message that **Message delivery** says is
dropped. `TestSendMessage` waits only for the first `exited`, so it passes
with the clear removed.

### Evidence

Removing `sess.inbox = nil` from the failed-start branch of `deliver` leaves
`make test` passing.

### Action

In `TestSendMessage`, "a failed start drops the messages": after the `exited`
update for D, run another operation on D (`session/get`), and assert that its
history holds exactly one `exited` update.

## F3. The fake's echo of a taken input's content is untested

Low · Testing · Rejected · `internal/fake/fake.go:168`

Changing `take` to echo `Text` instead of `Content` fails no test, because
`TestSendMessage` checks the wrapped reply only on the idle path.

### Action

Rejected: the fake is test infrastructure, and its echo exists only so tests
can see the model's input. The Claude harness's write of `Content` for a
queued message is covered by `TestPromptSentWhileClaudeWorks`. A further
assertion costs more than it protects.

## F4. `deliver`'s `ended` check is untested

Low · Testing · Rejected · `internal/server/session.go:88`

Removing `sess.ended ||` from `deliver` fails no test.

### Action

Rejected: every path that ends a session clears the inbox through `parked()`
first, except `end()` after a failed `Stop`. There the check keeps `deliver`
from writing to an agent `oxd` is abandoning. The plan decides it, it costs
one condition, and a test would need an injected `Stop` failure.

## F5. The plan-mode live case passes when Haiku makes no call

Low · Testing · Rejected · `internal/server/live_test.go:373`

In plan mode Haiku declined to call `send_message` in every run, including
this review's run of `make live`. The case accepts either an approval before
delivery or no call and no delivery, so it can pass without seeing plan mode
ask. **Claude can message other sessions** says plan mode asks for each
message.

### Action

Rejected: the refusal comes from the model, outside Ox. The behavior Ox
relies on is recorded in [Persistent session](../../docs/claude-code.md#persistent-session):
plan mode asks for an `--allowedTools` MCP tool without `readOnlyHint`. Ox's
part, `send_message` without `readOnlyHint`, is held by `TestForwarder`. The
case still fails if `send_message` runs without asking, and checks the
delivery when Haiku does call it. The model behavior is recorded in
`docs/claude-code.md`.

## F6. The fake keeps its queued input in its own struct

Low · Intent · Rejected · `internal/fake/fake.go:30`

The plan has the fake keep a `*agent.Prompt` and its `Content` apart. The fake
keeps one `queued` value holding the message ID and the whole `agent.Input`,
and derives the `agent.Prompt` from it for the snapshot.

### Action

Rejected: one value that exists only while an input is queued is the
"correct by construction" form of the plan's intent. It cannot hold a
`Content` without its prompt, and its updates and snapshot match the Claude
harness's.

## Checks run

- `make test`: passed (lint, web build, `go test -race ./...`).
- `make e2e`: 25 passed, 3 skipped (the live-only specs), including both new
  messaging specs.
- `make live`: passed (`TestLive` and the 3 `approval.spec.ts` tests). Haiku declined to call `send_message` in plan mode (F5).
- Removed conditions, one at a time, in a scratch worktree, running
  `go test -race` on `internal/server`, `cmd/ox-mcp`, and `internal/claude`:
  - `inbox = nil` in `interruptSession`: `TestSendMessage` fails.
  - the caller filter in `listSessions`: `TestSendMessage` fails.
  - the self-message refusal: `TestSendMessage` fails.
  - the `from IS NULL` filter in `FirstPrompt`: `TestSendMessage` fails.
  - `TrimSpace` on `text` in `sendMessage`: `TestSendMessage` fails.
  - `inbox = nil` in `parked()`: nothing fails (F1).
  - `inbox = nil` after a failed start in `deliver`: nothing fails (F2).
  - `sess.ended` in `deliver`: nothing fails (F4).
  - the fake's `take` echoing `Content`: nothing fails (F3).
- Probe for F1: a stand-in `claude` that exits after its first message starts
  once with the `parked()` clear and twice without it.
- Headless Chrome through `make dev` was not needed: the page sends no new
  requests to `oxd`. The new rendering is covered by the e2e specs.

## Verification 1

Checked the working tree over `ecb15ff`. Since the review only
`internal/server/methods_test.go` changed: it adds `TestExitDropsWaitingMessages`
and extends the failed-start case of `TestSendMessage`.

| ID | Status   | Evidence |
| -- | -------- | -------- |
| F1 | Resolved | `TestExitDropsWaitingMessages` queues one message in a stand-in `claude`, leaves a second waiting, and has the process exit. It then asserts that Claude started once and that the second message never appeared. With `inbox = nil` removed from `parked()`, it fails with two recorded starts. |
| F2 | Resolved | `TestSendMessage` runs another operation on D after the failed start and asserts that D's history holds one `exited`. With the failed-start `inbox = nil` removed from `deliver`, it fails. |

No new findings. The new tests do not compile at the base revision, which
lacks `send_message` and the `attach` helper.

Checks run:

- `make test`: passed.
- `make e2e`: 25 passed, 3 skipped (the live-only specs).
- `TestExitDropsWaitingMessages` and `TestSendMessage` with `-count=3`: passed.
- Each removed condition above, in a scratch worktree that was then removed.
- `make live` was not rerun. Only server tests with the fake and stand-in
  harnesses changed, so how Ox drives Claude Code is unchanged since the run
  recorded above.

## Verdict

Ready to commit. F1 and F2 are resolved, and F3 through F6 are rejected.
