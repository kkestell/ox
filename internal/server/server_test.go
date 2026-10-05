package server_test

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ox/internal/acp"
	"ox/internal/agent"
	"ox/internal/catalog"
	fake "ox/internal/openroutertest"
	"ox/internal/server"
	"ox/internal/settings"
	"ox/internal/store"
	"ox/internal/transcript"
)

type object = map[string]any

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params object          `json:"params"`
	Result object          `json:"result"`
	Error  *acp.Error      `json:"error"`
}

// client is the ACP client side of an in-memory connection to a server.
type client struct {
	t         *testing.T
	in        io.WriteCloser
	out       chan message
	nextID    int
	home      string
	workspace string
	done      chan struct{}
}

func connect(t *testing.T, replies ...fake.Reply) *client {
	t.Helper()
	sessions, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	openrouter := fake.Start(t, replies...)
	a := &agent.Agent{Store: sessions, OpenRouter: openrouter.Client(), Catalog: fake.ParsedCatalog()}
	home := t.TempDir()
	skill := filepath.Join(home, ".config/ox/skills/tally")
	os.MkdirAll(skill, 0o777)
	os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: tally\ndescription: Count tallies.\nargument-hint: what\n---\nCount them.\n"), 0o666)
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	conn := acp.NewConn(serverOut)
	srv := server.New(conn, a, settings.Settings{Model: fake.DefaultModel, Effort: catalog.EffortDefault, Mode: transcript.ModeAsk}, home)
	c := &client{t: t, in: clientOut, out: make(chan message, 1000), home: home, workspace: t.TempDir(), done: make(chan struct{})}
	go func() {
		scanner := bufio.NewScanner(clientIn)
		scanner.Buffer(nil, 1<<24)
		for scanner.Scan() {
			var m message
			if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
				panic(err)
			}
			c.out <- m
		}
		close(c.out)
	}()
	go func() {
		conn.Serve(serverIn, srv)
		srv.Shutdown()
		serverOut.Close()
		sessions.Close()
		close(c.done)
	}()
	t.Cleanup(func() {
		c.in.Close()
		<-c.done
	})
	return c
}

func (c *client) send(m object) {
	m["jsonrpc"] = "2.0"
	data, _ := json.Marshal(m)
	if _, err := c.in.Write(append(data, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

// request sends a request and returns its ID.
func (c *client) request(method string, params object) json.RawMessage {
	c.nextID++
	c.send(object{"id": c.nextID, "method": method, "params": params})
	id, _ := json.Marshal(c.nextID)
	return id
}

func (c *client) next() message {
	c.t.Helper()
	select {
	case m, ok := <-c.out:
		if !ok {
			c.t.Fatal("the server closed the connection")
		}
		return m
	case <-time.After(10 * time.Second):
		c.t.Fatal("no message from the server")
	}
	panic("unreachable")
}

// call sends a request and returns its response and the notifications and
// permission requests that arrived first. answer answers each permission
// request with an option ID, or "cancelled".
func (c *client) call(method string, params object, answer ...string) (message, []message) {
	c.t.Helper()
	id := c.request(method, params)
	return c.await(id, answer...)
}

func (c *client) await(id json.RawMessage, answer ...string) (message, []message) {
	c.t.Helper()
	var before []message
	for {
		m := c.next()
		if string(m.ID) == string(id) && m.Method == "" {
			return m, before
		}
		before = append(before, m)
		if m.Method == acp.MethodRequestPermission {
			outcome := object{"outcome": "cancelled"}
			if answer[0] != "cancelled" {
				outcome = object{"outcome": "selected", "optionId": answer[0]}
			}
			answer = answer[1:]
			c.send(object{"id": m.ID, "result": object{"outcome": outcome}})
		}
	}
}

func (c *client) newSession() string {
	c.t.Helper()
	response, _ := c.call(acp.MethodNewSession, object{"cwd": c.workspace, "mcpServers": []any{}})
	if response.Error != nil {
		c.t.Fatal(response.Error)
	}
	return response.Result["sessionId"].(string)
}

func (c *client) prompt(id, text string, answer ...string) (message, []message) {
	c.t.Helper()
	return c.call(acp.MethodPrompt, object{"sessionId": id, "prompt": []any{object{"type": "text", "text": text}}}, answer...)
}

func updates(messages []message, kind string) []object {
	var found []object
	for _, m := range messages {
		if m.Method == acp.MethodUpdate {
			if update := m.Params["update"].(map[string]any); update["sessionUpdate"] == kind {
				found = append(found, update)
			}
		}
	}
	return found
}

func TestSessionsCanBePromptedReplayedListedClosedAndDeleted(t *testing.T) {
	c := connect(t, fake.Shell("printf hi"), fake.Text("Done."))
	initialized, _ := c.call(acp.MethodInitialize, object{"protocolVersion": 1})
	if initialized.Result["protocolVersion"] != 1.0 {
		t.Errorf("initialize = %v", initialized.Result)
	}
	id := c.newSession()
	commands := c.next()
	if update := commands.Params["update"].(map[string]any); update["sessionUpdate"] != "available_commands_update" ||
		update["availableCommands"].([]any)[0].(map[string]any)["input"].(map[string]any)["hint"] != "what" {
		t.Errorf("commands = %v", commands.Params)
	}

	response, before := c.prompt(id, "Run it", "approve")
	if response.Result["stopReason"] != "end_turn" {
		t.Fatalf("prompt = %+v", response)
	}
	var permission message
	for _, m := range before {
		if m.Method == acp.MethodRequestPermission {
			permission = m
		}
	}
	toolCall := permission.Params["toolCall"].(map[string]any)
	content := toolCall["content"].([]any)[0].(map[string]any)["content"].(map[string]any)["text"]
	if content != "Working directory: "+c.workspace+"\n\nCommand:\n\n    printf hi" || toolCall["title"] != "printf hi" {
		t.Errorf("permission = %v", permission.Params)
	}
	if info := updates(before, "session_info_update"); len(info) != 1 || info[0]["title"] != "Run it" {
		t.Errorf("session info = %v", info)
	}
	if finished := updates(before, "tool_call_update"); len(finished) != 2 || finished[1]["status"] != "completed" {
		t.Errorf("tool updates = %v", finished)
	}

	listed, _ := c.call(acp.MethodListSessions, object{"cwd": c.workspace})
	sessions := listed.Result["sessions"].([]any)
	if len(sessions) != 1 || sessions[0].(map[string]any)["title"] != "Run it" {
		t.Errorf("list = %v", listed.Result)
	}

	loaded, replay := c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": c.workspace, "mcpServers": []any{}})
	if loaded.Error != nil {
		t.Fatal(loaded.Error)
	}
	var kinds []string
	for _, m := range replay {
		kinds = append(kinds, m.Params["update"].(map[string]any)["sessionUpdate"].(string))
	}
	if strings.Join(kinds, " ") != "user_message_chunk tool_call agent_message_chunk usage_update" {
		t.Errorf("replay = %v", kinds)
	}
	c.next() // available commands

	if closed, _ := c.call(acp.MethodCloseSession, object{"sessionId": id}); closed.Error != nil {
		t.Fatal(closed.Error)
	}
	if response, _ := c.prompt(id, "Again"); response.Error == nil || !strings.Contains(response.Error.Error(), "is not active") {
		t.Errorf("prompt after close = %+v", response)
	}
	if moved, _ := c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": "/elsewhere"}); moved.Error == nil || !strings.Contains(moved.Error.Error(), "belongs to workspace") {
		t.Errorf("load elsewhere = %+v", moved)
	}
	if deleted, _ := c.call(acp.MethodDeleteSession, object{"sessionId": id}); deleted.Error != nil {
		t.Fatal(deleted.Error)
	}
	if missing, _ := c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": c.workspace}); missing.Error == nil || missing.Error.Code != -32002 {
		t.Errorf("load deleted = %+v", missing)
	}
}

func TestACompactionIsShownAsAToolCallLiveAndOnReplay(t *testing.T) {
	answer := func(text string, input int) fake.Reply {
		return fake.Chunks(fake.Delta(object{"role": "assistant", "content": text}, ""), fake.Usage(input, 10, 0.25))
	}
	// The first answer uses more than 80% of the default model's context.
	c := connect(t, answer("Hello.", 900000), answer("The user said hi.", 900000), fake.Text("Done."))
	id := c.newSession()
	c.next() // available commands
	c.prompt(id, "Hi")
	response, live := c.prompt(id, "Continue")
	if response.Result["stopReason"] != "end_turn" {
		t.Fatalf("prompt = %+v", response)
	}
	started, finished := updates(live, "tool_call"), updates(live, "tool_call_update")
	if len(started) != 1 || started[0]["title"] != "Context compaction" || started[0]["status"] != "in_progress" {
		t.Errorf("started = %v", started)
	}
	if len(finished) != 1 || finished[0]["toolCallId"] != started[0]["toolCallId"] || finished[0]["status"] != "completed" ||
		finished[0]["rawOutput"] != "The user said hi." {
		t.Errorf("finished = %v", finished)
	}
	for _, chunk := range updates(live, "agent_message_chunk") {
		if chunk["content"].(map[string]any)["text"] != "Done." {
			t.Errorf("the summary was streamed: %v", chunk)
		}
	}

	_, replay := c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": c.workspace, "mcpServers": []any{}})
	var kinds []string
	for _, m := range replay {
		kinds = append(kinds, m.Params["update"].(map[string]any)["sessionUpdate"].(string))
	}
	if strings.Join(kinds, " ") != "user_message_chunk agent_message_chunk user_message_chunk tool_call agent_message_chunk usage_update" {
		t.Errorf("replay = %v", kinds)
	}
	if call := updates(replay, "tool_call")[0]; call["title"] != "Context compaction" || call["status"] != "completed" || call["rawOutput"] != "The user said hi." {
		t.Errorf("replayed compaction = %v", call)
	}
	if usage := updates(replay, "usage_update")[0]; usage["cost"].(map[string]any)["amount"] != 0.5 {
		t.Errorf("usage = %v", usage)
	}
}

func TestConfigurationSelectionsValidateSaveAndApplyToTheNextTurn(t *testing.T) {
	c := connect(t, fake.Text("one"), fake.Text("two"))
	id := c.newSession()
	c.next()
	set := func(config, value string) message {
		response, _ := c.call(acp.MethodSetConfigOption, object{"sessionId": id, "configId": config, "value": value})
		return response
	}
	if response := set("effort", "max"); response.Error != nil {
		t.Fatal(response.Error)
	}
	// GLM 5.3 Flash accepts max, so the effort survives the model change.
	if response := set("model", "openrouter:z-ai/glm-5.3-flash"); response.Error != nil {
		t.Fatal(response.Error)
	}
	if response := set("model", "openrouter:acme/plain"); response.Result["configOptions"].([]any)[1].(map[string]any)["currentValue"] != "default" {
		t.Errorf("an unsupported effort was kept: %v", response.Result)
	}
	for _, test := range [][2]string{{"effort", "max"}, {"model", "openrouter:missing"}, {"mode", "loud"}, {"bogus", "x"}} {
		if response := set(test[0], test[1]); response.Error == nil || response.Error.Code != -32602 {
			t.Errorf("%v: %+v", test, response)
		}
	}
	set("model", "openrouter:z-ai/glm-5.3-flash")
	set("mode", "auto")
	data, _ := os.ReadFile(settings.GlobalPath(c.home))
	var saved object
	json.Unmarshal(data, &saved)
	if saved["model"] != "openrouter:z-ai/glm-5.3-flash" || saved["mode"] != "auto" || saved["effort"] != "default" {
		t.Errorf("saved settings = %s", data)
	}
	if response, _ := c.prompt(id, "Hi"); response.Error != nil {
		t.Fatal(response.Error)
	}
	if response, _ := c.call(acp.MethodNewSession, object{"cwd": c.workspace}); response.Result["configOptions"].([]any)[0].(map[string]any)["currentValue"] != "openrouter:z-ai/glm-5.3-flash" {
		t.Errorf("new sessions do not use the saved defaults: %v", response.Result)
	}
}

func TestAModeChangeDuringAPromptAppliesFromTheNextBatch(t *testing.T) {
	c := connect(t, fake.Shell("printf one"), fake.Shell("printf two"), fake.Text("Done."))
	id := c.newSession()
	c.next()
	prompt := c.request(acp.MethodPrompt, object{"sessionId": id, "prompt": []any{object{"type": "text", "text": "Run them"}}})
	var permission message
	for permission.Method != acp.MethodRequestPermission {
		permission = c.next()
	}
	// The server reads the mode change before the answer, so the second
	// batch starts in Auto mode.
	c.request(acp.MethodSetConfigOption, object{"sessionId": id, "configId": "mode", "value": "auto"})
	c.send(object{"id": permission.ID, "result": object{"outcome": object{"outcome": "selected", "optionId": "approve"}}})
	response, after := c.await(prompt, "approve")
	if response.Result["stopReason"] != "end_turn" {
		t.Fatalf("prompt = %+v", response)
	}
	for _, m := range after {
		if m.Method == acp.MethodRequestPermission {
			t.Errorf("the second batch asked for permission: %v", m.Params)
		}
	}
	var statuses []string
	for _, update := range updates(after, "tool_call_update") {
		statuses = append(statuses, update["status"].(string))
	}
	if strings.Join(statuses, " ") != "in_progress completed in_progress completed" {
		t.Errorf("tool statuses = %v", statuses)
	}
}

func TestSlashCommandsInvokeSkills(t *testing.T) {
	c := connect(t, fake.Echo(), fake.Echo())
	id := c.newSession()
	c.next()
	response, before := c.prompt(id, "  /tally the votes ")
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	text := ""
	for _, chunk := range updates(before, "agent_message_chunk") {
		text += chunk["content"].(map[string]any)["text"].(string)
	}
	if text != "you said: Skill /tally invoked.\n\nInstructions:\nCount them.\n\nArguments:\nthe votes" {
		t.Errorf("answer = %q", text)
	}
	if info := updates(before, "session_info_update"); info[0]["title"] != "/tally the votes" {
		t.Errorf("title = %v", info)
	}
	response, before = c.prompt(id, "/unknown command")
	if chunk := updates(before, "agent_message_chunk"); response.Error != nil || chunk[0]["content"].(map[string]any)["text"] != "you said: /unknown command" {
		t.Errorf("an unknown command is a message: %v", chunk)
	}
}

func TestInvalidPromptsAreRejectedBeforeAnythingIsSaved(t *testing.T) {
	c := connect(t)
	id := c.newSession()
	c.next()
	for _, prompt := range []any{
		[]any{object{"type": "text", "text": "  "}},
		[]any{object{"type": "audio", "data": "aGk=", "mimeType": "audio/wav"}},
		[]any{object{"type": "image", "data": "aGk=", "mimeType": "image/bmp"}},
		[]any{object{"type": "image", "data": "not base64", "mimeType": "image/png"}},
		[]any{object{"type": "image", "data": "aGk=", "mimeType": "image/png"}},
	} {
		response, _ := c.call(acp.MethodPrompt, object{"sessionId": id, "prompt": prompt})
		if response.Error == nil || response.Error.Code != -32602 {
			t.Errorf("%v: %+v", prompt, response)
		}
	}
	listed, _ := c.call(acp.MethodListSessions, object{})
	if session := listed.Result["sessions"].([]any)[0].(map[string]any); session["title"] != nil {
		t.Errorf("a rejected prompt was saved: %v", session)
	}
}

func TestEndOfInputCancelsThePromptStopsProcessesAndDrainsTheResponse(t *testing.T) {
	gate := make(chan struct{})
	defer close(gate)
	c := connect(t, fake.Calls(fake.Call{ID: "bg", Name: "shell", Arguments: object{"command": "echo $$ > pid; sleep 60", "background": true}}),
		fake.Gated(gate, fake.Text("never")))
	id := c.newSession()
	c.next()
	c.call(acp.MethodSetConfigOption, object{"sessionId": id, "configId": "mode", "value": "auto"})
	prompt := c.request(acp.MethodPrompt, object{"sessionId": id, "prompt": []any{object{"type": "text", "text": "Start it"}}})
	pid := filepath.Join(c.workspace, "pid")
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(pid); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background command never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.in.Close()
	response, _ := c.await(prompt)
	if response.Result["stopReason"] != "cancelled" {
		t.Errorf("response = %+v", response)
	}
	<-c.done
	text, _ := os.ReadFile(pid)
	output, _ := exec.Command("ps", "-o", "stat=", "-p", strings.TrimSpace(string(text))).Output()
	if state := strings.TrimSpace(string(output)); state != "" && !strings.HasPrefix(state, "Z") {
		t.Errorf("the background command survived shutdown: %s", state)
	}
}

func TestClosingCancelsTheRunningPromptAndWaitsForIt(t *testing.T) {
	c := connect(t, fake.Hang(""))
	id := c.newSession()
	c.next()
	prompt := c.request(acp.MethodPrompt, object{"sessionId": id, "prompt": []any{object{"type": "text", "text": "Wait"}}})
	for {
		if m := c.next(); m.Method == acp.MethodUpdate {
			break
		}
	}
	closing := c.request(acp.MethodCloseSession, object{"sessionId": id})
	response, _ := c.await(prompt)
	if response.Result["stopReason"] != "cancelled" {
		t.Errorf("prompt = %+v", response)
	}
	if closed, _ := c.await(closing); closed.Error != nil {
		t.Errorf("close = %+v", closed)
	}
	if response, _ := c.prompt(id, "Again"); response.Error == nil {
		t.Error("a closed session accepted a prompt")
	}
}
