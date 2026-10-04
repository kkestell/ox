package tui

import (
	"image/color"
	"strings"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/rivo/uniseg"

	"ox/internal/control"
)

// style is a set of colors and attributes. Patching one style with another
// replaces the colors the other sets and adds its attributes.
type style struct {
	fg, bg    color.Color
	attrs     uint8
	underline bool
}

func fg(c color.Color) style { return style{fg: c} }

func (s style) patch(other style) style {
	if other.fg != nil {
		s.fg = other.fg
	}
	if other.bg != nil {
		s.bg = other.bg
	}
	s.attrs |= other.attrs
	s.underline = s.underline || other.underline
	return s
}

var (
	bold          = style{attrs: uv.AttrBold}
	italic        = style{attrs: uv.AttrItalic}
	strikethrough = style{attrs: uv.AttrStrikethrough}
	underlined    = style{underline: true}
)

type span struct {
	text  string
	style style
}

// line is one row of spans. Its style lies under every span's style.
type line struct {
	spans []span
	style style
}

// styled is a line of unstyled text with the style.
func styled(text string, s style) line {
	return line{spans: []span{{text: text}}, style: s}
}

func (l line) String() string {
	var text strings.Builder
	for _, span := range l.spans {
		text.WriteString(span.text)
	}
	return text.String()
}

func (l line) width() int { return width(l.String()) }

// width is the display width of text.
func width(text string) int { return uniseg.StringWidth(text) }

// graphemes calls f with each grapheme cluster of text and its width.
func graphemes(text string, f func(cluster string, width int)) {
	state := -1
	for text != "" {
		var cluster string
		var w int
		cluster, text, w, state = uniseg.FirstGraphemeClusterInString(text, state)
		f(cluster, w)
	}
}

func blank(text string) bool {
	return strings.TrimFunc(text, unicode.IsSpace) == ""
}

// wrap word-wraps rows by display width. The whole text is trimmed, interior
// blank rows are kept, tabs are expanded, and words wider than a row are
// split. The rows after the first of a wrapped row start with its hanging
// prefix.
func wrap(lines []line, width int) []line {
	width = max(width, 1)
	var rows []line
	for _, l := range lines {
		rows = append(rows, wrapLine(l, width)...)
	}
	for len(rows) > 0 && rows[len(rows)-1].width() == 0 {
		rows = rows[:len(rows)-1]
	}
	for len(rows) > 0 && rows[0].width() == 0 {
		rows = rows[1:]
	}
	return rows
}

// hanging returns a row's hanging prefix: the `>` of a quote and blank space
// as wide as a list marker, which the rows after the first of a wrapped row
// start with.
func hanging(l line) []span {
	var hang []span
	spans := l.spans
	for len(spans) > 0 && (spans[0].text == ">" || spans[0].text == " ") {
		hang = append(hang, spans[0])
		spans = spans[1:]
	}
	if len(spans) > 0 {
		marker := strings.TrimLeft(spans[0].text, " ")
		number, ordered := strings.CutSuffix(marker, ". ")
		ordered = ordered && number != "" && strings.Trim(number, "0123456789") == ""
		if marker == "- " || strings.HasPrefix(marker, "- [") || ordered {
			hang = append(hang, span{strings.Repeat(" ", width(spans[0].text)), spans[0].style})
		}
	}
	return hang
}

func wrapLine(l line, rowWidth int) []line {
	hang := hanging(l)
	hangWidth := 0
	for _, span := range hang {
		hangWidth += width(span.text)
	}
	hangWidth = min(hangWidth, rowWidth-1)
	type cluster struct {
		text  string
		width int
		style style
	}
	// Every styled cluster stays intact; tabs expand and trailing blanks drop.
	var clusters []cluster
	column := 0
	for _, span := range l.spans {
		s := l.style.patch(span.style)
		for i, segment := range strings.Split(span.text, "\t") {
			if i > 0 {
				spaces := 8 - column%8
				for range spaces {
					clusters = append(clusters, cluster{" ", 1, s})
				}
				column += spaces
			}
			graphemes(segment, func(text string, w int) {
				clusters = append(clusters, cluster{text, w, s})
				column += w
			})
		}
	}
	for len(clusters) > 0 && blank(clusters[len(clusters)-1].text) {
		clusters = clusters[:len(clusters)-1]
	}
	textWidth := func(clusters []cluster) int {
		total := 0
		for _, c := range clusters {
			total += c.width
		}
		return total
	}
	rows := [][]cluster{nil}
	column = 0
	limit := func() int {
		if len(rows) == 1 {
			return rowWidth
		}
		return rowWidth - hangWidth
	}
	rest := clusters
	for len(rest) > 0 {
		spaceEnd := len(rest)
		for i, c := range rest {
			if !blank(c.text) {
				spaceEnd = i
				break
			}
		}
		wordEnd := len(rest)
		for i, c := range rest[spaceEnd:] {
			if blank(c.text) {
				wordEnd = spaceEnd + i
				break
			}
		}
		space, word := rest[:spaceEnd], rest[spaceEnd:wordEnd]
		if column > 0 && column+textWidth(space)+textWidth(word) > limit() {
			rows = append(rows, nil)
			column = 0
			space = nil
		}
		for _, c := range append(space[:len(space):len(space)], word...) {
			if column+c.width > limit() && column > 0 {
				rows = append(rows, nil)
				column = 0
			}
			rows[len(rows)-1] = append(rows[len(rows)-1], c)
			column += c.width
		}
		rest = rest[wordEnd:]
	}
	lines := make([]line, len(rows))
	for i, row := range rows {
		var spans []span
		if i > 0 {
			spans = append(spans, hang...)
		}
		start := len(spans)
		for _, c := range row {
			if last := len(spans) - 1; last >= start && spans[last].style == c.style {
				spans[last].text += c.text
			} else {
				spans = append(spans, span{c.text, c.style})
			}
		}
		lines[i] = line{spans: spans, style: l.style}
	}
	return lines
}

// prefixed wraps rows and puts first before the first row and rest, of the
// same width, before the others.
func prefixed(lines []line, rowWidth int, first, rest string) []line {
	rows := wrap(lines, rowWidth-width(first))
	for i, row := range rows {
		prefix := rest
		if i == 0 {
			prefix = first
		}
		if i == 0 || row.width() > 0 {
			rows[i].spans = append([]span{{text: prefix}}, row.spans...)
		}
	}
	return rows
}

// expand replaces tabs with spaces up to the next tab stop.
func expand(text string) string {
	var out strings.Builder
	column := 0
	graphemes(text, func(cluster string, w int) {
		if cluster == "\t" {
			spaces := 8 - column%8
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
			return
		}
		out.WriteString(cluster)
		column += w
	})
	return out.String()
}

// clip escapes control characters and clips the text with `…`.
func clip(text string, maxWidth int) string {
	text = control.Escape(text)
	if width(text) <= maxWidth {
		return text
	}
	var clipped strings.Builder
	used := 0
	full := false
	graphemes(text, func(cluster string, w int) {
		if full || used+w+1 > maxWidth {
			full = true
			return
		}
		clipped.WriteString(cluster)
		used += w
	})
	if maxWidth > 0 {
		clipped.WriteString("…")
	}
	return clipped.String()
}
