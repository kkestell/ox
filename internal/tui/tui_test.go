package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	protocol "github.com/coder/acp-go-sdk"

	"ox/internal/acp"
	"ox/internal/client"
	"ox/internal/openroutertest"
	"ox/internal/settings"
)

// agent is a scripted ACP server. It answers session requests from its
// fields and each prompt with the next reply.
type agent struct {
	conn     *acp.Conn
	options  []protocol.SessionConfigOption
	sessions []protocol.SessionInfo
	// replay is the updates a session load sends before its response.
	replay  []protocol.SessionUpdate
	replies []reply
	// cancels closes when the running prompt is cancelled.
	cancels chan struct{}
}

// turn is one prompt as a reply sees it. send delivers a session update, ask
// sends a permission request and returns the chosen option ID, or "" when
// the request was cancelled, and cancelled closes when the prompt is
// cancelled.
type turn struct {
	text      string
	send      func(protocol.SessionUpdate)
	ask       func() string
	cancelled <-chan struct{}
}

// reply answers one prompt.
type reply func(turn)

func echo(t turn) { t.send(protocol.UpdateAgentMessageText("you said: " + t.text)) }

func hang(t turn) { <-t.cancelled }

// ask sends one permission request and then runs then.
func ask(then reply) reply {
	return func(t turn) {
		t.ask()
		then(t)
	}
}

func (a *agent) HandleRequest(r *acp.Request) {
	switch r.Method {
	case acp.MethodInitialize:
		r.Respond(protocol.InitializeResponse{ProtocolVersion: acp.ProtocolVersion, AgentCapabilities: protocol.AgentCapabilities{
			LoadSession:         true,
			SessionCapabilities: protocol.SessionCapabilities{List: &protocol.SessionListCapabilities{}, Close: &protocol.SessionCloseCapabilities{}},
		}})
	case acp.MethodNewSession:
		var params protocol.NewSessionRequest
		r.Params(&params)
		id := protocol.SessionId(fmt.Sprintf("session-%d", len(a.sessions)+1))
		a.sessions = append(a.sessions, protocol.SessionInfo{SessionId: id, Cwd: params.Cwd})
		r.Respond(protocol.NewSessionResponse{SessionId: id, ConfigOptions: a.options})
	case acp.MethodLoadSession:
		var params protocol.LoadSessionRequest
		r.Params(&params)
		for _, update := range a.replay {
			a.send(params.SessionId, update)
		}
		r.Respond(protocol.LoadSessionResponse{ConfigOptions: a.options})
		a.send(params.SessionId, protocol.SessionUpdate{AvailableCommandsUpdate: &protocol.SessionAvailableCommandsUpdate{
			SessionUpdate: "available_commands_update", AvailableCommands: []protocol.AvailableCommand{},
		}})
	case acp.MethodSetConfigOption:
		var params protocol.SetSessionConfigOptionValueId
		r.Params(&params)
		a.options = chosen(a.options, params.ConfigId, params.Value)
		r.Respond(protocol.SetSessionConfigOptionResponse{ConfigOptions: a.options})
	case acp.MethodListSessions:
		r.Respond(protocol.ListSessionsResponse{Sessions: a.sessions})
	case acp.MethodCloseSession:
		r.Respond(protocol.CloseSessionResponse{})
	case acp.MethodPrompt:
		var params protocol.PromptRequest
		r.Params(&params)
		if len(a.replies) == 0 {
			r.Fail(&acp.Error{Code: -32603, Message: "no scripted reply"})
			return
		}
		reply := a.replies[0]
		a.replies = a.replies[1:]
		cancelled := make(chan struct{})
		a.cancels = cancelled
		t := turn{
			text:      params.Prompt[0].Text.Text,
			send:      func(update protocol.SessionUpdate) { a.send(params.SessionId, update) },
			ask:       func() string { return a.ask(params.SessionId) },
			cancelled: cancelled,
		}
		// The reply runs apart from the connection, which must keep reading
		// answers and cancellations.
		go func() {
			reply(t)
			stop := protocol.StopReasonEndTurn
			select {
			case <-cancelled:
				stop = protocol.StopReasonCancelled
			default:
			}
			r.Respond(protocol.PromptResponse{StopReason: stop})
		}()
	default:
		r.Fail(&acp.Error{Code: -32601, Message: "Method not found"})
	}
}

func (a *agent) HandleNotification(method string, _ json.RawMessage) {
	if method == acp.MethodCancel && a.cancels != nil {
		close(a.cancels)
		a.cancels = nil
	}
}

func (*agent) Shutdown() {}

func (a *agent) send(id protocol.SessionId, update protocol.SessionUpdate) {
	a.conn.Notify(acp.MethodUpdate, acp.SessionNotification{SessionID: string(id), Update: update})
}

func (a *agent) ask(id protocol.SessionId) string {
	p := permission("shell", "true")
	request := acp.RequestPermissionRequest{
		RequestPermissionRequest: protocol.RequestPermissionRequest{SessionId: id, Options: p.Options},
		ToolCall:                 acp.PermissionToolCall{ToolCallUpdate: p.ToolCall, Name: p.Name},
	}
	var response protocol.RequestPermissionResponse
	if a.conn.Call(context.Background(), acp.MethodRequestPermission, request, &response) != nil || response.Outcome.Selected == nil {
		return ""
	}
	return string(response.Outcome.Selected.OptionId)
}

// configOptions returns the options of a new session on openroutertest's
// default model in Ask mode, with the model metadata the server sends.
func configOptions() []protocol.SessionConfigOption {
	models := selectConfig("model", openroutertest.DefaultModel, protocol.SessionConfigOptionCategoryModel)
	efforts := selectConfig("effort", "default", protocol.SessionConfigOptionCategoryThoughtLevel)
	modes := selectConfig("mode", "ask", protocol.SessionConfigOptionCategoryMode)
	catalog := openroutertest.ParsedCatalog()
	var modelChoices, effortChoices protocol.SessionConfigSelectOptionsUngrouped
	for _, model := range catalog {
		modelChoices = append(modelChoices, protocol.SessionConfigSelectOption{
			Value: protocol.SessionConfigValueId(model.QualifiedID()), Name: model.Name,
			Meta: map[string]any{"contextLimit": model.ContextLimit, "inputPrice": model.InputPrice, "outputPrice": model.OutputPrice, "provider": "OpenRouter"},
		})
	}
	for _, effort := range catalog.Lookup(openroutertest.DefaultModel).Efforts {
		effortChoices = append(effortChoices, protocol.SessionConfigSelectOption{Value: protocol.SessionConfigValueId(effort), Name: effort.Name()})
	}
	models.Select.Options.Ungrouped = &modelChoices
	efforts.Select.Options.Ungrouped = &effortChoices
	modes.Select.Options.Ungrouped = &protocol.SessionConfigSelectOptionsUngrouped{{Value: "ask", Name: "Ask"}, {Value: "auto", Name: "Auto"}}
	return []protocol.SessionConfigOption{models, efforts, modes}
}

// driver runs a model against a scripted agent the way the program does,
// except that it runs each command at once and leaves server events to the
// test.
type driver struct {
	t *testing.T
	*model
	agent *agent
	quit  bool
}

func newDriver(t *testing.T, replies ...reply) *driver {
	t.Helper()
	clientIn, agentOut := io.Pipe()
	agentIn, clientOut := io.Pipe()
	a := &agent{conn: acp.NewConn(agentOut), options: configOptions(), replies: replies}
	go func() {
		a.conn.Serve(agentIn, a)
		agentOut.Close()
	}()
	conn, created, err := client.Connect(clientIn, clientOut, t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	m := &model{conn: conn, session: client.NewSession(conn, created), input: newInput(), focused: true, layout: layout{height: 10}}
	return &driver{t: t, model: m, agent: a}
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

// finish sends server events to the model until the turn ends, and returns
// the response text and whether the turn succeeded.
func (d *driver) finish() (string, bool) {
	d.t.Helper()
	var text strings.Builder
	for {
		event := d.conn.Next()
		d.send(event)
		switch event := event.(type) {
		case client.Update:
			if chunk := event.Update.AgentMessageChunk; chunk != nil {
				text.WriteString(content(chunk.Content))
			}
		case client.Finished:
			return text.String(), event.Err == nil
		}
	}
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
	started := make(chan struct{})
	d := newDriver(t, func(t turn) {
		close(started)
		hang(t)
	}, echo)
	d.session.Prompt("running")
	<-started
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
	if _, ok := d.finish(); !ok {
		t.Error("the cancelled turn failed")
	}
	d.press(tea.KeyEnter)
	equal(t, d.input.Value() == "", true)
	text, _ := d.finish()
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
	d := newDriver(t)
	d.agent.replay = []protocol.SessionUpdate{protocol.UpdateUserMessageText("first"), protocol.UpdateAgentMessageText("you said: first")}
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
	d.send(d.conn.Next())
	equal(t, d.held != nil, true)
	equal(t, len(d.view.items), 0)
	_, deliver := d.model.Update(result)
	d.send(deliver())
	// The available commands follow the load's response.
	for {
		event := d.conn.Next()
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
	d := newDriver(t, ask(echo), ask(echo), ask(echo), echo)
	prompt := d.session.Prompt
	prompt("run a command")
	d.send(d.conn.Next())
	d.press(tea.KeyDown)
	d.press(tea.KeyDown)
	equal(t, d.approvalSelected, 1)
	d.press(tea.KeyEscape)
	equal(t, d.session.Permission == nil, true)
	equal(t, d.approvalSelected, 0)
	if _, ok := d.finish(); !ok {
		t.Error("the rejected turn failed")
	}
	prompt("run another command")
	d.send(d.conn.Next())
	d.press(tea.KeyUp)
	d.press(tea.KeyDown)
	d.press(tea.KeyEnter)
	equal(t, d.session.Permission == nil, true)
	text, ok := d.finish()
	equal(t, text, "you said: run another command")
	equal(t, ok, true)
	prompt("run a third command")
	d.send(d.conn.Next())
	d.typeText("hi")
	d.press(tea.KeyEnter)
	equal(t, d.session.Permission == nil, false)
	equal(t, d.input.Value(), "hi")
	d.press(tea.KeyEscape)
	d.finish()
	d.press(tea.KeyEnter)
	text, ok = d.finish()
	equal(t, text, "you said: hi")
	equal(t, ok, true)
}
