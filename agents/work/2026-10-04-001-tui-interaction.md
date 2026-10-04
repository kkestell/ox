# Simplify terminal client interaction

## Plan

`agents/plans/2026-10-04-001-tui-interaction.md`

## Summary

Requests no longer pause interface events; only opening a session stops
listening for server events, holding at most one event until the session opens.
A turn keeps prompts and `/resume` in the composer until it ends. The model
picker shows one list with favorites first in bold. Diff rows come from
`udiff.ToUnified()`. The plan's goal is met.

## Departures from the plan

- **Config option requests are sent in order and shown at once.** Without the
  global queue, three quick Control+E presses each cycled from the same current
  value, so the terminal status-line test ended on Low instead of High.
  `acp.Conn.Dispatch()` now sends a request and returns a function that waits
  for its result, and `client.Conn.SetConfigOption()` uses it, so requests sent
  from the event loop reach the server in order. `setConfigOption()` applies the
  choice to the session's options immediately, and only the latest request's
  result replaces them; a failed latest request restores the options from before
  it.
- **Favorites are not sorted.** `filter()` lists matching favorites first, then
  the other matches, so `picker.models` keeps the session's order and toggling a
  favorite needs no re-sort.

## Decisions

- Diff tab expansion starts after the `+`, `-`, or space sign, matching the
  previous rows.
- The Go port was committed as `182ef53` before this work, with the user's
  approval, so this commit holds only this change.

## Checks run

- `make check` — Passed.
- `make e2e` — Passed.

## Manual verification

1. The status-line terminal test exercises quick successive effort changes
   against the real server.

   ```sh
   cd server && OX_E2E=1 go test -count=1 -run TestTerminalStatusLine ./cmd/ox
   ```

   Passed after the departure above; it failed before it.
