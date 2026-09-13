# Coherent edit semantics

## Goal

`write_file` refuses to replace an existing file the model has not read, or has
read before it changed. `edit_file` performs the same kind of mutation with no
such check, so a blind or stale exact edit lands. The documented rule also names
glob and search as evidence sources, which record none.

Exact edits are wrong in two more ways. Matches are counted as overlapping while
`strings.Replace` replaces non-overlapping ones, so an edit can report more
replacements than it made or refuse a unique edit as ambiguous. Restoration
rewrites the whole file to one line ending and strips whatever trailing newlines
the edit asked for, despite promising to preserve the file's format.

## Desired outcome

A write and an exact edit obey the same evidence rule, the documented sources
are the ones that exist, a replacement count is the number of replacements made,
and every byte outside a replaced span survives the edit unchanged.

## Summary of approach

Factor the evidence check out of `write_file` and apply it on both edit paths.
Evidence is a content hash, so `read_file` is the tool that establishes it; the
specification and architecture change to say read rather than read, glob, or
search. Count match sites the way `strings.Replace` consumes them. Splice the
replacement into the original bytes instead of normalizing the file: convert
only `old_string` and `new_string` to the file's dominant line ending, then
replace within the untouched body.

## Related code

- `internal/tools/write.go` - the evidence check on both write paths.
- `internal/tools/edit.go` - `replaceExact`, `matchSites`, and both edit paths.
- `internal/tools/text.go` - `inspectText`, `convertEnding`, `restoreText`,
  which `write_file` still needs for whole-file replacement.
- `docs/spec.md` and `eng/architecture.md` - the evidence rule.

## Current state

- Relevant existing behavior: `read_file` records a hash of the bytes it read,
  keyed by the workspace-canonical path; glob and grep record nothing.
- Existing patterns to follow: `write_file` already reads the current bytes
  inside the atomic edit callback, which is where the hash must be compared.
- Constraints from the current implementation: `restoreText` stays as it is for
  `write_file`, where the model supplies whole content and cannot know the
  file's trailing-newline state.

## Test plan

- **Key behaviors to verify:** a blind edit is refused, an edit after an
  external change is refused, overlapping candidate text replaces and counts
  once, a file with mixed line endings keeps the endings outside the edit, and
  an edit whose replacement ends with a newline keeps it.
- **Test levels:** unit, in `internal/tools`.
- **Edge cases and failure modes:** a BOM file, a file with no trailing newline,
  and `replace_all` over adjacent matches.
- **What not to test:** the write path's evidence rule, already covered.

## Implementation plan

- Extract the evidence check and use it on both write and both edit paths.
- Count non-overlapping match sites.
- Replace within the original body and stop normalizing untouched bytes.
- Update the edit tests and add the blind, stale, mixed-ending, and overlapping
  cases.
- Correct the evidence sentence in `docs/spec.md` and `eng/architecture.md`.

## Documentation updates

- `docs/spec.md` and `eng/architecture.md` name read as the evidence source.
- Todo list item "Enforce coherent edit evidence, replacement, and text-format
  semantics (F08, F09)".

## Impact assessment

- Code paths affected: `write_file` and `edit_file`, local and delegated.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none. An edit now requires a prior read, which is a
  visible tool-behavior change.

## Validation

- Tests to write and run: the tests above, then the tools, agent, integration,
  and end-to-end suites.
- Static checks: `make check-go`.
