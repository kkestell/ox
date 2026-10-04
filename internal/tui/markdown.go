package tui

import (
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	xansi "github.com/charmbracelet/x/ansi"

	"ox/internal/control"
)

// markdown renders text with control characters escaped as rows width
// columns wide, without blank rows at either end. Every line break is kept,
// so typed and streamed breaks show as typed.
func markdown(text string, styles ansi.StyleConfig, width int) []string {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(styles), glamour.WithWordWrap(max(width, 1)), glamour.WithPreservedNewLines(),
	)
	if err != nil {
		panic(err)
	}
	rendered, err := renderer.Render(expand(control.Escape(text)))
	if err != nil {
		return plain(text, width)
	}
	rows := strings.Split(rendered, "\n")
	for len(rows) > 0 && blank(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	for len(rows) > 0 && blank(rows[0]) {
		rows = rows[1:]
	}
	return rows
}

// plain returns text with control characters escaped, the ends trimmed, and
// tabs expanded, word-wrapped to width.
func plain(text string, width int) []string {
	text = expand(strings.TrimSpace(control.Escape(text)))
	if text == "" {
		return nil
	}
	return strings.Split(xansi.Wrap(text, max(width, 1), ""), "\n")
}

func blank(row string) bool { return strings.TrimSpace(xansi.Strip(row)) == "" }
