package tui

import (
	"fmt"
	"image/color"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	protocol "github.com/coder/acp-go-sdk"
)

// named is a session update with the tool name Ox adds to it.
type named struct {
	update protocol.SessionUpdate
	name   string
}

func thought(text string) named { return named{update: protocol.UpdateAgentThoughtText(text)} }
func message(text string) named { return named{update: protocol.UpdateAgentMessageText(text)} }
func userChunk(text string) named {
	return named{update: protocol.UpdateUserMessageText(text)}
}

func read(id, path string) named {
	return named{protocol.StartToolCall(protocol.ToolCallId(id), "Read "+path, protocol.WithStartStatus(protocol.ToolCallStatusCompleted)), "read_file"}
}

func shellCall(id, command string, background bool, status protocol.ToolCallStatus) named {
	input := map[string]any{"command": command, "background": background}
	return named{protocol.StartToolCall(protocol.ToolCallId(id), "shell", protocol.WithStartStatus(status), protocol.WithStartRawInput(input)), "shell"}
}

func turnError(id, text string) named {
	content := []protocol.ToolCallContent{protocol.ToolContent(protocol.TextBlock(text))}
	return named{update: protocol.StartToolCall(protocol.ToolCallId(id), "Turn error", protocol.WithStartStatus(protocol.ToolCallStatusCompleted), protocol.WithStartContent(content))}
}

func newTranscript(now time.Time, updates ...named) *transcript {
	t := &transcript{}
	for _, u := range updates {
		t.update(u.update, u.name, now)
	}
	return t
}

// allLines returns every row of the transcript.
func allLines(t *transcript, width int, show bool, output toolOutput, now time.Time) []line {
	_, rows := t.visibleRows(width, show, output, now, math.MaxInt/2)
	return rows
}

// rows returns the text of every row with tool output hidden.
func rows(t *transcript, width int, show bool, now time.Time) []string {
	return texts(allLines(t, width, show, summary, now))
}

func texts(lines []line) []string {
	out := []string{}
	for _, l := range lines {
		out = append(out, l.String())
	}
	return out
}

// colorOf returns a line's color, whether set on the line or its first span.
func colorOf(l line) color.Color {
	if l.style.fg != nil {
		return l.style.fg
	}
	if len(l.spans) > 0 {
		return l.spans[0].style.fg
	}
	return nil
}

func equal(t *testing.T, got, want any, context ...any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s\ngot  %#v\nwant %#v", fmt.Sprint(context...), got, want)
	}
}

func TestTextIsTrimmedAndWrappedByDisplayWidth(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name, text string
		width      int
		want       []string
	}{
		{"surrounding whitespace", "  \n\t hello world \n\n ", 40, []string{"hello world"}},
		{"whitespace only", " \n\t   ", 40, []string{}},
		{"interior blank lines", "one\n\n\ntwo\n", 40, []string{"one", "", "", "two"}},
		{"explicit newlines", "a\nb  \n c", 40, []string{"a", "b", " c"}},
		{"tabs", "a\tb\t\tc", 40, []string{"a       b               c"}},
		{"word wrap", "one two three four", 10, []string{"one two", "three four"}},
		{"long word", "ab abcdefgh", 6, []string{"ab", "abcdef", "gh"}},
		{"display width", "界界 界界界 界界界界", 7, []string{"界界", "界界界", "界界界", "界"}},
		{"joined emoji at boundary", "abcde👨‍👩‍👧‍👦", 6, []string{"abcde", "👨‍👩‍👧‍👦"}},
		{"combining mark at boundary", "abcde é", 6, []string{"abcde", "é"}},
		{"escaped control characters", "a\rb \x1b", 20, []string{`a\rb \u{1b}`}},
	} {
		equal(t, texts(wrap(plain(test.text, style{}), test.width)), test.want, test.name)
	}
	text := "  first line of text\nsecond  "
	user := &transcript{}
	user.user(text, now)
	equal(t, rows(user, 12, false, now), []string{
		"            ",
		" first line ",
		" of text    ",
		" second     ",
		"            ",
	})
	for _, l := range allLines(user, 12, false, summary, now) {
		if l.style.bg != userMessage {
			t.Errorf("%q has background %v", l.String(), l.style.bg)
		}
	}
	equal(t, rows(newTranscript(now, message(text)), 12, false, now), []string{"● first line", "  of text", "  second"})
}

func TestMessagesRenderAsMarkdownWithHangingListAndQuoteRows(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name, text string
		width      int
		want       []string
	}{
		{"line breaks are kept", "one\ntwo\n\nthree", 40, []string{"● one", "  two", "", "  three"}},
		{"headings drop their marker", "# Title\n\nbody", 40, []string{"● Title", "", "  body"}},
		{"wrapped list items hang under the marker", "- one two three four\n  - five six seven\n\n1. eight nine ten", 18, []string{
			"● - one two three",
			"    four",
			"      - five six",
			"        seven",
			"",
			"  1. eight nine",
			"     ten",
		}},
		{"wrapped quotes keep their bar", "> one two three four", 14, []string{"● > one two", "  > three four"}},
		{"code blocks keep their rows without fences", "```\nfn a() {\n    b()\n}\n```", 40, []string{"● fn a() {", "      b()", "  }"}},
		{"escapes and entities", `a \* b &amp; c`, 40, []string{"● a * b & c"}},
		{"task lists", "- [x] done\n- [ ] todo", 40, []string{"● - [x] done", "  - [ ] todo"}},
		{"links keep their destination", "see [docs](https://a.b)", 40, []string{"● see docs (https://a.b)"}},
		{"tables", "| a | bb |\n|---|---:|\n| ccc | d |", 40, []string{
			"● ┌─────┬────┐",
			"  │ a   │ bb │",
			"  ├─────┼────┤",
			"  │ ccc │  d │",
			"  └─────┴────┘",
		}},
	} {
		equal(t, rows(newTranscript(now, message(test.text)), test.width, false, now), test.want, test.name)
	}
	lines := allLines(newTranscript(now, message("**bold** `code`")), 40, false, summary, now)
	equal(t, lines[0].spans, []span{{"● ", style{}}, {"bold", bold}, {" ", style{}}, {"code", fg(lightYellow)}})
	dimmed := allLines(newTranscript(now, thought("**bold** `code`")), 40, true, summary, now)
	for _, span := range dimmed[0].spans[1:] {
		if span.style.fg != dim {
			t.Errorf("%q is not dim", span.text)
		}
	}
	output := named{protocol.StartToolCall("r", "Read a.md", protocol.WithStartStatus(protocol.ToolCallStatusCompleted),
		protocol.WithStartContent([]protocol.ToolCallContent{protocol.ToolContent(protocol.TextBlock("# not a heading"))})), "read_file"}
	view := newTranscript(now, output, turnError("a", "# heading"))
	equal(t, texts(allLines(view, 40, false, full, now)), []string{
		"● Read a.md",
		"  └ # not a heading",
		"",
		"● Turn error",
		"  └ heading",
	}, "named output stays plain and a nameless call is Markdown")
}

func TestChunksOfOneKindJoinOneItemAndTheNextUpdateEndsThinking(t *testing.T) {
	start := time.Now()
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	view := &transcript{}
	view.update(thought("weigh").update, "", at(0))
	equal(t, rows(view, 40, true, at(0)), []string{"● weigh"})
	view.update(thought("ing").update, "", at(1))
	view.update(message("one ").update, "", at(3))
	equal(t, rows(view, 40, true, at(3)), []string{"● weighing", "", "● one"})
	view.update(message("two").update, "", at(3))
	view.update(thought("again").update, "", at(4))
	equal(t, rows(view, 40, true, at(5)), []string{"● weighing", "", "● one two", "", "● again"})
	equal(t, rows(view, 40, false, at(5)), []string{"● Thought for 3s", "", "● one two", "", "● Thinking..."})
	view.endTurn(at(6))
	view.update(thought("next").update, "", at(7))
	equal(t, rows(view, 40, false, at(8)), []string{
		"● Thought for 3s", "", "● one two", "", "● Thought for 2s", "", "● Thinking...",
	})
}

func TestReplayKeepsUserMessagesAndSeparatesTheirResponses(t *testing.T) {
	now := time.Now()
	info := named{update: protocol.SessionUpdate{SessionInfoUpdate: &protocol.SessionSessionInfoUpdate{Title: new("saved")}}}
	view := newTranscript(now, userChunk("title"), info, userChunk("first "), userChunk("question"), message("first answer"),
		userChunk("second question"), message("second answer"))
	blank := strings.Repeat(" ", 40)
	equal(t, rows(view, 40, false, now), []string{
		blank, " title                                  ", blank, "",
		blank, " first question                         ", blank, "",
		"● first answer", "",
		blank, " second question                        ", blank, "",
		"● second answer",
	})
}

func TestReusedToolIDsKeepEachTurnsCallInTheTranscript(t *testing.T) {
	now := time.Now()
	view := newTranscript(now, userChunk("first"), read("same", "first.txt"), userChunk("second"), read("same", "second.txt"))
	equal(t, rows(view, 40, false, now)[10], "● Read second.txt")
	failed := protocol.UpdateToolCall("same", protocol.WithUpdateStatus(protocol.ToolCallStatusFailed))
	view.update(failed, "", now)
	blank := strings.Repeat(" ", 40)
	equal(t, rows(view, 40, false, now), []string{
		blank, " first                                  ", blank, "", "● Read first.txt", "",
		blank, " second                                 ", blank, "", "● Read second.txt",
	})
	lines := allLines(view, 40, false, summary, now)
	equal(t, lines[4].spans[0].style.fg, green)
	equal(t, lines[10].spans[0].style.fg, red)
	view.endTurn(now)
	view.update(read("same", "third.txt").update, "read_file", now)
	all := rows(view, 40, false, now)
	equal(t, all[len(all)-1], "● Read third.txt")
}

func TestThinkingShowsPlaceholdersByElapsedTimeUnlessToggled(t *testing.T) {
	start := time.Now()
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	view := newTranscript(start, thought("weighing it"))
	equal(t, rows(view, 40, false, at(3)), []string{"● Thinking..."})
	equal(t, rows(view, 40, false, at(9)), []string{"● Thinking..."})
	equal(t, rows(view, 40, false, at(12)), []string{"● Thinking for 12s..."})
	equal(t, rows(view, 40, true, at(12)), []string{"● weighing it"})
	view.endTurn(at(42))
	equal(t, rows(view, 40, false, at(60)), []string{"● Thought for 42s"})
	equal(t, rows(view, 40, true, at(60)), []string{"● weighing it"})
	for _, show := range []bool{false, true} {
		equal(t, colorOf(allLines(view, 40, show, summary, at(60))[0]), dim, show)
	}
	equal(t, rows(newTranscript(start, thought("  ")), 40, true, at(3)), []string{"● Thinking..."})
}

func TestVisibleRowsFollowUpdatesResizeAndDisplaySettings(t *testing.T) {
	now := time.Now()
	call := named{protocol.StartToolCall("a", "Read old.txt", protocol.WithStartStatus(protocol.ToolCallStatusCompleted),
		protocol.WithStartContent([]protocol.ToolCallContent{protocol.ToolContent(protocol.TextBlock("alpha beta gamma"))})), "read_file"}
	view := newTranscript(now, call, message("tail"))
	page := func(width int, output toolOutput) (int, []string) {
		total, visible := view.visibleRows(width, false, output, now, 2)
		return total, texts(visible)
	}
	total, visible := page(40, summary)
	equal(t, total, 3)
	equal(t, visible, []string{"", "● tail"})
	view.update(message(" end").update, "", now)
	_, visible = page(40, summary)
	equal(t, visible, []string{"", "● tail end"})
	view.update(protocol.UpdateToolCall("a", protocol.WithUpdateTitle("Read new.txt")), "", now)
	equal(t, rows(view, 40, false, now)[0], "● Read new.txt")
	total, visible = page(40, full)
	equal(t, total, 4)
	equal(t, visible, []string{"", "● tail end"})
	_, all := view.visibleRows(12, false, full, now, 20)
	equal(t, texts(all), []string{"● Read new.…", "  └ alpha", "    beta", "    gamma", "", "● tail end"})
	total, _ = page(40, summary)
	equal(t, total, 3)
}

func TestItemsRenderWithIconsPrefixesAndSpacing(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name    string
		updates []named
		width   int
		want    []string
	}{
		{"one-line tool call", []named{read("a", "Makefile")}, 40, []string{"● Read Makefile"}},
		{"clipped with an ellipsis", []named{read("a", "tallies/2026/september/a.tally")}, 20, []string{"● Read tallies/2026…"}},
		{"nameless tool call with wrapped content", []named{turnError("a", "Fixed the tallies today.")}, 24, []string{
			"● Turn error", "  └ Fixed the tallies", "    today.",
		}},
		{"a blank row between items except between named calls", []named{
			message("Checking"), read("a", "a"), shellCall("s", "ls -la", false, protocol.ToolCallStatusCompleted),
			turnError("c", "Fixed."), read("d", "d"), message("Found it"),
		}, 40, []string{
			"● Checking", "", "● Read a", "● Shell ls -la", "", "● Turn error", "  └ Fixed.", "", "● Read d", "", "● Found it",
		}},
	} {
		equal(t, rows(newTranscript(now, test.updates...), test.width, false, now), test.want, test.name)
	}
	for _, output := range []toolOutput{truncated, full} {
		view := newTranscript(now, read("a", "a"), read("b", "b"))
		equal(t, texts(allLines(view, 40, false, output, now)), []string{"● Read a", "", "● Read b"}, output)
	}
	view := newTranscript(now, turnError("a", "Fixed."))
	view.notice("Turn error: bad", red, now)
	view.notice("stderr line", dim, now)
	lines := allLines(view, 40, false, summary, now)
	equal(t, rows(view, 40, false, now)[2:], []string{"", "Turn error: bad", "", "stderr line"})
	equal(t, colorOf(lines[1]), dim)
	equal(t, colorOf(lines[3]), red)
	equal(t, colorOf(lines[5]), dim)
}

func TestANamedCallShowsItsTextAndDiffBlocksOnlyWhileOutputIsShown(t *testing.T) {
	now := time.Now()
	old := "fn main() {\n    let config = Config::load();\n    run(config)\n}\n"
	changed := "fn main() {\n    let config = Config::load()?;\n    run(config)\n}\n"
	call := named{protocol.StartToolCall("p", "Apply patch to a.rs", protocol.WithStartStatus(protocol.ToolCallStatusCompleted),
		protocol.WithStartContent([]protocol.ToolCallContent{
			protocol.ToolContent(protocol.TextBlock("Modified a.rs")),
			protocol.ToolDiffContent("/w/a.rs", changed, old),
		})), "apply_patch"}
	view := newTranscript(now, call)
	equal(t, rows(view, 32, false, now), []string{"● Apply patch to a.rs"})
	lines := allLines(view, 32, false, full, now)
	equal(t, texts(lines), []string{
		"● Apply patch to a.rs",
		"  └ Modified a.rs",
		"    @@ -1,4 +1,4 @@",
		"     fn main() {",
		"    -    let config = Config::l…",
		"    +    let config = Config::l…",
		"         run(config)",
		"     }",
	})
	var colors []color.Color
	for _, l := range lines[1:] {
		colors = append(colors, colorOf(l))
	}
	equal(t, colors, []color.Color{dim, dim, dim, red, green, dim, dim})
	added := named{protocol.StartToolCall("a", "Apply patch to b", protocol.WithStartStatus(protocol.ToolCallStatusCompleted),
		protocol.WithStartContent([]protocol.ToolCallContent{protocol.ToolDiffContent("/w/b", "one\n\ttwo\n")})), "apply_patch"}
	equal(t, texts(allLines(newTranscript(now, added), 40, false, full, now)), []string{
		"● Apply patch to b",
		"  └ @@ -0,0 +1,2 @@",
		"    +one",
		"    +        two",
	}, "a new file diffs against nothing and tabs are expanded")
}

func TestTruncatedOutputShowsTheHiddenRowCountAndTheLastFiveRows(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		lines int
		want  []string
	}{
		{52, []string{"● Read a.txt", "  └ ...47 more lines", "    line 48", "    line 49", "    line 50", "    line 51", "    line 52"}},
		{6, []string{"● Read a.txt", "  └ line 1", "    line 2", "    line 3", "    line 4", "    line 5", "    line 6"}},
	} {
		var text []string
		for n := 1; n <= test.lines; n++ {
			text = append(text, fmt.Sprintf("line %d", n))
		}
		call := named{protocol.StartToolCall("r", "Read a.txt", protocol.WithStartStatus(protocol.ToolCallStatusCompleted),
			protocol.WithStartContent([]protocol.ToolCallContent{protocol.ToolContent(protocol.TextBlock(strings.Join(text, "\n")))})), "read_file"}
		lines := allLines(newTranscript(now, call), 40, false, truncated, now)
		equal(t, texts(lines), test.want, test.lines)
		first := dim
		if test.lines > 6 {
			first = faint
		}
		equal(t, lines[1].spans[1].style.fg, first, test.lines)
		for _, l := range lines[2:] {
			equal(t, l.spans[1].style.fg, dim, test.lines)
		}
	}
}

func TestShellCallsRenderAsOneClippedRow(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		call  named
		width int
		want  string
	}{
		{shellCall("s", "ls", false, protocol.ToolCallStatusPending), 40, "● Shell ls"},
		{shellCall("s", "rg -n \\\n  --glob '*.rs' \\\n  'TODO|FIXME' src\n", false, protocol.ToolCallStatusInProgress), 40, "● Shell rg -n \\ --glob '*.rs' \\ 'TODO|F…"},
		{shellCall("s", "npm run dev", true, protocol.ToolCallStatusCompleted), 40, "● Shell npm run dev &"},
		{shellCall("s", "echo abcdefghij", false, protocol.ToolCallStatusCompleted), 14, "● Shell echo …"},
		{shellCall("s", "rm -rf x", false, protocol.ToolCallStatusFailed), 40, "● Shell rm -rf x"},
	} {
		equal(t, rows(newTranscript(now, test.call), test.width, false, now), []string{test.want})
	}
	failed := newTranscript(now, shellCall("s", "rm -rf x", false, protocol.ToolCallStatusFailed))
	equal(t, allLines(failed, 40, false, summary, now)[0].spans[0].style.fg, red)
}

func TestTheToolStatusSetsTheIconColor(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		status protocol.ToolCallStatus
		color  color.Color
	}{
		{protocol.ToolCallStatusPending, yellow},
		{protocol.ToolCallStatusInProgress, nil},
		{protocol.ToolCallStatusCompleted, green},
		{protocol.ToolCallStatusFailed, red},
	} {
		call := named{protocol.StartToolCall("a", "Read a", protocol.WithStartStatus(test.status)), "read_file"}
		first := allLines(newTranscript(now, call), 40, false, summary, now)[0]
		equal(t, first.spans[0].text, "●")
		equal(t, first.spans[0].style.fg, test.color, test.status)
	}
}

func TestPagingTurnsFollowingOffAndEndOrTheBottomTurnsItOn(t *testing.T) {
	now := time.Now()
	view := &transcript{}
	height, total := 10, 35
	view.pageUp(height, 5)
	equal(t, view.firstRow(height, 5), 0)
	equal(t, view.scrolled, false, "nothing to scroll")
	view.pageUp(height, total)
	equal(t, view.firstRow(height, total), 16)
	view.update(message("more").update, "", now)
	equal(t, view.newActivity, true)
	view.pageUp(height, total)
	view.pageUp(height, total)
	equal(t, view.firstRow(height, total), 0)
	view.pageDown(height, total)
	equal(t, view.firstRow(height, total), 9)
	equal(t, view.newActivity, true)
	view.pageDown(height, total)
	view.pageDown(height, total)
	equal(t, view.firstRow(height, total), 25)
	equal(t, view.scrolled, false)
	equal(t, view.newActivity, false)
	view.pageUp(height, total)
	view.changed()
	equal(t, view.newActivity, true)
	view.end()
	equal(t, view.scrolled, false)
	equal(t, view.newActivity, false)
	view.update(message("later").update, "", now)
	equal(t, view.newActivity, false)
}
