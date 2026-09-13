# Turn orchestration

## Goal

A turn's fixed context — the session, the claimed turn, the permission and
elicitation callbacks, the client executors, and the update channel — is passed
down by hand. Turn functions take ten to fourteen positional values, so a reader
matches arguments to parameters by counting.

`runFrom` also treats two of its parameters as loop state, assigning to
`suspended` and `reissue` as it goes, so what a caller supplied and what the
loop has since decided are the same names. Activation takes two unexplained
booleans. The prompt and the recovery paths each spell out the same goroutine
and update-relay loop.

## Desired outcome

The values a turn carries travel as one named context. Loop state is local and
distinct from what a caller asked for. An activation's options read at the call
site. One relay drives both turn entry points.

## Summary of approach

Introduce a `turnRun` holding what every step of a turn needs, and a `turnStart`
saying where a turn begins. `runFrom` copies the start into locals, so assigning
to them cannot be mistaken for changing an argument. Replace the activation
booleans with a named options value. Extract the goroutine and relay loop that
the prompt and recovery paths share, leaving admission and compaction where they
are.

## Related code

- `internal/agent/loop.go` - `run`, `resume`, `runFrom`,
  `executeSuspendedBatch`, `dispatchApprovedBatch`, `executeOne`.
- `internal/agent/agent.go` - `activateSession`, `Prompt`, `recoverSession`.

## Current state

- Relevant existing behavior: an `activeTurn` already carries the turn's trace,
  so a turn context does not need a separate trace parameter.
- Existing patterns to follow: `loopOutcome` already bundles a turn's result.
- Constraints from the current implementation: tests call the dispatch half
  directly, so they build the same context a turn would.

## Test plan

- **Key behaviors to verify:** unchanged. The existing agent, integration, and
  end-to-end suites cover turn execution, recovery, cancellation, and relay
  failure.
- **Test levels:** unit, integration, end-to-end.
- **Edge cases and failure modes:** none new.
- **What not to test:** the context struct itself.

## Implementation plan

- Add `turnRun` and `turnStart` and thread them through the turn functions.
- Make `runFrom`'s pending exchange and reissue flag local.
- Replace the activation booleans with named options.
- Extract the shared relay.

## Documentation updates

- Todo list item "Simplify turn orchestration and lifecycle plumbing (F23)".

## Impact assessment

- Code paths affected: turn execution and session activation. No behavior
  change.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the whole suite.
- Static checks: `make check-go`.
