# Bounded file reads

## Goal

`read_file` exempts the first selected line from its inline byte bound, so a
file with one very long line is returned whole. That output becomes a tool
result in a durable session record, which the store rejects above its record
ceiling. The reads underneath have no bound either: workspace reads for the read
and edit tools and credential-file loading each call `io.ReadAll` on whatever
the path names.

## Desired outcome

`read_file` never returns more than the inline byte bound, and says so when it
elides part of a line. Workspace reads and credential reads refuse oversized
input at their boundary with a clear message instead of allocating it.

## Summary of approach

Give the workspace a maximum readable file size, checked from the file's size
before allocation and enforced again while reading so a growing file cannot slip
past. Apply the same bound to text a delegating client returns. In `read_file`,
reserve the footer's bytes, keep the existing whole-file fast path for content
that already fits, and window everything else under the bound, truncating a
single oversized line at a UTF-8 boundary with a footer that reports how much of
it was shown.

## Related code

- `internal/tools/read.go` - `window` and its footers.
- `internal/workspace/workspace.go` - `ReadFile`.
- `internal/workspace/spill.go` - `InlineMaxBytes` and `validUTF8Prefix`, which
  already truncates an oversized first preview line.
- `internal/tools/text.go` - `acquireText`, shared by the edit and write tools.
- `internal/credentials/credentials.go` - credential file loading.

## Current state

- Relevant existing behavior: `window` charges a byte cost only for lines after
  the first, so the first line is always emitted in full.
- Existing patterns to follow: `SpilledPreview` reserves its footer length and
  truncates an oversized first line with `validUTF8Prefix`.
- Constraints from the current implementation: `read_file` records a content
  hash as read evidence, so it needs the whole file; the bound is what keeps
  that safe.

## Test plan

- **Key behaviors to verify:** a file exactly at the inline byte bound is still
  returned whole, a single line over the bound is truncated with a footer that
  names the shown and total byte counts, a file over the workspace bound is
  refused, and an oversized credential file is refused.
- **Test levels:** unit, in `internal/tools`, `internal/workspace`, and
  `internal/credentials`.
- **Edge cases and failure modes:** an oversized single line with no trailing
  newline, and a multibyte rune spanning the truncation point.
- **What not to test:** the spill path, which already bounds its own output.

## Implementation plan

- Add `workspace.MaxFileBytes` and enforce it in `ReadFile`.
- Export `workspace.ValidUTF8Prefix` for the read tool.
- Reserve the footer in `window`, keep the whole-file fast path, and truncate an
  oversized first line with an elision footer.
- Bound delegated text at the same limit in the read, edit, and write paths.
- Bound the credential file read.
- Add the tests above and update the existing huge-line expectation.

## Documentation updates

- `docs/spec.md` gains a sentence on the read bound if the shipped behavior is
  not already covered there.
- Todo list item "Bound model-chosen file reads and `read_file` output (F03,
  F26)".

## Impact assessment

- Code paths affected: read, edit, and write tools, workspace reads, credential
  loading.
- Data, protocol, or schema impact: none.
- Dependency or API impact: `validUTF8Prefix` becomes exported.

## Validation

- Tests to write and run: the tests above, then the tools, workspace,
  credentials, agent, and integration suites.
- Static checks: `make check-go`.
