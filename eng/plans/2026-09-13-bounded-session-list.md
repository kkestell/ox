# Bounded session list

## Goal

`session/list` reads every session log in the store and folds each one into full
durable state before it filters by workspace or takes a page. Folding rebuilds
model history, validates every record, and restores checkpoints, none of which
the listing shows. The response needs four values per session.

Because folding validates, one damaged log fails the whole call, so a single
unreadable session hides every other session from the client. Paging and cursor
handling have no boundary coverage.

## Desired outcome

A listing decodes only the records that carry what it shows, a damaged session
is skipped rather than fatal, and the page boundary and every cursor rejection
are covered.

## Summary of approach

Project a log into the four values the listing needs: the identity and workspace
from the creation record, a title from the first prompt, and the last applied
record's timestamp. Skip and log a session whose log cannot be read or
projected, rather than failing the call.

## Related code

- `internal/agent/store.go` - `fileStore.list`.
- `internal/agent/agent.go` - `ListSessions` filtering, cursor handling, paging.
- `internal/agent/state.go` - `foldRecords`, `sessionTitle`, and the record
  types the projection decodes.

## Current state

- Relevant existing behavior: `apply` advances `updatedAt` to each record's
  timestamp, and a checkpoint is never applied, so the projection has to ignore
  a trailing checkpoint to agree with a folded state.
- Existing patterns to follow: the cursor already carries the workspace filter,
  the timestamp, and the session id, and rejects a mismatch.
- Constraints from the current implementation: the title comes from the first
  user message's content, so the projection reads that record and stops.

## Test plan

- **Key behaviors to verify:** fifty sessions return one page with no cursor and
  fifty-one return a cursor whose next page holds the remainder; a stale,
  workspace-mismatched, or malformed cursor is refused; a corrupt log is omitted
  while its neighbours are listed; and a projection agrees with a folded state.
- **Test levels:** unit, in `internal/agent`.
- **Edge cases and failure modes:** a log whose last record is a checkpoint.
- **What not to test:** the page size constant.

## Implementation plan

- Add the list projection and return it from `fileStore.list`.
- Skip and log an unreadable or unprojectable session.
- Point `ListSessions` at the projection.
- Add the paging, cursor, and corruption tests.

## Documentation updates

- Todo list item "Make `session/list` bounded, resilient, and cursor-tested
  (F15)".

## Impact assessment

- Code paths affected: `session/list` only.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the agent, integration, and
  end-to-end suites.
- Static checks: `make check-go`.
