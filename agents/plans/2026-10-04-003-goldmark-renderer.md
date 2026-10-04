# Render Markdown with Goldmark instead of Glamour

## Goal

Glamour grew the `ox` binary from 5.7 MB to 12.5 MB: it always links Chroma's
embedded lexers, its regular expression engine, and bluemonday, none of which Ox
uses. Without Glamour the binary is 5.5 MB.

When the work is done, a Goldmark syntax-tree visitor in `markdown.go` renders
Markdown as ANSI rows styled with Lip Gloss, Glamour is gone from `go.mod`, and
the binary is within a few hundred kilobytes of 5.5 MB. Rendering returns to the
earlier layout: headings and code blocks without markers or fences, bordered
tables, links followed by their destination in parentheses, and wrapped list and
quote rows that hang under their marker.

## Related code

- `internal/tui/markdown.go` — `markdown()` and `plain()`.
- `internal/tui/theme.go` — the Glamour stylesheets and `hex()`.
- `internal/tui/text.go` — `width()`, `styled()`, `prefixed()`, `expand()`, and
  `clip()`.
- `internal/tui/transcript.go` — `itemLines()`, `userLines()`, and
  `contentRows()` call `markdown()` with a stylesheet.
- `internal/tui/view.go` — `newFrame()` builds a buffer that measures by
  grapheme cluster.
- Commit `8f9e6e5^`, `server/internal/tui/markdown.go` — the earlier visitor,
  whose block and inline rules this ports.

## Decisions

- **`markdown(text string, base lipgloss.Style, width int) []string`.** `base`
  wins over every Markdown style, so dim text stays dim and a user message's
  background covers every cell: a span's style is
  `base.Inherit(inline.Inherit(block))`. Callers pass no style for responses,
  `dimStyle` for thinking and dim tool content, and the user message background
  for user messages. The three Glamour stylesheets and `hex()` are deleted.
- **The visitor records each row's prefixes.** A row has a first-row prefix and
  a hanging prefix: open quotes contribute `>`; a list item's marker row starts
  with its marker, and its other rows, and the rows of later blocks in the item,
  start with spaces as wide as the open markers. Nested lists indent by their
  parent's marker width. This replaces inferring hanging indentation from span
  text.
- **Rows wrap after styling.** `wrap(text, width)` in `text.go` word-wraps an
  ANSI string with `ansi.Wrap` and splits it into self-contained rows by drawing
  it into a `newFrame()` buffer and rendering each line, because `ansi.Wrap`
  does not reopen a style after a line break and each row is drawn alone.
- **Tabs expand before parsing**, as with Glamour, so Lip Gloss never converts
  them.
- Tables keep box-drawing borders at their natural width; links keep
  `text (destination)`; images show `[img]`; HTML blocks show as text.

## Test plan

- Headings, emphasis, code spans and blocks, block quotes, nested and ordered
  lists, tasks, links, tables, entities, and preserved line breaks render as in
  the earlier Rust layout.
- A wrapped bold or code span keeps its style on its continuation row.
- Wrapped list items and quotes hang under their marker or bar; a later
  paragraph in a list item is indented under the marker.
- Thinking text is dim everywhere; a user message's every cell has its
  background.
- `go.mod` no longer lists Glamour, Chroma, or bluemonday, and `make build`
  produces an `ox` binary near 5.5 MB.

## Implementation plan

1. Replace `internal/tui/markdown.go` with the Goldmark visitor described above,
   keeping `plain()`.
2. Add `wrap()` to `internal/tui/text.go`.
3. Delete the stylesheets and `hex()` from `internal/tui/theme.go`.
4. Update the `markdown()` callers in `internal/tui/transcript.go`.
5. Run `go mod tidy`.
6. Restore the Markdown expectations in `internal/tui/transcript_test.go` and
   add the wrapped-style and list-paragraph cases.
