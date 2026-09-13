# Stateless file mutations

## Goal

File mutations currently depend on hashes recorded by earlier `read_file` calls.
Shell clears every recorded hash, so otherwise independent shell and file calls
interfere and models repeatedly reread unchanged files.

## Desired outcome

`write_file` creates or replaces a text file without requiring an earlier read.
`edit_file` validates its exact match against current content without requiring
an earlier read. Shell execution has no file-read state to invalidate, and
primary and child tool loops no longer own read-evidence scopes.

## Summary of approach

Remove `FileReads` from agent and tool invocation state. Stop hashing reads and
mutations, remove the historical evidence checks from both mutation tools, and
retain their existing current-content behavior: whole-file write remains an
explicit create-or-replace operation, while exact edit still refuses missing or
ambiguous matches. Keep ACP filesystem reads and writes paired because delegated
exact edits must read and write through the same filesystem. Concurrent
whole-file writes remain last-writer-wins; compare-and-swap mutation would
require a separate executor contract and is outside this change.

## Related code

- `internal/agent/{tool,loop,subagent,reads}.go` - read-evidence ownership and
  propagation.
- `internal/tools/{read,write,edit,text,shell}.go` - evidence recording,
  checking, and invalidation.
- `internal/tools/tools_test.go`, `integration/agent_loop_test.go`, and
  `internal/e2e/filesystem_test.go` - behavior at tool, loop, and
  shipped-process boundaries.
- `docs/spec.md` and `eng/architecture.md` - the settled stateless mutation
  contract.

## Current state

- `read_file` hashes the complete file even when it returns only a window.
- Local writes and edits compare that hash while already holding current file
  content; delegated operations reread current client content before writing.
- Every shell call clears evidence for the primary and every live child.
- `edit_file` independently requires an exact match and preserves bytes outside
  it.

## Structural considerations

Removing the evidence scopes deletes cross-tool and parent-child coordination
from the session owner. Mutation validation remains inside the file tools, and
the workspace and ACP filesystem boundaries retain confinement, executor
selection, text preservation, and atomic local replacement. No replacement state
or abstraction is introduced.

## Test plan

- Verify `write_file` replaces an unread existing file locally and through the
  delegated filesystem while preserving its existing text-format behavior.
- Verify `edit_file` changes an unread existing file locally and through the
  delegated filesystem, while missing and ambiguous exact matches still fail.
- Verify shell and file mutations can run in either order without hidden
  evidence failures.
- Remove unit and integration coverage whose only contract is recording,
  sharing, refreshing, clearing, or rejecting stale read evidence.
- Retain confinement, symlink, permission, executor-conformance, exact-match,
  changed-file, and text-preservation coverage.

## Implementation plan

- Remove `FileReads`, its session and child storage, and its propagation through
  primary and child tool execution.
- Remove content hashing and evidence helpers from file tools, then simplify
  local and delegated write and edit paths without changing their other
  validation or formatting behavior.
- Remove shell invalidation and update tool descriptions to state their direct
  preconditions.
- Replace evidence-focused tests with the stateless local, delegated, and
  mixed-call behavior above.
- Run one completeness and simplification review, address its findings, and
  complete F01.

## Impact assessment

- **Code paths affected:** primary and child tool dispatch plus built-in read,
  write, edit, and shell tools.
- **Data, protocol, or schema impact:** none; read evidence is in-memory only.
- **Dependency or API impact:** no external protocol or dependency change.
  `write_file` and `edit_file` no longer reject a call solely because the model
  did not read the target earlier or another operation changed it after a read.

## Validation

- Run focused tests for `internal/tools`, `internal/agent`, `integration`, and
  `internal/e2e` during development.
- Run `make check` after the behavior and test changes are complete.
