package tui

import (
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// maxInputRows is the most rows the composer shows.
const maxInputRows = 8

// newInput returns the composer: an unstyled textarea that grows to
// maxInputRows rows and then scrolls, with `❯ ` before its first row.
// Shift+Enter inserts a newline, since Enter submits.
func newInput() textarea.Model {
	in := textarea.New()
	in.ShowLineNumbers = false
	in.CharLimit = 0
	in.MaxWidth = 0
	in.DynamicHeight = true
	in.MinHeight = 1
	in.MaxHeight = maxInputRows
	// The height limits only the rows shown, never the text.
	in.MaxContentHeight = math.MaxInt
	in.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "❯ "
		}
		return "  "
	})
	in.SetStyles(textarea.Styles{})
	in.SetVirtualCursor(false)
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter"))
	in.Focus()
	return in
}

// lineEndings turns carriage returns, alone or before a newline, into newlines.
var lineEndings = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// paste inserts text at the cursor. It goes through the textarea's update,
// which keeps the cursor row in view.
func paste(in *textarea.Model, text string) tea.Cmd {
	var cmd tea.Cmd
	*in, cmd = in.Update(tea.PasteMsg{Content: lineEndings.Replace(text)})
	return cmd
}

// atEnd reports whether the composer's cursor is at the end of its text.
func atEnd(in *textarea.Model) bool {
	lines := strings.Split(in.Value(), "\n")
	return in.Line() == len(lines)-1 && in.Column() == utf8.RuneCountInString(lines[len(lines)-1])
}

// ghostText returns the rest of the first command name that text, one word
// starting with `/` with the cursor at its end, is a strict prefix of.
func ghostText(text string, atEnd bool, commands []string) string {
	word, ok := strings.CutPrefix(text, "/")
	if !ok || word == "" || !atEnd || strings.ContainsFunc(word, unicode.IsSpace) {
		return ""
	}
	for _, command := range commands {
		if rest, ok := strings.CutPrefix(command, word); ok {
			return rest
		}
	}
	return ""
}

// unknownCommand returns the slash command word of text, `/` and a name of
// lowercase letters, digits, and hyphens as the first word of the trimmed
// text, when the name is not one of the commands. Other first words, such as
// paths, are not slash command words.
func unknownCommand(text string, commands []string) string {
	words := strings.Fields(text)
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
