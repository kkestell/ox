# Fix Ratatui text layout and redraw cost

## Goal

Fix OX-0004 and OX-0005. The transcript view and composer keep a joined emoji on
one display row when it fits, and the composer cursor and editing keys move
across the whole emoji. Redrawing an unchanged, long transcript view does not
parse and wrap its earlier items again.

Follow `AGENTS.md`, `agents/architecture.md`, `agents/code-style.md`,
`agents/glossary.md`, and `agents/testing.md`.

## Related code

- `crates/ox/src/tui/transcript.rs` — `wrap_line` counts code-point widths;
  `TranscriptView::lines` formats every item on every draw. Updates can change
  the last item or an earlier tool call.
- `crates/ox/src/tui/input.rs` — editing and `Input::rows` move and wrap by code
  point.
- `crates/ox/src/tui.rs` — `draw` requests all transcript view rows, then shows
  one page; its tests render through Ratatui's `TestBackend`.
- `crates/ox/Cargo.toml` and `Cargo.lock` — `unicode-segmentation` is already in
  the dependency graph but is not a direct client dependency.

## Decisions

- Use grapheme clusters as the unit for wrapping and composer movement. Use
  Ratatui's styled grapheme iterator for transcript view lines and
  `unicode-segmentation` for composer text. Measure each cluster with
  `UnicodeWidthStr`, which matches Ratatui's display width. Keep the existing
  tab and control-character display rules.
- Put each item's formatted rows beside that item in `TranscriptView`, with one
  cache key for the current width, thinking setting, and tool output setting.
  Invalidate only an item changed by a chunk or tool update. A width or display
  setting change invalidates all formatted rows. Regenerate the active Thinking
  placeholder as time advances. This keeps cache ownership with the item and
  avoids a second index that must track item lifetimes.
- Have the transcript view return the total row count and only the visible rows
  needed by `draw`. Calculate the page from cached row counts and preserve the
  current spacing, manual scroll position, and new-activity notice. Let `draw`
  borrow the view mutably to update its render cache; the event loop already
  owns the view exclusively.
- Keep the one-second tick for the Thinking placeholder. Once formatted rows are
  cached, an idle draw only traverses item counts and copies the visible rows;
  it does not parse old text.

## Naming

- **Transcript view** — the client's display above the composer, as defined in
  `agents/glossary.md`; `TranscriptView` owns the items and their formatted
  rows.
- **Composer** — the bottom input and status region, as defined in
  `agents/glossary.md`; `Input` owns its text and cursor.
- **Grapheme cluster** — the user-visible character unit used for wrapping and
  editing, including joined emoji and a letter with combining marks.
- **Formatted rows** — the Ratatui lines for one transcript view item at one
  width and display setting.

## Test plan

- Extend the transcript view wrapping test with a joined emoji and a combining
  mark at a row boundary. Check the displayed cells through `TestBackend` so the
  test covers Ratatui's actual width and cluster rendering.
- Extend the composer editing and wrapping tests to check left, right,
  backspace, delete, vertical movement, and cursor position with a joined emoji
  and combining mark. Keep the existing ASCII, tab, and wide-character cases.
- Extend transcript view rendering tests to check the same rows and page after a
  streamed chunk, an update to an earlier tool call, a resize, each display
  toggle, and a Thinking timer tick. These cover cache invalidation through
  visible behavior rather than cache internals.
- Measure repeated redraws of a long unchanged transcript view before and after
  the change, and record the result in the work log.

## Implementation plan

1. Add `unicode-segmentation` to `crates/ox/Cargo.toml` and update `Cargo.lock`.
   Change `wrap_line` in `crates/ox/src/tui/transcript.rs` to keep each styled
   grapheme cluster intact while preserving word wrap, hanging prefixes, and
   tabs.
2. Change `Input` movement, deletion, display-column seeking, and row
   construction in `crates/ox/src/tui/input.rs` to use grapheme boundaries. Keep
   the byte cursor and ensure insertion or paste leaves it at a boundary.
3. In `crates/ox/src/tui/transcript.rs`, store formatted rows with each item,
   invalidate rows when that item changes, and expose total and visible rows for
   the current page. Update `crates/ox/src/tui.rs` to use this result.
4. Update the owning tests in those files, then measure the redraw improvement
   and run the repository checks. Mark OX-0004 and OX-0005 fixed in
   `agents/issues.csv` and check their tasks in `agents/todo.md`.
