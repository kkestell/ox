package tui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/client"
)

// render draws the screen and returns its rows, the cursor, the layout, and
// the buffer.
func render(s screen, w, h int) ([]string, image.Point, layout, *uv.Buffer) {
	buf := uv.NewBuffer(w, h)
	cursor, l := draw(buf, s)
	var rows []string
	for y := range h {
		var row strings.Builder
		for x := range w {
			row.WriteString(buf.CellAt(x, y).Content)
		}
		rows = append(rows, strings.TrimRight(row.String(), " "))
	}
	return rows, cursor, l, buf
}

func colors(buf *uv.Buffer, x, y int) [2]color.Color {
	cell := buf.CellAt(x, y)
	return [2]color.Color{cell.Style.Fg, cell.Style.Bg}
}

func testScreen(view *transcript, in *input, now time.Time) screen {
	return screen{view: view, input: in, usage: "0% • $0.00", now: now}
}

func permission(name, text string) *client.Permission {
	return &client.Permission{
		SessionID: "session",
		ToolCall: protocol.ToolCallUpdate{
			ToolCallId: "call-1", Status: new(protocol.ToolCallStatusPending),
			Content: []protocol.ToolCallContent{protocol.ToolContent(protocol.TextBlock(text))},
		},
		Name: name,
		Options: []protocol.PermissionOption{
			{OptionId: "approve", Name: "Yes", Kind: protocol.PermissionOptionKindAllowOnce},
			{OptionId: "deny", Name: "No", Kind: protocol.PermissionOptionKindRejectOnce},
		},
	}
}

func contains(rows []string, text string) bool {
	for _, row := range rows {
		if strings.Contains(row, text) {
			return true
		}
	}
	return false
}

func TestMouseWheelMovesTheTranscriptOneRowAtATime(t *testing.T) {
	m := &model{layout: layout{height: 10, lines: 35}}
	wheel := func(button tea.MouseButton, y int) { m.mouse(tea.Mouse{X: 4, Y: y, Button: button}) }
	wheel(tea.MouseWheelUp, 3)
	equal(t, m.view.firstRow(10, 35), 24)
	wheel(tea.MouseWheelUp, 3)
	equal(t, m.view.firstRow(10, 35), 23)
	m.view.changed()
	equal(t, m.view.newActivity, true)
	wheel(tea.MouseWheelDown, 3)
	equal(t, m.view.firstRow(10, 35), 24)
	wheel(tea.MouseWheelDown, 3)
	equal(t, m.view.firstRow(10, 35), 25)
	equal(t, m.view.newActivity, false)
	wheel(tea.MouseWheelUp, 12)
	equal(t, m.view.firstRow(10, 35), 25, "the wheel below the transcript does nothing")
	m.picker = newSessionPicker(nil)
	wheel(tea.MouseWheelUp, 3)
	equal(t, m.view.firstRow(10, 35), 25, "the wheel does nothing in a picker")
}

func TestTheFramePlacesTheTranscriptTheApprovalDialogAndTheComposer(t *testing.T) {
	start := time.Now()
	now := start.Add(20 * time.Second)
	view := &transcript{}
	view.user("Run the tests!", start)
	view.update(thought("weighing").update, "", start)
	for _, call := range []struct{ id, title, name string }{{"read", "Read Makefile", "read_file"}, {"glob", "Find files matching *.rs", "glob"}} {
		view.update(protocol.StartToolCall(protocol.ToolCallId(call.id), call.title, protocol.WithStartStatus(protocol.ToolCallStatusCompleted)), call.name, start.Add(12*time.Second))
	}
	shell := protocol.StartToolCall("shell-0", "ls -la", protocol.WithStartStatus(protocol.ToolCallStatusCompleted), protocol.WithStartRawInput(map[string]any{"command": "ls -la"}))
	view.update(shell, "shell", now)
	view.update(message("Two tallies were counted in the workspace.").update, "", now)
	in := &input{}
	in.paste("Also check the docs\nwhen you are done")
	s := testScreen(view, in, now)
	s.approval = permission("shell", "Working directory: /workspace\n\nCommand:\n\n    cargo test")
	s.settings = "ask • deepseek/deepseek-v4-flash • high"
	s.usage = "5% • $0.01"
	rows, cursor, l, buf := render(s, 72, 27)
	equal(t, rows, []string{
		"",
		"  ● Thought for 12s",
		"",
		"  ● Read Makefile",
		"  ● Find files matching *.rs",
		"  ● Shell ls -la",
		"",
		"  ● Two tallies were counted in the workspace.",
		"",
		"",
		"  Would you like to run the following command?",
		"",
		"    Working directory: /workspace",
		"",
		"    Command:",
		"",
		"        cargo test",
		"",
		"  › 1. Yes",
		"    2. No",
		"",
		"",
		"  ❯ Also check the docs",
		"    when you are done",
		"",
		fmt.Sprintf("  %-58s5%% • $0.01", "ask • deepseek/deepseek-v4-flash • high"),
		"",
	})
	equal(t, cursor, image.Pt(21, 23))
	equal(t, [2]int{l.height, l.lines}, [2]int{7, 11})
	equal(t, colors(buf, 0, 8), [2]color.Color{textColor, background})
	equal(t, colors(buf, 0, 9), [2]color.Color{textColor, approval})
	equal(t, colors(buf, 8, 16), [2]color.Color{textColor, approval})
	equal(t, colors(buf, 4, 22), [2]color.Color{textColor, composer})
}

func TestALongApprovalKeepsTheOptionsAndComposerVisibleAndPagesThroughDetails(t *testing.T) {
	var details []string
	for n := range 24 {
		details = append(details, fmt.Sprintf("detail %02d", n))
	}
	m := &model{session: &client.Session{Permission: permission("shell", strings.Join(details, "\n"))}}
	m.input.paste("draft")
	now := time.Now()
	show := func() []string {
		s := m.screen(now)
		s.settings, s.usage = "", "0% • $0.00"
		rows, cursor, l, _ := render(s, 80, 24)
		m.layout = l
		if cursor.Y >= 24 {
			t.Errorf("cursor %v is off the screen", cursor)
		}
		return rows
	}
	rows := show()
	for _, text := range []string{"detail 00", "Page Down for more", "› 1. Yes", "❯ draft"} {
		if !contains(rows, text) {
			t.Errorf("missing %q:\n%s", text, strings.Join(rows, "\n"))
		}
	}
	pgDown := tea.KeyPressMsg{Code: tea.KeyPgDown}
	pgUp := tea.KeyPressMsg{Code: tea.KeyPgUp}
	m.key(pgDown, now)
	m.key(pgDown, now)
	rows = show()
	for _, text := range []string{"detail 23", "Page Up for earlier", "› 1. Yes"} {
		if !contains(rows, text) {
			t.Errorf("missing %q:\n%s", text, strings.Join(rows, "\n"))
		}
	}
	m.key(pgUp, now)
	m.key(pgUp, now)
	if rows = show(); !contains(rows, "detail 00") {
		t.Errorf("missing the first detail:\n%s", strings.Join(rows, "\n"))
	}
}

func TestAShellProcessApprovalShowsItsContentWithoutTheToolLine(t *testing.T) {
	lines := approvalLines(&transcript{}, permission("shell_process", "Shell process: p-1\n\nInput:\n\n    y"), 0, 40)
	equal(t, texts(lines), []string{
		"Would you like to send the following input?",
		"",
		"  Shell process: p-1",
		"",
		"  Input:",
		"",
		"      y",
		"",
		"› 1. Yes",
		"  2. No",
	})
}

func TestTheNewActivityNoticeCoversTheLastRowOnlyWhileFollowingIsOff(t *testing.T) {
	now := time.Now()
	view := &transcript{}
	for n := range 20 {
		view.user(fmt.Sprintf("message %d", n), now)
	}
	in := &input{}
	rows, _, l, _ := render(testScreen(view, in, now), 40, 13)
	equal(t, [2]int{l.height, l.lines}, [2]int{6, 79})
	equal(t, rows[5], "   message 19")
	view.pageUp(l.height, l.lines)
	rows, _, _, _ = render(testScreen(view, in, now), 40, 13)
	equal(t, rows[2], "   message 17")
	equal(t, rows[6], "   message 18")
	view.user("message 20", now)
	rows, _, _, buf := render(testScreen(view, in, now), 40, 13)
	equal(t, rows[:6], []string{"", "", "   message 17", "", "", ""})
	equal(t, rows[6], "              new activity")
	equal(t, buf.CellAt(14, 6).Style.Fg, lightYellow)
	view.end()
	rows, _, _, _ = render(testScreen(view, in, now), 40, 13)
	equal(t, rows[5], "   message 20")
}

func TestTheTranscriptKeepsGraphemesAtRowBoundaries(t *testing.T) {
	now := time.Now()
	for _, test := range []struct{ message, want string }{{"abcde👨‍👩‍👧‍👦", "👨‍👩‍👧‍👦"}, {"abcde e\u0301", "e\u0301"}} {
		view := newTranscript(now, message(test.message))
		_, _, _, buf := render(testScreen(view, &input{}, now), 12, 10)
		equal(t, buf.CellAt(4, 2).Content, test.want, test.message)
	}
}

func TestGhostTextIsDrawnDimAfterTheCursor(t *testing.T) {
	in := &input{}
	in.paste("/mo")
	s := testScreen(&transcript{}, in, time.Now())
	s.commands = []string{"model"}
	rows, cursor, _, buf := render(s, 20, 6)
	equal(t, rows[2], "  ❯ /model")
	equal(t, cursor, image.Pt(7, 2))
	for x := 4; x < 9; x++ {
		want := textColor
		if x >= 7 {
			want = dim
		}
		equal(t, buf.CellAt(x, 2).Style.Fg, want, "column ", x)
	}
}

func selectConfig(id, value string, category protocol.SessionConfigOptionCategory) protocol.SessionConfigOption {
	choices := protocol.SessionConfigSelectOptionsUngrouped{{Value: protocol.SessionConfigValueId(value), Name: value}}
	option := &protocol.SessionConfigOptionSelect{
		Id: protocol.SessionConfigId(id), Name: id, Type: "select", CurrentValue: protocol.SessionConfigValueId(value),
		Options: protocol.SessionConfigSelectOptions{Ungrouped: &choices},
	}
	if category != "" {
		option.Category = &category
	}
	return protocol.SessionConfigOption{Select: option}
}

func TestTheStatusLineShowsSettingsOnTheLeftAndUsageOnTheRight(t *testing.T) {
	options := []protocol.SessionConfigOption{
		selectConfig("model", "deepseek", protocol.SessionConfigOptionCategoryModel),
		selectConfig("effort", "high", protocol.SessionConfigOptionCategoryThoughtLevel),
		selectConfig("pace", "steady", ""),
		selectConfig("approval", "ask", protocol.SessionConfigOptionCategoryMode),
	}
	equal(t, settingsSummary(options), "ask • deepseek • high")
	equal(t, settingsSummary(options[:1]), "deepseek")
	equal(t, settingsSummary(options[2:3]), "")
	equal(t, usageSummary(nil), "0% • $0.00")
	equal(t, usageSummary(&protocol.SessionUsageUpdate{Used: 1200, Size: 8000, Cost: &protocol.Cost{Amount: 0.25, Currency: "USD"}}), "15% • $0.25")
	equal(t, usageSummary(&protocol.SessionUsageUpdate{Used: 2, Size: 3}), "67% • $0.00")
	s := testScreen(&transcript{}, &input{}, time.Now())
	s.settings, s.usage = "ask • deepseek • high", "15% • $0.25"
	rows, cursor, _, buf := render(s, 40, 7)
	equal(t, rows[5], fmt.Sprintf("  %-25s15%% • $0.25", "ask • deepseek • high"))
	equal(t, buf.CellAt(2, 5).Style.Fg, gray)
	equal(t, rows[6], "")
	equal(t, rows[3], "  ❯")
	equal(t, cursor, image.Pt(4, 3))
}

func sessionInfo(id, title, updated string) protocol.SessionInfo {
	info := protocol.SessionInfo{SessionId: protocol.SessionId(id), Cwd: "/tmp", Title: &title}
	if updated != "" {
		info.UpdatedAt = &updated
	}
	return info
}

func TestTheSessionPickerSortsDatesAndKeepsTheSelectionVisible(t *testing.T) {
	p := newSessionPicker([]protocol.SessionInfo{
		sessionInfo("a", "older", "2026-09-28T00:30:00+05:00"),
		sessionInfo("b", "newer", "2026-09-27T23:00:00Z"),
		sessionInfo("c", "undated", ""),
	})
	var ids []protocol.SessionId
	for _, session := range p.sessions {
		ids = append(ids, session.SessionId)
	}
	equal(t, ids, []protocol.SessionId{"b", "a", "c"})
	s := testScreen(&transcript{}, &input{}, time.Now())
	s.picker = p
	rows, _, l, _ := render(s, 40, 7)
	equal(t, rows[4], fmt.Sprintf("    %-22s2026-09-27", "newer"))
	p.moveTo(2, l.height)
	rows, _, _, _ = render(s, 40, 7)
	if !contains(rows, "undated") || !contains(rows, "Unknown date") {
		t.Errorf("%q", rows)
	}
}

func modelChoiceWith(value, name string, meta map[string]any) modelChoice {
	return newModelChoice(protocol.SessionConfigSelectOption{Value: protocol.SessionConfigValueId(value), Name: name, Meta: meta})
}

func TestModelPickerRowsAlignProviderAndPriceColumnsAndLeaveInvalidMetadataBlank(t *testing.T) {
	models := []modelChoice{
		modelChoiceWith("flash", "DeepSeek: DeepSeek V4.1 Flash", map[string]any{"provider": "OpenRouter", "inputPrice": 0.03, "outputPrice": 0.6, "contextLimit": 1048576.0}),
		modelChoiceWith("opus", "Anthropic: Claude Opus 5.5 with a much longer name", map[string]any{"provider": "OpenAI", "inputPrice": 4.0, "outputPrice": 20.0, "contextLimit": 200000.0}),
		modelChoiceWith("other", "Other server model", map[string]any{"provider": 7.0, "inputPrice": "free", "contextLimit": 1.5}),
		modelChoiceWith("missing", "Missing provider", map[string]any{"inputPrice": 1.5, "contextLimit": 8001.0}),
	}
	p := newModelPicker(newModelRows(models, nil))
	p.moveTo(1, 4)
	s := testScreen(&transcript{}, &input{}, time.Now())
	s.picker = p
	rows, cursor, _, _ := render(s, 68, 13)
	equal(t, rows, []string{
		"",
		"",
		"    Search",
		"",
		"    DeepSeek: DeepSeek V…  OpenRouter  $0.03   $0.60   1,048,576",
		"    Anthropic: Claude Op…  OpenAI      $4.00   $20.00    200,000",
		"    Other server model",
		"    Missing provider                   $1.50               8,001",
		"", "", "", "", "",
	}, "the picker hides the composer")
	equal(t, cursor, image.Pt(4, 2), "the cursor starts on the search placeholder")
}

func TestTheModelPickerSearchRowShowsTheActiveListInWhiteAndAllShowsFavoritesInBold(t *testing.T) {
	models := []modelChoice{modelChoiceWith("flash", "Flash", nil), modelChoiceWith("opus", "Opus", nil)}
	p := newModelPicker(newModelRows(models, []string{"opus"}))
	for _, test := range []struct {
		showingFavorites bool
		all, favorites   color.Color
		shown            []string
		bold             [2]bool
	}{
		{false, bright, dim, []string{"    Flash", "    Opus"}, [2]bool{false, true}},
		{true, dim, bright, []string{"    Opus", ""}, [2]bool{false, false}},
	} {
		p.models.showingFavorites = test.showingFavorites
		p.filter()
		s := testScreen(&transcript{}, &input{}, time.Now())
		s.picker = p
		rows, cursor, _, buf := render(s, 40, 8)
		equal(t, rows[2], fmt.Sprintf("    %-17sFavorites / All", "Search"))
		equal(t, cursor, image.Pt(4, 2))
		equal(t, []color.Color{buf.CellAt(21, 2).Style.Fg, buf.CellAt(31, 2).Style.Fg, buf.CellAt(33, 2).Style.Fg},
			[]color.Color{test.favorites, dim, test.all}, "showing favorites ", test.showingFavorites)
		equal(t, rows[4:6], test.shown)
		isBold := func(y int) bool { return buf.CellAt(4, y).Style.Attrs&uv.AttrBold != 0 }
		equal(t, [2]bool{isBold(4), isBold(5)}, test.bold, "showing favorites ", test.showingFavorites)
	}
}

func TestAPickerScrollsAPreselectedRowIntoView(t *testing.T) {
	var sessions []protocol.SessionInfo
	for n := range 10 {
		sessions = append(sessions, sessionInfo(fmt.Sprint(n), fmt.Sprintf("s%d", n), ""))
	}
	p := newSessionPicker(sessions)
	p.selected = 9
	p.moveTo(p.selected, pickerRows(12))
	s := testScreen(&transcript{}, &input{}, time.Now())
	s.picker = p
	if rows, _, _, _ := render(s, 40, 12); !contains(rows, "s9") {
		t.Errorf("%q", rows)
	}
}

func TestAPickerDrawsTheSelectedRowInWhiteAndTheOthersInGray(t *testing.T) {
	p := newSessionPicker([]protocol.SessionInfo{sessionInfo("a", "older", ""), sessionInfo("b", "newer", "")})
	p.moveTo(1, 2)
	s := testScreen(&transcript{}, &input{}, time.Now())
	s.picker = p
	_, _, _, buf := render(s, 40, 10)
	equal(t, buf.CellAt(4, 5).Style.Fg, bright)
	equal(t, buf.CellAt(4, 4).Style.Fg, gray)
	for _, point := range []image.Point{{0, 0}, {39, 9}, {20, 9}} {
		equal(t, buf.CellAt(point.X, point.Y).Style.Bg, background)
	}
}
