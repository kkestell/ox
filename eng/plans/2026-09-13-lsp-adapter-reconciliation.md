# LSP adapter reconciliation

## Goal

Nothing imports `internal/lsp`, but `eng/architecture.md` lists live
`agent -> lsp` and `tools -> lsp` dependency edges and describes the package as
one of the shipped owners. A reader cannot tell from the ownership documents
that the adapter is written but not wired.

The adapter also has defects that integration would inherit. `Manager.Close`
reads each server's client under its lock, so a server whose start is still in
flight is left running: the start does not observe manager cancellation, and
nothing waits for it. Diagnostics come out in map iteration order, so the same
report can arrive in a different order each time. Every framed message costs two
unbuffered writes. Four query methods repeat the same resolve, read, select, and
start sequence.

## Desired outcome

The ownership documents agree that the adapter exists and is not yet reachable.
Closing an activation stops a server whose start is in flight, diagnostics have
a stable order, a framed message is one write, and the query preamble exists
once.

## Summary of approach

Mark the dependency edges as planned in the architecture and say plainly that no
package imports the adapter yet, leaving `eng/todo.md` to own when that changes.
Derive a start's context from the manager so cancellation reaches it, and have
`Close` wait for an in-flight start before closing what it produced. Sort
diagnostics by path and position. Write the header and body together. Extract
the shared preamble.

## Related code

- `eng/architecture.md` - package ownership and the dependency graph.
- `internal/lsp/lsp.go` - `Manager`, `serverState.get`, `Close`, and the query
  methods.
- `internal/lsp/protocol.go` - `decodeDiagnostics`.
- `internal/lsp/client.go` - `send`.

## Current state

- Relevant existing behavior: `docs/spec.md` defines target behavior and already
  describes language-server queries, so it needs no change; `eng/todo.md`
  already carries the unchecked integration item.
- Existing patterns to follow: the manager already owns a cancellable context
  that queries wait on.
- Constraints from the current implementation: a start must still honour its
  caller's cancellation as well as the manager's.

## Test plan

- **Key behaviors to verify:** closing while a start is in flight returns only
  after that server is shut down, a started server is not left running, and
  diagnostics from several documents come back in a stable order.
- **Test levels:** unit, in `internal/lsp`.
- **Edge cases and failure modes:** closing before anything starts, and a start
  that fails.
- **What not to test:** framing bytes, which the existing protocol tests cover.

## Implementation plan

- Say in the architecture that the adapter is not yet imported and mark its
  edges planned.
- Derive the start context from the manager and wait for an in-flight start in
  `Close`.
- Sort decoded diagnostics.
- Write each framed message in one call.
- Extract the shared query preamble.

## Documentation updates

- `eng/architecture.md` describes the adapter's real state.
- Todo list item "Reconcile the parked LSP adapter and harden it before
  integration (F20, F28)".

## Impact assessment

- Code paths affected: the language-server adapter only, which nothing imports.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the lsp suite and `make check`.
- Static checks: `make check-go`.
