# Cycle effort and restore session settings

## Plan

`agents/plans/2026-09-29-002-effort-shortcut-and-session-defaults.md`

## Summary

The plan's goal is met. Ctrl+E cycles offered effort levels, and successful
model, effort, and mode selections become defaults for new sessions.

## Decisions

- Settings now accept unrelated JSON fields so a preserved field does not make
  the saved file unreadable on restart. The former unknown-field rejection test
  changed to cover that behavior.
- Settings and ACP tests gained ownership of effective defaults, invalid values,
  file selection, persistence, and failed saves. Client key and terminal tests
  gained ownership of effort cycling; existing picker expectations were updated
  for the fake server's effort option. No other test guarantee moved or was
  lost.

## Automated checks

- `make check` — passed.
- `make e2e` — passed, including the Ctrl+E terminal test.
