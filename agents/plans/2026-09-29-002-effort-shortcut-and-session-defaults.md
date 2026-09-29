# Cycle effort and restore session settings

## Goal

Ox cycles through the effort levels offered by the active session with Ctrl+E.
Ctrl+Shift+E has the same effect when the terminal reports Shift separately.
Selecting a model, effort level, or session mode saves the resulting session
settings, so a new session after restarting Ox starts with those choices.

## Related code

- `crates/ox/src/tui.rs` — handles keys and changes ACP configuration options.
- `crates/ox-acp/src/acp.rs` — owns session selections and supplies defaults for
  new sessions.
- `crates/ox-acp/src/settings.rs` — reads the user and workspace settings files.
- `crates/ox-fake-server/src/lib.rs` and `crates/ox/tests/tui.rs` — exercise Ox
  against offered ACP configuration options in a terminal.

## Decisions

- Ox ACP owns these defaults because it defines the model, effort level, and
  session mode and already reads the model default. Ox's `tui.json` remains
  specific to client choices such as servers and favorite models.
- Save all three current values after a successful selection. When the workspace
  has `.ox/settings.json`, update that file; otherwise update
  `~/.config/ox/settings.json`. Preserve other settings and replace the file
  atomically. A failed write leaves the selection unchanged and reports an error
  to the ACP client. Loading a saved session does not change defaults.
- The global settings file still requires `model`; omitted `effort` means
  `default`, and omitted `mode` means `ask`. A workspace file can override any
  of the three. Reject an unknown effort level or session mode, or an effort
  level unsupported by the effective model, with an error naming the file.
- Cycle in the order of the server's `ThoughtLevel` choices, wrapping to the
  first. Do nothing when that option is absent or has fewer than two choices.
  Match Ctrl+E with or without Shift because many terminals cannot distinguish
  those key combinations. The shortcut applies while composing, like Tab for
  session mode.

## Naming

- **Session settings** — the model, effort level, and session mode for a turn.
- **Session mode** — Ask or Auto.
- **Effort level** — the reasoning level offered for the selected model.

## Test plan

- Settings tests cover omitted fields, workspace overrides, invalid values and
  unsupported model and effort combinations, plus a save and reload that
  preserves unrelated settings and selects the proper file.
- ACP tests cover new sessions using all three defaults, successful changes
  being saved, model changes that reset effort, a failed save leaving the active
  selection unchanged, and saved-session loads retaining transcript settings
  instead of overwriting defaults.
- Ox key tests cover forward cycling, wraparound, unavailable choices, and
  Ctrl+E with and without Shift. A terminal test checks that the shortcut
  changes the status line when the server offers effort levels.

## Implementation plan

1. In `crates/ox-acp/src/settings.rs`, add optional effort and mode fields to
   both settings files, validate the effective session settings against the
   model catalog, and save the selected settings to the applicable file.
2. In `crates/ox-acp/src/acp.rs`, use all three defaults for new sessions and
   persist a successful configuration change before updating active selections.
   Keep the server's in-memory global defaults current after a global write.
   Extend the existing ACP tests.
3. In `crates/ox/src/tui.rs`, reuse the choice-cycling logic for the
   `ThoughtLevel` option, bind Ctrl+E and Ctrl+Shift+E, and extend key tests.
4. In `crates/ox-fake-server/src/lib.rs`, offer and accept an effort option for
   client tests. Add the terminal shortcut test in `crates/ox/tests/tui.rs`.
5. Add the optional fields to `examples/settings.json`, which is the example for
   the settings file affected by this change.
