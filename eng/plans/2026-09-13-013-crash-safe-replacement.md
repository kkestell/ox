# Crash-safe replacement

## Goal

Atomic replacement writes to `<base>.ox-tmp` and opens it exclusively. The name
is derived only from the target, so a crash between creating that file and
renaming it leaves the name taken, and every later write to that path fails at
the exclusive open. Two writers aiming at the same file collide for the same
reason, and the leftover is visible in the workspace where a model may find it.

`workspace.Edit` also has no coverage for a directory sync failing after the
rename, although `WriteFile` proves that path: an edit that lands but cannot be
made durable must report that without losing the change.

## Desired outcome

A replacement uses a temporary name no other attempt can take, cleans up after
itself when it fails, and keeps its atomic rename and directory sync. An exact
edit proves the same durability contract a write already does.

## Summary of approach

Name the temporary file from a random suffix, so an abandoned one never blocks a
later write and concurrent writers cannot meet. Keep the exclusive open, which
now guards against a genuine collision rather than against the previous run.
Mirror the write's sync-failure proof for `Edit`.

## Related code

- `internal/workspace/workspace.go` - `atomicReplace`, and `Edit` which uses it.
- `internal/workspace/spill.go` - `openSpillFile`, which already names files
  uniquely.
- `internal/workspace/workspace_test.go` -
  `TestWriteReportsAPostRenameSyncFailureAsCommitted`, the proof to mirror.

## Current state

- Relevant existing behavior: a failed attempt already removes its temporary
  file through a deferred cleanup; the problem is only the name.
- Existing patterns to follow: `MutationCommitted` marks an error that must not
  be read as "the change did not happen".
- Constraints from the current implementation: the temporary must stay in the
  target's directory so the rename is atomic.

## Test plan

- **Key behaviors to verify:** an abandoned temporary file does not block a
  later write; two concurrent writers to one path both complete; an edit that
  cannot sync its directory reports a committed mutation and leaves the new
  contents in place.
- **Test levels:** unit, in `internal/workspace`.
- **Edge cases and failure modes:** a leftover file bearing the old fixed name.
- **What not to test:** the rename itself.

## Implementation plan

- Give the temporary file a random suffix.
- Add the leftover, concurrency, and edit sync-failure tests.

## Documentation updates

- Todo list item "Make atomic file replacement crash-safe and cover its sync
  failures (F27, F43)".

## Impact assessment

- Code paths affected: every workspace write and exact edit.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the workspace, tools, agent,
  integration, and end-to-end suites.
- Static checks: `make check-go`.
