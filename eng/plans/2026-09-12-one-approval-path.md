# One approval path

## Goal

Tool approval has two implementations. `executeSuspendedBatch` is the production
path; `executeBatch` and `executeBatchWith` are reachable only from tests and
carry their own lookup, cancellation, grant, downgrade, and batch-result rules.
They have already drifted: the test copy cancels on a context error checked
inside the approval lock, and logs a downgrade the production copy does not. So
the tests that cover rule scoping prove it about code no session ever runs.

## Desired outcome

One approval implementation, exercised by the tests that describe approval
behavior. The durable open-or-reissue step of a permission request reads as one
named operation rather than two inline branches that also clear a flag.

## Summary of approach

Delete `executeBatch` and `executeBatchWith`. Give the approval tests a helper
that pauses a durable model exchange and runs `executeSuspendedBatch`, and give
the dispatch tests a helper that calls `dispatchApprovedBatch` with every call
approved, which is exactly what the approval loop hands it. Extract the pending
permission record's open-or-reissue handling into one function that returns the
generation to ask under.

## Related code

- `internal/agent/loop.go` - `executeSuspendedBatch`, `executeBatch`,
  `executeBatchWith`, `dispatchApprovedBatch`.
- `internal/agent/approval_test.go` and `internal/agent/agent_test.go` - the
  callers of the test-only path.
- `internal/agent/agent_test.go` - `durableTestSession`, already used by the one
  test that drives the production path.

## Current state

- Relevant existing behavior: a session runs one turn at a time, and a batch's
  approvals are sequential, so the production path never has two approvals
  outstanding for one session.
- Existing patterns to follow:
  `TestRecoveredPermissionDoesNotRedispatchStartedSibling` already builds a
  durable session, commits a paused exchange, and calls `executeSuspendedBatch`.
- Constraints from the current implementation: `executeSuspendedBatch` reads the
  batch's calls and targets from the durable suspended exchange, so a test must
  commit one before calling it.

## Test plan

- **Key behaviors to verify:** unchanged. Unknown tools, approval-free tools,
  rule-scoped allow-always, an allow-always with no derivable rule, parallel and
  exclusive dispatch, tool failures, and cancellation all keep their assertions.
- **Test levels:** unit, in `internal/agent`.
- **Edge cases and failure modes:** none new.
- **What not to test:** two approvals outstanding at once for one session. A
  session runs one turn at a time, so only the deleted copy could reach it.

## Implementation plan

- Extract the open-or-reissue step out of `executeSuspendedBatch`.
- Add the suspended-batch and dispatch test helpers.
- Move every `executeBatch` caller onto one of them.
- Delete `executeBatch` and `executeBatchWith`.

## Documentation updates

- Todo list item "Consolidate permission approval and move tests onto its
  production path (F06)".

## Impact assessment

- Code paths affected: tool approval and dispatch, tests only. No shipped
  behavior changes.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the migrated tests, then the agent, integration, and
  end-to-end suites.
- Static checks: `make check-go`.
