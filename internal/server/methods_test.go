package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/jsonrpc"
	"github.com/kkestell/ox/internal/store"
)

func openStore(t *testing.T) *store.Store {
	st, err := store.Open(filepath.Join(t.TempDir(), "ox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func answer(s *Server, line string) *jsonrpc.Response {
	responses := make(chan *jsonrpc.Response, 1)
	s.handle(func(r *jsonrpc.Response) { responses <- r }, []byte(line))
	return <-responses
}

func TestSettingsValidation(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"h": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}}}},
		PermissionModes: []config.Option{{ID: "plan"}},
	}}}
	s, err := New(openStore(t), cfg, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, params string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
	}
	for _, params := range []string{
		`{}`,
		`{"session_id":"missing"}`,
		`{"session_id":"missing","model":42}`,
	} {
		if response := answer(s, request("session/configure", params)); response.Error == nil || response.Error.Code != -32602 {
			t.Errorf("invalid configuration %s: %+v", params, response.Error)
		}
	}
	for _, params := range []string{
		`{"workspace_id":"missing"}`,
		`{"workspace_id":"missing","harness":"x","model":"m","effort":"low","permission_mode":"plan"}`,
		`{"workspace_id":"missing","harness":"h","model":"x","effort":"low","permission_mode":"plan"}`,
		`{"workspace_id":"missing","harness":"h","model":"m","effort":"high","permission_mode":"plan"}`,
		`{"workspace_id":"missing","harness":"h","model":"m","effort":"low","permission_mode":""}`,
	} {
		if response := answer(s, request("session/create", params)); response.Error == nil || response.Error.Code != -32602 {
			t.Errorf("invalid creation %s: %+v", params, response.Error)
		}
	}
	if response := answer(s, request("session/configure", `{"session_id":"missing","effort":""}`)); response.Error == nil || response.Error.Code != -32001 {
		t.Fatalf("configuration of one setting did not reach session lookup: %+v", response.Error)
	}
}

// A client that missed its create reply can identify the same parked
// session without overwriting settings another page has since changed.
func TestRepeatSessionCreation(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "no-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(openStore(t), cfg, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	workspace := func() string {
		var w struct {
			ID string `json:"workspace_id"`
		}
		json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &w)
		return w.ID
	}
	id := "00112233445566778899aabbccddeeff"
	params := map[string]string{
		"workspace_id": workspace(), "session_id": id, "harness": cfg.Defaults.Harness,
		"model": cfg.Defaults.Model, "effort": cfg.Defaults.Effort, "permission_mode": cfg.Defaults.PermissionMode,
	}
	check := func(existing bool) {
		t.Helper()
		var got struct {
			ID       string `json:"session_id"`
			Existing bool   `json:"existing"`
		}
		if err := json.Unmarshal(c.call("session/create", params), &got); err != nil || got.ID != id || got.Existing != existing {
			t.Fatalf("repeat=%v returned %+v, %v", existing, got, err)
		}
	}
	check(false)
	params["permission_mode"] = "obsolete"
	check(true)
	params["workspace_id"] = workspace()
	if r := c.request("session/create", params); r.Error == nil || r.Error.Code != jsonrpc.InvalidParams {
		t.Fatal("a different workspace claimed the session")
	}
}

func TestSessionLifecycle(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}}},
		PermissionModes: []config.Option{{ID: "plan"}},
	}}}
	s, err := New(openStore(t), cfg, 300*time.Millisecond, map[string]agent.Start{"fake": fake.Start})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)

	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "effort": "low", "permission_mode": "plan",
	}), &created)
	id := created.SessionID

	// The reply outlasts the park period, which starts only once the agent
	// is parkable.
	prompt := "one two three four five six seven eight"
	c.call("session/send", map[string]string{"session_id": id, "text": prompt})
	if busy := c.request("session/send", map[string]string{"session_id": id, "text": "again"}); busy.Error == nil || busy.Error.Code != sessionBusy {
		t.Fatalf("a running session took a second prompt: %+v", busy.Error)
	}
	for {
		var u agent.Update
		c.next(func(m message) bool {
			if m.Method != "session/update" || m.sessionID() != id {
				return false
			}
			u = agent.Update{}
			json.Unmarshal(m.Params, &u)
			return u.Type == "state" || u.Type == "parked"
		})
		if u.Type == "parked" {
			t.Fatal("a running session parked")
		}
		if u.State == "idle" {
			break
		}
	}

	if invalid := c.request("session/configure", map[string]string{"session_id": id, "effort": "max"}); invalid.Error == nil || invalid.Error.Code != jsonrpc.InvalidParams {
		t.Fatalf("configuration took an effort the model does not offer: %+v", invalid.Error)
	}
	c.call("session/configure", map[string]string{"session_id": id, "effort": "high"})
	configured := c.next(func(m message) bool { return m.Method == "session/configured" })
	if !strings.Contains(string(configured.Params), `"model":"m","effort":"high","permission_mode":"plan"`) {
		t.Fatalf("configuring effort changed other settings: %s", configured.Params)
	}

	type got struct {
		State string `json:"state"`
		agent.Settings
		History []agent.Update `json:"history"`
	}
	var running got
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &running)
	if running.State != "idle" || running.Effort != "high" || len(running.History) != 3 || running.History[1].Text != "You said: "+prompt {
		t.Fatalf("unexpected session: %+v", running)
	}

	c.update(id, "parked")
	var parked got
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &parked)
	if parked.State != "parked" || len(parked.History) != 3 || parked.Model != "m" {
		t.Fatalf("unexpected parked session: %+v", parked)
	}

	c.call("session/send", map[string]string{"session_id": id, "text": "resumed"})
	c.call("session/destroy", map[string]string{"session_id": id})
	c.next(func(m message) bool { return m.Method == "session/destroyed" })
	if gone := c.request("session/get", map[string]string{"session_id": id}); gone.Error == nil || gone.Error.Code != notFound {
		t.Fatalf("a destroyed session answered: %+v", gone)
	}
}

// A running process keeps settings it has taken even when storing them
// fails, so every client is told what it now uses.
func TestConfigurationTakenButNotStored(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}}},
		PermissionModes: []config.Option{{ID: "plan"}},
	}}}
	st := openStore(t)
	s, err := New(st, cfg, 0, map[string]agent.Start{"fake": fake.Start})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "effort": "low", "permission_mode": "plan",
	}), &created)
	id := created.SessionID
	c.call("session/send", map[string]string{"session_id": id, "text": "hi"})
	for c.update(id, "state").State != "idle" {
	}

	st.Close()
	if failed := c.request("session/configure", map[string]string{"session_id": id, "effort": "high"}); failed.Error == nil {
		t.Fatal("configuration succeeded without being stored")
	}
	configured := c.next(func(m message) bool { return m.Method == "session/configured" })
	if !strings.Contains(string(configured.Params), `"effort":"high"`) {
		t.Fatalf("clients were not told the settings the process took: %s", configured.Params)
	}
}

// A process that exits during a configuration is replaced by one started with
// the requested settings, so the configuration succeeds.
func TestConfigurationOutlivesItsProcess(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}}},
		PermissionModes: []config.Option{{ID: "plan"}},
	}}}
	s, err := New(openStore(t), cfg, 0, map[string]agent.Start{"fake": fake.Start})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "effort": "low", "permission_mode": "plan",
	}), &created)
	id := created.SessionID
	c.call("session/send", map[string]string{"session_id": id, "text": "hi"})
	for c.update(id, "state").State != "idle" {
	}

	// The fake acknowledges a tick after the request, so the destroy stops
	// its process first.
	configure := c.send("session/configure", map[string]string{"session_id": id, "effort": "high"})
	time.Sleep(10 * time.Millisecond)
	c.call("session/destroy", map[string]string{"session_id": id})
	if r := c.response(configure); r.Error != nil {
		t.Fatalf("a configuration whose process exited failed: %+v", r.Error)
	}
	configured := c.next(func(m message) bool { return m.Method == "session/configured" })
	if !strings.Contains(string(configured.Params), `"effort":"high"`) {
		t.Fatalf("the requested settings were not taken: %s", configured.Params)
	}
}

// A resume token whose conversation Claude no longer has, such as one whose
// transcript Claude deleted, fails one send with a notice in the transcript;
// the next send starts a new conversation.
func TestUnresumableConversationStartsAnew(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	// A stand-in for Claude, which fails a resume as Claude does when it has
	// no transcript for the ID.
	script := "#!/bin/sh\necho \"$@\" >> " + args + "\n" +
		`case "$*" in *--resume=*) echo '{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["No conversation found with session ID: gone"]}'; exit 1;; esac` + "\n" +
		"read line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		"exec cat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{ID: "s", Harness: "claude", Model: "m", PermissionMode: "plan", ResumeToken: "gone", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Start{"claude": claude.Start})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)

	failed := c.request("session/send", map[string]string{"session_id": "s", "text": "hi"})
	if failed.Error == nil || failed.Error.Message != lostConversation {
		t.Fatalf("a send that found the conversation gone did not say so: %+v", failed.Error)
	}
	if notice := c.update("s", "exited"); notice.Error != lostConversation {
		t.Fatalf("the transcript has no notice: %+v", notice)
	}
	var got struct {
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": "s"}), &got)
	if len(got.History) != 1 || got.History[0].Error != lostConversation {
		t.Fatalf("the notice was not recorded: %+v", got.History)
	}

	c.call("session/send", map[string]string{"session_id": "s", "text": "hi"})
	starts, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(starts)), "\n")
	if len(lines) != 2 || strings.Contains(lines[1], "--resume") || !strings.Contains(lines[1], "--session-id=") {
		t.Fatalf("the next send did not start a new conversation: %q", lines)
	}
	workspaces, err := st.Workspaces()
	if err != nil || workspaces[0].Sessions[0].ResumeToken != "" {
		t.Fatalf("the stored token was kept: %v", err)
	}
}
