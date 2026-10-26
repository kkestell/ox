package server_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
	"net/http"
	"os"
	"os/exec"

	"ox/internal/acp"
	"ox/internal/agent"
	"ox/internal/catalog"
	"ox/internal/mcptest"
	fake "ox/internal/openroutertest"
	"ox/internal/provider"
	"ox/internal/server"
	"ox/internal/settings"
	"ox/internal/store"
	"ox/internal/transcript"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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
	return connectWithStore(t, sessions, replies...)
}

func connectWithStore(t *testing.T, sessions *store.Store, replies ...fake.Reply) *client {
	t.Helper()
	openrouter := fake.Start(t, replies...)
	a := &agent.Agent{Store: sessions, Providers: map[string]provider.Provider{catalog.OpenRouter: openrouter.Client()}, Catalog: fake.ParsedCatalog()}
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

func TestReusedProviderCallIDsStayDistinctInUpdatesPermissionsAndReplay(t *testing.T) {
	answer := func(w http.ResponseWriter, r *http.Request, body object) {
		calls, outcomes := 0, 0
		for _, value := range body["messages"].([]any) {
			message := value.(object)
			if encoded, ok := message["tool_calls"].([]any); ok {
				for _, value := range encoded {
					calls++
					if value.(object)["id"] != "shell-0" {
						t.Error("ACP call ID replaced the provider ID in model history")
					}
				}
			}
			if message["role"] == "tool" {
				outcomes++
				if message["tool_call_id"] != "shell-0" {
					t.Error("ACP call ID replaced the provider ID in model outcomes")
				}
			}
		}
		if calls != 2 || outcomes != 2 {
			t.Errorf("model history has %d calls and %d outcomes", calls, outcomes)
		}
		fake.Text("Done.")(w, r, body)
	}
	c := connect(t, fake.Shell("printf one"), fake.Shell("printf two"), answer)
	id := c.newSession()
	c.next()
	response, before := c.prompt(id, "Run both", "approve", "approve")
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	started := updates(before, "tool_call")
	if len(started) != 2 || started[0]["toolCallId"] == started[1]["toolCallId"] {
		t.Fatalf("tool calls are not distinct: %v", started)
	}
	var permissionIDs []any
	for _, message := range before {
		if message.Method == acp.MethodRequestPermission {
			permissionIDs = append(permissionIDs, message.Params["toolCall"].(object)["toolCallId"])
		}
	}
	if len(permissionIDs) != 2 || permissionIDs[0] != started[0]["toolCallId"] || permissionIDs[1] != started[1]["toolCallId"] {
		t.Fatalf("permissions %v do not match calls %v", permissionIDs, started)
	}
	finished := updates(before, "tool_call_update")
	if len(finished) != 4 {
		t.Fatalf("tool updates = %v", finished)
	}
	for i, update := range finished {
		if update["toolCallId"] != started[i/2]["toolCallId"] {
			t.Errorf("tool update %v does not match its invocation", update)
		}
	}
	loaded, replay := c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": c.workspace})
	if loaded.Error != nil {
		t.Fatal(loaded.Error)
	}
	replayed := updates(replay, "tool_call")
	if len(replayed) != 2 || replayed[0]["toolCallId"] == replayed[1]["toolCallId"] {
		t.Fatalf("replayed tool calls are not distinct: %v", replayed)
	}
	for i := range started {
		if replayed[i]["title"] != started[i]["title"] || replayed[i]["status"] != "completed" {
			t.Errorf("replayed tool call = %v", replayed[i])
		}
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
	response, before := c.await(prompt)
	if response.Result["stopReason"] != "cancelled" {
		t.Errorf("prompt = %+v", response)
	}
	// The close can respond as soon as the turn finishes, before the prompt
	// goroutine writes its own response.
	var closed message
	for _, m := range before {
		if string(m.ID) == string(closing) && m.Method == "" {
			closed = m
		}
	}
	if len(closed.ID) == 0 {
		closed, _ = c.await(closing)
	}
	if closed.Error != nil {
		t.Errorf("close = %+v", closed)
	}
	if response, _ := c.prompt(id, "Again"); response.Error == nil {
		t.Error("a closed session accepted a prompt")
	}
}

func TestMCPHelper(t *testing.T) { mcptest.Run() }

func writeMCPSettings(t *testing.T, path string, definitions map[string]settings.MCPServer) {
	t.Helper()
	data, err := json.Marshal(object{"model": fake.DefaultModel, "mcpServers": definitions})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func stdioDefinition(name string, definition settings.MCPServer) object {
	env := []any{}
	for key, value := range definition.Env {
		env = append(env, object{"name": key, "value": value})
	}
	return object{"name": name, "command": definition.Command, "args": definition.Args, "env": env}
}

func TestMCPConfigurationReusePermissionReplayAndCleanup(t *testing.T) {
	marker := t.TempDir() + "/events"
	definition, path := mcptest.Definition(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("write", false)}, Marker: marker})
	call := fake.Calls(fake.Call{ID: "one", Name: "mcp__project__write", Arguments: object{"number": 7}})
	c := connect(t, call, fake.Text("First"), call, fake.Text("Second"), call, fake.Text("Third"))
	// Workspace replaces global, then ACP replaces the complete workspace definition.
	writeMCPSettings(t, settings.GlobalPath(c.home), map[string]settings.MCPServer{"bad": {Command: "/nonexistent-global"}})
	writeMCPSettings(t, filepath.Join(c.workspace, ".ox/settings.json"), map[string]settings.MCPServer{"project": {Command: "/nonexistent-workspace", Env: map[string]string{"SHOULD_NOT_MERGE": "yes"}}})
	definitions := []any{stdioDefinition("project", definition)}
	response, _ := c.call(acp.MethodNewSession, object{"cwd": c.workspace, "mcpServers": definitions})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	id := response.Result["sessionId"].(string)
	response, before := c.prompt(id, "Call MCP", "approve")
	if response.Error != nil || response.Result["stopReason"] != "end_turn" {
		t.Fatalf("prompt: %+v", response)
	}
	permissions := 0
	for _, m := range before {
		if m.Method == acp.MethodRequestPermission {
			permissions++
			raw, _ := json.Marshal(m.Params)
			for _, want := range []string{"MCP server: project", "Tool: write", `\"number\":7`} {
				if !strings.Contains(string(raw), want) {
					t.Fatalf("permission missing %s: %s", want, raw)
				}
			}
		}
	}
	if permissions != 1 {
		t.Fatal("missing permission")
	}
	// Changed settings do not alter active sessions, including repeated loads.
	writeMCPSettings(t, filepath.Join(c.workspace, ".ox/settings.json"), map[string]settings.MCPServer{"bad": {Command: "/new-missing"}})
	response, before = c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": c.workspace, "mcpServers": definitions})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	if len(updates(before, "tool_call")) == 0 {
		t.Fatal("MCP transcript was not replayed")
	}
	changed := []any{stdioDefinition("other", definition)}
	response, _ = c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": c.workspace, "mcpServers": changed})
	if response.Error == nil || !strings.Contains(response.Error.Error(), "close and reload") {
		t.Fatalf("changed definitions: %+v", response)
	}
	response, _ = c.prompt(id, "Again", "deny")
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	data, _ := os.ReadFile(marker)
	if strings.Count(string(data), "start ") != 1 || strings.Count(string(data), "call write") != 1 {
		t.Fatalf("reuse or denial: %s", data)
	}
	response, _ = c.call(acp.MethodCloseSession, object{"sessionId": id})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	assertMCPParentsGone(t, marker)
	writeMCPSettings(t, filepath.Join(c.workspace, ".ox/settings.json"), map[string]settings.MCPServer{})
	// Loading replays without starting the now unavailable executable.
	unavailable := definition
	unavailable.Command = "/missing-on-replay"
	response, before = c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": c.workspace, "mcpServers": []any{stdioDefinition("project", unavailable)}})
	if response.Error != nil || len(updates(before, "tool_call")) == 0 {
		t.Fatalf("offline replay: %+v", response)
	}
	c.call(acp.MethodCloseSession, object{"sessionId": id})
	response, _ = c.call(acp.MethodLoadSession, object{"sessionId": id, "cwd": c.workspace, "mcpServers": definitions})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	mcptest.Write(t, path, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("write", true)}, Marker: marker})
	response, _ = c.prompt(id, "Fresh connection")
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	response, _ = c.call(acp.MethodDeleteSession, object{"sessionId": id})
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	assertMCPParentsGone(t, marker)
	data, _ = os.ReadFile(marker)
	if strings.Count(string(data), "start ") != 2 {
		t.Fatalf("reload did not reconnect: %s", data)
	}
}

func assertMCPParentsGone(t *testing.T, marker string) {
	t.Helper()
	data, _ := os.ReadFile(marker)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "start ") {
			pid, err := strconv.Atoi(strings.TrimPrefix(line, "start "))
			if err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("MCP process %d survived: %v", pid, err)
			}
		}
	}
}

func TestMCPInvalidACPDefinitions(t *testing.T) {
	c := connect(t)
	for _, definitions := range [][]any{
		{object{"type": "http", "name": "remote", "url": "https://example.invalid", "headers": []any{}}},
		{object{"type": "sse", "name": "remote", "url": "https://example.invalid", "headers": []any{}}},
		{object{"name": "x", "command": "", "args": []any{}, "env": []any{}}},
		{object{"name": "x", "command": "x", "args": []any{}, "env": []any{}}, object{"name": "x", "command": "x", "args": []any{}, "env": []any{}}},
	} {
		response, _ := c.call(acp.MethodNewSession, object{"cwd": c.workspace, "mcpServers": definitions})
		if response.Error == nil {
			t.Fatalf("accepted %v", definitions)
		}
	}
}

func TestMCPCancelDuringPreparationAndConnectionShutdown(t *testing.T) {
	for _, action := range []string{"cancel", "close", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			marker := t.TempDir() + "/events"
			definition, _ := mcptest.Definition(t, mcptest.Config{Startup: "wait", Marker: marker})
			c := connect(t, func(w http.ResponseWriter, r *http.Request, body object) {
				t.Error("cancelled preparation made a model request")
				fake.Text("unexpected")(w, r, body)
			})
			writeMCPSettings(t, settings.GlobalPath(c.home), map[string]settings.MCPServer{"slow": definition})
			id := c.newSession()
			prompt := c.request(acp.MethodPrompt, object{"sessionId": id, "prompt": []any{object{"type": "text", "text": "hello"}}})
			mcptest.WaitFor(t, marker, "start ")
			switch action {
			case "cancel":
				c.send(object{"method": acp.MethodCancel, "params": object{"sessionId": id}})
				response, _ := c.await(prompt)
				if response.Result["stopReason"] != "cancelled" {
					t.Fatalf("cancel: %+v", response)
				}
			case "close":
				closeID := c.request(acp.MethodCloseSession, object{"sessionId": id})
				gotPrompt, gotClose := false, false
				for !gotPrompt || !gotClose {
					response := c.next()
					if string(response.ID) == string(prompt) {
						gotPrompt = true
						if response.Result["stopReason"] != "cancelled" {
							t.Fatal(response)
						}
					}
					if string(response.ID) == string(closeID) {
						gotClose = true
						if response.Error != nil {
							t.Fatal(response.Error)
						}
					}
				}
			case "disconnect":
				c.in.Close()
				select {
				case <-c.done:
				case <-time.After(5 * time.Second):
					t.Fatal("shutdown blocked")
				}
			}
			assertMCPParentsGone(t, marker)
		})
	}
}

func TestMCPFailedDeleteKeepsConnectionUsable(t *testing.T) {
	path := t.TempDir() + "/ox.db"
	sessions, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TRIGGER reject_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'test deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	marker := t.TempDir() + "/events"
	definition, _ := mcptest.Definition(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("read", true)}, Marker: marker})
	call := fake.Calls(fake.Call{ID: "one", Name: "mcp__project__read", Arguments: object{}})
	c := connectWithStore(t, sessions, call, fake.Text("one"), call, fake.Text("two"))
	writeMCPSettings(t, settings.GlobalPath(c.home), map[string]settings.MCPServer{"project": definition})
	id := c.newSession()
	if response, _ := c.prompt(id, "first"); response.Error != nil {
		t.Fatal(response.Error)
	}
	response, _ := c.call(acp.MethodDeleteSession, object{"sessionId": id})
	if response.Error == nil {
		t.Fatal("delete unexpectedly succeeded")
	}
	if response, _ := c.prompt(id, "second"); response.Error != nil {
		t.Fatal(response.Error)
	}
	data, _ := os.ReadFile(marker)
	if strings.Count(string(data), "start ") != 1 || strings.Count(string(data), "call read") != 2 {
		t.Fatalf("connection lost after failed delete: %s", data)
	}
}

func TestMCPSessionsRemainIsolatedAndRefreshPermissions(t *testing.T) {
	marker := t.TempDir() + "/events"
	config := mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("read", true)}, Marker: marker}
	definition, path := mcptest.Definition(t, config)
	call := fake.Calls(fake.Call{ID: "one", Name: "mcp__project__read", Arguments: object{}})
	c := connect(t, call, fake.Text("one"), call, fake.Text("two"), call, fake.Text("three"))
	writeMCPSettings(t, settings.GlobalPath(c.home), map[string]settings.MCPServer{"project": definition})
	one, two := c.newSession(), c.newSession()
	if response, _ := c.prompt(one, "first"); response.Error != nil {
		t.Fatal(response.Error)
	}
	if response, _ := c.prompt(two, "second"); response.Error != nil {
		t.Fatal(response.Error)
	}
	c.call(acp.MethodCloseSession, object{"sessionId": one})
	config.Tools[0].Annotations.ReadOnlyHint = false
	mcptest.Write(t, path, config)
	response, before := c.prompt(two, "third", "deny")
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	asked := false
	for _, m := range before {
		if m.Method == acp.MethodRequestPermission {
			asked = true
		}
	}
	if !asked {
		t.Fatal("changed annotation did not require permission")
	}
	data, _ := os.ReadFile(marker)
	if strings.Count(string(data), "start ") != 2 || strings.Count(string(data), "call read") != 2 {
		t.Fatalf("isolation or refresh: %s", data)
	}
	c.in.Close()
	<-c.done
	assertMCPParentsGone(t, marker)
}

func TestPromptResponseReportsTheTurnsTokens(t *testing.T) {
	usage := fake.Usage(100, 20, 0.5)
	usage["usage"].(map[string]any)["prompt_tokens_details"] = object{"cached_tokens": 60}
	usage["usage"].(map[string]any)["completion_tokens_details"] = object{"reasoning_tokens": 5}
	c := connect(t, fake.Chunks(fake.Delta(object{"role": "assistant", "content": "Done."}, ""), usage))
	response, _ := c.prompt(c.newSession(), "Hi")
	want := object{"inputTokens": 40.0, "cachedReadTokens": 60.0, "outputTokens": 20.0, "thoughtTokens": 5.0, "totalTokens": 120.0}
	if got := response.Result["usage"]; !reflect.DeepEqual(got, map[string]any(want)) {
		t.Errorf("usage = %v", got)
	}
}

func TestTeamsAskBeforeCreatingAndRunWorkersInAuto(t *testing.T) {
	var mu sync.Mutex
	counts := map[bool]int{}
	dispatch := func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		messages := body["messages"].([]any)
		worker := strings.Contains(messages[0].(map[string]any)["content"].(string), "You are worker reader")
		mu.Lock()
		counts[worker]++
		count := counts[worker]
		mu.Unlock()
		if worker {
			if count == 1 {
				fake.Shell("printf auto > child")(w, r, body)
			} else {
				fake.Text("Worker done.")(w, r, body)
			}
		} else if count == 1 {
			fake.Calls(fake.Call{ID: "wait", Name: "wait", Arguments: object{}})(w, r, body)
		} else {
			fake.Text("All done.")(w, r, body)
		}
	}
	c := connect(t, fake.Calls(fake.Call{ID: "create", Name: "create_agent", Arguments: object{"name": "reader", "message": "Read.", "dir": "."}}), dispatch, dispatch, dispatch, dispatch)
	id := c.newSession()
	response, before := c.prompt(id, "Delegate.", "approve")
	if response.Error != nil || response.Result["stopReason"] != "end_turn" {
		t.Fatalf("prompt = %+v", response)
	}
	var permissions int
	for _, message := range before {
		if message.Method == acp.MethodRequestPermission {
			permissions++
			call := message.Params["toolCall"].(map[string]any)
			if call["title"] != "Create agent reader" {
				t.Errorf("permission = %v", call)
			}
		}
	}
	if permissions != 1 {
		t.Fatalf("permissions = %d", permissions)
	}
	if data, err := os.ReadFile(filepath.Join(c.workspace, "child")); err != nil || string(data) != "auto" {
		t.Fatalf("worker shell = %q, %v", data, err)
	}
	toolUpdates := updates(before, "tool_call_update")
	encoded, _ := json.Marshal(toolUpdates)
	if !strings.Contains(string(encoded), "Worker reader finished its turn:") || !strings.Contains(string(encoded), "Worker done.") {
		t.Fatalf("wait result = %s", encoded)
	}
	mu.Lock()
	defer mu.Unlock()
	if counts[true] != 2 || counts[false] != 2 {
		t.Fatalf("model requests = %v", counts)
	}
}

func TestLoadingWakesWorkersClosingLeavesThemRunningAndDeletionRefuses(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	parent, err := db.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := db.CreateWorker(parent.ID, "reader", workspace, "", fake.DefaultModel, catalog.EffortDefault)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.AppendTurnStart(worker.ID, &transcript.TurnStart{
		Model: "openrouter:z-ai/glm-5.3-flash", Effort: catalog.EffortLow, Mode: transcript.ModeAuto,
		Input: transcript.TurnInput{Message: new(transcript.TextMessage("Earlier work."))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Send(worker.ID, "orchestrator", "Resume the work."); err != nil {
		t.Fatal(err)
	}
	requested := make(chan map[string]any, 1)
	gate := make(chan struct{})
	c := connectWithStore(t, db, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		requested <- body
		fake.Gated(gate, fake.Text("Resumed."))(w, r, body)
	}, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		data, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(data), "Worker reader finished its turn:") || !strings.Contains(string(data), "Resumed.") {
			t.Errorf("orchestrator did not receive report: %s", data)
		}
		fake.Text("Seen.")(w, r, body)
	})
	c.workspace = workspace
	load := object{"sessionId": parent.ID, "cwd": workspace, "mcpServers": []any{}}
	if response, _ := c.call(acp.MethodLoadSession, load); response.Error != nil {
		t.Fatal(response.Error)
	}
	select {
	case body := <-requested:
		if body["model"] != "z-ai/glm-5.3-flash" || body["reasoning"].(map[string]any)["effort"] != "low" {
			t.Fatalf("restored model and effort = %v", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("load did not wake worker")
	}
	if response, _ := c.call(acp.MethodDeleteSession, object{"sessionId": parent.ID}); response.Error == nil || !strings.Contains(response.Error.Error(), "remove the session's workers") {
		t.Fatalf("deletion with workers = %+v", response)
	}
	if response, _ := c.call(acp.MethodCloseSession, object{"sessionId": parent.ID}); response.Error != nil {
		t.Fatal(response.Error)
	}
	close(gate)
	deadline := time.Now().Add(5 * time.Second)
	for {
		has, err := db.HasMessages(parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if has {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closed orchestrator did not receive worker report")
		}
		time.Sleep(time.Millisecond)
	}
	if response, _ := c.call(acp.MethodLoadSession, load); response.Error != nil {
		t.Fatal(response.Error)
	}
	if response, _ := c.prompt(parent.ID, "Continue."); response.Error != nil {
		t.Fatal(response.Error)
	}
}

func TestLoadingOrchestratorContinuesWhenWorkerModelIsUnavailable(t *testing.T) {
	db, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	parent, err := db.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := db.CreateWorker(parent.ID, "reader", workspace, "", "openrouter:gone", catalog.EffortDefault)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Send(worker.ID, "orchestrator", "Pending task."); err != nil {
		t.Fatal(err)
	}
	c := connectWithStore(t, db, fake.Text("Still available."))
	response, _ := c.call(acp.MethodLoadSession, object{"sessionId": parent.ID, "cwd": workspace, "mcpServers": []any{}})
	if response.Error != nil {
		t.Fatalf("worker blocked orchestrator load: %v", response.Error)
	}
	if response, _ := c.prompt(parent.ID, "Continue."); response.Error != nil {
		t.Fatalf("orchestrator prompt = %v", response.Error)
	}
	if has, err := db.HasMessages(worker.ID); err != nil || !has {
		t.Fatalf("worker task was lost: %t, %v", has, err)
	}
}
