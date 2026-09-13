# Cheaper durable commits

## Goal

Every durable commit deep-clones the whole live state. The append-only record
history is copied even though nothing ever rewrites a recorded entry, model
history is copied message by message, and several nested values are cloned by
marshalling to JSON and decoding it again. The work and allocation per commit
grow with the session, so the total grows with its square.

## Desired outcome

A commit's copy cost no longer grows with the number of records the session has
already written, and no clone goes through the JSON encoder. Commit cost at
increasing session sizes is measurable.

## Summary of approach

Stop copying the record slice on clone: records are only ever appended, and
appending to a slice shared with the previous state writes past that state's
length, which it never reads. Copy model history as a slice rather than message
by message, since a recorded message is never modified in place. Replace the
JSON clone helpers with explicit field copies that copy what is appended to or
mutated and share what is immutable once recorded.

## Related code

- `internal/agent/state.go` - `durableState.clone`, `apply`, and the `clone*`
  helpers.
- `internal/agent/agent.go` - `commitLocked`.
- `internal/agent/loop.go` - `cloneMessages`, still used where a caller really
  does build a new message list.

## Current state

- Relevant existing behavior: `clone` copies the records slice, deep-copies
  every message, and JSON round trips the resolved settings, the suspended
  exchange, and every pending tool execution.
- Existing patterns to follow: commits are serialized under `stateMu`, and only
  one successor state is ever published, so a shared append-only backing array
  has exactly one writer.
- Constraints from the current implementation: the identity sets and the tool
  execution map are mutated in place by `apply`, so they must still be copied.

## Test plan

- **Key behaviors to verify:** existing durable-state, replay, and restart
  coverage still passes, and a benchmark shows commit cost that does not grow
  with the number of prior records.
- **Test levels:** unit, in `internal/agent`.
- **Edge cases and failure modes:** a failed commit must leave the published
  state intact even though the shared array was written past its length.
- **What not to test:** the clone helpers field by field, which the durable
  round-trip tests already cover.

## Implementation plan

- Stop copying `records` in `clone` and name the invariant that allows it.
- Copy history and the open-turn base as slices.
- Replace `cloneStoredToolResult`, `cloneToolExecution`,
  `cloneSuspendedExchange`, `cloneResolved`, and `cloneConfigOptions` with
  explicit copies.
- Add a commit benchmark over increasing session sizes.

## Documentation updates

- Todo list item "Remove quadratic durable-state cloning from record commits
  (F05)".

## Impact assessment

- Code paths affected: durable commit and everything that reads live state.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the benchmark, then the agent, integration, and
  end-to-end suites.
- Static checks: `make check-go`.
