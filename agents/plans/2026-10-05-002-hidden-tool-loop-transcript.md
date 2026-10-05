# Plan: collapse hidden tool loops into one transcript item

## Goal

When tool output is hidden, show one transcript item for the assistant's
progress across a turn's tool calls. Use the short assistant text that
accompanies each tool batch as its description, replacing the description as the
loop continues. Show the final assistant answer as a separate message. When tool
output is truncated or full, keep showing individual tool calls.

## Related code

- `internal/tui/transcript.go` — Stores assistant message and tool call items,
  applies live and replayed session updates, and renders the summary, truncated,
  and full tool output modes.
- `internal/tui/transcript_test.go` — Covers transcript update ordering, tool
  output modes, item spacing, and rendered rows.
- `internal/tui/tui.go` — Cycles tool output modes and ends a turn, giving the
  transcript a point to settle a streamed assistant message as final.

## Decisions

- In summary mode, treat assistant text followed by one or more tool calls as
  the description for that tool iteration. Keep one progress item for the turn
  and replace its text when the next tool iteration arrives; do not append every
  description. Retain the individual calls and their outcomes so the truncated
  and full modes can continue to show them.
- The transcript receives streamed assistant text before it knows whether that
  response will call tools. Keep text visible while it streams. When a tool call
  arrives, adopt that text as the progress item; if the response has no tool
  calls, keep it as an ordinary assistant message. After a tool batch, a later
  response that calls more tools updates the progress item, while a final
  response remains its own message. `endTurn` settles the final response.
- Use the first available tool title as the progress description when a batch
  has calls but no assistant text. This keeps the loop visible for models that
  omit the requested accompanying sentence.
- Keep this behavior limited to named tool calls in summary mode. Nameless items
  such as turn errors keep their current rendering.

### Example transcript

The assistant makes five tool batches, with one short description before each
batch, then answers:

```text
user: Find why the test is failing.
assistant: I’ll inspect the failing test and its setup.
  tool: read test file
assistant: I’ll trace the function the test exercises.
  tool: read implementation
assistant: I’ll check the recent changes around that function.
  tool: search history
assistant: I’ll run the focused test to confirm the failure.
  tool: run test
assistant: I’ll inspect the error path and compare expected behavior.
  tool: read error handler
assistant: The test fails because ...
```

With tool output hidden, the live transcript settles to:

```text
user: Find why the test is failing.
assistant: I’ll inspect the error path and compare expected behavior.
assistant: The test fails because ...
```

The first assistant item is one persistent item whose text changed on each tool
iteration. With truncated or full tool output, the five tool calls remain
individual transcript items, followed by the final assistant message.

## Test plan

- A batch with one tool call and a description shows the description in summary
  mode, not the tool title or output.
- Five successive described tool batches occupy one progress item whose text is
  the fifth description; the final no-tool assistant message occupies a second
  item.
- Multiple tool calls in one batch still use one progress item.
- A batch without assistant text falls back to its first tool title.
- Truncated and full modes retain individual tool calls and their existing
  output. Nameless turn-error items keep their current rendering.
- Replay updates produce the same collapsed transcript as live updates.
- Switching output modes during a turn keeps both the progress item and the
  individual call records usable for rendering.

## Implementation plan

1. In `internal/tui/transcript.go`, track the tool-loop progress item and
   associate calls from each assistant batch with the transcript state needed to
   render summary, truncated, and full modes. Reuse or replace the progress text
   for later tool batches and settle no-tool text as a normal response at turn
   end.
2. In `internal/tui/transcript_test.go`, cover five iterations, final answer
   separation, batches with multiple calls or no description, replay ordering,
   mode changes, and unchanged truncated/full rendering.
