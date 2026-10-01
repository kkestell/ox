# Refuse unknown slash commands

## Plan

`agents/plans/2026-10-01-004-refuse-unknown-slash-commands.md`

## Summary

Enter on a slash command word that names no known command now shows the notice
`Unknown command /mo` and keeps the text in the composer. The plan's goal is
met.

## Decisions

- The key test reads the notice by rendering the screen with the existing
  `render` and `screen` test helpers, because the transcript view's items are
  private.

## Automated checks

- `make check` — Passed.
- `make e2e` — Passed.
