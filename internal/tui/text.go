package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"ox/internal/control"
)

// width is the display width of text, which may hold styles.
func width(text string) int { return ansi.StringWidth(text) }

// wrap word-wraps styled text to width and returns self-contained rows: each
// row reopens the styles a break interrupted, since rows are drawn alone.
// Unstyled trailing spaces are dropped.
func wrap(text string, rowWidth int) []string {
	rowWidth = max(rowWidth, 1)
	wrapped := ansi.Wrap(text, rowWidth, "")
	buf := newFrame(rowWidth, strings.Count(wrapped, "\n")+1)
	uv.NewStyledString(wrapped).Draw(buf, buf.Bounds())
	rows := make([]string, len(buf.Lines))
	for i, line := range buf.Lines {
		rows[i] = strings.TrimRight(line.Render(), " ")
	}
	return rows
}

// styled renders each row in the style.
func styled(s lipgloss.Style, rows ...string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = s.Render(row)
	}
	return out
}

// prefixed puts first before the first row and rest before the others.
func prefixed(rows []string, first, rest string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		if i == 0 {
			out[i] = first + row
		} else {
			out[i] = rest + row
		}
	}
	return out
}

// expand replaces tabs with spaces up to the next tab stop of their line.
func expand(text string) string {
	if !strings.Contains(text, "\t") {
		return text
	}
	var out strings.Builder
	column := 0
	for text != "" {
		cluster, w := ansi.FirstGraphemeCluster(text, ansi.GraphemeWidth)
		text = text[len(cluster):]
		switch cluster {
		case "\t":
			spaces := 8 - column%8
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
			continue
		case "\n":
			column = 0
		}
		out.WriteString(cluster)
		column += w
	}
	return out.String()
}

// clip escapes control characters and clips the text with `…`.
func clip(text string, maxWidth int) string {
	return ansi.Truncate(control.Escape(text), max(maxWidth, 0), "…")
}
