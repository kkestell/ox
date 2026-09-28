# Resume workspace sessions from Ox

## Goal

Typing `/resume` in the composer opens a scrollable list of the workspace's
saved main sessions. The newest activity appears first. Each row shows the
session title and activity date. Enter closes the current active session and
loads the selected session, replacing the transcript view with replayed content.
Escape returns to the current session without changing it.

For an 80-column terminal, the list could look like this:

```text
Resume a session                         ↑/↓ move  Enter load  Esc cancel

› Fix integer literal diagnostics                      2026-09-28
  Specify Fern's module system                         2026-09-27
  Untitled session                                      2026-09-26
  Review parser errors                                 2026-09-22
  ...

────────────────────────────────────────────────────────────────────────────────
❯
────────────────────────────────────────────────────────────────────────────────
ask • deepseek/deepseek-v4-flash • high                        5% • $0.01
```

The visible rows scroll with the selection. After loading, the list disappears
and the selected session's transcript and settings fill the usual screen. If a
listed session cannot be loaded, the list stays open with an error above it, so
the user can select another session, including the one just closed.

## Related code

- `crates/ox/src/acp.rs` always creates a new session; its event type drops the
  session ID from notifications. It owns the active session ID, settings, usage,
  permissions, cancellation, and queued prompts.
- `crates/ox/src/tui.rs` handles composer input, selection keys, the frame, and
  the event loop. Its current approval selection and transcript paging are
  nearby patterns for the list.
- `crates/ox-acp/src/acp.rs` already advertises and implements `session/list`
  and `session/load`, but not `session/close`. Its session store lists main
  sessions by activity, with session title and RFC 3339 activity time.
- `crates/ox-acp/src/acp/operations.rs` coordinates prompt, load, and delete for
  each session; close must cancel and wait for an active prompt before releasing
  session resources.
- `crates/ox-fake-server/src/lib.rs` supplies paginated session lists and replay
  for client tests. `crates/ox/tests/tui.rs` tests the terminal in tmux.
- Follow `AGENTS.md`, `agents/architecture.md`, `agents/code-style.md`,
  `agents/glossary.md`, and `agents/testing.md`.

## Decisions

- `/resume` is an exact local command, never a model prompt. If a prompt run is
  active, cancel it and open the list when its turn ends. Pending permission
  requests receive the existing cancellation response. Do not send a queued
  prompt during this transition.
- Keep the current active session and transcript view while browsing. Fetch all
  `session/list` pages with the canonical workspace path, then sort the
  collected sessions by parsed `updatedAt`, newest first. Preserve response
  order for equal or absent dates. Format a present date as `YYYY-MM-DD`; show
  `Unknown
  date` when absent and `Untitled session` when the session title is
  absent. Clip long session titles to leave the date visible.
- Check the agent's advertised list, load, and close capabilities from
  `initialize`. If any is absent, `/resume` shows an unavailable notice and
  leaves the active session alone.
- On selection, close the current active session first, then load the selected
  one. `session/close` releases process state and shell processes but leaves the
  saved transcript. The client can select its current session to close and
  reload it. A load failure leaves the list open for another selection; Escape
  is available only while an active session exists. Do not treat close as
  `session/delete`.
- Route ACP notifications by session ID. Clear the old transcript view and
  session-specific settings and usage when loading, and apply replay to the
  selected session only. Ignore late updates for the closed session. Keep the
  picker visible until the load response arrives.
- Use the existing alternate-screen frame: replace the transcript region with
  the list while the picker is open, keep the composer and status line, and give
  list navigation priority over approval and composer keys. Up/Down move one
  row; Page Up/Page Down move one visible page; Home/End jump to the ends. Enter
  selects, Escape cancels when possible. A list with no sessions shows
  `No saved
  sessions` and permits Escape.

## Naming

- **Session picker** — the temporary list shown by `/resume` in the transcript
  region. Use `session_picker` for its UI state.
- **Active session**, **session title**, **transcript**, **transcript view**,
  and **session operation** retain their definitions in `agents/glossary.md`.

## Test plan

- Client ACP test: `/resume` collects every page, sorts timestamps rather than
  trusting response order, checks capabilities, and closes the old session
  before loading the selected one. Replay and settings belong only to the new
  session. A failed load permits another selection.
- Terminal tests: the list shows session titles and dates; scrolling keeps the
  selected row visible; Enter loads and displays the saved transcript; Escape
  leaves the current session unchanged; `/resume` during a prompt cancels the
  turn before showing the list.
- Ox ACP tests: closing an idle session removes its active state and stops its
  shell processes without deleting its saved transcript; closing a running
  session cancels its prompt and waits for the prompt run to finish. Loading the
  closed session works.

## Implementation plan

1. In `crates/ox-acp/src/acp/operations.rs`, add per-session close coordination
   that cancels an active prompt, waits for its operation guard, and prevents a
   new operation from starting before close finishes. In
   `crates/ox-acp/src/acp.rs`, advertise and handle `session/close`, stop that
   session's shell processes, and remove only its active state.
2. In `crates/ox-fake-server/src/lib.rs`, advertise and implement
   `session/close`. Supply distinct activity times and close/load outcomes for
   client and terminal tests.
3. In `crates/ox/src/acp.rs`, retain the initialization capabilities and
   workspace path, add the paginated list, close, and load requests, and carry
   session IDs with update and prompt-finished events. Represent the interval
   after close and before load without an active session. Keep normal prompt
   behavior after loading.
4. In `crates/ox/src/tui.rs`, dispatch exact `/resume`, add the session picker
   state and keys, draw its scrollable rows, and switch the transcript view and
   status to the loaded session. In `crates/ox/Cargo.toml`, add `chrono` for
   correct RFC 3339 ordering and date display across agents with different UTC
   offsets.
5. Add the focused tests above in `crates/ox/src/acp.rs`,
   `crates/ox/src/tui.rs`, `crates/ox/tests/tui.rs`, `crates/ox-acp/src/acp.rs`,
   and `crates/ox-acp/src/acp/operations.rs`.

## Documentation updates

- `agents/architecture.md`: correct the client and shell-process lifecycles to
  include switching and closing active sessions.
- `agents/glossary.md`: include close in the definition of session operation and
  closing an active session in the shell-process lifetime.
