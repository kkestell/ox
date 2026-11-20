package server

import (
	"encoding/json"
	"errors"
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
	s, err := New(openStore(t), cfg, 0, nil, MCP{})
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
	s, err := New(openStore(t), cfg, 0, nil, MCP{})
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
	st := openStore(t)
	s, err := New(st, cfg, 300*time.Millisecond, map[string]agent.Start{"fake": fake.Start}, MCP{})
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
	if parked.State != "parked" || len(parked.History) != 4 || parked.History[3].Type != "parked" || parked.Model != "m" {
		t.Fatalf("unexpected parked session: %+v", parked)
	}

	listed := func(c *client) int {
		var list struct {
			Workspaces []json.RawMessage `json:"workspaces"`
		}
		json.Unmarshal(c.call("workspace/list", struct{}{}), &list)
		return len(list.Workspaces)
	}
	c.call("session/send", map[string]string{"session_id": id, "text": "resumed"})
	if refused := c.request("workspace/remove", map[string]string{"workspace_id": workspace.WorkspaceID}); refused.Error == nil || refused.Error.Code != operationFailed || refused.Error.Message != "workspace has sessions" {
		t.Fatalf("a workspace with a session was removed: %+v", refused.Error)
	}
	if n := listed(c); n != 1 {
		t.Fatalf("a refused removal changed the workspace list: %d", n)
	}
	c.call("session/destroy", map[string]string{"session_id": id})
	c.next(func(m message) bool { return m.Method == "session/destroyed" })
	if gone := c.request("session/get", map[string]string{"session_id": id}); gone.Error == nil || gone.Error.Code != notFound {
		t.Fatalf("a destroyed session answered: %+v", gone)
	}

	if err := c.request("workspace/remove", map[string]string{"workspace_id": ""}).Error; err == nil || err.Code != jsonrpc.InvalidParams {
		t.Fatalf("removing without a workspace ID: %+v", err)
	}
	c.call("workspace/remove", map[string]string{"workspace_id": workspace.WorkspaceID})
	removed := c.next(func(m message) bool { return m.Method == "workspace/removed" })
	if !strings.Contains(string(removed.Params), workspace.WorkspaceID) {
		t.Fatalf("workspace/removed names another workspace: %s", removed.Params)
	}
	if n := listed(c); n != 0 {
		t.Fatalf("a removed workspace is still listed: %d", n)
	}
	if again := c.request("workspace/remove", map[string]string{"workspace_id": workspace.WorkspaceID}); again.Error == nil || again.Error.Code != notFound {
		t.Fatalf("a removed workspace was removed again: %+v", again.Error)
	}
	if late := c.request("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "effort": "low", "permission_mode": "plan",
	}); late.Error == nil || late.Error.Code != notFound {
		t.Fatalf("a session was created in a removed workspace: %+v", late.Error)
	}

	// The removal is stored: a daemon over the same store loads no workspace.
	restarted, err := New(st, cfg, 0, map[string]agent.Start{"fake": fake.Start}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if n := listed(connect(t, restarted)); n != 0 {
		t.Fatalf("a removed workspace came back after a restart: %d", n)
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
	s, err := New(st, cfg, 0, map[string]agent.Start{"fake": fake.Start}, MCP{})
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
	s, err := New(openStore(t), cfg, 0, map[string]agent.Start{"fake": fake.Start}, MCP{})
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
	st, c, args := unresumableSession(t)

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
	if lines := starts(t, args); len(lines) != 2 || strings.Contains(lines[1], "--resume") || !strings.Contains(lines[1], "--session-id=") {
		t.Fatalf("the next send did not start a new conversation: %q", lines)
	}
	workspaces, err := st.Workspaces()
	if err != nil || workspaces[0].Sessions[0].ResumeToken != "" {
		t.Fatalf("the stored token was kept: %v", err)
	}
}

// A session forgets a conversation Claude no longer has even when storing
// that fails, so the next send starts a new one instead of failing again.
func TestUnresumableConversationForgottenWhenNotStored(t *testing.T) {
	st, c, args := unresumableSession(t)
	st.Close()
	if failed := c.request("session/send", map[string]string{"session_id": "s", "text": "hi"}); failed.Error == nil || failed.Error.Message != lostConversation {
		t.Fatalf("a send that found the conversation gone did not say so: %+v", failed.Error)
	}
	if notice := c.update("s", "exited"); notice.Error != lostConversation {
		t.Fatalf("the transcript has no notice: %+v", notice)
	}
	c.call("session/send", map[string]string{"session_id": "s", "text": "hi"})
	if lines := starts(t, args); len(lines) != 2 || strings.Contains(lines[1], "--resume") {
		t.Fatalf("the next send did not start a new conversation: %q", lines)
	}
}

// starts returns the arguments of each start the stand-in Claude recorded.
func starts(t *testing.T, args string) []string {
	t.Helper()
	recorded, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(recorded)), "\n")
}

// unresumableSession serves a stored session whose resume token Claude no
// longer has. It returns the store, a client, and the file where the
// stand-in Claude records its arguments.
func unresumableSession(t *testing.T) (*store.Store, *client, string) {
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
	s, err := New(st, &config.Config{}, 0, map[string]agent.Start{"claude": claude.Start}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return st, connect(t, s), args
}

// A session keeps its conversation's resume token even when storing it fails,
// so the next process resumes the conversation.
func TestResumeTokenKeptWhenNotStored(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	// A stand-in for Claude that answers one prompt and exits.
	script := "#!/bin/sh\necho \"$@\" >> " + args + "\n" +
		"read line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		"read prompt\n" +
		`echo '{"type":"result","subtype":"success","is_error":false,"result":"done"}'` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{ID: "s", Harness: "claude", Model: "m", PermissionMode: "plan", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Start{"claude": claude.Start}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)

	st.Close()
	c.call("session/send", map[string]string{"session_id": "s", "text": "hi"})
	c.update("s", "exited")
	c.update("s", "parked")
	c.call("session/send", map[string]string{"session_id": "s", "text": "again"})
	starts, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(starts)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "--resume=") {
		t.Fatalf("the next process did not resume the conversation: %q", lines)
	}
}

// A mode Claude changes itself, as it does when a plan is approved, reaches
// every client and the store.
func TestClaudeModeChangeIsAdopted(t *testing.T) {
	dir := t.TempDir()
	// A stand-in for Claude that leaves plan mode once it has read a prompt.
	script := "#!/bin/sh\n" +
		"read line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		"read prompt\n" +
		`echo '{"type":"system","subtype":"status","status":null,"permissionMode":"default"}'` + "\n" +
		"exec cat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{ID: "s", Harness: "claude", Model: "m", Effort: "low", PermissionMode: "plan", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Start{"claude": claude.Start}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)

	c.call("session/send", map[string]string{"session_id": "s", "text": "hi"})
	configured := c.next(func(m message) bool { return m.Method == "session/configured" && m.sessionID() == "s" })
	var settings agent.Settings
	json.Unmarshal(configured.Params, &settings)
	if want := (agent.Settings{Model: "m", Effort: "low", PermissionMode: "manual"}); settings != want {
		t.Fatalf("session/configured reported %+v", settings)
	}
	var got struct {
		PermissionMode string `json:"permission_mode"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": "s"}), &got)
	if got.PermissionMode != "manual" {
		t.Fatalf("session/get reported %q", got.PermissionMode)
	}
	workspaces, err := st.Workspaces()
	if err != nil || workspaces[0].Sessions[0].PermissionMode != "manual" {
		t.Fatalf("the stored mode was not updated: %v", err)
	}
}

// An approval reaches every client and a reloaded snapshot, takes one answer,
// which reaches the harness untouched, and is refused once answered.
func TestApprovalAnsweredOnce(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m"}},
		PermissionModes: []config.Option{{ID: "manual"}},
	}}}
	s, err := New(openStore(t), cfg, 0, map[string]agent.Start{"fake": fake.Start}, MCP{})
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
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "permission_mode": "manual",
	}), &created)
	id := created.SessionID
	approve := func(requestID, response string) message {
		return c.request("session/approve", map[string]any{"session_id": id, "request_id": requestID, "response": json.RawMessage(response)})
	}

	if parked := approve("none", `{}`); parked.Error == nil || parked.Error.Code != approvalNotPending {
		t.Fatalf("a parked session took an answer: %+v", parked.Error)
	}
	c.call("session/send", map[string]string{"session_id": id, "text": `approve Bash {"command":"ls"}`})
	asked := c.update(id, "approval")
	var snapshot struct {
		State     string           `json:"state"`
		Approvals []agent.Approval `json:"approvals"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &snapshot)
	if snapshot.State != "requires_action" || len(snapshot.Approvals) != 1 || snapshot.Approvals[0].RequestID != asked.RequestID ||
		!strings.Contains(string(snapshot.Approvals[0].Request), `"input":{"command":"ls"}`) {
		t.Fatalf("the snapshot does not hold the approval: %+v", snapshot)
	}

	response := `{"behavior":"allow","updatedInput":{"n":9007199254740993}}`
	if r := approve(asked.RequestID, response); r.Error != nil {
		t.Fatalf("the answer was refused: %+v", r.Error)
	}
	if answered := c.update(id, "approval_answered"); answered.RequestID != asked.RequestID {
		t.Fatalf("approval_answered names another request: %+v", answered)
	}
	if result := c.update(id, "tool_result"); result.Result.Text != response {
		t.Fatalf("the harness received %q", result.Result.Text)
	}
	if again := approve(asked.RequestID, response); again.Error == nil || again.Error.Code != approvalNotPending {
		t.Fatalf("an answered approval took a second answer: %+v", again.Error)
	}
}

// A prompt sent while the harness works is queued, one at a time, until the
// harness takes it or Stop drops it.
func TestPromptQueuedWhileClaudeWorks(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m"}},
		PermissionModes: []config.Option{{ID: "manual"}},
	}}}
	s, err := New(openStore(t), cfg, 0, map[string]agent.Start{"fake": fake.Start}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)

	for _, ending := range []string{"stop", "allow"} {
		t.Run(ending, func(t *testing.T) {
			c.t = t
			var created struct {
				SessionID string `json:"session_id"`
			}
			json.Unmarshal(c.call("session/create", map[string]string{
				"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "permission_mode": "manual",
			}), &created)
			id := created.SessionID
			c.call("session/send", map[string]string{"session_id": id, "text": "approve Bash {}"})
			asked := c.update(id, "approval")
			c.forget(id)

			var sent struct {
				MessageID string `json:"message_id"`
			}
			json.Unmarshal(c.call("session/send", map[string]string{"session_id": id, "text": "steer"}), &sent)
			if queued := c.update(id, "prompt_queued"); queued.MessageID != sent.MessageID || queued.Text != "steer" {
				t.Fatalf("prompt_queued names another prompt: %+v", queued)
			}
			if busy := c.request("session/send", map[string]string{"session_id": id, "text": "again"}); busy.Error == nil || busy.Error.Code != sessionBusy {
				t.Fatalf("a second prompt was queued: %+v", busy.Error)
			}
			var snapshot struct {
				Queued *agent.Prompt `json:"queued"`
			}
			json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &snapshot)
			if snapshot.Queued == nil || snapshot.Queued.MessageID != sent.MessageID || snapshot.Queued.Text != "steer" {
				t.Fatalf("the snapshot does not hold the queued prompt: %+v", snapshot.Queued)
			}

			var history struct {
				History []agent.Update `json:"history"`
			}
			switch ending {
			case "stop":
				c.call("session/interrupt", map[string]string{"session_id": id})
				if cancelled := c.update(id, "prompt_cancelled"); cancelled.MessageID != sent.MessageID || cancelled.Text != "steer" {
					t.Fatalf("prompt_cancelled names another prompt: %+v", cancelled)
				}
				for c.update(id, "state").State != "idle" {
				}
				json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &history)
				for _, u := range history.History {
					if u.Type == "prompt" && u.MessageID == sent.MessageID {
						t.Fatalf("the dropped prompt is in the history: %+v", history.History)
					}
				}
			case "allow":
				c.call("session/approve", map[string]any{"session_id": id, "request_id": asked.RequestID, "response": json.RawMessage(`{"behavior":"allow"}`)})
				if taken := c.update(id, "prompt"); taken.MessageID != sent.MessageID || taken.Text != "steer" {
					t.Fatalf("the queued prompt was not taken: %+v", taken)
				}
				if result := c.update(id, "result"); result.MessageID != sent.MessageID || result.Result.Text != "You said: steer" {
					t.Fatalf("the taken prompt did not reply: %+v", result)
				}
			}
		})
	}
}

// A directory reached through a symlink is the workspace it already is.
func TestSameDirectoryIsOneWorkspace(t *testing.T) {
	s, err := New(openStore(t), &config.Config{}, 0, nil, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	add := func(path string) string {
		var w struct {
			ID string `json:"workspace_id"`
		}
		json.Unmarshal(c.call("workspace/create", map[string]string{"path": path}), &w)
		return w.ID
	}
	if first, again := add(dir), add(link); first != again {
		t.Fatalf("the directory and a symlink to it are two workspaces: %s, %s", first, again)
	}
}

// Claude in one session lists the others and sends them messages, which
// reach the recipient as prompts with their sender, one queued at a time.
func TestSendMessage(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake":   {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
		"broken": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
	}}
	broken := func(agent.Options) (agent.Agent, error) { return nil, errors.New("no such harness binary") }
	s, err := New(openStore(t), cfg, 0, map[string]agent.Start{"fake": fake.Start, "broken": broken}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	dir := t.TempDir()
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": dir}), &workspace)
	create := func(harness string) string {
		var created struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(c.call("session/create", map[string]string{
			"workspace_id": workspace.WorkspaceID, "harness": harness, "model": "m", "permission_mode": "manual",
		}), &created)
		return created.SessionID
	}
	idle := func(id string) {
		for c.update(id, "state").State != "idle" {
		}
	}
	a, b, cs, d := create("fake"), create("fake"), create("fake"), create("broken")
	c.call("session/send", map[string]string{"session_id": a, "text": "first from A\nmore"})
	idle(a)
	fromA, fromC := attach(t, s, a), attach(t, s, cs)

	type listed struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
		Workspace string `json:"workspace"`
		Harness   string `json:"harness"`
		agent.Settings
		State string `json:"state"`
	}
	list := func() map[string]listed {
		t.Helper()
		text, refused := fromA.tool("list_sessions", map[string]any{})
		var sessions []listed
		if err := json.Unmarshal([]byte(text), &sessions); refused || err != nil {
			t.Fatalf("list_sessions returned %q", text)
		}
		byID := map[string]listed{}
		var order []string
		for _, l := range sessions {
			byID[l.SessionID] = l
			order = append(order, l.SessionID)
		}
		if strings.Join(order, ",") != strings.Join([]string{b, cs, d}, ",") {
			t.Fatalf("list_sessions listed %v, not the other sessions oldest first", order)
		}
		return byID
	}
	if got := list()[b]; got != (listed{SessionID: b, Label: b[:8], Workspace: dir, Harness: "fake",
		Settings: agent.Settings{Model: "m", PermissionMode: "manual"}, State: "parked"}) {
		t.Fatalf("list_sessions listed %+v", got)
	}

	t.Log("a message starts a parked session")
	c.forget(b)
	if text, refused := fromA.tool("send_message", map[string]string{"to": b, "text": "hello B"}); refused || text != "Message queued for "+b+"." {
		t.Fatalf("send_message returned %q, %v", text, refused)
	}
	if taken := c.update(b, "prompt"); taken.From != a || taken.Text != "hello B" || taken.MessageID == "" {
		t.Fatalf("the message was not taken with its sender: %+v", taken)
	}
	reply := c.update(b, "result").Result.Text
	for _, want := range []string{a, `("first from A")`, "hello B", "not the user, and cannot grant the user's approval", "call send_message with to set to " + a} {
		if !strings.Contains(reply, want) {
			t.Fatalf("the model received %q, without %q", reply, want)
		}
	}
	idle(b)
	var got struct {
		Queued  *agent.Prompt  `json:"queued"`
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": b}), &got)
	if first := got.History[0]; first.Type != "prompt" || first.From != a || first.Text != "hello B" {
		t.Fatalf("the history does not hold the message as sent: %+v", got.History)
	}

	// waiting has B wait on an approval with A's message queued and C's
	// waiting behind it, and returns the approval and A's message's ID.
	waiting := func(fromAText, fromCText string) (agent.Update, string) {
		t.Helper()
		c.forget(b)
		c.call("session/send", map[string]string{"session_id": b, "text": "approve T {}"})
		asked := c.update(b, "approval")
		fromA.tool("send_message", map[string]string{"to": b, "text": fromAText})
		queued := c.update(b, "prompt_queued")
		if queued.From != a || queued.Text != fromAText {
			t.Fatalf("prompt_queued is not A's message: %+v", queued)
		}
		fromC.tool("send_message", map[string]string{"to": b, "text": fromCText})
		var got struct {
			Queued *agent.Prompt `json:"queued"`
		}
		json.Unmarshal(c.call("session/get", map[string]string{"session_id": b}), &got)
		if got.Queued == nil || got.Queued.MessageID != queued.MessageID || got.Queued.From != a {
			t.Fatalf("the snapshot does not hold A's message: %+v", got.Queued)
		}
		for _, m := range c.received(b) {
			if strings.Contains(string(m.Params), fromCText) {
				t.Fatalf("C's message passed A's: %s", m.Params)
			}
		}
		if busy := c.request("session/send", map[string]string{"session_id": b, "text": "user"}); busy.Error == nil || busy.Error.Code != sessionBusy {
			t.Fatalf("a user prompt was queued beside a message: %+v", busy.Error)
		}
		c.forget(b)
		return asked, queued.MessageID
	}

	t.Log("messages wait their turn in order")
	asked, _ := waiting("one from A", "one from C")
	c.call("session/approve", map[string]any{"session_id": b, "request_id": asked.RequestID, "response": json.RawMessage(`{"behavior":"allow"}`)})
	if taken := c.update(b, "prompt"); taken.From != a || taken.Text != "one from A" {
		t.Fatalf("A's message was not taken first: %+v", taken)
	}
	if queued := c.update(b, "prompt_queued"); queued.From != cs || queued.Text != "one from C" {
		t.Fatalf("C's message was not queued next: %+v", queued)
	}
	if taken := c.update(b, "prompt"); taken.From != cs || taken.Text != "one from C" {
		t.Fatalf("C's message was not taken: %+v", taken)
	}
	c.call("session/interrupt", map[string]string{"session_id": b})
	idle(b)

	t.Log("Stop drops every message Claude has not taken")
	_, dropped := waiting("two from A", "two from C")
	c.call("session/interrupt", map[string]string{"session_id": b})
	if cancelled := c.update(b, "prompt_cancelled"); cancelled.MessageID != dropped || cancelled.From != a {
		t.Fatalf("prompt_cancelled is not A's message: %+v", cancelled)
	}
	idle(b)
	time.Sleep(100 * time.Millisecond)
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": b}), &got)
	for _, m := range c.received(b) {
		if strings.Contains(string(m.Params), "two from C") {
			t.Fatalf("C's message outlived Stop: %s", m.Params)
		}
	}
	for _, u := range got.History {
		if strings.HasPrefix(u.Text, "two from") {
			t.Fatalf("a dropped message is in the history: %+v", u)
		}
	}

	t.Log("refusals")
	c.forget(b)
	for _, refusal := range []struct {
		arguments map[string]string
		reason    string
	}{
		{map[string]string{"to": b, "text": " "}, "to and text are required"},
		{map[string]string{"text": "hi"}, "to and text are required"},
		{map[string]string{"to": a, "text": "hi"}, "cannot send a message to yourself"},
		{map[string]string{"to": "nobody", "text": "hi"}, "session not found"},
	} {
		if text, refused := fromA.tool("send_message", refusal.arguments); !refused || text != refusal.reason {
			t.Errorf("send_message %v returned %q, %v", refusal.arguments, text, refused)
		}
	}
	c.call("session/get", map[string]string{"session_id": b})
	if received := c.received(b); len(received) != 0 {
		t.Fatalf("a refused message reached B: %v", received)
	}

	t.Log("a failed start drops the messages")
	fromA.tool("send_message", map[string]string{"to": d, "text": "hello D"})
	if exited := c.update(d, "exited"); !strings.Contains(exited.Error, "no such harness binary") {
		t.Fatalf("the failed start was reported as %+v", exited)
	}
	// A later operation would retry a start for a message still waiting; the
	// one after it reads the history that retry would leave.
	c.call("session/get", map[string]string{"session_id": d})
	var history struct {
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": d}), &history)
	exits := 0
	for _, u := range history.History {
		if u.Type == "exited" {
			exits++
		}
	}
	if exits != 1 {
		t.Fatalf("the failed start was retried: %+v", history.History)
	}

	sessions := list()
	if got := sessions[b]; got.Label != "approve T {}" || got.State != "idle" {
		t.Fatalf("B, whose first user prompt followed a message, is listed as %+v", got)
	}
	if got := sessions[cs]; got.Label != cs[:8] || got.State != "parked" {
		t.Fatalf("C is listed as %+v", got)
	}
}

// Messages waiting when Claude exits by itself are dropped, and Claude is not
// started again to deliver them.
func TestExitDropsWaitingMessages(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	exit := filepath.Join(dir, "exit")
	// A stand-in for Claude that reads one message and exits once told to,
	// without taking the message.
	script := "#!/bin/sh\necho \"$@\" >> " + args + "\n" +
		"read line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		"read message\n" +
		"while [ ! -e " + exit + " ]; do sleep 0.05; done\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	const sender, recipient = "sender-session", "recipient-session"
	for _, id := range []string{sender, recipient} {
		if err := st.AddSession("w", &store.Session{ID: id, Harness: "claude", Model: "m", PermissionMode: "manual", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Start{"claude": claude.Start}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	mcp := attach(t, s, sender)

	mcp.tool("send_message", map[string]string{"to": recipient, "text": "first"})
	if queued := c.update(recipient, "prompt_queued"); queued.Text != "first" {
		t.Fatalf("the first message was not queued in the harness: %+v", queued)
	}
	mcp.tool("send_message", map[string]string{"to": recipient, "text": "second"})
	if err := os.WriteFile(exit, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c.update(recipient, "parked")
	c.call("session/get", map[string]string{"session_id": recipient})
	if lines := starts(t, args); len(lines) != 1 {
		t.Fatalf("Claude was started again for a waiting message: %q", lines)
	}
	for _, m := range c.received(recipient) {
		if strings.Contains(string(m.Params), `"text":"second"`) {
			t.Fatalf("the waiting message outlived the exit: %s", m.Params)
		}
	}
}
