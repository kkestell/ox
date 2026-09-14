# Turn finalization for malformed and partial provider completions

## Goal

Keep a session usable and its transcript faithful when a provider completion is
malformed or is cut short mid-stream.

## Desired outcome

A completion whose tool-call IDs are invalid fails its own turn and leaves the
session ready for the next prompt. A turn cancelled after the client has already
seen text keeps that text in history even when the same stream had begun a
tool-call fragment.

## Summary of approach

Tool-call identity is provider output, so it is external input validated at the
boundary between the provider and the turn. Treat its rejection like any other
post-acceptance failure by routing it through the ordinary failed-turn
finalization instead of returning out of the loop with the turn still open.

On cancellation, persist the text, reasoning, and usage the stream completed
independently of whether it had also started an unfinished tool call. An
incomplete tool call is not a call the turn made, so it never belongs in
history; the streamed text does, because the client already saw it.

## Related code

- `internal/agent/loop.go` - Owns the turn loop, tool-call ID validation, and
  the cancellation and failure finalization paths.
- `internal/openrouter/client.go` - Returns the partially assembled completion
  alongside a cancellation error.
- `internal/e2e/prompt_test.go` - Holds a stream open, cancels mid-turn, and
  asserts the conversation the next request carries.

## Current state

- `validateToolCallIDs` returns its error directly from the loop, so no terminal
  turn record is appended and the durable state keeps an open turn. The session
  refuses later prompts until it is closed and reloaded.
- Cancellation persists a partial exchange only when the assembled completion
  has no tool calls, so a stream that emitted text and then began a tool-call
  fragment drops the text the client already rendered.
- Commit failures deliberately return without finalizing: the durable log is the
  thing that failed, so appending a terminal record to it cannot succeed.

## Structural considerations

- **Hierarchy:** Validation stays in the turn loop, which already owns the
  provider-to-turn boundary and every terminal turn record.
- **Abstraction:** Both fixes reuse the existing finalization and partial
  exchange paths rather than adding a new failure mode.
- **Testability:** Both behaviors are ACP-visible: a following prompt succeeds,
  and the next provider request carries the streamed text.

## Test plan

- **Key behaviors to verify:** A duplicate tool-call ID fails one turn and the
  next prompt in the same session runs; a turn cancelled after text and an
  unfinished tool-call fragment keeps the text in the conversation the next
  request carries and replays it after a reload.
- **Test levels:** End-to-end process tests, which are where both the stuck
  session and the lost transcript are observable.
- **Edge cases and failure modes:** An empty tool-call ID; a cancelled stream
  whose only content is an unfinished tool-call fragment must add nothing.
- **What not to test:** Commit-failure paths, whose non-finalization is correct,
  and the existing text-only cancellation case.

## Implementation plan

- Finalize the turn as failed when tool-call ID validation rejects a completion.
- Persist a cancelled stream's completed text, reasoning, and usage regardless
  of unfinished tool-call fragments.
- Add end-to-end coverage for both.

## Documentation updates

- None. `docs/spec.md` already requires that output already streamed to the
  client remains part of the session; these changes make the implementation
  match it.

## Impact assessment

- Code paths affected: Primary turn loop finalization and cancellation history.
- Data, protocol, or schema impact: None. Both paths use existing records.
- Dependency or API impact: None.

## Validation

- Tests to write and run: The end-to-end tests above, then `make check`.
- Static checks: The checks included by `make check`.
