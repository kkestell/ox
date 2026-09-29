# Ratatui usage review

## Scope and coverage

Reviewed Ratatui use throughout `crates/ox/src/tui.rs` and `crates/ox/src/tui/`,
the relevant client and terminal tests, and the client and workspace dependency
declarations. Checked the rendering decisions against the TUI plan. Lenses:
correctness, performance, Rust idioms, dependencies, and simplicity. No live
terminal emulator was exercised. The redraw measurement used an unoptimized test
build; release performance was not measured.

## Findings

### Medium

#### Correctness

- **OX-0004: Joined emoji split across display rows**
  (`crates/ox/src/tui/transcript.rs:558`, `crates/ox/src/tui/input.rs:164`):
  Both wrappers count each Unicode code point separately, while Ratatui draws a
  grapheme cluster as one cell group. For `👩‍💻`, the point widths add to four
  columns but Ratatui draws it in two. A focused check of
  `wrap(plain("a👩‍💻b"), 3)` produced `a👩‍` and `💻b` on separate rows, breaking
  one emoji into two. The composer uses the same width calculation, so its
  wrapping and cursor placement can disagree with the drawn text. Wrap by
  grapheme cluster using the same display width as Ratatui, and keep the
  composer cursor on grapheme boundaries.

#### Performance

- **OX-0005: Every draw formats the entire transcript**
  (`crates/ox/src/tui.rs:438`, `crates/ox/src/tui/transcript.rs:214`): A key
  press or the one-second tick redraws the frame. Each draw reparses Markdown
  and wraps every item, although the frame displays only the current page. With
  100 roughly 1 KB messages, ten `TranscriptView::lines` calls took 226 ms in an
  unoptimized test build. This makes input and streaming redraws increasingly
  expensive during a long session. Cache formatted rows for completed items by
  width and display mode, or otherwise limit formatting to the rows needed for
  the visible page. Invalidate the active item as it grows.

## Checks run

- A focused temporary unit check confirmed the emoji split and measured the ten
  redraws. It passed and was removed after the check.
- A small Rust program confirmed that Ratatui renders `👩‍💻` in two cells while
  summing individual code-point widths gives four.
- `cargo tree -p ox -d` confirmed one `crossterm` version.
- `make check-docs` passed.
- `git diff --check` passed.

## Verdict

The terminal setup and direct use of Ratatui's frame and buffer fit this UI. Two
medium findings remain in text layout and redraw cost. No code fix had a single
clear implementation within this focused review.
