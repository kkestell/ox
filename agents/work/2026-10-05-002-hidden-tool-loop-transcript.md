# Collapse hidden tool loops into one transcript item

## Plan

`agents/plans/2026-10-05-002-hidden-tool-loop-transcript.md`

## Summary

Summary mode now keeps one progress item for named tool calls in a turn and
updates it with each assistant description. The final answer remains separate;
truncated and full modes retain each description and tool call. The plan's goal
is met.

## Decisions

- Use the usage update between live assistant batches to recognize a new batch
  when the assistant emits no description. In that case, use the first tool
  title as the progress text.
- Preserve the original assistant and tool items and use the progress item only
  in summary mode. This keeps output mode changes reversible during a turn.

## Checks run

- `go test ./internal/tui` — passed.
- `make check` — passed. The first run stopped at Markdown formatting for the
  new plan; after formatting it, the full check passed.

## Follow-up work

None.
