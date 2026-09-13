# Bounded incremental output tails

## Goal

`outputTail` cuts the retained tool-output tail to its byte bound and then
deletes one leading byte at a time until the whole string validates as UTF-8. A
single invalid byte in the middle therefore discards every valid byte before it,
and each deletion rescans the whole tail.

Accumulation is also paid per chunk rather than per byte. `appendOutput`
concatenates the entire retained tail with each chunk, and the workspace stream
recorder retrims and recounts its whole preview tail on every write.

## Desired outcome

A retained tail keeps its valid output, with invalid bytes replaced rather than
used to truncate. Accumulating output costs work proportional to the new bytes
instead of the retained ones.

## Summary of approach

Trim only the partial rune the byte cut created, then replace any remaining
invalid sequence in one pass with the Unicode replacement character. Hold the
agent's tail in a byte buffer that is allowed to grow to twice its bound and is
compacted only when it crosses that, which amortizes trimming across a bound's
worth of new output; bound it exactly when a flush renders it. Apply the same
slack to the stream recorder's preview tail, and count the preview head's lines
incrementally so the head is validated and trimmed once rather than per write.

## Related code

- `internal/agent/adapter.go` - `outputTail`, `appendOutput`, `outputState`,
  `flush`.
- `internal/workspace/stream.go` - `capturePreview`, `Finish`,
  `streamLineCounter`, `validUTF8Suffix`, `firstLines`, `lastLines`.
- `internal/agent/state.go` - replay also renders a completed tool result's
  tail.

## Current state

- Relevant existing behavior: the recorder's `sanitize` already replaces invalid
  UTF-8 in the stream it records, so the preview tail's own trimming only has to
  handle the partial rune a byte cut creates.
- Existing patterns to follow: `validUTF8Suffix` already trims a leading partial
  rune for the recorder.
- Constraints from the current implementation: `Finish` derives the head and
  tail overlap from the tail's length, so it must measure the bounded tail.

## Test plan

- **Key behaviors to verify:** output before an invalid byte survives with the
  bad byte replaced, a tail cut mid-rune drops only that rune, the retained tail
  stays within its bound across many small chunks, and a spilled preview is
  unchanged by the added slack.
- **Test levels:** unit, in `internal/agent` and `internal/workspace`.
- **Edge cases and failure modes:** a chunk larger than the whole bound, and a
  stream that is entirely invalid bytes.
- **What not to test:** the flush cadence, which is unchanged.

## Implementation plan

- Rewrite `outputTail` to trim a leading partial rune and replace the rest.
- Hold the agent tail as bytes with doubling slack, bounding it at flush.
- Give the recorder's preview tail the same slack and bound it in `Finish`.
- Count preview head lines incrementally and trim the head once.
- Add the tests above.

## Documentation updates

- Todo list item "Make streamed tool-output retention bounded and incremental
  (F11, F17)".

## Impact assessment

- Code paths affected: ACP tool-output updates and shell output previews.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the tests above, then the agent, workspace, tools,
  integration, and end-to-end suites.
- Static checks: `make check-go`.
