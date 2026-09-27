# Simplify TUI transcript layout

## Plan

`agents/plans/2026-09-27-001-tui-transcript-layout.md`

## Summary

Ox now prints each model tool call once as its raw name and compact JSON,
clipped to one row, and word-wraps and trims user messages, visible reasoning,
and responses. Visible reasoning is bright black without a heading. The plan's
goal is met.

## Departures from the plan

- ACP's tool call `name` is behind the `unstable_tool_call_name` feature of
  `agent-client-protocol`. The workspace and `ox-acp` dependencies enable it.
- The ACP crate enables `serde_json`'s `preserve_order`, so compact JSON keeps
  object keys in the order they arrived rather than sorting them. The tool line
  test asserts the received order.
- The `running` e2e step now waits for `running; waiting for`, because the last
  word of a streamed message waits for the message to end. The step then checks
  that cancellation writes that word before `Cancelling…`.

## Decisions

- A tool call without both `name` and `raw_input` shows only its tool call
  title. If it arrives already completed or failed, as subagent answers do, its
  content follows as wrapped text. Tool calls from other servers that are still
  running therefore show only their tool call titles.
- A tool call's transcript line is written the first time the call is updated
  outside a permission request. A permission request merges its fields without
  writing the line.
- Blocks are separated by a blank line. Consecutive tool call lines and a user
  message that follows the previous turn's output have none.
- Tabs expand to 8-column tab stops, measured from the start of the terminal
  line.
- Test guarantees: `partial_tool_updates_retain_omitted_fields` became
  `partial_tool_updates_retain_omitted_fields_without_new_lines`. New owning
  tests cover message formatting, tool call lines, message finalization at
  boundaries, and the narrow terminal layout and color. The convert test now
  also owns replayed and live names. The tmux key test now also checks
  permission details and the cancellation flush. No guarantee lost its test.

## Automated checks

- `make check` — Passed.
- `make e2e` — Passed, 4 tests.

## Manual verification

1. Read the physical rows and ANSI attributes the new tmux test captures at 40
   columns.

   ```sh
   make e2e
   ```

   The rows matched the plan's layout. Reasoning was written as `\x1b[38;5;8m`,
   which is crossterm's `Color::DarkGrey`, and `\x1b[39m` reset it before the
   first tool call line. No live OpenRouter run was made.

## Follow-up work

- Message text that arrives while a permission prompt is drawn continues on the
  row below the prompt, but the formatter's column still counts from the earlier
  row, so wrapping on that row can be off.
