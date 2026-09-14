# Delegated-task remnants

## Goal

Removing the child-agent runtime left its shape behind. Parent tool-call
identity threads through dispatch, approval, durable records, ACP metadata, and
the diagnostic trace, and every caller passes an empty value. A session still
carries per-child read scopes nothing creates, a tool still declares itself
parent-only when there is only one loop, and the ACP boundary still defines a
parent-tool-call metadata key.

End-to-end tests still drive the removed task tools, so six of them fail and the
suite cannot act as a gate. The architecture and specification still describe
child agents, subagents, and task queues.

## Desired outcome

One tool loop, with no parameter, field, key, or document describing a second
one. The end-to-end suite passes.

## Summary of approach

Delete the parent identity from every signature, record, and metadata key it
reaches, along with the child read scopes, the parent-only tool flag, and
whatever else only the removed runtime used. Rewrite the end-to-end tests that
described delegated behavior so they cover the retained behavior through the
parent loop, and drop those that only described delegation. Correct each owning
document once.

## Related code

- `internal/agent/loop.go`, `agent.go`, `compact.go`, `adapter.go`, `state.go`,
  `reads.go` - the parent plumbing and child read scopes.
- `internal/agent/tool.go` and `internal/tools/tools.go` - `ParentOnly`.
- `internal/acp/types.go` - the parent tool-call metadata key.
- `internal/trace/trace.go` - the parent tool-call correlation field.
- `internal/e2e` - the tests that still drive removed tools.
- `eng/architecture.md` and `docs/spec.md` - child-agent text.

## Current state

- Relevant existing behavior: durable records carry `parentCallId` as an
  omitted-when-empty field, so no written log contains it and removing the field
  leaves old logs readable.
- Existing patterns to follow: the earlier approval consolidation already moved
  tests onto the production path.
- Constraints from the current implementation: the trace's correlation field is
  part of its emitted records, so removing it changes trace output.

## Test plan

- **Key behaviors to verify:** the end-to-end suite passes; compaction fixtures
  assert compaction rather than a tool count; read-scope invalidation still
  clears the session's reads.
- **Test levels:** end-to-end and unit, in `internal/e2e` and `internal/agent`.
- **Edge cases and failure modes:** a session log written before this change
  still loads.
- **What not to test:** the removed delegation itself.

## Implementation plan

- Remove the parent identity from dispatch, approval, records, ACP metadata, and
  the trace.
- Remove child read scopes, the parent-only flag, and helpers only the removed
  runtime used.
- Rewrite or remove the end-to-end tests that depend on delegated tasks.
- Correct the architecture and specification.

## Documentation updates

- `eng/architecture.md` and `docs/spec.md` stop describing child agents.
- Todo list item "Remove delegated-task remnants and repair affected docs and
  tests (F22, F24, F31)".

## Impact assessment

- Code paths affected: tool dispatch, approval, durable records, ACP updates,
  trace.
- Data, protocol, or schema impact: an always-empty durable field and an ACP
  metadata key are removed; trace records lose a correlation field.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the whole suite, including `internal/e2e`.
- Static checks: `make check-go`.
