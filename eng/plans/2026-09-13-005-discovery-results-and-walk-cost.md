# Discovery results and walk cost

## Goal

`grep` throws away a file's matches when it cannot read a later line. A line
over the scan bound or one that is not UTF-8 makes `scanFile` return nothing
from that file, so matches it had already found disappear with no diagnostic. In
`files_with_matches` mode it also reads every remaining line of a file it has
already decided to report.

Discovery also pays avoidable per-entry cost. Both search tools normalize and
reparse their glob for every walked file, and ignore matching joins and splits a
path for every directory entry.

Hidden-file exclusion is real shipped behavior that only the tool descriptions
mention; `docs/spec.md` does not own it.

## Desired outcome

A file's earlier matches survive a later unreadable line and say they were cut
short. A filename-only search stops at a file's first match. Per-entry walk work
does not repeat what a directory already settled. The specification states the
hidden-file rule, and a test holds discovery to it.

## Summary of approach

Have `scanFile` report a truncation note alongside whatever it collected, and
break out of the scan once a filename-only search has its answer. Normalize a
glob once before walking. Build a directory's ignore path components once and
replace only the final element per entry. State the hidden-file rule in the
specification and cover a dotfile and a dot-directory.

## Related code

- `internal/tools/grep.go` - `scanFile`, `readBoundedLine`, and the walk
  callback that collects entries.
- `internal/tools/glob.go` - `normalizeGlob`, `globMatches`, the glob walk.
- `internal/workspace/walk.go` - `walkRoot`'s per-entry ignore matching.
- `docs/spec.md` - workspace operations.

## Current state

- Relevant existing behavior: `Capped` carries a `Truncated` flag that the
  renderer already turns into a visible note, and the walk callback stops the
  whole walk when the byte budget is exhausted.
- Existing patterns to follow: the renderer reports collection truncation, so a
  per-file note is an ordinary result line.
- Constraints from the current implementation: `count` mode has to read a whole
  file, so a truncated count must say so rather than be silently low.

## Test plan

- **Key behaviors to verify:** matches before an overlong line are returned with
  a note naming the file; a filename-only search reads no further than the first
  match; a dotfile and a dot-directory are excluded from both glob and grep.
- **Test levels:** unit, in `internal/tools`.
- **Edge cases and failure modes:** a file whose very first line is unreadable,
  and a count cut short mid-file.
- **What not to test:** the ignore matcher's semantics, which are unchanged.

## Implementation plan

- Return collected matches and a truncation note from `scanFile`.
- Stop scanning at the first match in `files_with_matches` mode.
- Normalize each tool's glob once before its walk.
- Reuse a directory's ignore path components across its entries.
- State the hidden-file rule in `docs/spec.md` and add the discovery tests.

## Documentation updates

- `docs/spec.md` owns hidden-file exclusion for discovery.
- Todo list item "Preserve partial grep results and reduce workspace-walk
  overhead (F19, F34, F41)".

## Impact assessment

- Code paths affected: `grep`, `glob`, and the confined workspace walk.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the tools, workspace,
  integration, and end-to-end suites.
- Static checks: `make check-go`.
