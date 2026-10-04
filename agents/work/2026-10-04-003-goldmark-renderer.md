# Render Markdown with Goldmark instead of Glamour

## Plan

`agents/plans/2026-10-04-003-goldmark-renderer.md`

## Summary

A Goldmark visitor renders Markdown as Lip Gloss-styled ANSI rows, and Glamour,
Chroma, and bluemonday are gone from `go.mod`. The `ox` binary is 6.1 MB, down
from 12.5 MB. That is 0.6 MB above the plan's estimate of about 5.5 MB, and 0.4
MB above the 5.7 MB before the Charm libraries; Lip Gloss and the Bubbles
textarea account for the rest. The layout matches the earlier client, except
that nested lists indent by their parent's marker width.

## Departures from the plan

- The binary misses the plan's size target by about 0.6 MB, as described above.

## Decisions

- Ordered list numbers are no longer right-aligned to a column; nested items
  start under their parent's text.
- A task box joins its item's marker, so the item's later rows hang under the
  box's end.

## Checks run

- `make check` — Passed.
- `make e2e` — Passed.

## Manual verification

1. Rendered a Markdown reply with a heading, a wrapping bold span, a code span,
   a link, nested lists, a wrapping quote, a Go code block with a tab, and a
   table in a 60-column tmux pane, using a temporary test in `cmd/ox` built on
   the e2e harness (`startTmux`, `x.prompt`, `x.screen()`), deleted afterwards.

   ```sh
   OX_E2E=1 go test -count=1 -v -run TestManualLook ./cmd/ox
   ```

   Wrapped list and quote rows hung under their marker and bar, the table had
   box borders at its natural width, and the link showed its destination in
   parentheses.

2. Measured the release build.

   ```sh
   make build && ls -l bin/ox
   ```

   6,137,714 bytes.
