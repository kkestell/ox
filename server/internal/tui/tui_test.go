package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/client"
	"ox/internal/openroutertest"
	"ox/internal/servertest"
	"ox/internal/settings"
)

func TestMain(m *testing.M) { os.Exit(servertest.Run(m)) }

// driver runs a model against ox-server the way the program does, except that
// it runs each command at once and leaves server events to the test.
type driver struct {
	t *testing.T
	*model
	quit bool
}

func newDriver(t *testing.T, replies ...openroutertest.Reply) *driver {
	t.Helper()
	server, workspace := servertest.Start(t, replies...)
	conn, created, err := client.Start(server, workspace, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	m := &model{conn: conn, session: client.NewSession(conn, created), focused: true, layout: layout{height: 10}}
	return &driver{t: t, model: m}
}

// send updates the model with msg and the messages its commands produce.
func (d *driver) send(msg tea.Msg) {
	d.t.Helper()
	for queue := []tea.Msg{msg}; len(queue) > 0; queue = queue[1:] {
		_, cmd := d.model.Update(queue[0])
		queue = append(queue, d.run(cmd)...)
	}
	if d.err != nil {
		d.t.Fatal(d.err)
	}
}

func (d *driver) run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case nil, tea.RawMsg:
		return nil
	case tea.QuitMsg:
		d.quit = true
		return nil
	case tea.BatchMsg:
		var msgs []tea.Msg
		for _, cmd := range msg {
			msgs = append(msgs, d.run(cmd)...)
		}
		return msgs
	default:
		return []tea.Msg{msg}
	}
}

func (d *driver) press(code rune, mod ...tea.KeyMod) {
	d.t.Helper()
	key := tea.KeyPressMsg{Code: code}
	for _, m := range mod {
		key.Mod |= m
	}
	d.send(key)
}

func (d *driver) typeText(text string) {
	d.t.Helper()
	for _, r := range text {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// command enters a slash command.
func (d *driver) command(text string) {
	d.t.Helper()
	d.input.paste(text)
	d.press(tea.KeyEnter)
}

func (d *driver) settings() string { return settingsSummary(d.session.ConfigOptions) }

// next waits for the next server event.
func (d *driver) next() client.Event {
	d.t.Helper()
	events := make(chan client.Event, 1)
	go func() { events <- d.conn.Next() }()
	select {
	case event := <-events:
		return event
	case <-time.After(10 * time.Second):
		d.t.Fatal("no event within 10 seconds")
		return nil
	}
}

// collectRequest holds the next permission request.
func (d *driver) collectRequest() {
	d.t.Helper()
	for d.session.Permission == nil {
		if permission, ok := d.next().(*client.Permission); ok {
			if err := d.session.Ask(permission); err != nil {
				d.t.Fatal(err)
			}
		}
	}
}

// awaitMessage waits for the first response chunk.
func (d *driver) awaitMessage() {
	d.t.Helper()
	for {
		if update, ok := d.next().(client.Update); ok && update.Update.AgentMessageChunk != nil {
			return
		}
	}
}

// finish waits for the turn to end, holding permission requests, and returns
// the queued prompt it sent.
func (d *driver) finish() string {
	d.t.Helper()
	for {
		switch event := d.next().(type) {
		case *client.Permission:
			if err := d.session.Ask(event); err != nil {
				d.t.Fatal(err)
			}
		case client.Finished:
			queued, err := d.session.Finished()
			if err != nil {
				d.t.Fatal(err)
			}
			return queued
		}
	}
}

// turn collects the turn's response text and reports whether it succeeded.
func (d *driver) turn() (string, bool) {
	d.t.Helper()
	var text strings.Builder
	for {
		switch event := d.next().(type) {
		case client.Update:
			if chunk := event.Update.AgentMessageChunk; chunk != nil {
				text.WriteString(content(chunk.Content))
			}
		case client.Finished:
			if queued, err := d.session.Finished(); queued != "" || err != nil {
				d.t.Fatalf("queued %q, %v", queued, err)
			}
			return text.String(), event.Err == nil
		}
	}
}

func hang(text string) openroutertest.Reply {
	data, _ := json.Marshal(openroutertest.Delta(map[string]any{"role": "assistant", "content": text}, ""))
	return openroutertest.Hang("data: " + string(data) + "\n\n")
}

func TestPickerSearchKeepsRowsContainingEveryWordAndEnterChoosesAMatch(t *testing.T) {
	d := newDriver(t)
	d.command("/model")
	for _, test := range []struct {
		typed string
		want  []int
	}{{"", []int{0, 1, 2, 3}}, {"FLASH glm", []int{1}}, {"nonexistent", nil}} {
		d.picker.query = ""
		d.typeText(test.typed)
		equal(t, d.picker.matches, test.want, test.typed)
	}
	d.press(tea.KeyEnter)
	if d.picker == nil {
		t.Fatal("Enter without a match closed the picker")
	}
	d.picker.query = ""
	d.typeText("glm")
	d.press(tea.KeyEnter)
	equal(t, d.picker == nil, true)
	equal(t, d.settings(), "Ask • GLM 5.3 Flash • Default")
}

func TestModelKeysChooseAModelOrCancel(t *testing.T) {
	d := newDriver(t)
	d.command("/model")
	equal(t, d.picker.selected, 0)
	d.press(tea.KeyDown)
	d.press(tea.KeyEscape)
	equal(t, d.picker == nil, true)
	equal(t, d.settings(), "Ask • DeepSeek V4.1 Flash • Default")
	d.command("/model")
	d.press(tea.KeyDown)
	d.press(tea.KeyEnter)
	equal(t, d.picker == nil, true)
	equal(t, d.settings(), "Ask • GLM 5.3 Flash • Default")
	d.command("/model")
	equal(t, d.picker.selected, 1, "the current model is selected")
	equal(t, d.input.empty(), true)
}

func TestTheModelPickerOpensOnFavoritesAndLeftAndRightSwitchLists(t *testing.T) {
	d := newDriver(t)
	d.favorites = []string{"openrouter:z-ai/glm-5.3-flash", openroutertest.DefaultModel}
	state := func() ([]int, int) { return d.picker.matches, d.picker.selected }
	check := func(matches []int, selected int, context string) {
		t.Helper()
		gotMatches, gotSelected := state()
		equal(t, gotMatches, matches, context)
		equal(t, gotSelected, selected, context)
	}
	d.command("/model")
	check([]int{1, 0}, 1, "opens on Favorites, in the order they were added, with the current model selected")
	d.press(tea.KeyRight)
	check([]int{0, 1, 2, 3}, 0, "Right shows All")
	d.typeText("deep")
	check([]int{0}, 0, "search")
	d.press(tea.KeyLeft)
	check([]int{0}, 0, "the query is kept across a switch")
	for range 4 {
		d.press(tea.KeyBackspace)
	}
	check([]int{1, 0}, 0, "cleared")
	d.press(tea.KeyEnter)
	equal(t, d.picker == nil, true)
	equal(t, d.settings(), "Ask • GLM 5.3 Flash • Default")
}

func TestTheModelPickerShowsOnlyAllWithoutAnOfferedFavorite(t *testing.T) {
	d := newDriver(t)
	for _, favorites := range [][]string{nil, {"missing/model"}} {
		d.favorites = favorites
		d.command("/model")
		d.press(tea.KeyLeft)
		equal(t, d.picker.matches, []int{0, 1, 2, 3}, favorites)
		s := testScreen(&transcript{}, &input{}, time.Now())
		s.picker = d.picker
		rows, _, _, _ := render(s, 40, 8)
		equal(t, rows[2], "    Search", favorites)
		d.press(tea.KeyEscape)
	}
}

func TestControlFFavoritesAndUnfavoritesTheSelectedModelAndSavesTheFavorites(t *testing.T) {
	d := newDriver(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "ox/settings.json")
	d.favorites = []string{"missing/model"}
	d.configPath = path
	check := func(favorites []string, matches []int, selected int, context string) {
		t.Helper()
		saved, err := settings.ReadClient(path)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, saved.Favorites, d.favorites, context)
		equal(t, d.favorites, favorites, context)
		equal(t, d.picker.matches, matches, context)
		equal(t, d.picker.selected, selected, context)
	}
	glm := "openrouter:z-ai/glm-5.3-flash"
	d.command("/model")
	d.press(tea.KeyDown)
	d.press('f', tea.ModCtrl)
	check([]string{"missing/model", glm}, []int{0, 1, 2, 3}, 1, "favoriting keeps the selection")
	d.press(tea.KeyUp)
	d.press('f', tea.ModCtrl)
	d.press(tea.KeyLeft)
	check([]string{"missing/model", glm, openroutertest.DefaultModel}, []int{1, 0}, 0, "favorites")
	d.press('f', tea.ModCtrl)
	check([]string{"missing/model", openroutertest.DefaultModel}, []int{0}, 0, "unfavoriting removes the row from Favorites")
	d.press('f', tea.ModCtrl)
	check([]string{"missing/model"}, []int{0, 1, 2, 3}, 0, "the last offered favorite's removal shows All")
	equal(t, d.input.empty(), true)
	d.configPath = directory
	d.press('f', tea.ModCtrl)
	if !strings.HasPrefix(d.picker.err, "Favorite failed: ") {
		t.Errorf("error %q", d.picker.err)
	}
	equal(t, d.favorites, []string{"missing/model"})
}

func TestResumeKeysCancelOrReloadTheCurrentSession(t *testing.T) {
	d := newDriver(t)
	old := d.session.ID
	d.command("/resume")
	if d.picker == nil {
		t.Fatal("no picker")
	}
	d.press(tea.KeyRight)
	equal(t, d.picker.matches, []int{0})
	d.press(tea.KeyEscape)
	equal(t, d.picker == nil, true)
	equal(t, d.session.ID, old)
	d.command("/resume")
	d.press(tea.KeyEnter)
	equal(t, d.picker == nil, true)
	equal(t, d.session.ID, old)
	equal(t, d.session.Active(), true)
}

func TestNewCommandReplacesTheSessionAndQuitCommandQuits(t *testing.T) {
	d := newDriver(t)
	old := d.session.ID
	d.command("/new")
	equal(t, d.quit, false)
	equal(t, d.session.Active(), true)
	if d.session.ID == old {
		t.Error("the session was not replaced")
	}
	equal(t, d.input.empty(), true)
	d.command("/quit")
	equal(t, d.quit, true)
}

func TestEnterRefusesAnUnknownSlashCommand(t *testing.T) {
	d := newDriver(t)
	d.command("/mo")
	equal(t, d.input.text, "/mo")
	equal(t, d.session.Busy, false)
	rows, _, _, _ := render(testScreen(&d.view, &d.input, time.Now()), 40, 10)
	if !contains(rows, "Unknown command /mo") {
		t.Errorf("%q", rows)
	}
}

func TestTheResumeWaitDoesNotQueueAnotherPrompt(t *testing.T) {
	d := newDriver(t, hang("running"))
	if _, err := d.session.Prompt("running"); err != nil {
		t.Fatal(err)
	}
	d.command("/resume")
	equal(t, d.resumeAfterTurn, true)
	d.command("later")
	equal(t, d.input.text, "later")
	equal(t, d.session.Queued, "")
}

func TestASecondSubmissionStaysInTheComposerWhileAPromptIsQueued(t *testing.T) {
	d := newDriver(t, hang("running"), openroutertest.Echo())
	if _, err := d.session.Prompt("running"); err != nil {
		t.Fatal(err)
	}
	d.awaitMessage()
	now := time.Now()
	d.input.paste("first")
	d.submit(now)
	equal(t, d.input.empty(), true)
	d.input.paste("second")
	d.submit(now)
	equal(t, d.input.text, "second")
	equal(t, d.session.Queued, "first")
	equal(t, d.finish(), "first")
	text, _ := d.turn()
	equal(t, text, "you said: first")
	equal(t, d.input.text, "second")
}

func TestControlTTogglesThinkingAndControlOCyclesToolOutput(t *testing.T) {
	d := newDriver(t)
	for _, test := range []struct {
		key      rune
		thinking bool
		output   toolOutput
	}{{'o', false, truncated}, {'t', true, truncated}, {'o', true, full}, {'o', true, summary}} {
		d.press(test.key, tea.ModCtrl)
		equal(t, d.showThinking, test.thinking, "Ctrl+", string(test.key))
		equal(t, d.toolOutput, test.output, "Ctrl+", string(test.key))
	}
	equal(t, d.input.empty(), true)
}

func TestTabAndShiftTabCycleTheAvailableModes(t *testing.T) {
	d := newDriver(t)
	equal(t, d.settings(), "Ask • DeepSeek V4.1 Flash • Default")
	for _, test := range []struct {
		mod  tea.KeyMod
		want string
	}{
		{0, "Auto • DeepSeek V4.1 Flash • Default"},
		{0, "Ask • DeepSeek V4.1 Flash • Default"},
		{tea.ModShift, "Auto • DeepSeek V4.1 Flash • Default"},
		{tea.ModShift, "Ask • DeepSeek V4.1 Flash • Default"},
	} {
		d.press(tea.KeyTab, test.mod)
		equal(t, d.settings(), test.want)
	}
	equal(t, d.input.empty(), true)
}

func TestControlECyclesEffortWithOrWithoutShift(t *testing.T) {
	d := newDriver(t)
	d.input.paste("draft")
	for _, test := range []struct {
		code rune
		mod  tea.KeyMod
		want string
	}{{'e', tea.ModCtrl, "Low"}, {'e', tea.ModCtrl | tea.ModShift, "Medium"}, {'E', tea.ModCtrl | tea.ModShift, "High"}} {
		d.press(test.code, test.mod)
		if !strings.HasSuffix(d.settings(), test.want) {
			t.Errorf("%s does not end with %s", d.settings(), test.want)
		}
		equal(t, d.input.text, "draft")
	}
}

func TestTheEffortCycleIgnoresMissingOrSingleChoices(t *testing.T) {
	category := protocol.SessionConfigOptionCategoryThoughtLevel
	if _, _, ok := nextChoice(nil, category, true); ok {
		t.Error("cycled without options")
	}
	if _, _, ok := nextChoice([]protocol.SessionConfigOption{selectConfig("effort", "low", category)}, category, true); ok {
		t.Error("cycled one choice")
	}
}

func TestTabInsertsGhostTextAndOtherwiseCyclesTheMode(t *testing.T) {
	d := newDriver(t)
	d.session.Commands = []string{"tally"}
	d.typeText("/ta")
	d.press(tea.KeyTab, tea.ModShift)
	if !strings.HasPrefix(d.settings(), "Auto") || d.input.text != "/ta" {
		t.Errorf("Shift+Tab: %s, %q", d.settings(), d.input.text)
	}
	d.press(tea.KeyTab)
	if !strings.HasPrefix(d.settings(), "Auto") || d.input.text != "/tally" {
		t.Errorf("Tab with ghost text: %s, %q", d.settings(), d.input.text)
	}
	d.press(tea.KeyTab)
	if !strings.HasPrefix(d.settings(), "Ask") || d.input.text != "/tally" {
		t.Errorf("Tab: %s, %q", d.settings(), d.input.text)
	}
}

func TestApprovalKeysMoveTheSelectionAndEnterAnswersOnlyWithAnEmptyInput(t *testing.T) {
	d := newDriver(t,
		openroutertest.Shell("true"), openroutertest.Echo(),
		openroutertest.Shell("true"), openroutertest.Echo(),
		openroutertest.Shell("true"), openroutertest.Echo(),
	)
	prompt := func(text string) {
		t.Helper()
		if _, err := d.session.Prompt(text); err != nil {
			t.Fatal(err)
		}
	}
	prompt("run a command")
	d.collectRequest()
	d.press(tea.KeyDown)
	d.press(tea.KeyDown)
	equal(t, d.approvalSelected, 1)
	d.press(tea.KeyEscape)
	equal(t, d.session.Permission == nil, true)
	equal(t, d.approvalSelected, 0)
	if _, ok := d.turn(); !ok {
		t.Error("the rejected turn failed")
	}
	prompt("run another command")
	d.collectRequest()
	d.press(tea.KeyUp)
	d.press(tea.KeyDown)
	d.press(tea.KeyEnter)
	equal(t, d.session.Permission == nil, true)
	text, ok := d.turn()
	equal(t, text, "you said: run another command")
	equal(t, ok, true)
	prompt("run a third command")
	d.collectRequest()
	d.typeText("hi")
	d.press(tea.KeyEnter)
	equal(t, d.session.Permission == nil, true)
	equal(t, d.session.Queued, "hi")
	equal(t, d.input.empty(), true)
	equal(t, d.finish(), "hi")
	text, ok = d.turn()
	equal(t, text, "you said: hi")
	equal(t, ok, true)
}
