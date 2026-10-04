# Render the terminal client with Charm libraries

## Plan

`agents/plans/2026-10-04-002-tui-rendering-libraries.md`

## Summary

Glamour renders Markdown, rows are ANSI strings styled with Lip Gloss and
measured with `x/ansi`, Ultraviolet draws them, and the composer is a Bubbles
textarea. `markdown.go`, `text.go`, and `input.go` shrink from about 1,020 lines
to about 200. The plan's goal is met.

## Departures from the plan

- **`dimStyles` instead of `thinkingStyles`.** The dim stylesheet also renders
  nameless tool content, such as replayed turn errors, so it is named for its
  look rather than for thinking.
- **Frames are `uv.ScreenBuffer`s.** `StyledString.Draw` needs a screen with a
  width method, which `uv.Buffer` lacks. `newFrame()` builds a screen buffer
  that measures by grapheme cluster, matching `ansi.StringWidth`.
- **Pastes and ghost-text completion go through the textarea's update.**
  `InsertString()` does not scroll the textarea to the cursor, and the
  textarea's sanitizer turns `\r\n` into two newlines. `paste()` normalizes line
  endings and sends a `tea.PasteMsg`.
- **`approvalLines()` escapes option names**, since a row is now drawn as ANSI
  text and an option name comes from the server.

## Decisions

- Rows padded by Glamour keep their trailing spaces; tests compare text with
  styles and trailing spaces removed, and colors through drawn cells.
- The terminal test of a green `+two` diff row now matches from the `+`, since
  the indent before it takes the default text color.

## Checks run

- `make check` — Passed.
- `make e2e` — Passed.

## Manual verification

1. Rendered a Markdown reply with a heading, emphasis, code span, link, nested
   lists, a quote, a Go code block with a tab, and a table in a 70-column tmux
   pane, and typed `/mo`. A temporary test in `server/cmd/ox` used the e2e
   harness (`startTmux`, `x.prompt`, `x.screen()`, `x.styledScreen()`) and was
   deleted afterwards.

   ```sh
   cd server && OX_E2E=1 go test -count=1 -v -run TestManualLook ./cmd/ox
   ```

   Headings were bold without markers, code light yellow with the tab expanded,
   the quote gray behind `>`, the link a hyperlink followed by its URL, and the
   table borderless at full width. The user message kept its background across
   bold and code. Ghost text `del` was dim after `/mo`.

2. Compared stripped `ox` binaries built with `make build` flags before and
   after the change.

   ```sh
   cd server && go build -trimpath -ldflags="-s -w" -o /tmp/ox-new ./cmd/ox
   ```

   5.7 MB before, 12.5 MB after; Glamour brings Chroma and bluemonday.

## Follow-up work

- Glamour builds a renderer for every rendered item and streamed response chunk.
  Measure long streamed responses before caching renderers by width.
