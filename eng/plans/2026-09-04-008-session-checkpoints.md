# Add Durable Session Checkpoints

## Sources

- `eng/roadmap.md#checkpoints` — owns checkpoint scope and parity, repair, and
  load-work gates.
- `eng/architecture.md#session-and-turn-state` and
  `eng/architecture.md#testing-boundaries` — define the authoritative JSONL log,
  persisted-before-live mutation, replay, and test boundaries.
- `internal/agent/state.go` — current durable projection, sequence validation,
  model history, identity sets, accounting, and ACP replay fold.
- `internal/agent/store.go` and `internal/agent/agent.go` — current synced log
  append, torn-tail repair, activation fold, and commit transaction.
- `personal/gamma/internal/agent/state.go:checkpointRec` and `foldLines`,
  `personal/gamma/internal/agent/engine.go:deriveRecords`, and
  `personal/gamma/internal/agent/engine_test.go:TestFoldRestoresFromCheckpoint`
  — prior art for retaining the authoritative prefix while restoring the last
  complete projection and folding only its tail.

## Goal

Append a checkpoint after each closed turn and restore durable session state
from the latest checkpoint plus its tail without changing model history,
accounting, identities, or `session/load` replay.

## Implementation

- `internal/agent/state.go` — add a versioned checkpoint record whose payload is
  the serializable folded projection: identity and timestamps, frozen request
  configuration, model history, usage and cost, message and tool-call identity
  sets, and title. Exclude the raw record list and reject checkpoints that
  represent an open turn.
- `internal/agent/state.go` — make `foldRecords` validate the complete record
  envelope and unbroken sequence, restore the latest checkpoint, and apply only
  later records. Preserve the complete raw record stream separately so ACP
  replay still derives from the authoritative log and ignores checkpoint
  records.
- `internal/agent/store.go` and `internal/agent/agent.go` — append and sync a
  terminal turn record followed by its checkpoint as one ordered commit. Advance
  live state only after persistence succeeds, while retaining the existing
  poisoned-session behavior for an uncertain write.
- `eng/roadmap.md` — remove the completed checkpoint slice while leaving the
  remaining durable-session work in order.

## Tests

- `internal/agent/state_test.go` — compare a complete fold with a
  checkpoint-and-tail fold across configuration changes and multiple turns,
  including model history, usage, cost, timestamps, message and tool-call
  identities, title, and ordered replay updates. Prove covered records are not
  applied again and reject malformed, misplaced, or sequence-breaking
  checkpoints.
- `internal/agent/store_test.go` — prove a torn final checkpoint is truncated,
  its preceding completed turn remains valid, and the next record continues the
  sequence.
- `integration/agent_loop_test.go` — run enough turns to create checkpointed
  history, reactivate the session, verify the next provider request sees the
  same history, and compare replay before and after reactivation.
- `internal/e2e/session_test.go` — restart the real process after multiple turns
  and assert `session/load` replays the complete ordered transcript with stable
  message and tool-call identities.

## Decisions

- Port Gamma's last-checkpoint baseline and tail fold, not its Coral journal
  compaction, replay floor, or pending callback state. Ox checkpoints are
  storage-internal records written only after `turn_finished`; they add no ACP
  method or wire value and need no browser-client case.
