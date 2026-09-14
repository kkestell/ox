# Amortized session checkpoints

## Goal

Every turn boundary appends a full-state checkpoint to the session log, and the
log only grows. Checkpoint bytes therefore grow with the square of the session's
age, and an aged session reaches the 64 MiB load limit and stops loading. Each
commit also encodes a checkpoint and decodes it again purely to check itself.

## Desired outcome

Total checkpoint bytes stay inside the transcript they summarize, so a session
log grows with its conversation rather than with the square of its age. A commit
encodes a checkpoint at most once.

## Summary of approach

A checkpoint is a load-time shortcut, not a durability requirement: folding the
records it covers rebuilds the same state, including across compaction. So write
one only when it is no larger than the records it would let a load skip. The
session log counts the bytes appended since its last checkpoint, and the commit
compares that against the encoded checkpoint. Drop the decode-what-we-encoded
round trip and advance the sequence directly.

## Related code

- `internal/agent/agent.go` - `commitLocked`.
- `internal/agent/store.go` - `sessionLog.append`, `readRecords`.
- `internal/agent/state.go` - `newCheckpointRecord`, `restoreCheckpoint`,
  `foldRecords`.

## Current state

- Relevant existing behavior: `foldRecords` restores from the last checkpoint
  and folds the rest, and handles a log with no checkpoint at all.
- Existing patterns to follow: `restoreCheckpoint` sets `sequence` from the
  checkpoint record, which is what currently advances the committed sequence
  past it.
- Constraints from the current implementation: `apply` keeps
  `openTurnHistory == len(openTurnBase)`, and the frozen turn configuration
  always carries a model, so the projection round trip normalizes nothing that
  `apply` has not already settled.

## Test plan

- **Key behaviors to verify:** over many turns the log stays within a small
  multiple of its records' own bytes, the session still loads and replays every
  turn, and a checkpoint is still written once the suffix earns it.
- **Test levels:** unit, in `internal/agent`, driving commits through the store.
- **Edge cases and failure modes:** a session whose first boundaries write no
  checkpoint must still load; a reopened log must resume its byte accounting.
- **What not to test:** checkpoint projection contents, which
  `restoreCheckpoint` and its existing tests already cover.

## Implementation plan

- Give `sessionLog` a byte count since its last checkpoint, maintained by
  `append` and initialized when a log is opened.
- Factor record encoding out of `append` so a commit can measure a checkpoint
  without encoding it twice.
- In `commitLocked`, append the checkpoint only when it fits the bytes it
  covers, advance the sequence directly, and drop the round trip.
- Add the growth and reload test.

## Documentation updates

- `eng/architecture.md` gains a sentence that a checkpoint is written only when
  it is smaller than the records it replaces at load.
- Todo list item "Bound checkpoint growth and keep aged sessions reloadable
  (F04)".

## Impact assessment

- Code paths affected: durable commit and session load.
- Data, protocol, or schema impact: none. The record format is unchanged, and a
  log with a checkpoint at every boundary still loads.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the growth and reload test, then the agent,
  integration, and end-to-end suites.
- Static checks: `make check-go`.
