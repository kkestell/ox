# Render the terminal client with Charm libraries

## Goal

The terminal client owns a Markdown renderer (`markdown.go`), a styled-text
layer with its own style, span, line, wrapping, and clipping code (`text.go`),
manual cell drawing (`view.go`), and an editable multiline composer
(`input.go`). Glamour, Lip Gloss, `x/ansi`, Ultraviolet, and Bubbles textarea
provide each of these.

When the work is done, Glamour renders Markdown, every rendered row is an ANSI
string, Ultraviolet draws those strings into the frame, and the composer is a
Bubbles textarea. Rendering differs from the Rust client in indentation, list
wrapping, table borders, and heading and code spacing; the composer edits by
rune rather than by grapheme cluster. Both differences are accepted.

## Related code

- `server/internal/tui/markdown.go` — the Goldmark visitor, `markdown()`, and
  `plain()`.
- `server/internal/tui/text.go` — `style`, `span`, `line`, `wrap()`,
  `hanging()`, `prefixed()`, `expand()`, `clip()`, and `width()`.
- `server/internal/tui/view.go` — `draw()`, `put()`, `fill()`, `patched()`,
  `pickerLines()`, and `approvalLines()`.
- `server/internal/tui/transcript.go` — `itemLines()`, `userLines()`,
  `contentRows()`, `diffRows()`, `toolLine()`, and the per-item row cache.
- `server/internal/tui/input.go` — the composer, `ghostText()`, and
  `unknownCommand()`.
- `server/internal/tui/tui.go` — `key()` routes keys to the composer; `View()`
  places the cursor.
- `server/internal/tui/theme.go` — the exact colors.
- `server/internal/control/` — escapes control characters in external text.
- Tests: `server/internal/tui/view_test.go`,
  `server/internal/tui/transcript_test.go`, `server/internal/tui/input_test.go`,
  `server/internal/tui/tui_test.go`, `server/cmd/ox/e2e_test.go`.

## Decisions

- **Rows are ANSI strings.** `style`, `span`, and `line` are deleted. Text is
  styled with Lip Gloss styles and measured with `ansi.StringWidth`; Ultraviolet
  uses the same grapheme width when it draws.
- **Glamour stylesheets live in `theme.go`.** `messageStyles` sets bold
  headings, light yellow code spans and blocks, gray block quotes with a `>`
  indent token, `-` items, `[x]`/`[ ]` tasks, italic, bold, strikethrough,
  underlined links, and `---` rules, with no document margin. `thinkingStyles`
  is the same structure with every color replaced by dim, so thinking text stays
  dim. `userStyles` is `messageStyles` with the user message background on the
  document, so every rendered cell of a user message carries it.
- **`markdown(text, styles, width)` renders one item.** It escapes control
  characters, expands tabs, renders with `glamour.WithStyles`,
  `glamour.WithWordWrap(width)`, and `glamour.WithPreservedNewLines()`, splits
  the result into rows, and drops blank rows at both ends. It builds a renderer
  per call; the transcript's per-item row cache already limits rendering to
  changed items.
- **Plain text wraps with `x/ansi`.** `plain(text, width)` escapes, trims,
  expands tabs, and word-wraps each line with `ansi.Wrap`. Callers color the
  rows with a Lip Gloss style. `clip()` becomes `ansi.Truncate` with `…` after
  escaping.
- **Drawing writes strings, then fills defaults.** `put()` draws one row with
  `uv.NewStyledString(row).Draw` into a one-row rectangle, which clips at the
  area's edge. After a region is drawn, `fill()` gives every cell without a
  foreground the text color and every cell without a background the region's
  background. Styled cells, such as a user message's, keep their own colors.
- **The composer is a `textarea.Model`.** `newInput()` configures it: no line
  numbers, a prompt function that shows `❯` on the first row and two spaces on
  the others, no character limit, `DynamicHeight` between one and `maxInputRows`
  rows, `MaxContentHeight` set to `math.MaxInt` so height never limits input,
  unstyled focused and blurred styles, the real cursor, and Shift+Enter as the
  newline binding. `key()` handles Ox's keys first and passes every other key,
  and every paste outside a picker, to the textarea. End also scrolls the
  transcript to the end.
- **Ghost text is drawn at the cursor.** `ghostText(value, atEnd, commands)` and
  `unknownCommand(text, commands)` become plain functions in `input.go`; the
  ghost text is drawn dim starting at the textarea's cursor position.
- **Dependencies.** Add `charm.land/glamour/v2`, `charm.land/lipgloss/v2`, and
  `charm.land/bubbles/v2`; `github.com/charmbracelet/x/ansi` becomes direct;
  drop the direct `goldmark` and `uniseg` requirements.

## Test plan

- Markdown: headings, emphasis, code spans and blocks, block quotes, lists,
  tasks, links, tables, and preserved line breaks render with the theme's
  colors; control characters in the text appear escaped.
- Thinking text shown with Control+T is dim everywhere, including code.
- A user message fills the transcript width with its background, including
  padding and styled spans.
- Plain tool output wraps by display width, keeps wide characters whole at row
  boundaries, expands tabs, and clips diff rows with `…`.
- The frame still places the transcript, approval dialog, and composer; regions
  keep their backgrounds; the cursor sits in the composer.
- The composer grows to eight rows and scrolls; Shift+Enter inserts a newline;
  Enter submits; pasted text waits for Enter; Control+U clears; ghost text is
  dim after the cursor and Tab completes it; unknown slash commands are refused.
- Terminal tests pass with expectations updated for Glamour's layout.

## Implementation plan

1. Add the dependencies to `server/go.mod`.
2. `server/internal/tui/theme.go`: add `messageStyles`, `thinkingStyles`, and
   `userStyles`, and Lip Gloss styles for the colors the views use.
3. Replace `server/internal/tui/markdown.go` with `markdown()` and `plain()`.
4. Rewrite `server/internal/tui/text.go` down to `prefixed()`, `expand()`,
   `clip()`, and `width()` over strings.
5. `server/internal/tui/transcript.go`: item rows become `[]string`;
   `itemLines()`, `userLines()`, `contentRows()`, `diffRows()`, `toolLine()`,
   `shellLine()`, and `icon()` produce styled strings.
6. `server/internal/tui/view.go`: replace `patched()`, `fill()`, and `put()`
   with the string drawing and default fill; port `draw()`, `pickerLines()`, and
   `approvalLines()`.
7. Replace `server/internal/tui/input.go` with `newInput()`, `ghostText()`, and
   `unknownCommand()`; update `tui.go` key routing, paste, submit, the composer
   width on resize, and the cursor in `View()`.
8. Rewrite `input_test.go` for the composer behavior above, and update
   `view_test.go`, `transcript_test.go`, `tui_test.go`, and
   `server/cmd/ox/e2e_test.go` to compare stripped text and cell colors.
