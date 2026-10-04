# Simplify terminal client interaction

## Goal

Four parts of the terminal client do more than they need to:

- Every session request pauses all interface events until its result arrives, so
  a slow session list or configuration change freezes quitting, resizing,
  permissions, and streaming.
- Submitting during a turn cancels the turn and sends the prompt when it ends,
  and `/resume` during a turn waits for a cancel before opening the picker.
- The model picker keeps a second Favorites list with its own order, tabs, and
  switching rules.
- `hunkHeader()` reimplements the hunk header formatting `go-udiff` already
  provides.

When the work is done:

- Keys, resizes, focus changes, and server events apply while requests run. Only
  opening a session (`/new` or choosing a saved session) holds back server
  events, and only until the session opens or fails to open.
- While a turn runs, Enter leaves a prompt or `/resume` in the composer. Escape
  cancels the turn; after it ends, Enter sends the draft or opens the picker.
- The model picker shows one list: favorites first, in the session's order and
  in bold, then the other models.
- Diff rows come from the unified diff text `go-udiff` formats.

## Related code

- `server/internal/tui/tui.go` — `request()`, `waiting`, and `deferred` pause
  every message; `newSession()` and `choose()` open sessions; `resumeAfterTurn`,
  `enter()`, and `submit()` hold the interrupt behavior; `pickerKey()` and
  `toggleFavorite()` switch and edit the Favorites list.
- `server/internal/client/session.go` — `Queued`, `Prompt()`, and `Finished()`
  hold and resend the interrupting prompt.
- `server/internal/client/conn.go` — `Next()` keeps server events in a queue
  until the client asks for the next one.
- `server/internal/server/server.go` — `loadSession()` sends the replay before
  its response and the available commands after it; `newSession()` sends the
  available commands after its response.
- `server/internal/tui/picker.go` — `modelRows` holds the All and Favorites
  lists.
- `server/internal/tui/view.go` — `pickerLines()` draws the Favorites / All
  toggle and bold favorites.
- `server/internal/tui/transcript.go` — `diffRows()` and `hunkHeader()`.
- `server/internal/settings/client.go` and `README.md` — describe favorites as
  ordered.
- Tests: `server/internal/tui/tui_test.go`, `server/internal/tui/view_test.go`,
  `server/internal/tui/transcript_test.go`,
  `server/internal/client/client_test.go`, `server/cmd/ox/e2e_test.go`.

## Decisions

- **Only opening a session holds server events.** The server sends a loaded
  session's replay before its response, and both new and loaded sessions'
  available commands after it, so events for the opening session can arrive on
  either side of the request's result. While `opening` is set, the model does
  not ask the connection for the next event: the one event already being waited
  for is kept in `held`, and later events stay in the connection's own queue.
  The open request's result clears `opening`, opens the session, and then
  delivers `held` as a message, which resumes listening. Events therefore apply
  in arrival order to the session the result installed, with no client-side
  message queue.
- **Session operations do not overlap.** While `opening` is set, Enter in the
  composer and Enter and Escape in the session picker do nothing. The picker
  stays open until the open succeeds, as it does today when a load fails.
- **Results check their session.** A config option result applies only when the
  session it was sent for is still open; the model picker closes only when it is
  still the open picker.
- **Busy sessions keep drafts.** `Session.Prompt()` is only called when no turn
  runs; it panics otherwise, since the caller broke an invariant. While busy,
  `enter()` leaves prompts and `/resume` in the composer. `/new`, `/model`, and
  `/quit` keep working during a turn.
- **Favorites are a flag.** `modelChoice` gains `favorite bool`.
  `newModelPicker()` sorts favorites first with a stable sort, so both groups
  keep the session's order. Toggling a favorite saves the settings, flips the
  flag, re-sorts, and keeps the toggled model selected. `m.favorites` stays a
  list for saving; its order is no longer shown.
- **Diff rows come from `UnifiedDiff.String()`.** Drop its two file header rows
  and color each remaining row by its first character: `@@` and `\` dim, `-`
  red, `+` green, others the context style. The library's
  `\ No newline at end of file` row is shown.

## Test plan

- A config option change does not hold back a key press or a server event: the
  test applies a key before applying the request's result.
- A config option result for a session that has since been replaced is ignored.
- Loading a session applies its replay to the new transcript whether a replay
  event arrives before or after the result; an event received while opening is
  held and delivered after the result.
- While a session opens, Enter in the composer and the picker does nothing.
- During a turn, Enter leaves a prompt and `/resume` in the composer and sends
  nothing; after Escape and the turn's end, Enter sends the prompt.
- The model picker lists offered favorites first in bold, in the session's
  order; Control+F moves the selected model between the groups, keeps it
  selected, and saves the favorites; Left and Right no longer change the list.
- Diff content shows `@@ -1 +1 @@`, `-one`, `+two` with their colors and the
  `\ No newline at end of file` row for a file without a final newline.
- Terminal: `/resume` during a prompt stays in the composer until Escape ends
  the turn, then opens the picker.

## Implementation plan

1. `server/internal/client/session.go`: remove `Queued`. `Prompt(text string)`
   panics when `Busy` and returns nothing. `Finished()` returns only an error.
   Update `client_test.go`: drop
   `TestAPromptDuringATurnCancelsItAndIsSentAfterItFinishes` and adjust callers.
2. `server/internal/tui/tui.go`: remove `waiting`, `deferred`, `request()`, and
   the queue branch of `Update()`. Requests return their work as a `tea.Cmd`
   directly.
3. Add `opening bool` and `held client.Event` to `model`. `newSession()` and
   `choose()` set `opening`; their results clear it and return a command that
   delivers `held`, if any. In `update()`, a `client.Event` received while
   `opening` is stored in `held` without listening again.
4. Guard `enter()` and the session picker's Enter and Escape on `opening`. Make
   `setConfigOption()` results check the session ID they were sent for, and the
   model picker's close check that the picker is still the model picker.
5. Remove `resumeAfterTurn`. In `enter()`, while `m.session.Busy`, return
   without changing the input for `/resume` and prompts. Simplify `submit()` and
   the `client.Finished` branch of `event()`.
6. `server/internal/tui/picker.go`: replace `modelRows` with
   `models
   []modelChoice` on `picker`; add `favorite` to `modelChoice`;
   `newModelPicker(models []modelChoice, favorites []string)` marks and sorts.
   `filter()` lists every model. Update `openModelPicker()`, `toggleFavorite()`,
   and `choose()`; remove the Left and Right branch of `pickerKey()`.
7. `server/internal/tui/view.go`: `pickerLines()` drops the Favorites / All
   toggle and bolds every favorite.
8. `server/internal/tui/transcript.go`: rewrite `diffRows()` from
   `unified.String()` and delete `hunkHeader()`.
9. Update `tui_test.go`, `view_test.go`, `transcript_test.go`, and
   `server/cmd/ox/e2e_test.go` for the tests above, replacing the tests of
   queued prompts, the resume wait, and the Favorites / All lists.

## Documentation updates

- `README.md`: `favorites` is a list of qualified model IDs, shown first in the
  model picker; drop "ordered".
- `server/internal/settings/client.go`: `Favorites` comment drops "in the order
  they were added".
