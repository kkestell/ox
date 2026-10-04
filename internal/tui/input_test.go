package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestShiftEnterInsertsANewlineAndPasteWaitsForEnter(t *testing.T) {
	in := typed("ab")
	*in, _ = in.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	equal(t, in.Value(), "ab", "Enter is left to the client")
	*in, _ = in.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	paste(in, "c\r\nd")
	equal(t, in.Value(), "ab\nc\nd")
}

func TestTheComposerGrowsToEightRowsAndPromptsOnTheFirst(t *testing.T) {
	in := typed("one")
	in.SetWidth(20)
	equal(t, in.Height(), 1)
	equal(t, strings.TrimRight(in.View(), " "), "❯ one")
	in = typed("0\n1\n2\n3\n4\n5\n6\n7\n8\n9")
	in.SetWidth(20)
	equal(t, in.Height(), maxInputRows)
	rows := strings.Split(in.View(), "\n")
	equal(t, strings.TrimRight(rows[len(rows)-1], " "), "  9", "the cursor row stays visible")
	equal(t, in.Cursor().Position.Y, maxInputRows-1)
}

func TestGhostTextCompletesALoneSlashWordAtTheEndOfTheInput(t *testing.T) {
	commands := []string{"compact", "model", "resume"}
	for _, test := range []struct {
		text  string
		atEnd bool
		want  string
	}{
		{"", true, ""}, {"/", true, ""}, {"/mo", true, "del"}, {"/model", true, ""}, {"/model x", true, ""},
		{"/mo", false, ""}, {"\n/mo", true, ""}, {"hi /mo", true, ""}, {"/zzz", true, ""},
	} {
		equal(t, ghostText(test.text, test.atEnd, commands), test.want, test.text)
	}
	overlapping := []string{"model", "model-fast"}
	equal(t, ghostText("/mode", true, overlapping), "l")
	equal(t, ghostText("/model", true, overlapping), "")
	equal(t, atEnd(typed("/mo")), true)
	moved := typed("/mo")
	moved.SetCursorColumn(1)
	equal(t, atEnd(moved), false)
}

func TestUnknownCommandNamesASlashCommandWordMissingFromTheCommands(t *testing.T) {
	commands := []string{"compact", "model"}
	for _, test := range []struct{ text, want string }{
		{"/mo", "/mo"}, {"/modle", "/modle"}, {"/mo more text", "/mo"}, {"  /mo", "/mo"}, {"/mo\nmore", "/mo"},
		{"/model", ""}, {"/model x", ""}, {"/compact now", ""}, {"/", ""}, {"/Users/kyle/x.rs", ""},
		{"/tmp/out", ""}, {"hi /mo", ""}, {"plain text", ""}, {"", ""},
	} {
		equal(t, unknownCommand(test.text, commands), test.want, test.text)
	}
}
