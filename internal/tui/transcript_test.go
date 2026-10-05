package tui

import (
	"fmt"
	"image/color"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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
func allLines(t *transcript, width int, show bool, output toolOutput, now time.Time) []string {
	_, rows := t.visibleRows(width, show, output, now, math.MaxInt/2)
	return rows
}

// rows returns the text of every row with tool output hidden.
func rows(t *transcript, width int, show bool, now time.Time) []string {
	return texts(allLines(t, width, show, summary, now))
}

// texts returns the rows without styles or trailing spaces.
func texts(rows []string) []string {
	out := []string{}
	for _, row := range rows {
		out = append(out, strings.TrimRight(ansi.Strip(row), " "))
	}
	return out
}

// colorOf returns the foreground color of a row's first visible character.
func colorOf(row string) color.Color {
	return colorAt(row, len(ansi.Strip(row))-len(strings.TrimLeft(ansi.Strip(row), " ")))
}

// colorAt returns the foreground color of a row's cell at column x.
func colorAt(row string, x int) color.Color {
	buf := newFrame(max(width(row), x+1), 1)
	put(buf, buf.Bounds(), 0, row)
	return exact(buf.CellAt(x, 0).Style.Fg)
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
		equal(t, texts(plain(test.text, test.width)), test.want, test.name)
	}
	text := "  first line of text\nsecond  "
	user := &transcript{}
	user.user(text, now)
	equal(t, rows(user, 12, false, now), []string{"", " first line", " of text", " second", ""})
	for y, row := range allLines(user, 12, false, summary, now) {
		buf := newFrame(12, 1)
		put(buf, buf.Bounds(), 0, row)
		for x := range 12 {
			if bg := exact(buf.CellAt(x, 0).Style.Bg); bg != userMessage {
				t.Errorf("row %d column %d has background %v", y, x, bg)
			}
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
			"    - five six",
			"      seven",
			"",
			"  1. eight nine",
			"     ten",
		}},
		{"a later paragraph in an item hangs under the marker", "- one\n\n  two three four", 14, []string{
			"● - one",
			"",
			"    two three",
			"    four",
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
	cells := func(row string) []*uv.Cell {
		buf := newFrame(40, 1)
		put(buf, buf.Bounds(), 0, row)
		return []*uv.Cell{buf.CellAt(2, 0), buf.CellAt(7, 0)}
	}
	styled := cells(allLines(newTranscript(now, message("**bold** `code`")), 40, false, summary, now)[0])
	equal(t, styled[0].Style.Attrs&uv.AttrBold != 0, true, "bold")
	equal(t, exact(styled[1].Style.Fg), lightYellow, "code")
	for _, cell := range cells(allLines(newTranscript(now, thought("**bold** `code`")), 40, true, summary, now)[0]) {
		equal(t, exact(cell.Style.Fg), dim, "thinking is dim")
	}
	wrapped := allLines(newTranscript(now, message("**one two three**")), 9, false, summary, now)
	equal(t, texts(wrapped), []string{"● one two", "  three"})
	buf := newFrame(9, 1)
	put(buf, buf.Bounds(), 0, wrapped[1])
	equal(t, buf.CellAt(2, 0).Style.Attrs&uv.AttrBold != 0, true, "a wrapped bold span stays bold")
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
	equal(t, rows(view, 40, false, now), []string{
		"", " title", "", "",
		"", " first question", "", "",
		"● first answer", "",
		"", " second question", "", "",
		"● second answer",
	})
}

func TestReusedToolIDsKeepEachTurnsCallInTheTranscript(t *testing.T) {
	now := time.Now()
	view := newTranscript(now, userChunk("first"), read("same", "first.txt"), userChunk("second"), read("same", "second.txt"))
	equal(t, rows(view, 40, false, now)[10], "● Read second.txt")
	failed := protocol.UpdateToolCall("same", protocol.WithUpdateStatus(protocol.ToolCallStatusFailed))
	view.update(failed, "", now)
	equal(t, rows(view, 40, false, now), []string{
		"", " first", "", "", "● Read first.txt", "",
		"", " second", "", "", "● Read second.txt",
	})
	lines := allLines(view, 40, false, summary, now)
	var toolColors []color.Color
	for _, line := range lines {
		if c := colorOf(line); c != nil {
			toolColors = append(toolColors, c)
		}
	}
	equal(t, toolColors, []color.Color{green, red})
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
			"● Checking", "", "● Turn error", "  └ Fixed.", "", "● Found it",
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
	equal(t, colorAt(lines[1], 4), dim)
	equal(t, colorOf(lines[3]), red)
	equal(t, colorOf(lines[5]), dim)
}

func TestHiddenToolLoopUpdatesOneProgressItemAndKeepsTheFinalAnswer(t *testing.T) {
	now := time.Now()
	view := &transcript{}
	descriptions := []string{
		"I’ll inspect the failing test and its setup.",
		"I’ll trace the function the test exercises.",
		"I’ll check the recent changes around that function.",
		"I’ll run the focused test to confirm the failure.",
		"I’ll inspect the error path and compare expected behavior.",
	}
	for index, description := range descriptions {
		view.update(message(description).update, "", now)
		view.update(read(fmt.Sprintf("call-%d", index), fmt.Sprintf("file-%d.go", index)).update, "read_file", now)
	}
	view.update(message("The test fails because the error path returns before updating the result.").update, "", now)
	equal(t, rows(view, 80, false, now), []string{
		"● I’ll inspect the error path and compare expected behavior.",
		"",
		"● The test fails because the error path returns before updating the result.",
	}, "summary mode collapses five tool iterations and preserves the final answer")
	full := texts(allLines(view, 80, false, full, now))
	if !reflect.DeepEqual(full, []string{
		"● I’ll inspect the failing test and its setup.", "", "● Read file-0.go",
		"", "● I’ll trace the function the test exercises.", "", "● Read file-1.go",
		"", "● I’ll check the recent changes around that function.", "", "● Read file-2.go",
		"", "● I’ll run the focused test to confirm the failure.", "", "● Read file-3.go",
		"", "● I’ll inspect the error path and compare expected behavior.", "", "● Read file-4.go",
		"", "● The test fails because the error path returns before updating the result.",
	}) {
		t.Errorf("full mode should retain each description and tool call:\ngot  %#v", full)
	}
}

func TestHiddenToolLoopUsesOneItemForMultipleCallsAndFallsBackToATitle(t *testing.T) {
	now := time.Now()
	multiple := newTranscript(now, message("Checking two files"), read("a", "a.go"), read("b", "b.go"))
	equal(t, rows(multiple, 40, false, now), []string{"● Checking two files"}, "multiple calls share one progress item")
	noDescription := newTranscript(now, read("c", "c.go"))
	equal(t, rows(noDescription, 40, false, now), []string{"● Read c.go"}, "the first tool title is the fallback")
	noText := newTranscript(now, named{update: protocol.UpdateAgentMessageText("")}, read("d", "d.go"))
	equal(t, rows(noText, 40, false, now), []string{"● Read d.go"}, "an empty assistant message uses the fallback")
	emptyIterations := newTranscript(now, read("e", "first.go"), named{update: protocol.SessionUpdate{UsageUpdate: &protocol.SessionUsageUpdate{}}}, read("f", "second.go"))
	equal(t, rows(emptyIterations, 40, false, now), []string{"● Read second.go"}, "a later empty batch replaces the fallback title")
}

func TestHiddenToolLoopReplayAndModeChangesKeepCallRecords(t *testing.T) {
	now := time.Now()
	view := newTranscript(now, message("Checking the source"), read("a", "a.go"), message("Checking the test"), read("b", "a_test.go"))
	equal(t, rows(view, 40, false, now), []string{"● Checking the test"}, "replayed updates collapse like live updates")
	equal(t, texts(allLines(view, 40, false, full, now)), []string{
		"● Checking the source", "", "● Read a.go", "", "● Checking the test", "", "● Read a_test.go",
	}, "switching to full output restores descriptions and calls")
	equal(t, rows(view, 40, false, now), []string{"● Checking the test"}, "switching back restores the persistent summary")
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
	for _, row := range lines[1:] {
		colors = append(colors, colorAt(row, 4))
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
	unterminated := named{protocol.StartToolCall("u", "Apply patch to c", protocol.WithStartStatus(protocol.ToolCallStatusCompleted),
		protocol.WithStartContent([]protocol.ToolCallContent{protocol.ToolDiffContent("/w/c", "two", "one")})), "apply_patch"}
	lines = allLines(newTranscript(now, unterminated), 40, false, full, now)
	equal(t, texts(lines), []string{
		"● Apply patch to c",
		"  └ @@ -1 +1 @@",
		"    -one",
		"    \\ No newline at end of file",
		"    +two",
		"    \\ No newline at end of file",
	}, "a file without a final newline")
	equal(t, colorOf(lines[3]), dim)
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
		equal(t, colorAt(lines[1], 4), first, test.lines)
		for _, row := range lines[2:] {
			equal(t, colorAt(row, 4), dim, test.lines)
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
	equal(t, colorOf(allLines(failed, 40, false, summary, now)[0]), red)
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
		equal(t, colorOf(first), test.color, test.status)
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
