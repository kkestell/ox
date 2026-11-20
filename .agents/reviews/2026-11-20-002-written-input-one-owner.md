# Review: the harness alone owns the written input

## Scope and coverage

Uncommitted changes on `main` at base `96d4e3c`, reviewed against
`.agents/plans/2026-11-20-002-written-input-one-owner.md`. The working tree
held the plan and changes to `docs/architecture.md`, `internal/agent/agent.go`,
`internal/claude/agent.go`, `internal/claude/prompt.go`,
`internal/claude/prompt_test.go`, `internal/fake/fake.go`,
`internal/server/mcp.go`, `internal/server/methods.go`,
`internal/server/methods_test.go`, and `internal/server/session.go`.

Traced: `session.deliver`, `sendPrompt`, `acceptMessage`, `cancelInbox`,
`agentUpdate`, `startForInput`, `parked`; `getSession` and `configureSession`;
`list_sessions` and `send_message` in `mcp.go`; `history.record`; the Claude
harness's `Send`, `Interrupt`, `queued`, `commandStarted`, `commandCancelled`,
`dropQueued`, `sessionState`, `exit`, `Configure`, `idle`, `Snapshot`; the fake
harness's `Send`, `take`, `drop`, `Interrupt`, `Configure`, `Stop`,
`Snapshot`.

The change delivers the plan's Goal: `session.written`, `pendingInput`,
`writeInput`, and `pending` are gone, the server has no copy of the written
input, and `agentUpdate` no longer dereferences one. Both harnesses build every
lifecycle update through `Input.Update`, so `prompt_queued`, `prompt`, and
`prompt_cancelled` carry the same fields as before, and `session/get` lists the
same `queued` array. The busy rule for a written input is decided by the
harnesses alone: Claude's `Send` and `Configure` refuse in the `prompting`
phase, the fake's while an input is queued or a turn runs. No requirement text
changed, as the plan says.

Gaps:

- Headless Chrome through `make dev` was not run: the change does not alter how
  the page reaches `oxd`; `make e2e` covers the page against the fake harness.

## Summary

| ID | Severity | Category      | Action   | Status | Title                                                     |
| -- | -------- | ------------- | -------- | ------ | --------------------------------------------------------- |
| F1 | Low      | Documentation | Fix      | Open   | `Agent.Send` contract still omits what the harness owns   |
| F2 | Low      | Testing       | Rejected | —      | No test reaches the inbox check in send and configure     |

## F1. `Agent.Send` contract still omits what the harness owns

Low · Documentation · Fix · `internal/agent/agent.go:24`

The plan's implementation step 1 says to update the `Send` doc: its lifecycle
updates carry the input's text and attribution, and `Snapshot` reports the
untaken input until then. The comment is unchanged. It still reads "Send
writes input's Content with its session-assigned ID. It publishes
prompt_queued after writing, then prompt or prompt_cancelled on uptake or
drop."

This is the contract a harness implements. Since the server no longer fills
in `Text`, `From`, and `FromLabel`, a harness written to this comment would
publish lifecycle updates without them and leave `Snapshot.Queued` nil, and
the page would show a pending prompt with no text and no sender, and lose it
from `session/get`.

The two paragraphs edited in `docs/architecture.md` (Sessions, lines 70–73,
and Agents, lines 89–92) are also left unwrapped, unlike the rest of the file.

### Evidence

`git diff -- internal/agent/agent.go` touches only `Snapshot`, `Input`, and
the new `Input.Update`; lines 24–26 match the base revision.

### Action

Update the `Send` comment to say the lifecycle updates carry the input's
`Text`, `From`, and `FromLabel` (built with `Input.Update`), and that
`Snapshot().Queued` reports the input from the write until it is taken or
dropped. Rewrap the two edited paragraphs in `docs/architecture.md`. No test:
the comment's behavior is already guarded by `TestPromptSentWhileClaudeWorks`,
`TestStopDropsQueuedPrompt`, and `TestPromptQueuedWhileClaudeWorks`.

## F2. No test reaches the inbox check in send and configure

Low · Testing · Rejected · `internal/server/session.go:108`

Replacing `messagesWaiting()` with `false` in `sendPrompt` or in
`configureSession` (`internal/server/methods.go:431`) fails no test. The check
implements the plan's Decision that waiting messages go ahead of a user prompt
and hold off a configuration, and One prompt at a time ("Send is unavailable
… while a message from another session is pending or waiting").

### Evidence

Mutation runs in a scratch worktree with the change applied; both survived
`go test ./internal/server`. Because `deliver` runs after every operation and
event, a non-empty inbox at the start of an operation means the agent refused
the last write as busy, and the harness's own `Send` and `Configure` refuse for
the same reason. The check decides the outcome only after a write to Claude
fails with an error other than `ErrBusy` while further messages wait; the base
revision had the same unprotected half in `inputPending`.

### Action

Rejected: the check is decisive only after a failed write to Claude's stdin
with more messages queued, a failure not observed in use. A test would need an
injected write fault the check does not earn, and the check is one line that
matches a requirement.

## Checks run

- `make test`: passed (lint, web build, `go test -race ./...`).
- `make e2e`: 35 passed, 3 skipped.
- `make live`: `TestLive` passed (169s) and the web `approval` e2e passed (3 tests).
- New tests against base `96d4e3c` in a detached worktree: the changed
  `prompt_test.go` and `methods_test.go` do not compile at base (`agent.Input`
  has no `Text`; no `Snapshot.Queued`). The added `session/configure` `-32002`
  assertion guards behavior the base server enforced itself; it now depends on
  the fake harness's `Configure` busy check, and removing that check fails
  `TestPromptQueuedWhileClaudeWorks`.
- Removed conditions, one at a time, each failing a test: fake `Snapshot`
  `Queued`; `getSession` prepending `snapshot.Queued`; the `agentUpdate`
  `prompt_queued`/`From` filter (`TestSendMessage`); text and attribution on the
  fake's `prompt_queued`, `prompt` (in `Send` and `take`), and
  `prompt_cancelled`; the Claude harness's `Snapshot` `Queued`, and text on its
  `prompt_queued`, `prompt`, and `prompt_cancelled` (`TestPromptSentWhileClaudeWorks`,
  `TestStopDropsQueuedPrompt`). The two `messagesWaiting` checks survived (F2).

## Verification 1

Working tree on base `96d4e3c` after the F1 fix. Since the review, only the
`Send` comment in `internal/agent/agent.go` and the two paragraphs in
`docs/architecture.md` changed; every other file's diff is unchanged.

| ID | Status   | Evidence                                                                                                                                                                                 |
| -- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F1 | Resolved | The `Send` comment now states that each update is built with `Input.Update` and carries `Text`, `From`, and `FromLabel`, and that `Snapshot().Queued` reports the input until taken or dropped; both `docs/architecture.md` paragraphs are wrapped. |

No new findings.

Checks: `go vet ./...` and `make test` passed; `make e2e` 35 passed, 3
skipped. `make live` was not rerun: the changes since its passing run are a
comment and documentation only.

## Verdict

Ready to commit.
