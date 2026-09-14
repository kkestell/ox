# Concurrent Subagents

## Goal

Let the primary agent start independent child agents that continue running while
the primary loop works, exchange messages with them, inspect their state, wait
for progress, and stop them deliberately.

## Desired outcome

During one prompt turn, the primary agent can run several bounded child loops at
the same time. Each child works from a standalone prompt with the turn's frozen
model, settings, workspace, permissions, and non-coordination tools. The primary
agent can send follow-up messages, receive child reports and final answers, wait
without polling, and stop live children. Ending or cancelling the parent turn
cancels every remaining child. Neither the primary nor child loop has an
artificial model-request or tool-loop iteration ceiling.

## Summary of approach

Add a turn-owned in-memory subagent group beside the primary loop. Coordination
tools call that group directly: start launches a goroutine and returns its ID,
send queues input for a child, wait blocks until selected child state advances,
list snapshots the group, and stop cancels a child. A child runs a small model
and tool loop using the existing execution, approval, event, context-admission,
and accounting boundaries, but keeps its conversation private and non-durable.
Children have no model-request or tool-loop iteration cap; they run until they
finish or are cancelled. Children cannot create children. A child-only report
tool delivers interim messages to the primary inbox. The primary loop likewise
runs until it finishes, fails, or is cancelled; its durable request counter is
sequence and recovery state, not a budget.

## Related code

- `internal/agent/{agent,loop,tool,prompt}.go` - turn ownership, model loop,
  tool execution, and frozen prompt configuration.
- `internal/agent/state.go` - durable primary history, the primary request
  allowance, and session usage accounting that child traffic must not corrupt.
- `internal/tools/tools.go` - built-in tool catalog and coordination tools.
- `integration/agent_loop_test.go` and `internal/e2e` - stable ACP-visible
  behavior and real-process cancellation coverage.
- `docs/spec.md` and `eng/architecture.md` - product contract and runtime
  ownership.
- `~/src/references/repos/personal/alpha/runtime/internal/agent/subagent.go` and
  `~/src/references/repos/personal/beta/src/subagent.rs` - focused child-loop
  precedents without the removed queue semantics.

## Current state

- A session admits one primary prompt but its tool dispatcher already runs
  independent safe tools concurrently.
- The deleted child runtime blocked the spawning tool until one child finished;
  its later durable queue serialized children and expanded the session schema.
- The active turn already owns cancellation, callbacks, tracing, and the
  immutable configuration needed by a child.

## Structural considerations

- **Hierarchy:** `internal/agent` owns live child orchestration; built-in tools
  remain thin adapters over invocation callbacks.
- **Abstraction:** one concrete turn-owned group owns lifecycle and mailboxes;
  the child loop reuses existing agent boundaries without becoming a second
  session implementation.
- **Modularization:** child orchestration lives in one focused agent file and
  coordination argument handling in one tools file.
- **Encapsulation:** child histories and goroutines never enter public ACP or
  durable session state. The client observes ordinary nested tool activity.
- **Testability:** deterministic fake model responses and wait notifications
  verify concurrency and messaging without timing sleeps.

## Test plan

- **Key behaviors to verify:** start returns before completion; multiple
  children overlap; messages reach a child's next request; interim and final
  child messages reach the parent; wait wakes on progress; stop and parent
  cancellation terminate children; child tools exclude coordination and share
  workspace serialization, permissions, and usage; child provider requests are
  independent from the primary's durable request sequence.
- **Test levels:** strict tool-schema unit tests, agent lifecycle tests, model
  integration tests, and one real-process cancellation test.
- **Edge cases and failure modes:** blank prompts/messages, unknown IDs,
  duplicate names, active/total limits, messages after terminal state, empty
  final answers, provider/tool failure, cancellation while streaming or waiting,
  and a parent attempting to finish with children still active.
- **What not to test:** durable child recovery, because live child state is
  intentionally scoped to one prompt request.

## Implementation plan

- Add scoped coordination tools and invocation callbacks, with parent-only
  lifecycle tools and one child-only reporting tool.
- Add the turn-owned subagent group, bounded state/mailboxes, lifecycle
  snapshots, cancellation, and wait notification.
- Add the child model/tool loop over the parent turn's immutable configuration
  and shared workspace policy, while keeping child history private.
- Remove the fixed provider-request ceiling from the primary loop while keeping
  its durable monotonically increasing request count.
- Wire group creation and cleanup into every primary turn lifecycle and expose
  child tool activity through the existing ACP event stream.
- Add focused unit, integration, and end-to-end coverage.
- Run one completeness and simplification review, address findings, then mark
  the todo item complete.

## Documentation updates

- Specify live subagent behavior and limits in `docs/spec.md`.
- Record turn-owned child lifecycle and dependency boundaries in
  `eng/architecture.md`.
- Add and complete the concurrent-subagents todo item.

## Impact assessment

- Code paths affected: tool catalog filtering, prompt composition, provider
  request accounting, tool execution, turn cancellation, and ACP tool updates.
- Data, protocol, or schema impact: no ACP change or durable child state; one
  content-free session record accounts for subagent provider usage.
- Dependency or API impact: no new dependency or configuration surface.

## Validation

- Tests to write and run: focused `internal/tools`, `internal/agent`,
  `integration`, and `internal/e2e` tests, followed by `make check`.
- Static checks: gofmt, `go vet`, and staticcheck through the repository gates.
- Manual verification: inspect the session log to confirm child-private history
  is absent and the parent transcript contains only coordination results.
