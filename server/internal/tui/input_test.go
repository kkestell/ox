package tui

import "testing"

func typed(text string) *input {
	in := &input{}
	for _, r := range text {
		in.insert(string(r))
	}
	return in
}

func TestEditingKeysMoveTheCursorAndChangeTheText(t *testing.T) {
	in := typed("ab")
	in.left()
	in.insert("x")
	equal(t, in.text, "axb")
	in.home()
	in.delete()
	equal(t, in.text, "xb")
	in.end()
	in.backspace()
	equal(t, in.text, "x")
	in.right()
	in.newline()
	in.insert("界")
	in.insert("y")
	equal(t, in.text, "x\n界y")
	in.up()
	in.insert("!")
	equal(t, in.text, "x!\n界y")
	in.down()
	in.insert("?")
	equal(t, in.text, "x!\n界?y")
	in.end()
	in.up()
	in.insert(".")
	equal(t, in.text, "x!.\n界?y")
	in.down()
	in.down()
	in.insert("$")
	equal(t, in.text, "x!.\n界?$y")
	in.clear()
	equal(t, in.empty(), true)
	in.backspace()
	in.left()
	in.up()
	equal(t, in.take(), "")
}

func TestPasteNormalizesLineEndingsAndWaitsForEnter(t *testing.T) {
	in := typed("ad")
	in.left()
	in.paste("b\r\nc\r")
	equal(t, in.text, "ab\nc\nd")
	in.insert("!")
	equal(t, in.take(), "ab\nc\n!d")
	equal(t, in.empty(), true)
}

func TestGhostTextCompletesALoneSlashWordAtTheEndOfTheInput(t *testing.T) {
	commands := []string{"compact", "model", "resume"}
	moved := typed("/mo")
	moved.left()
	for _, test := range []struct {
		in   *input
		want string
	}{
		{typed(""), ""}, {typed("/"), ""}, {typed("/mo"), "del"}, {typed("/model"), ""}, {typed("/model x"), ""},
		{moved, ""}, {typed("\n/mo"), ""}, {typed("hi /mo"), ""}, {typed("/zzz"), ""},
	} {
		equal(t, test.in.ghostText(commands), test.want, test.in.text)
	}
	overlapping := []string{"model", "model-fast"}
	equal(t, typed("/mode").ghostText(overlapping), "l")
	equal(t, typed("/model").ghostText(overlapping), "")
	in := typed("/mo")
	in.paste(in.ghostText(commands))
	equal(t, in.text, "/model")
}

func TestUnknownCommandNamesASlashCommandWordMissingFromTheCommands(t *testing.T) {
	commands := []string{"compact", "model"}
	for _, test := range []struct{ text, want string }{
		{"/mo", "/mo"}, {"/modle", "/modle"}, {"/mo more text", "/mo"}, {"  /mo", "/mo"}, {"/mo\nmore", "/mo"},
		{"/model", ""}, {"/model x", ""}, {"/compact now", ""}, {"/", ""}, {"/Users/kyle/x.rs", ""},
		{"/tmp/out", ""}, {"hi /mo", ""}, {"plain text", ""}, {"", ""},
	} {
		equal(t, typed(test.text).unknownCommand(commands), test.want, test.text)
	}
}

func TestRowsWrapAtTheWidthAndKeepTheCursorVisible(t *testing.T) {
	in := typed("abcdefgh")
	equal(t, in.rows(6), inputRows{[]string{"❯ abcd", "  efgh", "  "}, 2, 2})
	in.home()
	equal(t, in.rows(6), inputRows{[]string{"❯ abcd", "  efgh"}, 0, 2})
	equal(t, typed("a\tb").rows(20), inputRows{[]string{"❯ a→b"}, 0, 5})
	tall := typed("0\n1\n2\n3\n4\n5\n6\n7\n8\n9")
	rows := tall.rows(20)
	equal(t, len(rows.lines), 8)
	equal(t, rows.lines[0], "  2")
	equal(t, rows.lines[7], "  9")
	equal(t, [2]int{rows.row, rows.column}, [2]int{7, 3})
	for range 9 {
		tall.up()
	}
	rows = tall.rows(20)
	equal(t, rows.lines[0], "❯ 0")
	equal(t, rows.lines[7], "  7")
	equal(t, [2]int{rows.row, rows.column}, [2]int{0, 3})
	equal(t, (&input{}).rows(20), inputRows{[]string{"❯ "}, 0, 2})
}

func TestJoinedEmojiAndCombiningMarksAreSingleEditingUnits(t *testing.T) {
	in := &input{}
	in.paste("a👨‍👩‍👧‍👦éb")
	in.home()
	in.right()
	in.right()
	equal(t, in.rows(12).column, 5)
	in.left()
	in.delete()
	equal(t, in.text, "aéb")
	in.right()
	in.backspace()
	equal(t, in.text, "ab")

	in = &input{}
	in.paste("a👨‍👩‍👧‍👦b\nxy")
	in.up()
	in.insert("!")
	equal(t, in.text, "a!👨‍👩‍👧‍👦b\nxy")
	in.down()
	in.insert("?")
	equal(t, in.text, "a!👨‍👩‍👧‍👦b\nxy?")

	in = &input{}
	in.paste("e")
	in.paste("́")
	in.left()
	equal(t, in.rows(12).column, 2)
	in.right()
	equal(t, in.rows(12).column, 3)
}

func TestRowsKeepJoinedEmojiAndCombiningMarksTogether(t *testing.T) {
	in := &input{}
	in.paste("abcd👨‍👩‍👧‍👦é")
	equal(t, in.rows(8), inputRows{[]string{"❯ abcd👨‍👩‍👧‍👦", "  é"}, 1, 3})
	in.left()
	equal(t, in.rows(8).column, 2)
}
