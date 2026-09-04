# Recover Pending Permissions Across Restart

## Sources

- `eng/roadmap.md#pending-permission-recovery` — owns the recovery scope and
  process-boundary gates.
- `eng/architecture.md#session-and-turn-state` — owns load-time continuation,
  stale-generation handling, and the durable-only recovered stop reason.
- `internal/agent/agent.go`, `internal/agent/loop.go`, and
  `internal/agent/state.go` — current prompt lifecycle, serialized approval
  phase, durable fold, checkpoint, and ACP replay paths.
- `~/src/references/repos/personal/gamma/internal/agent/state.go:approvalRec`,
  `runner.go:awaitApproval`, `engine.go:Subscribe` and `Approval`, and
  `engine_test.go:TestRestartReissuesPendingAndResumes` and
  `TestCancelIgnoresLateApproval` — prior art for durable pending requests,
  recorded answers, stable identities, new callback generations, resumed
  runners, and ignored late answers.
- `~/src/references/repos/third-party/protocol/agent-client-protocol/agent-client-protocol-schema/src/v1/client.rs:RequestPermissionRequest`
  and `agent.rs:LoadSessionResponse` and `PromptResponse` — the ACP v1 callback
  and response shapes that constrain recovery.

## Goal

Keep a turn suspended on `session/request_permission` recoverable after Ox is
killed. Loading the session replays and reissues the pending request, consumes
one current-generation decision, and continues the same turn without another
provider request or duplicate tool execution.

## Implementation

- `internal/agent/state.go` — add records and folded state for a suspended
  tool-bearing model exchange. Persist its turn and message identities, provider
  completion, request count, ordered tool calls, decisions already made, pending
  permission request, and generation before sending the callback. Require one
  pending request at most, require responses to match its tool-call identity and
  generation, and clear the suspended state only when the complete exchange or
  terminal cancellation is committed. Keep open-turn recovery out of checkpoints
  and reject malformed record sequences.
- `internal/agent/loop.go` — separate the approval phase from tool dispatch so
  both a live prompt and load-time recovery drive the same resumable exchange.
  Preserve the existing rule that all required approvals finish before any tool
  runs. Record each decision before advancing, retain earlier decisions across
  restart, execute allowed calls once, record rejected calls once, then commit
  the complete exchange and continue at the next model-request count. A
  cancelled permission ends the recovered turn as cancelled.
- `internal/agent/state.go` and the ACP event adapter — replay the suspended
  assistant content and pending tool-call update once before reissuing the
  callback. When recovery advances, publish only the new tool outcomes, usage,
  and later model output so the client does not see duplicated updates.
- `internal/agent/agent.go` — stop converting an open turn with a pending
  permission to `interrupted`. During `session/load`, replay first, increment
  and persist the callback generation, reissue the permission with the original
  tool-call identity, and wait for the shared loop to reach a durable terminal
  state before returning. Use the turn's recorded request configuration during
  recovery; persist any activation configuration change only after it closes. Do
  not add a private stop-reason field to the load response.
- `internal/e2e/harness_test.go` — add a focused hard-kill operation that does
  not treat the intentionally lost prompt response as a harness failure.
- `eng/roadmap.md` — collapse the completed durable-session milestone after its
  cumulative completeness and simplification review passes, keeping ACP method
  coverage synchronized.

## Tests

- `internal/agent/state_test.go` — fold suspended exchanges and permission
  generations through restart; reject mismatched, stale, duplicated, and
  out-of-order decisions; prove checkpoints and replay preserve the complete
  transcript without duplicating the suspended updates.
- `internal/agent/approval_test.go` and `integration/agent_loop_test.go` — prove
  earlier batch decisions survive suspension, stale generations do nothing,
  allow executes once and continues the model loop, rejection records one failed
  tool result, cancellation closes the turn replayably, and recovery retains the
  original request count and frozen configuration.
- `internal/e2e/session_test.go` — kill the real Ox process while permission is
  outstanding, start a new process, begin `session/load`, assert the same
  tool-call identity is reissued before load completes, and cover allow, reject,
  and cancel. Verify one tool result, no duplicate provider request or mutation,
  complete replay after another restart, and no non-ACP stdout.

## Decisions

- Port Gamma's pending-request and generation invariants, not its Coral
  subscription protocol, elicitation support, activity tree, or separate
  turn-resume method. Ox resumes inside `session/load` and uses that response as
  the completion signal; the recovered stop reason remains durable-only as
  specified by `eng/architecture.md`.
