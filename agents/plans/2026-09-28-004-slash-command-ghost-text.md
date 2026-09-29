# Slash command ghost text

## Goal

While the composer holds one word that starts with `/`, the rest of the first
slash command name that word starts appears dim after the cursor. Tab inserts
that text; the cursor ends after it. Without ghost text, Tab cycles the session
mode as it does today.

With the commands `compact`, `model`, `ox-plan`, and `resume`, typing `/ox`
shows this, where `-plan` is dim:

```text
❯ /ox-plan
```

## Related code

- `crates/ox/src/tui/input.rs` — `Input` holds the composer text and its byte
  cursor. `paste` inserts text at the cursor and advances it. `rows` wraps the
  text into prefixed rows and reports the cursor's row and column.
- `crates/ox/src/tui.rs` — `Screen` carries what `draw` needs; `run` builds it
  each loop beside `settings` and `usage`. `draw` puts each input row into the
  composer with `put`, which clips at the region's width. The `Tab | BackTab`
  arm of `key` calls `next_mode`. The Enter arm matches `/resume` and `/model`
  exactly. The tests `screen`, `render`, `press`, and
  `tab_and_backtab_cycle_the_available_modes` are the patterns to follow.
- `crates/ox/src/tui/theme.rs` — `DIM`.
- `crates/ox/src/acp.rs` — `Session::config_options` is set when the session is
  created in `start`, replaced by `update` on a `ConfigOptionUpdate`, cleared by
  `close`, and set again by `load`. `update` ignores every other update. The
  test `resume_lists_every_page_and_loads_a_different_session_after_close`
  checks the options across close and load.
- `crates/ox-acp/src/acp.rs` — `available_commands` sends `compact` and each
  skill in an `AvailableCommandsUpdate` after a session is created or loaded.
- `crates/ox-fake-server/src/lib.rs` — a new session sends the `tally` command
  in an `AvailableCommandsUpdate` and saves it for replay on load. Any other
  prompt gets the reply `you said: <text>`.
- `crates/ox/tests/tui.rs` — the tmux tests.
  `tab_and_shift_tab_cycle_modes_in_the_terminal` presses Tab with an empty
  composer.

## Decisions

- The slash command names are the client's own `model` and `resume`, listed in
  `tui.rs`, plus the session's available commands. `run` and `key` build the
  sorted list from both sources each time they need it. The first name in that
  order that the typed word starts supplies the ghost text.
- Ghost text appears only when the whole input is one word that starts with `/`,
  with no whitespace or newline, the cursor is at the end of the text, and the
  word is a strict prefix of a command name. An input that equals a name exactly
  shows nothing. Ghost text never changes what Enter sends.
- Tab with ghost text inserts it and nothing else: no trailing space, because
  `/resume` and `/model` are matched exactly, and no mode change. Tab without
  ghost text, and Shift-Tab always, cycle the session mode as today.
- Ghost text is drawn in `theme::DIM` directly after the text on the last input
  row. When the cursor is at the end of the text that row is the cursor's row,
  so the ghost text starts at the cursor.
- `Session::commands` holds the names from the latest `AvailableCommandsUpdate`.
  A new update replaces them, `close` clears them, and a created or loaded
  session has none until the server's update arrives, which `handle` records
  through `Session::update`.

## Naming

- **Slash command** — a prompt whose first word is `/` followed by a command
  name. The names are the client's `model` and `resume` and the session's
  available commands. Keeps its use in `agents/glossary.md`.
- **Available commands** — the command names the server sends in an
  `AvailableCommandsUpdate`. `Session::commands` in `crates/ox/src/acp.rs`.
- **Ghost text** — the rest of the first slash command name that the composer's
  word starts, drawn dim after the cursor. `Input::ghost_text` in `input.rs` and
  the `commands` field of `Screen` in `tui.rs`.

## Test plan

- `crates/ox/src/tui/input.rs`: one table-driven test of `ghost_text` over
  `["compact", "model", "resume"]`: an empty input and `/` alone, `/mo`,
  `/model`, `/model x`, `/mo` with the cursor moved left, `/mo` after a newline,
  `hi /mo`, and `/zzz`. It also checks that pasting the ghost text for `/mo`
  gives `/model`.
- `crates/ox/src/tui.rs`: a render test draws the composer with the input `/mo`
  and the command `model`, and checks that the row reads `❯ /model`, the cells
  after `/mo` are `theme::DIM`, and the cursor sits after `/mo`. A key test sets
  `session.commands` to `tally`, types `/ta`, and checks that Tab gives `/tally`
  with the mode unchanged, that a second Tab changes the mode and leaves the
  text alone, and that BackTab with `/ta` changes the mode and leaves the text
  alone. `tab_and_backtab_cycle_the_available_modes` stays as it is.
- `crates/ox/src/acp.rs`: the close and load test also records the created
  session's `AvailableCommandsUpdate` through `Session::update` and checks
  `commands` holds `tally`, that `close` empties it, and that the update
  replayed on load fills it again.
- `crates/ox/tests/tui.rs`: one tmux test types `/ta`, waits for `/tally` in the
  composer, presses Tab and then Enter, and waits for `you said: /tally` with
  `Ask` still in the status line.

## Implementation plan

1. In `crates/ox/src/acp.rs`, add `commands: Vec<String>` to `Session`, empty in
   `start`, replaced in `update` on `AvailableCommandsUpdate`, and cleared in
   `close`. Extend the close and load test.
2. In `crates/ox/src/tui/input.rs`, add
   `ghost_text(&self, commands: &[String])
   -> Option<&str>` and its test.
3. In `crates/ox/src/tui.rs`, list the client's `model` and `resume`, add a
   function that merges them with `session.commands` and sorts the names, add
   `commands` to `Screen`, draw the ghost text as a `DIM` span on the last input
   row, and make the Tab arm of `key` paste the ghost text when there is one and
   cycle the mode otherwise. Add the render and key tests and set `commands` in
   the `screen` test helper.
4. In `crates/ox/tests/tui.rs`, add the tmux test.
