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
	m := &model{conn: conn, session: client.NewSession(conn, created), input: newInput(), focused: true, layout: layout{height: 10}}
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
	paste(&d.input, text)
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
			if err := d.session.Finished(); err != nil {
				d.t.Fatal(err)
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
	equal(t, d.input.Value() == "", true)
}

// matchNames returns the names of the model picker's matches.
func (d *driver) matchNames() []string {
	var names []string
	for _, index := range d.picker.matches {
		names = append(names, d.picker.models[index].name)
	}
	return names
}

func TestTheModelPickerListsFavoritesFirstInTheSessionsOrder(t *testing.T) {
	d := newDriver(t)
	d.favorites = []string{"openrouter:acme/plain", "missing/model", "openrouter:z-ai/glm-5.3-flash"}
	d.command("/model")
	equal(t, d.matchNames(), []string{"GLM 5.3 Flash", "Plain", "DeepSeek V4.1 Flash", "Muse Spark 1.3 Contributor"})
	equal(t, d.picker.selected, 2, "the current model is selected")
	d.press(tea.KeyLeft)
	equal(t, d.matchNames(), []string{"GLM 5.3 Flash", "Plain", "DeepSeek V4.1 Flash", "Muse Spark 1.3 Contributor"}, "Left does not change the list")
	d.typeText("flash")
	equal(t, d.matchNames(), []string{"GLM 5.3 Flash", "DeepSeek V4.1 Flash"})
	d.press(tea.KeyEnter)
	equal(t, d.picker == nil, true)
	equal(t, d.settings(), "Ask • GLM 5.3 Flash • Default")
}

func TestControlFFavoritesAndUnfavoritesTheSelectedModelAndSavesTheFavorites(t *testing.T) {
	d := newDriver(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "ox/settings.json")
	d.favorites = []string{"missing/model"}
	d.configPath = path
	check := func(favorites, names []string, selected string, context string) {
		t.Helper()
		saved, err := settings.ReadClient(path)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, saved.Favorites, d.favorites, context)
		equal(t, d.favorites, favorites, context)
		equal(t, d.matchNames(), names, context)
		equal(t, d.matchNames()[d.picker.selected], selected, context)
	}
	glm := "openrouter:z-ai/glm-5.3-flash"
	d.command("/model")
	d.press(tea.KeyDown)
	d.press('f', tea.ModCtrl)
	check([]string{"missing/model", glm}, []string{"GLM 5.3 Flash", "DeepSeek V4.1 Flash", "Muse Spark 1.3 Contributor", "Plain"},
		"GLM 5.3 Flash", "favoriting moves the model first and keeps it selected")
	d.press(tea.KeyDown)
	d.press('f', tea.ModCtrl)
	check([]string{"missing/model", glm, openroutertest.DefaultModel}, []string{"DeepSeek V4.1 Flash", "GLM 5.3 Flash", "Muse Spark 1.3 Contributor", "Plain"},
		"DeepSeek V4.1 Flash", "favorites keep the session's order")
	d.press('f', tea.ModCtrl)
	check([]string{"missing/model", glm}, []string{"GLM 5.3 Flash", "DeepSeek V4.1 Flash", "Muse Spark 1.3 Contributor", "Plain"},
		"DeepSeek V4.1 Flash", "unfavoriting moves the model back")
	equal(t, d.input.Value() == "", true)
	d.configPath = directory
	d.press('f', tea.ModCtrl)
	if !strings.HasPrefix(d.picker.err, "Favorite failed: ") {
		t.Errorf("error %q", d.picker.err)
	}
	equal(t, d.favorites, []string{"missing/model", glm})
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
	equal(t, d.input.Value() == "", true)
	d.command("/quit")
	equal(t, d.quit, true)
}

func TestEnterRefusesAnUnknownSlashCommand(t *testing.T) {
	d := newDriver(t)
	d.command("/mo")
	equal(t, d.input.Value(), "/mo")
	equal(t, d.session.Busy, false)
	rows, _, _, _ := render(testScreen(&d.view, &d.input, time.Now()), 40, 10)
	if !contains(rows, "Unknown command /mo") {
		t.Errorf("%q", rows)
	}
}

func TestATurnKeepsPromptsAndResumeInTheComposerUntilItEnds(t *testing.T) {
	d := newDriver(t, hang("running"), openroutertest.Echo())
	d.session.Prompt("running")
	d.awaitMessage()
	for _, text := range []string{"next", "/resume"} {
		d.input.Reset()
		paste(&d.input, text)
		d.press(tea.KeyEnter)
		equal(t, d.input.Value(), text)
		equal(t, d.picker == nil, true)
	}
	d.input.Reset()
	paste(&d.input, "next")
	d.press(tea.KeyEscape)
	if _, ok := d.turn(); !ok {
		t.Error("the cancelled turn failed")
	}
	d.press(tea.KeyEnter)
	equal(t, d.input.Value() == "", true)
	text, _ := d.turn()
	equal(t, text, "you said: next")
}

func TestRequestsDoNotHoldBackKeysOrServerEvents(t *testing.T) {
	d := newDriver(t)
	cmd := d.cycle(protocol.SessionConfigOptionCategoryMode, true, "Mode change failed")
	d.send(tea.KeyPressMsg{Code: 'x', Text: "x"})
	equal(t, d.input.Value(), "x")
	d.send(client.Diagnostic("diagnostic"))
	equal(t, d.view.last().text, "diagnostic")
	d.send(cmd())
	equal(t, d.settings(), "Auto • DeepSeek V4.1 Flash • Default")
}

func TestQuickConfigChangesCycleFromTheShownChoiceAndTheLatestResultWins(t *testing.T) {
	d := newDriver(t)
	var cmds []tea.Cmd
	for range 3 {
		cmds = append(cmds, d.cycle(protocol.SessionConfigOptionCategoryThoughtLevel, true, "Effort change failed"))
	}
	equal(t, d.settings(), "Ask • DeepSeek V4.1 Flash • High")
	msgs := []tea.Msg{cmds[0](), cmds[1](), cmds[2]()}
	d.send(msgs[2])
	d.send(msgs[0])
	equal(t, d.settings(), "Ask • DeepSeek V4.1 Flash • High")
}

func TestAConfigResultForAReplacedSessionIsIgnored(t *testing.T) {
	d := newDriver(t)
	option := selectOption(d.session.ConfigOptions, protocol.SessionConfigOptionCategoryMode)
	applied := false
	msg := d.setConfigOption(option.Id, "auto", func(*model, error) { applied = true })()
	d.command("/new")
	options := d.session.ConfigOptions
	d.send(msg)
	equal(t, applied, false)
	equal(t, d.session.ConfigOptions, options)
}

func TestOpeningASessionHoldsServerEventsUntilItOpens(t *testing.T) {
	d := newDriver(t, openroutertest.Echo())
	d.session.Prompt("first")
	if _, ok := d.turn(); !ok {
		t.Fatal("the turn failed")
	}
	id := d.session.ID
	d.command("/resume")
	cmd := d.choose()
	equal(t, d.opening, true)
	d.press(tea.KeyEnter)
	d.press(tea.KeyEscape)
	if d.picker == nil {
		t.Fatal("Escape closed the picker while the session opened")
	}
	result := cmd()
	// The first replayed event arrives before the result and waits.
	d.send(d.next())
	equal(t, d.held != nil, true)
	equal(t, len(d.view.items), 0)
	_, deliver := d.model.Update(result)
	d.send(deliver())
	// The available commands follow the load's response.
	for {
		event := d.next()
		d.send(event)
		if update, ok := event.(client.Update); ok && update.Update.AvailableCommandsUpdate != nil {
			break
		}
	}
	equal(t, d.session.ID, id)
	equal(t, d.opening, false)
	equal(t, d.view.items[0].kind, userItem)
	equal(t, d.view.items[0].text, "first")
	equal(t, d.view.items[1].text, "you said: first")
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
	equal(t, d.input.Value() == "", true)
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
	equal(t, d.input.Value() == "", true)
}

func TestControlECyclesEffortWithOrWithoutShift(t *testing.T) {
	d := newDriver(t)
	paste(&d.input, "draft")
	for _, test := range []struct {
		code rune
		mod  tea.KeyMod
		want string
	}{{'e', tea.ModCtrl, "Low"}, {'e', tea.ModCtrl | tea.ModShift, "Medium"}, {'E', tea.ModCtrl | tea.ModShift, "High"}} {
		d.press(test.code, test.mod)
		if !strings.HasSuffix(d.settings(), test.want) {
			t.Errorf("%s does not end with %s", d.settings(), test.want)
		}
		equal(t, d.input.Value(), "draft")
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
	if !strings.HasPrefix(d.settings(), "Auto") || d.input.Value() != "/ta" {
		t.Errorf("Shift+Tab: %s, %q", d.settings(), d.input.Value())
	}
	d.press(tea.KeyTab)
	if !strings.HasPrefix(d.settings(), "Auto") || d.input.Value() != "/tally" {
		t.Errorf("Tab with ghost text: %s, %q", d.settings(), d.input.Value())
	}
	d.press(tea.KeyTab)
	if !strings.HasPrefix(d.settings(), "Ask") || d.input.Value() != "/tally" {
		t.Errorf("Tab: %s, %q", d.settings(), d.input.Value())
	}
}

func TestApprovalKeysMoveTheSelectionAndEnterAnswersOnlyWithAnEmptyInput(t *testing.T) {
	d := newDriver(t,
		openroutertest.Shell("true"), openroutertest.Echo(),
		openroutertest.Shell("true"), openroutertest.Echo(),
		openroutertest.Shell("true"), openroutertest.Echo(), openroutertest.Echo(),
	)
	prompt := d.session.Prompt
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
	equal(t, d.session.Permission == nil, false)
	equal(t, d.input.Value(), "hi")
	d.press(tea.KeyEscape)
	d.turn()
	d.press(tea.KeyEnter)
	text, ok = d.turn()
	equal(t, text, "you said: hi")
	equal(t, ok, true)
}
