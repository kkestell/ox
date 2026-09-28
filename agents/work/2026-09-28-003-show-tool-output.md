# Show tool output

## Plan

`agents/plans/2026-09-28-003-show-tool-output.md`

## Summary

Ctrl+O toggles the transcript view between one-row tool calls and each finished
call's content under its row, live and in replay, with a blank row between every
item. Ox ACP saves tool call content beside the model's text: a diff per patched
file, line and match counts for reads and searches, and the status and stream
tails for shell commands and shell process reads. The plan's goal is met.

## Departures from the plan

- `Prepared` holds the finished diff block rather than the raw old and new
  contents, since the absolute path is known when the operation is prepared and
  nothing else reads the contents.
- `bounded_result` takes the text and content; a `bounded_text` wrapper serves
  the subagent tools and the shell process list, which return only text.
- The tmux transcript test runs at 40x40 instead of 40x24. With a blank row
  between every call and the expanded output, the `render` script no longer fits
  24 rows.

## Decisions

- `ToolOutcome`'s fields are public and its `text()` and `status()` accessors
  are gone. `ToolStatus::id()` supplies the status word compaction labels
  outcomes with.
- A deleted file's contents are read lossily for its diff, so deleting a binary
  file still succeeds.
- Diff hunks come from `similar`'s unified diff iterator, so hunk headers use
  its format: `@@ -1 +1 @@` for a one-line range.
- Content rows of named calls are dim, like a subagent answer's rows. Diff
  context rows keep that style.
- The saved JSON shape of an outcome changed, so existing `ox.db` files must be
  recreated.

## Automated checks

- `make check` — Passed.
- `make e2e` — Passed.

Test guarantees that changed: the transcript view's "adjacent bullets share no
blank row" and "a shell line groups with other bullets" cases are replaced by
"every item is followed by a blank row"; a new transcript test owns the content
rendering and the toggle; a new `tui.rs` test owns Ctrl+T and Ctrl+O; the store
round-trip test now carries a diff block; a new `convert.rs` test owns the ACP
diff block mapping; the read, search, patch, and shell tests gained content
assertions.

## Manual verification

None beyond the tmux test. No live turn against OpenRouter was run.

## Follow-up work

- The search summary reads `1 files` and `1 matches in 1 files`, as the plan
  specified, without singular forms.
