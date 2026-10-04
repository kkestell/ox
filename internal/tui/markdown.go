package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
	headingStyle     = lipgloss.NewStyle().Bold(true)
	codeStyle        = fg(lightYellow)
	linkStyle        = lipgloss.NewStyle().Underline(true)
	blockquoteStyle  = fg(gray)
	tableHeaderStyle = lipgloss.NewStyle().Bold(true)
	tableBorderStyle = dimStyle
	italicStyle      = lipgloss.NewStyle().Italic(true)
	boldStyle        = lipgloss.NewStyle().Bold(true)
	strikeStyle      = lipgloss.NewStyle().Strikethrough(true)
)

// markdown renders text with control characters escaped as rows width
// columns wide, without blank rows at either end. base wins over the Markdown
// styles, so dim text stays dim and a background covers every cell. Every
// line break is kept: two trailing spaces make a Markdown line break, so typed
// and streamed breaks show as typed instead of joining into one paragraph.
func markdown(text string, base lipgloss.Style, rowWidth int) []string {
	source := []byte(strings.ReplaceAll(expand(control.Escape(text)), "\n", "  \n"))
	w := &markdownWriter{source: source, base: base}
	ast.Walk(markdownParser.Parse(goldtext.NewReader(source)), w.visit)
	var rows []string
	for _, row := range w.rows {
		content := wrap(row.text.String(), rowWidth-width(row.prefix))
		rows = append(rows, prefixed(content, row.prefix, row.hang)...)
	}
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
	return strings.Split(ansi.Wrap(text, max(width, 1), ""), "\n")
}

func blank(row string) bool { return strings.TrimSpace(ansi.Strip(row)) == "" }

// markdownRow is one unwrapped row. Its first wrapped row starts with prefix
// and the others with hang, which is as wide.
type markdownRow struct {
	prefix, hang string
	// style lies under the style of the row's text.
	style lipgloss.Style
	text  strings.Builder
}

// container is an open block quote or list item. Its mark starts the first
// row inside it and rest every later row.
type container struct {
	mark, rest string
	used       bool
	// row is a list item's marker row; -1 for a quote.
	row int
}

// markdownWriter turns a Markdown syntax tree into rows.
type markdownWriter struct {
	source []byte
	base   lipgloss.Style
	rows   []*markdownRow
	// inlineStyles holds nested inline styles, the active one last.
	inlineStyles []lipgloss.Style
	blockStyles  []lipgloss.Style
	containers   []container
	// needsNewline is set when the next block starts on a new row.
	needsNewline bool
	// lists holds each open list's next number, or -1 for a bulleted list.
	lists []int
	links []string
	table *table
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
				w.pushLine()
			}
			w.pushLine()
			w.last().style = headingStyle.Inherit(w.last().style)
			w.needsNewline = false
		} else {
			w.needsNewline = true
		}
	case *ast.Blockquote:
		if entering {
			if w.needsNewline {
				w.pushLine()
				w.needsNewline = false
			}
			mark := w.render("> ", blockquoteStyle)
			w.containers = append(w.containers, container{mark: mark, rest: mark, row: -1})
			w.blockStyles = append(w.blockStyles, blockquoteStyle)
		} else {
			w.containers = w.containers[:len(w.containers)-1]
			w.blockStyles = w.blockStyles[:len(w.blockStyles)-1]
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
				w.pushLine()
			}
			w.pushLine()
			w.write("---", lipgloss.NewStyle())
			w.needsNewline = true
		}
	case *ast.List:
		if entering {
			if len(w.lists) == 0 && w.needsNewline {
				w.pushLine()
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
			w.containers = w.containers[:len(w.containers)-1]
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
				w.pushLine()
			} else if n.SoftLineBreak() {
				w.write(" ", w.inline())
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
		w.write(code.String(), codeStyle.Inherit(w.inline()))
		return ast.WalkSkipChildren, nil
	case *ast.Emphasis:
		emphasis := italicStyle
		if n.Level == 2 {
			emphasis = boldStyle
		}
		w.inlineStyle(entering, emphasis)
	case *extast.Strikethrough:
		w.inlineStyle(entering, strikeStyle)
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
			w.write("[img] ", w.inline())
		}
	case *ast.RawHTML:
		if entering {
			for i := range n.Segments.Len() {
				segment := n.Segments.At(i)
				w.write(string(segment.Value(w.source)), w.inline())
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
			*row = append(*row, "")
			if _, header := n.Parent().(*extast.TableHeader); header {
				w.inlineStyles = append(w.inlineStyles, tableHeaderStyle.Inherit(w.inline()))
			}
		} else if _, header := n.Parent().(*extast.TableHeader); header {
			w.inlineStyles = w.inlineStyles[:len(w.inlineStyles)-1]
		}
	}
	return ast.WalkContinue, nil
}

func (w *markdownWriter) inline() lipgloss.Style {
	if len(w.inlineStyles) == 0 {
		return lipgloss.NewStyle()
	}
	return w.inlineStyles[len(w.inlineStyles)-1]
}

func (w *markdownWriter) inlineStyle(entering bool, s lipgloss.Style) {
	if entering {
		w.inlineStyles = append(w.inlineStyles, s.Inherit(w.inline()))
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
	w.write(" (", w.inline())
	w.write(destination, linkStyle.Inherit(w.inline()))
	w.write(")", w.inline())
}

func (w *markdownWriter) last() *markdownRow { return w.rows[len(w.rows)-1] }

func (w *markdownWriter) startParagraph() {
	// A loose list item's first paragraph stays on the marker row.
	if n := len(w.containers); n > 0 {
		if item := w.containers[n-1]; item.row >= 0 && !w.needsNewline && item.row == len(w.rows)-1 && w.last().text.Len() == 0 {
			return
		}
	}
	if w.needsNewline {
		w.pushLine()
	}
	w.pushLine()
	w.needsNewline = false
}

func (w *markdownWriter) startItem() {
	var marker string
	if next := &w.lists[len(w.lists)-1]; *next < 0 {
		marker = "- "
	} else {
		marker = strconv.Itoa(*next) + ". "
		*next++
	}
	w.containers = append(w.containers, container{
		mark: w.render(marker, lipgloss.NewStyle()), rest: w.render(strings.Repeat(" ", len(marker)), lipgloss.NewStyle()), row: len(w.rows),
	})
	w.pushLine()
	w.needsNewline = false
}

// taskMarker adds a task's box to its item's marker.
func (w *markdownWriter) taskMarker(checked bool) {
	mark := "[ ] "
	if checked {
		mark = "[x] "
	}
	row, item := w.last(), &w.containers[len(w.containers)-1]
	pad := w.render("    ", lipgloss.NewStyle())
	row.prefix += w.render(mark, lipgloss.NewStyle())
	row.hang += pad
	item.rest += pad
}

func (w *markdownWriter) codeBlock(n ast.Node) {
	if len(w.rows) > 0 {
		w.pushLine()
	}
	w.blockStyles = append(w.blockStyles, codeStyle)
	w.needsNewline = true
	var code strings.Builder
	for i := range n.Lines().Len() {
		segment := n.Lines().At(i)
		code.Write(segment.Value(w.source))
	}
	w.text(code.String())
	w.needsNewline = true
	w.blockStyles = w.blockStyles[:len(w.blockStyles)-1]
}

func (w *markdownWriter) htmlBlock(n *ast.HTMLBlock) {
	if w.needsNewline {
		w.pushLine()
	}
	w.pushLine()
	w.needsNewline = false
	var html strings.Builder
	for i := range n.Lines().Len() {
		segment := n.Lines().At(i)
		html.Write(segment.Value(w.source))
	}
	for _, row := range rustLines(html.String()) {
		if w.needsNewline {
			w.pushLine()
		}
		w.write(row, lipgloss.NewStyle())
		w.needsNewline = true
	}
}

// text writes text in the active inline style, starting a row at each line
// break.
func (w *markdownWriter) text(text string) {
	if w.table != nil {
		w.write(text, w.inline())
		return
	}
	for i, row := range rustLines(text) {
		if w.needsNewline {
			w.pushLine()
			w.needsNewline = false
		}
		if i > 0 {
			w.pushLine()
		}
		w.write(row, w.inline())
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

// pushLine starts a row in the active block style, inside the open quotes and
// list items.
func (w *markdownWriter) pushLine() {
	row := &markdownRow{}
	if n := len(w.blockStyles); n > 0 {
		row.style = w.blockStyles[n-1]
	}
	for i := range w.containers {
		c := &w.containers[i]
		if c.used {
			row.prefix += c.rest
		} else {
			row.prefix += c.mark
			c.used = true
		}
		row.hang += c.rest
	}
	w.rows = append(w.rows, row)
}

// render styles text with s under the base style.
func (w *markdownWriter) render(text string, s lipgloss.Style) string {
	return w.base.Inherit(s).Render(text)
}

// write adds text in style s to the open table cell or the last row.
func (w *markdownWriter) write(text string, s lipgloss.Style) {
	if text == "" {
		return
	}
	if w.table != nil {
		row := w.table.rows[len(w.table.rows)-1]
		row[len(row)-1] += w.render(text, s)
		return
	}
	if len(w.rows) == 0 {
		w.pushLine()
	}
	row := w.last()
	row.text.WriteString(w.render(text, s.Inherit(row.style)))
}

// table collects a table's rendered cells, the header row first.
type table struct {
	alignments []extast.Alignment
	rows       [][]string
}

// endTable writes the table at its natural width with box-drawing borders.
func (w *markdownWriter) endTable() {
	t := w.table
	w.table = nil
	var widths []int
	for _, row := range t.rows {
		for i, cell := range row {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], width(cell))
		}
	}
	border := func(left, middle, right string) {
		var text strings.Builder
		text.WriteString(left)
		for i, w := range widths {
			if i > 0 {
				text.WriteString(middle)
			}
			text.WriteString(strings.Repeat("─", w+2))
		}
		text.WriteString(right)
		w.pushLine()
		w.write(text.String(), tableBorderStyle)
	}
	if w.needsNewline {
		w.pushLine()
	}
	border("┌", "┬", "┐")
	for r, row := range t.rows {
		w.pushLine()
		for i, columnWidth := range widths {
			var cell string
			if i < len(row) {
				cell = row[i]
			}
			padding := columnWidth - width(cell)
			left := 0
			if i < len(t.alignments) {
				switch t.alignments[i] {
				case extast.AlignRight:
					left = padding
				case extast.AlignCenter:
					left = padding / 2
				}
			}
			w.write("│", tableBorderStyle)
			w.write(" "+strings.Repeat(" ", left), lipgloss.NewStyle())
			w.last().text.WriteString(cell)
			w.write(strings.Repeat(" ", padding-left)+" ", lipgloss.NewStyle())
		}
		w.write("│", tableBorderStyle)
		if r == 0 && len(t.rows) > 1 {
			border("├", "┼", "┤")
		}
	}
	border("└", "┴", "┘")
	w.needsNewline = true
}
