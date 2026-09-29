# Slash command ghost text

## Plan

`agents/plans/2026-09-28-004-slash-command-ghost-text.md`

## Summary

The composer shows the rest of a slash command name dim after the cursor, and
Tab inserts it. Without ghost text, Tab and Shift-Tab cycle the session mode as
before. The plan's goal is met.

## Decisions

- `/` alone shows no ghost text. The plan grouped it with the empty input in the
  `ghost_text` test, so the first command is not suggested until a letter is
  typed.
- `ghost_text` takes an explicit lifetime for `commands`, because the returned
  text borrows from the command list, not from the input.
- The close and load test records the load's update by reading the next
  `AvailableCommandsUpdate` from the event channel. That is the update sent when
  the second session was created, not necessarily the replay, so the test does
  not separate the two.

## Automated checks

- `make check` — passed.
- `make e2e` — passed, including
  `tab_completes_a_slash_command_from_ghost_text`.

## Manual verification

None beyond the tmux test.
