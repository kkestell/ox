# Test seams

## Goal

Four platform-specific test helpers are named `*_test_unix.go` and
`*_test_windows.go`. Go's test suffix is `_test.go`, so none of them is a test
file: they compile into the shipped binary, and `testing` is a dependency of
`cmd/ox` because of it.

The workspace's directory sync is a mutable package-level value that tests
replace and restore. Production reads whatever a test last assigned, two tests
cannot use it at once, and nothing stops a future assignment from outliving the
test that made it.

## Desired outcome

Platform test helpers are test files. The durability seam is per-workspace, so a
test proving what happens when a sync fails affects only the workspace it built.

## Summary of approach

Rename the four files so the suffix is last. Move the directory sync onto the
workspace as a field whose zero value is the real filesystem, and have the
replacement path call through it.

## Related code

- `internal/agent/prompt_test_unix.go`, `prompt_test_windows.go`.
- `internal/skills/skills_test_unix.go`, `skills_test_windows.go`.
- `internal/workspace/platform.go` - `syncDir` and `SyncDirectory`.
- `internal/workspace/workspace.go` - `atomicReplace` and its callers.

## Current state

- Relevant existing behavior: the spill writer syncs directories too, and no
  test replaces that path, so it can call the real sync directly.
- Existing patterns to follow: the web-fetch network is already injected as a
  parameter, with its default read but never assigned in a production build.
- Constraints from the current implementation: `atomicReplace` takes a root
  rather than a workspace, so it has to reach the field some other way.

## Test plan

- **Key behaviors to verify:** the shipped binary no longer depends on
  `testing`; the two sync-failure tests still prove a committed mutation.
- **Test levels:** unit, in `internal/workspace`; a dependency check for the
  binary.
- **Edge cases and failure modes:** a workspace built without a replacement
  still syncs for real.
- **What not to test:** the sync itself.

## Implementation plan

- Rename the four platform test files.
- Give the workspace a directory-sync field and route replacement through it.
- Point the sync-failure tests at the field.
- Assert the binary does not depend on `testing`.

## Documentation updates

- Todo list item "Remove production test artifacts and test-only mutable seams
  (F38)".

## Impact assessment

- Code paths affected: workspace writes and edits; the shipped binary's
  dependency set shrinks.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the dependency check, then the whole suite.
- Static checks: `make check-go`.
