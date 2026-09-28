# TUI layout and rendering audit

## Scope and coverage

Reviewed the layout and rendering code of the interactive client:
`crates/ox/src/tui.rs` (`draw`, the pickers, the approval dialog, the status
line, and the key handling that depends on the layout),
`crates/ox/src/tui/
transcript.rs`, `crates/ox/src/tui/input.rs`,
`crates/ox/src/tui/theme.rs`, and the terminal tests in
`crates/ox/tests/tui.rs`. Lenses: `simplicity`, `readability`, `correctness`,
`testing`, and `documentation`.

The terminal setup, the event loop, and the ACP session handling in `tui.rs`
were read for context only.

## Fixed findings

- **Each region padded itself a different way** (`crates/ox/src/tui.rs:284`):
  the transcript view and the input drew into a narrowed rectangle, the approval
  dialog inserted a two-space span into every line plus a blank line at each
  end, the status line formatted two spaces into its text, and the blank rows
  above and below each region were arithmetic spread through `draw` with four
  comments explaining it. A future padding change had to be made in several
  places. `draw` now computes one rectangle per region (transcript view,
  approval dialog, composer), fills the backgrounds from those, and draws each
  region's content inside `rect.inner(MARGIN)`, where one constant holds the
  shared margin. The `fill` helper and the approval dialog's own padding are
  gone.
- **Three right-justified rows shared no code** (`crates/ox/src/tui.rs:307`):
  the status line, the session picker rows, and the model picker rows each
  clipped a left text, computed padding, and appended a right text, with
  slightly different constants. The status line also duplicated `put`'s bounds
  check to draw its right side with `set_string`. One `justified` helper now
  serves all three, and the status line is a single `put`. The status line keeps
  two columns between its sides instead of one when the settings are too long to
  fit; before, the picker rows kept two and the status line one.
- **The new-activity notice was drawn twice** (`crates/ox/src/tui.rs:353`): a
  blank line to clear the row, then the centered text with hand-computed
  padding. It is now one centered line.
- **The cursor was clamped twice** (`crates/ox/src/tui.rs:295`): the picker and
  the composer each converted and clamped the cursor position with the same six
  lines. One `cursor` helper does it.
- **The picker computed its visible rows in three places**
  (`crates/ox/src/tui.rs:239`): `draw`, `Picker::move_to`, and the key handler
  each subtracted the header height from the screen height, two of them with a
  `.max(1)` guard for a screen too short to show any row. `draw` now returns the
  picker's row count as the layout height, so `move_to` and the key handler take
  rows directly. The guard is gone: with no rows, paging moves nothing and
  `move_to` scrolls harmlessly.
- **The input rows were clamped to a minimum that always held**
  (`crates/ox/src/tui/input.rs:176`): the row list always has at least one
  entry, so `MIN_ROWS` and the `clamp` were noise. Now `min(MAX_ROWS)`.
- **`item_lines` returned early from inside a binding**
  (`crates/ox/src/tui/transcript.rs:200`): a `let lines = match` with a `return`
  in one arm made the reader find the bullet flag in two places. Every arm now
  yields the pair.
- **Three tests re-rendered the screen to read colors**
  (`crates/ox/src/tui.rs:994`): each built a second terminal and drew again
  after `render` to inspect cells. `render` now returns the buffer too.
- **The glossary described rules that no longer exist** (`agents/glossary.md`):
  the composer entry said the input rows sit between two rules; the rules were
  removed in an earlier commit. Corrected.

## Findings

### Low

#### Simplicity

- **OX-0001: Picker draws outside the shared margins**
  (`crates/ox/src/tui.rs:387`): the picker puts its heading on row 0 and its
  selection marker in column 0, so it cannot use the region layout that every
  other part of the screen now uses. `picker_lines` inserts a two-space span
  into its header lines, prefixes each row with the marker and a space, and the
  row builders subtract four columns to leave room. Drawing the picker inside
  the same margin, with `›` and two-space prefixes like the approval dialog's
  options, would remove that handling and make the picker line up with the rest
  of the screen. The heading would move down one row and the marker right two
  columns, and the model picker's tmux test row width would change. This is a
  visible change, so it is left for the user to decide.

## Checks run

- `make check`: passed.
- `make e2e`: passed (9 tmux tests).

The unit tests that assert every row of a rendered screen passed unchanged,
except for the approval dialog line test, which now expects lines without the
dialog's own padding.

## Verdict

The layout code is now one shape: regions, a shared margin, and one helper for
each repeated drawing step. Nine findings fixed; one low finding left open. The
change removes 67 lines net (153 added, 220 deleted).
