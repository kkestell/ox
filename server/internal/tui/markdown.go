package tui

import (
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	goldtext "github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"ox/internal/control"
)

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough, extension.TaskList)).Parser()

// Markdown styles. Headings and code blocks show no markers or fences.
var (
	headingStyle     = bold
	codeStyle        = fg(lightYellow)
	linkStyle        = underlined
	blockquoteStyle  = fg(gray)
	tableHeaderStyle = bold
	tableBorderStyle = fg(dim)
)

// markdown renders text with control characters escaped as unwrapped rows of
// styled spans. s wins over the Markdown styles, so dim text stays dim. Every
// line break is kept: two trailing spaces make a Markdown line break, so typed
// and streamed breaks show as typed instead of joining into one paragraph.
func markdown(text string, s style) []line {
	source := []byte(strings.ReplaceAll(control.Escape(text), "\n", "  \n"))
	w := &markdownWriter{source: source}
	ast.Walk(markdownParser.Parse(goldtext.NewReader(source)), w.visit)
	for i, l := range w.lines {
		for j, span := range l.spans {
			l.spans[j].style = l.style.patch(span.style).patch(s)
		}
		w.lines[i].style = s
	}
	return w.lines
}

// plain renders text with control characters escaped and the ends trimmed as
// unwrapped rows, one per line.
func plain(text string, s style) []line {
	var lines []line
	for _, row := range strings.Split(strings.TrimSpace(control.Escape(text)), "\n") {
		lines = append(lines, styled(row, s))
	}
	return lines
}

// markdownWriter turns a Markdown syntax tree into rows. Block prefixes and
// styles apply to every row a block starts.
type markdownWriter struct {
	source []byte
	lines  []line
	// inlineStyles holds nested inline styles, the active one last.
	inlineStyles []style
	linePrefixes []span
	lineStyles   []style
	// needsNewline is set when the next block starts on a new row.
	needsNewline bool
	// lists holds each open list's next number, or -1 for a bulleted list.
	lists []int
	items []listItem
	links []string
	table *table
}

// listItem is where an open list item's marker row ends, so the first
// paragraph of a loose item stays on its marker row.
type listItem struct {
	markerLine, markerSpans int
}

func (w *markdownWriter) visit(n ast.Node, entering bool) (ast.WalkStatus, error) {
	switch n := n.(type) {
	case *ast.Paragraph:
		if entering {
			w.startParagraph()
		} else {
			w.needsNewline = true
		}
	case *ast.Heading:
		if entering {
			if w.needsNewline {
				w.pushLine(line{})
			}
			w.pushLine(line{style: headingStyle})
			w.needsNewline = false
		} else {
			w.needsNewline = true
		}
	case *ast.Blockquote:
		if entering {
			if w.needsNewline {
				w.pushLine(line{})
				w.needsNewline = false
			}
			w.linePrefixes = append(w.linePrefixes, span{text: ">"})
			w.lineStyles = append(w.lineStyles, blockquoteStyle)
		} else {
			w.linePrefixes = w.linePrefixes[:len(w.linePrefixes)-1]
			w.lineStyles = w.lineStyles[:len(w.lineStyles)-1]
			w.needsNewline = true
		}
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		if entering {
			w.codeBlock(n)
		}
		return ast.WalkSkipChildren, nil
	case *ast.HTMLBlock:
		if entering {
			w.htmlBlock(n)
		}
		return ast.WalkSkipChildren, nil
	case *ast.ThematicBreak:
		if entering {
			if w.needsNewline {
				w.pushLine(line{})
			}
			w.pushLine(styled("---", style{}))
			w.needsNewline = true
		}
	case *ast.List:
		if entering {
			if len(w.lists) == 0 && w.needsNewline {
				w.pushLine(line{})
			}
			next := -1
			if n.IsOrdered() {
				next = n.Start
			}
			w.lists = append(w.lists, next)
		} else {
			w.lists = w.lists[:len(w.lists)-1]
			w.needsNewline = true
		}
	case *ast.ListItem:
		if entering {
			w.startItem()
		} else {
			w.items = w.items[:len(w.items)-1]
		}
	case *extast.TaskCheckBox:
		if entering {
			w.taskMarker(n.IsChecked)
		}
	case *ast.Text:
		if entering {
			value := n.Value(w.source)
			if !n.IsRaw() {
				value = util.UnescapePunctuations(util.ResolveNumericReferences(util.ResolveEntityNames(value)))
			}
			w.text(string(value))
			if n.HardLineBreak() {
				w.pushLine(line{})
			} else if n.SoftLineBreak() {
				w.pushSpan(span{text: " "})
			}
		}
	case *ast.String:
		if entering {
			w.text(string(n.Value))
		}
	case *ast.CodeSpan:
		if !entering {
			return ast.WalkContinue, nil
		}
		var code strings.Builder
		for child := n.FirstChild(); child != nil; child = child.NextSibling() {
			if text, ok := child.(*ast.Text); ok {
				code.Write(text.Value(w.source))
				if text.SoftLineBreak() || text.HardLineBreak() {
					code.WriteByte(' ')
				}
			}
		}
		w.pushSpan(span{code.String(), codeStyle})
		return ast.WalkSkipChildren, nil
	case *ast.Emphasis:
		emphasis := italic
		if n.Level == 2 {
			emphasis = bold
		}
		w.inlineStyle(entering, emphasis)
	case *extast.Strikethrough:
		w.inlineStyle(entering, strikethrough)
	case *ast.Link:
		w.link(entering, string(n.Destination))
	case *ast.AutoLink:
		if entering {
			w.link(true, string(n.URL(w.source)))
			w.text(string(n.Label(w.source)))
			w.link(false, "")
		}
	case *ast.Image:
		if entering {
			w.pushSpan(span{text: "[img] "})
		}
	case *ast.RawHTML:
		if entering {
			for i := range n.Segments.Len() {
				segment := n.Segments.At(i)
				w.pushSpan(span{string(segment.Value(w.source)), w.inline()})
			}
		}
	case *extast.Table:
		if entering {
			w.table = &table{alignments: n.Alignments}
		} else {
			w.endTable()
		}
	case *extast.TableHeader, *extast.TableRow:
		if entering {
			w.table.rows = append(w.table.rows, nil)
		}
	case *extast.TableCell:
		if entering {
			row := &w.table.rows[len(w.table.rows)-1]
			*row = append(*row, nil)
			if _, header := n.Parent().(*extast.TableHeader); header {
				w.inlineStyles = append(w.inlineStyles, w.inline().patch(tableHeaderStyle))
			}
		} else if _, header := n.Parent().(*extast.TableHeader); header {
			w.inlineStyles = w.inlineStyles[:len(w.inlineStyles)-1]
		}
	}
	return ast.WalkContinue, nil
}

func (w *markdownWriter) inline() style {
	if len(w.inlineStyles) == 0 {
		return style{}
	}
	return w.inlineStyles[len(w.inlineStyles)-1]
}

func (w *markdownWriter) inlineStyle(entering bool, s style) {
	if entering {
		w.inlineStyles = append(w.inlineStyles, w.inline().patch(s))
	} else {
		w.inlineStyles = w.inlineStyles[:len(w.inlineStyles)-1]
	}
}

// link underlines the link text and follows it with the destination.
func (w *markdownWriter) link(entering bool, destination string) {
	w.inlineStyle(entering, linkStyle)
	if entering {
		w.links = append(w.links, destination)
		return
	}
	destination = w.links[len(w.links)-1]
	w.links = w.links[:len(w.links)-1]
	w.pushSpan(span{text: " ("})
	w.pushSpan(span{destination, linkStyle})
	w.pushSpan(span{text: ")"})
}

func (w *markdownWriter) startParagraph() {
	// A loose list item's first paragraph stays on the marker row.
	if n := len(w.items); n > 0 {
		item := w.items[n-1]
		if !w.needsNewline && len(w.lines) == item.markerLine+1 && len(w.lines[item.markerLine].spans) == item.markerSpans {
			return
		}
	}
	if w.needsNewline {
		w.pushLine(line{})
	}
	w.pushLine(line{})
	w.needsNewline = false
}

func (w *markdownWriter) startItem() {
	markerLine := len(w.lines)
	w.pushLine(line{})
	columns := len(w.lists)*4 - 3
	var marker string
	if next := &w.lists[len(w.lists)-1]; *next < 0 {
		marker = strings.Repeat(" ", columns-1) + "- "
	} else {
		number := strconv.Itoa(*next)
		marker = strings.Repeat(" ", max(columns-len(number), 0)) + number + ". "
		*next++
	}
	w.pushSpan(span{text: marker})
	w.items = append(w.items, listItem{markerLine, len(w.lines[markerLine].spans)})
	w.needsNewline = false
}

func (w *markdownWriter) taskMarker(checked bool) {
	mark := " "
	if checked {
		mark = "x"
	}
	last := &w.lines[len(w.lines)-1]
	if len(last.spans) > 0 {
		if first := &last.spans[0]; strings.HasSuffix(first.text, "- ") {
			first.text = strings.TrimSuffix(first.text, "- ") + "- [" + mark + "] "
			return
		}
	}
	w.pushSpan(span{text: "[" + mark + "] "})
}

func (w *markdownWriter) codeBlock(n ast.Node) {
	if len(w.lines) > 0 {
		w.pushLine(line{})
	}
	w.lineStyles = append(w.lineStyles, codeStyle)
	w.needsNewline = true
	var code strings.Builder
	for i := range n.Lines().Len() {
		segment := n.Lines().At(i)
		code.Write(segment.Value(w.source))
	}
	w.text(code.String())
	w.needsNewline = true
	w.lineStyles = w.lineStyles[:len(w.lineStyles)-1]
}

func (w *markdownWriter) htmlBlock(n *ast.HTMLBlock) {
	if w.needsNewline {
		w.pushLine(line{})
	}
	w.pushLine(line{})
	w.needsNewline = false
	var html strings.Builder
	for i := range n.Lines().Len() {
		segment := n.Lines().At(i)
		html.Write(segment.Value(w.source))
	}
	for _, row := range rustLines(html.String()) {
		if w.needsNewline {
			w.pushLine(line{})
		}
		w.pushSpan(span{text: row})
		w.needsNewline = true
	}
}

// text writes text in the active inline style, starting a row at each line
// break.
func (w *markdownWriter) text(text string) {
	if w.table != nil {
		w.pushSpan(span{text, w.inline()})
		return
	}
	for i, row := range rustLines(text) {
		if w.needsNewline {
			w.pushLine(line{})
			w.needsNewline = false
		}
		if i > 0 {
			w.pushLine(line{})
		}
		w.pushSpan(span{row, w.inline()})
	}
	w.needsNewline = false
}

// rustLines splits text at line breaks, dropping a final empty line and the
// carriage return before a newline.
func rustLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// pushLine starts a row with the active block style and prefixes.
func (w *markdownWriter) pushLine(l line) {
	if n := len(w.lineStyles); n > 0 {
		l.style = l.style.patch(w.lineStyles[n-1])
	}
	if len(w.linePrefixes) > 0 {
		prefixes := append([]span{}, w.linePrefixes...)
		l.spans = append(append(prefixes, span{text: " "}), l.spans...)
	}
	w.lines = append(w.lines, l)
}

func (w *markdownWriter) pushSpan(s span) {
	if w.table != nil {
		row := w.table.rows[len(w.table.rows)-1]
		cell := &row[len(row)-1]
		*cell = append(*cell, s)
		return
	}
	if len(w.lines) == 0 {
		w.pushLine(line{})
	}
	last := &w.lines[len(w.lines)-1]
	last.spans = append(last.spans, s)
}

// table collects a table's cells, the header row first.
type table struct {
	alignments []extast.Alignment
	rows       [][][]span
}

// endTable writes the table at its natural width with box-drawing borders.
func (w *markdownWriter) endTable() {
	t := w.table
	w.table = nil
	cellWidth := func(cell []span) int { return line{spans: cell}.width() }
	var widths []int
	for _, row := range t.rows {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], cellWidth(cell))
		}
	}
	border := func(left, middle, right string) line {
		var text strings.Builder
		text.WriteString(left)
		for i, w := range widths {
			if i > 0 {
				text.WriteString(middle)
			}
			text.WriteString(strings.Repeat("─", w+2))
		}
		text.WriteString(right)
		return line{spans: []span{{text.String(), tableBorderStyle}}}
	}
	if w.needsNewline {
		w.pushLine(line{})
	}
	w.pushLine(border("┌", "┬", "┐"))
	for r, row := range t.rows {
		var spans []span
		for i, columnWidth := range widths {
			var cell []span
			if i < len(row) {
				cell = row[i]
			}
			padding := columnWidth - cellWidth(cell)
			left := 0
			if i < len(t.alignments) {
				switch t.alignments[i] {
				case extast.AlignRight:
					left = padding
				case extast.AlignCenter:
					left = padding / 2
				}
			}
			spans = append(spans, span{"│", tableBorderStyle}, span{text: " " + strings.Repeat(" ", left)})
			spans = append(spans, cell...)
			spans = append(spans, span{text: strings.Repeat(" ", padding-left) + " "})
		}
		w.pushLine(line{spans: append(spans, span{"│", tableBorderStyle})})
		if r == 0 && len(t.rows) > 1 {
			w.pushLine(border("├", "┼", "┤"))
		}
	}
	w.pushLine(border("└", "┴", "┘"))
	w.needsNewline = true
}
