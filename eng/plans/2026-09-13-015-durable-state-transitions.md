# Durable-state transitions

## Goal

The record-application switch has one case that is far longer than the rest.
Applying a completed model exchange checks eight separate rules and builds the
turn's history in the same eighty lines, with the per-call loop validating and
appending at once, so a reader cannot see what the rule set is without reading
the construction too.

Compaction's splice exists twice: `applyCompaction` states it as a helper that
nothing calls, and the switch open-codes the same three appends. Four places
compare two values by marshalling both and comparing bytes, each spelling the
same six lines again. And the configuration comparison expresses "ignore where
the model came from" by assigning to its parameter, which reads as mutation even
though the copy is local.

## Desired outcome

The model exchange's rules are named and checked before any of it reaches
history. Compaction has one splice. Comparing two values by their JSON is one
function. The configuration comparison says what it excludes instead of
appearing to mutate.

## Summary of approach

Split the exchange case into a validation pass and an application pass, so the
per-call loop no longer does both. Keep one compaction splice and use it from
both callers. Replace the four marshal-and-compare copies with one helper, and
express the configuration comparison as a rendering that drops the field it does
not compare.

## Related code

- `internal/agent/state.go` - the `apply` switch, `applyCompaction`,
  `validateCompletedSuspension`, `sameRequestConfiguration`.
- `internal/agent/compact.go` - `compactedMessages`, the live splice.

## Current state

- Relevant existing behavior: a failed apply discards the whole successor state,
  so validating first changes no outcome; it only makes the rules visible.
- Existing patterns to follow: other cases already delegate to a named
  validator.
- Constraints from the current implementation: the per-call loop also rejects a
  tool call repeated inside one exchange, which the validation pass must keep.

## Test plan

- **Key behaviors to verify:** unchanged. The durable-state suite already covers
  every rule this moves, including duplicate identifiers, mismatched results,
  invalid targets, and compaction splices.
- **Test levels:** unit, in `internal/agent`.
- **Edge cases and failure modes:** a tool call repeated within one exchange.
- **What not to test:** the helpers themselves.

## Implementation plan

- Extract the model exchange's validation and application.
- Keep one compaction splice and delete the unused copy.
- Add one JSON comparison helper and use it at all four sites.
- Make the configuration comparison a rendering.

## Documentation updates

- Todo list item "Simplify durable-state transition code (F37)".

## Impact assessment

- Code paths affected: durable record application. No behavior change.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the whole suite.
- Static checks: `make check-go`.
