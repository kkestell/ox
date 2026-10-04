package tui

import (
	"slices"
	"strings"
	"unicode"

	"ox/internal/control"
)

// maxInputRows is the most rows the composer shows.
const maxInputRows = 8

// input is the composer's editable text with a byte cursor.
type input struct {
	text   string
	cursor int
}

// inputRows are the visible input rows, each with its prefix, and the
// cursor's row and column among them.
type inputRows struct {
	lines       []string
	row, column int
}

func (in *input) empty() bool { return in.text == "" }

func (in *input) take() string {
	text := in.text
	in.text, in.cursor = "", 0
	return text
}

func (in *input) clear() { in.take() }

func (in *input) insert(text string) {
	in.text = in.text[:in.cursor] + text + in.text[in.cursor:]
	in.cursor += len(text)
	in.nextBoundary()
}

func (in *input) paste(text string) {
	in.insert(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
}

func (in *input) newline() { in.insert("\n") }

// ghostText returns the rest of the first command name that the input, one
// word starting with `/` with the cursor at its end, is a strict prefix of.
func (in *input) ghostText(commands []string) string {
	word, ok := strings.CutPrefix(in.text, "/")
	if !ok || word == "" || in.cursor != len(in.text) || strings.ContainsFunc(word, unicode.IsSpace) {
		return ""
	}
	for _, command := range commands {
		if rest, ok := strings.CutPrefix(command, word); ok {
			return rest
		}
	}
	return ""
}

// unknownCommand returns the input's slash command word, `/` and a name of
// lowercase letters, digits, and hyphens as the first word of the trimmed
// text, when the name is not one of the commands. Other first words, such as
// paths, are not slash command words.
func (in *input) unknownCommand(commands []string) string {
	words := strings.Fields(in.text)
	if len(words) == 0 {
		return ""
	}
	name, ok := strings.CutPrefix(words[0], "/")
	isName := ok && name != "" && strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
	if !isName || slices.Contains(commands, name) {
		return ""
	}
	return words[0]
}

// boundaries returns the byte offsets where the grapheme clusters of text
// start, followed by its length.
func boundaries(text string) []int {
	var offsets []int
	offset := 0
	graphemes(text, func(cluster string, _ int) {
		offsets = append(offsets, offset)
		offset += len(cluster)
	})
	return append(offsets, len(text))
}

// previous returns the start of the cluster before the cursor.
func (in *input) previous() (int, bool) {
	start, found := 0, false
	for _, offset := range boundaries(in.text[:in.cursor]) {
		if offset < in.cursor {
			start, found = offset, true
		}
	}
	return start, found
}

// following returns the end of the cluster after the cursor.
func (in *input) following() (int, bool) {
	if in.cursor == len(in.text) {
		return 0, false
	}
	return in.cursor + boundaries(in.text[in.cursor:])[1], true
}

func (in *input) backspace() {
	if start, ok := in.previous(); ok {
		in.text = in.text[:start] + in.text[in.cursor:]
		in.cursor = start
	}
}

func (in *input) delete() {
	if end, ok := in.following(); ok {
		in.text = in.text[:in.cursor] + in.text[end:]
	}
}

func (in *input) left() {
	if start, ok := in.previous(); ok {
		in.cursor = start
	}
}

func (in *input) right() {
	if end, ok := in.following(); ok {
		in.cursor = end
	}
}

// nextBoundary moves the cursor to the next cluster boundary. Inserted text
// can join the following cluster, as when a base character lands before a
// combining mark, leaving the cursor inside it.
func (in *input) nextBoundary() {
	for _, offset := range boundaries(in.text) {
		if offset >= in.cursor {
			in.cursor = offset
			return
		}
	}
}

func (in *input) home() { in.cursor = in.lineStart() }
func (in *input) end()  { in.cursor = in.lineEnd() }

func (in *input) up() {
	column := in.column()
	start := in.lineStart()
	if start == 0 {
		return
	}
	in.cursor = in.seek(strings.LastIndexByte(in.text[:start-1], '\n')+1, column)
}

func (in *input) down() {
	column := in.column()
	end := in.lineEnd()
	if end == len(in.text) {
		return
	}
	in.cursor = in.seek(end+1, column)
}

func (in *input) lineStart() int {
	return strings.LastIndexByte(in.text[:in.cursor], '\n') + 1
}

func (in *input) lineEnd() int {
	if i := strings.IndexByte(in.text[in.cursor:], '\n'); i >= 0 {
		return in.cursor + i
	}
	return len(in.text)
}

// column returns the cursor's display column within its logical line.
func (in *input) column() int {
	column := 0
	graphemes(in.text[in.lineStart():in.cursor], func(cluster string, _ int) {
		column += width(displayCluster(cluster))
	})
	return column
}

// seek returns the byte offset at the display column in the logical line
// starting at start, or the line's end.
func (in *input) seek(start, column int) int {
	offset, used := start, 0
	done := false
	graphemes(in.text[start:], func(cluster string, _ int) {
		clusterWidth := width(displayCluster(cluster))
		if done || cluster == "\n" || used+clusterWidth > column {
			done = true
			return
		}
		used += clusterWidth
		offset += len(cluster)
	})
	return offset
}

// rows returns the rows to show at width: hard-wrapped by display width, at
// most eight rows, and scrolled to keep the cursor row visible.
func (in *input) rows(rowWidth int) inputRows {
	textWidth := max(rowWidth-2, 1)
	var rows []string
	var current strings.Builder
	column, offset := 0, 0
	cursorRow, cursorColumn := 0, 0
	for i, logical := range strings.Split(in.text, "\n") {
		if i > 0 {
			rows = append(rows, current.String())
			current.Reset()
			column = 0
			offset++
		}
		graphemes(logical, func(cluster string, _ int) {
			shown := displayCluster(cluster)
			clusterWidth := width(shown)
			if column+clusterWidth > textWidth && column > 0 {
				rows = append(rows, current.String())
				current.Reset()
				column = 0
			}
			if offset == in.cursor {
				cursorRow, cursorColumn = len(rows), column
			}
			current.WriteString(shown)
			column += clusterWidth
			offset += len(cluster)
		})
		if offset == in.cursor {
			if column >= textWidth {
				rows = append(rows, current.String())
				current.Reset()
				column = 0
			}
			cursorRow, cursorColumn = len(rows), column
		}
	}
	rows = append(rows, current.String())
	height := min(len(rows), maxInputRows)
	scroll := min(max(cursorRow-(height-1), 0), len(rows)-height)
	lines := make([]string, height)
	for i := range height {
		prefix := "  "
		if scroll+i == 0 {
			prefix = "❯ "
		}
		lines[i] = prefix + rows[scroll+i]
	}
	return inputRows{lines: lines, row: cursorRow - scroll, column: cursorColumn + 2}
}

// displayCluster shows tabs as an arrow and other control characters as their
// escapes.
func displayCluster(cluster string) string {
	var shown strings.Builder
	for _, r := range cluster {
		if r == '\t' {
			shown.WriteString("→")
		} else {
			shown.WriteString(control.Rune(r))
		}
	}
	return shown.String()
}
