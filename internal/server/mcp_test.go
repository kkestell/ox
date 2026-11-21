package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/store"
)

type snapshotReadAgent struct {
	agent.Agent
	read func()
}

func (a snapshotReadAgent) Snapshot() agent.Snapshot {
	a.read()
	return a.Agent.Snapshot()
}

func TestListSessionsReadsEachSessionFromItsOwner(t *testing.T) {
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	const caller, first, last = "caller-session", "first-session", "last-session"
	for _, id := range []string{caller, first, last} {
		if err := st.AddSession("w", &store.Session{ID: id, Harness: "fake", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	read, release := make(chan struct{}), make(chan struct{})
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"fake": {Start: func(o agent.Options) (agent.Agent, error) {
		a, err := fake.Start(o)
		if o.SessionID == first {
			return snapshotReadAgent{a, func() { close(read) }}, err
		}
		return snapshotReadAgent{a, func() { <-release }}, err
	}}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	c := connect(t, s)
	c.call("session/send", map[string]string{"session_id": first, "text": "label for " + first})
	c.next(func(m message) bool { return m.Method == "session/labeled" && m.sessionID() == first })
	c.call("session/send", map[string]string{"session_id": last, "text": "background 60000"})
	for c.update(last, "state").State != agent.StateBackground {
	}
	mcp := attach(t, s, caller)
	listing := mcp.send("tools/call", map[string]any{"name": "list_sessions"})
	select {
	case <-read:
	case <-time.After(c.timeout):
		t.Fatal("list_sessions did not read the first session")
	}
	// The owner must finish its list read before it handles destroy. The
	// tool can't assemble its response yet, because the other read is gated.
	c.call("session/destroy", map[string]string{"session_id": first})
	unblock()
	response := mcp.response(listing)
	var result struct {
		IsError bool                    `json:"isError"`
		Content []struct{ Text string } `json:"content"`
	}
	var sessions []struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
		State     string `json:"state"`
	}
	if response.Error != nil || json.Unmarshal(response.Result, &result) != nil || result.IsError || len(result.Content) != 1 ||
		json.Unmarshal([]byte(result.Content[0].Text), &sessions) != nil || len(sessions) != 2 ||
		sessions[0].SessionID != first || sessions[0].Label != "label for "+first {
		t.Fatalf("listing lost the session read before deletion: %+v (%s)", sessions, response.Result)
	}
	if sessions[1].SessionID != last || sessions[1].State != agent.StateBackground {
		t.Fatalf("listing did not report the background state: %+v", sessions[1])
	}
}

func TestReadSession(t *testing.T) {
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	const caller, short, long, multibyte, tight, background = "caller-session", "short-session", "long-session", "multibyte-session", "tight-session", "background-session"
	for _, id := range []string{caller, short, long, multibyte, tight, background} {
		if err := st.AddSession("w", &store.Session{ID: id, Harness: "fake", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)
	stamp := at.Local().Format(timeLayout)
	seed := func(id string, updates ...agent.Update) {
		t.Helper()
		for _, u := range updates {
			data, err := json.Marshal(u)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.AddEvent(id, store.Event{CreatedAt: at, Data: data}); err != nil {
				t.Fatal(err)
			}
		}
	}
	longInput := `{"text":"` + strings.Repeat("x", 300) + `"}`
	seed(short,
		agent.Update{Type: "prompt", MessageID: "m1", Text: "hi"},
		agent.Update{Type: "prompt", MessageID: "m2", Text: "hello", From: "other", FromLabel: "Other label"},
		agent.Update{Type: "text_delta", MessageID: "m2", Text: "Hello there"},
		agent.Update{Type: "tool_use", ToolUseID: "t1", ToolName: "Bash", Input: json.RawMessage(longInput)},
		agent.Update{Type: "tool_result", ToolUseID: "t1", Result: &agent.Result{Text: "secret output", IsError: true}},
		agent.Update{Type: "tool_use", ToolUseID: "t2", ToolName: "Read", Input: json.RawMessage(`{ "a": 1 }`)},
		agent.Update{Type: "tool_result", ToolUseID: "t2", Result: &agent.Result{Text: "secret output"}},
		agent.Update{Type: "result", Result: &agent.Result{Text: "Hello there"}},
		agent.Update{Type: "result", Result: &agent.Result{Text: "boom", IsError: true}},
		agent.Update{Type: "exited", Error: "process died"},
		agent.Update{Type: "parked"},
	)
	filler := func(c string, n int) string { return strings.Repeat(c, n) }
	seed(long, agent.Update{Type: "text_delta", MessageID: "m0", Text: filler("0", 50000)})
	for i := 1; i <= 5; i++ {
		seed(long, agent.Update{Type: "text_delta", MessageID: fmt.Sprint("m", i), Text: filler(fmt.Sprint(i), 15000)})
	}
	// The multibyte session's entries fit the page by runes, not bytes.
	seed(multibyte,
		agent.Update{Type: "text_delta", MessageID: "m0", Text: filler("é", 50000)},
		agent.Update{Type: "text_delta", MessageID: "m1", Text: filler("é", 15000)},
		agent.Update{Type: "text_delta", MessageID: "m2", Text: filler("ü", 15000)},
	)
	// Each tight entry is 20,000 runes, so two fit a page only without their
	// 2-rune separator.
	seed(tight,
		agent.Update{Type: "text_delta", MessageID: "m0", Text: filler("a", 19989)},
		agent.Update{Type: "text_delta", MessageID: "m1", Text: filler("b", 19989)},
	)
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	mcp := attach(t, s, caller)
	read := func(arguments map[string]any) (string, bool) {
		t.Helper()
		return mcp.tool("read_session", arguments)
	}

	want := strings.Join([]string{
		"[" + stamp + "] User: hi",
		"[" + stamp + "] Message from Other label (other): hello",
		"Assistant: Hello there",
		"Tool: Bash " + longInput[:200] + "… (failed)",
		`Tool: Read {"a":1}`,
		"(turn finished)",
		"Error: boom",
		"Exited: process died",
		"(parked)",
	}, "\n\n")
	if got, refused := read(map[string]any{"to": short}); refused || got != want {
		t.Fatalf("read_session returned %q, %v; want %q", got, refused, want)
	}

	// The long session has six entries, then the parked event recorded when it
	// loaded, which is not rendered but holds index 6.
	delta := func(c string, n int) string { return "Assistant: " + filler(c, n) }
	earlier := func(cursor int) string {
		return fmt.Sprintf("Earlier entries: call read_session with cursor %d.\n\n", cursor)
	}
	for _, page := range []struct {
		to     string
		cursor any
		want   string
	}{
		{long, nil, earlier(4) + delta("4", 15000) + "\n\n" + delta("5", 15000)},
		{long, 4, earlier(2) + delta("2", 15000) + "\n\n" + delta("3", 15000)},
		{long, 2, earlier(1) + delta("1", 15000)},
		{long, 1, string([]rune(delta("0", 50000))[:40000]) + "…"},
		{multibyte, nil, earlier(1) + delta("é", 15000) + "\n\n" + delta("ü", 15000)},
		{multibyte, 1, string([]rune(delta("é", 50000))[:40000]) + "…"},
		{tight, nil, earlier(1) + delta("b", 19989)},
		{tight, 1, delta("a", 19989)},
	} {
		arguments := map[string]any{"to": page.to}
		if page.cursor != nil {
			arguments["cursor"] = page.cursor
		}
		if got, refused := read(arguments); refused || got != page.want+"\n\n(parked)" {
			t.Fatalf("read_session %s %v returned %d runes (refused %v), starting %.80q; want %.80q", page.to, page.cursor, len([]rune(got)), refused, got, page.want)
		}
	}
	c := connect(t, s)
	c.call("session/send", map[string]string{"session_id": background, "text": "background 60000"})
	for c.update(background, "state").State != agent.StateBackground {
	}
	if got, refused := read(map[string]any{"to": background}); refused || !strings.HasSuffix(got, "(turn finished)\n\n(background)") {
		t.Fatalf("read_session on a session in the background returned %q, %v", got, refused)
	}

	if got, refused := read(map[string]any{"to": 5}); !refused || got != "invalid arguments" {
		t.Errorf("undecodable arguments returned %q, %v", got, refused)
	}
	for _, cursor := range []int{0, 8} {
		if got, refused := read(map[string]any{"to": long, "cursor": cursor}); !refused || got != "cursor out of range" {
			t.Errorf("cursor %d returned %q, %v", cursor, got, refused)
		}
	}
}

// Claude in a top-level session starts children, which take its settings and
// directory unless the call overrides them, hear from it first, list beneath
// it, and outlive a restart; a parent cannot be deleted while it has them.
func TestCreateSession(t *testing.T) {
	models := []config.Model{
		{ID: "m", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}},
		{ID: "n", Efforts: []config.Option{{ID: "medium"}}},
	}
	modes := []config.Option{{ID: "manual"}, {ID: "plan"}}
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake":   {Models: models, PermissionModes: modes},
		"broken": {Models: models, PermissionModes: modes},
		"manual": {Models: models, PermissionModes: []config.Option{{ID: "manual"}}},
	}}
	var startedMu sync.Mutex
	started := map[string]string{} // each started session's Options.Workspace
	startedIn := func(id string) string {
		startedMu.Lock()
		defer startedMu.Unlock()
		return started[id]
	}
	harnesses := map[string]agent.Harness{
		"fake": {Start: func(o agent.Options) (agent.Agent, error) {
			startedMu.Lock()
			started[o.SessionID] = o.Workspace
			startedMu.Unlock()
			return fake.Start(o)
		}},
		"broken": {Start: func(agent.Options) (agent.Agent, error) { return nil, errors.New("no such harness binary") }},
	}
	st := openStore(t)
	s, err := New(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": dir}), &workspace)
	var parent struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "effort": "high", "permission_mode": "manual",
	}), &parent)
	a := parent.SessionID
	idle := func(c *client, id string) {
		for c.update(id, "state").State != "idle" {
		}
	}
	c.call("session/send", map[string]string{"session_id": a, "text": "parent A"})
	idle(c, a)
	fromA := attach(t, s, a)

	type created struct {
		SessionID string `json:"session_id"`
		Name      string `json:"name"`
		Harness   string `json:"harness"`
		agent.Settings
		Directory string `json:"directory"`
	}
	createChild := func(arguments map[string]string) created {
		t.Helper()
		text, refused := fromA.tool("create_session", arguments)
		var child created
		if err := json.Unmarshal([]byte(text), &child); refused || err != nil {
			t.Fatalf("create_session %v returned %q, %v", arguments, text, refused)
		}
		return child
	}
	createdChild := func(c *client, id string) child {
		t.Helper()
		var params struct {
			Label string `json:"label"`
			Child *child `json:"child"`
		}
		json.Unmarshal(c.next(func(m message) bool { return m.Method == "session/created" && m.sessionID() == id }).Params, &params)
		if params.Child == nil {
			t.Fatalf("session/created for %s has no child", id)
		}
		if params.Label != params.Child.Title {
			t.Fatalf("session/created labeled child %s %q, not its title %q", id, params.Label, params.Child.Title)
		}
		return *params.Child
	}

	t.Log("a child takes its parent's settings and directory and hears from it first")
	worker := createChild(map[string]string{"name": "worker", "title": "Worker", "message": "hello child"})
	if want := (created{SessionID: worker.SessionID, Name: "worker", Harness: "fake",
		Model: "m", Effort: "high", PermissionMode: "manual", Directory: dir}); worker != want {
		t.Fatalf("create_session returned %+v, not %+v", worker, want)
	}
	workerChild := child{ParentID: a, Name: "worker", Title: "Worker", Directory: dir}
	if got := createdChild(c, worker.SessionID); got != workerChild {
		t.Fatalf("session/created carried %+v", got)
	}
	if taken := c.update(worker.SessionID, "prompt"); taken.From != a || taken.FromLabel != "parent A" || taken.Text != "hello child" {
		t.Fatalf("the child did not take the message from its parent: %+v", taken)
	}
	if got := startedIn(worker.SessionID); got != dir {
		t.Fatalf("the child started in %q, not %q", got, dir)
	}
	idle(c, worker.SessionID)
	c.call("session/send", map[string]string{"session_id": worker.SessionID, "text": "from the user"})
	idle(c, worker.SessionID)
	for _, m := range c.received(worker.SessionID) {
		if m.Method == "session/labeled" {
			t.Fatalf("a prompt relabeled the child: %s", m.Params)
		}
	}

	type listedSession struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
		Child     *child `json:"child"`
	}
	listWorkspace := func(c *client) []listedSession {
		t.Helper()
		var listed struct {
			Workspaces []struct {
				Sessions []listedSession `json:"sessions"`
			} `json:"workspaces"`
		}
		json.Unmarshal(c.call("workspace/list", map[string]any{}), &listed)
		return listed.Workspaces[0].Sessions
	}
	if got := listWorkspace(c); len(got) != 2 || got[0].SessionID != a || got[0].Child != nil ||
		got[1].SessionID != worker.SessionID || got[1].Label != "Worker" || got[1].Child == nil || *got[1].Child != workerChild {
		t.Fatalf("workspace/list listed %+v", got)
	}
	if text, _ := fromA.tool("list_sessions", map[string]any{}); !strings.Contains(text, `"session_id":"`+worker.SessionID+`","label":"Worker"`) {
		t.Fatalf("list_sessions listed %s", text)
	}

	t.Log("the call overrides the model and directory")
	if other := createChild(map[string]string{"name": "other", "title": "Other", "message": "hi", "model": "n"}); other.Model != "n" || other.Effort != "medium" {
		t.Fatalf("a child on model n got %+v", other)
	}
	if low := createChild(map[string]string{"name": "low", "title": "Low", "message": "hi", "effort": "low"}); low.Model != "m" || low.Effort != "low" {
		t.Fatalf("a child at effort low got %+v", low)
	}
	if abs := createChild(map[string]string{"name": "abs", "title": "Abs", "message": "hi", "dir": filepath.Join(dir, "sub")}); abs.Directory != filepath.Join(dir, "sub") {
		t.Fatalf("a child in an absolute dir has directory %q", abs.Directory)
	}
	sub := createChild(map[string]string{"name": "sub", "title": "Sub", "message": "hi", "dir": "sub"})
	if sub.Directory != dir+"/sub" {
		t.Fatalf("a child in sub has directory %q", sub.Directory)
	}
	c.update(sub.SessionID, "prompt")
	if got := startedIn(sub.SessionID); got != dir+"/sub" {
		t.Fatalf("the child in sub started in %q", got)
	}
	c.call("session/configure", map[string]string{"session_id": a, "permission_mode": "plan"})
	if planner := createChild(map[string]string{"name": "planner", "title": "Planner", "message": "hi"}); planner.PermissionMode != "plan" {
		t.Fatalf("a child of a plan-mode parent got %+v", planner)
	}

	t.Log("refusals")
	if text, refused := fromA.tool("create_session", []string{"worker"}); !refused || text != "invalid arguments" {
		t.Fatalf("create_session with an array returned %q, %v", text, refused)
	}
	for _, refusal := range []struct {
		arguments map[string]string
		want      string
	}{
		{map[string]string{"name": "Bad", "title": "T", "message": "hi"}, "name must be lowercase letters, digits, and hyphens, starting with a letter or digit"},
		{map[string]string{"name": "x", "title": "  ", "message": "hi"}, "title must be one line of 1 to 48 characters"},
		{map[string]string{"name": "x", "title": strings.Repeat("t", 49), "message": "hi"}, "title must be one line of 1 to 48 characters"},
		{map[string]string{"name": "x", "title": "two\nlines", "message": "hi"}, "title must be one line of 1 to 48 characters"},
		{map[string]string{"name": "x", "title": "T", "message": " "}, "message is required"},
		{map[string]string{"name": "x", "title": "T", "message": "hi", "dir": "missing"}, "dir must be an existing directory"},
		{map[string]string{"name": "x", "title": "T", "message": "hi", "harness": "nope"}, `harness "nope" is not configured`},
		{map[string]string{"name": "x", "title": "T", "message": "hi", "harness": "broken"}, "model is required with another harness"},
		{map[string]string{"name": "x", "title": "T", "message": "hi", "model": "x"}, `model "x" is not configured`},
		{map[string]string{"name": "x", "title": "T", "message": "hi", "harness": "manual", "model": "m"}, `permission mode "plan" is not configured`},
		{map[string]string{"name": "worker", "title": "T", "message": "hi"}, `a child named "worker" already exists`},
	} {
		if text, refused := fromA.tool("create_session", refusal.arguments); !refused || text != refusal.want {
			t.Fatalf("create_session %v returned %q, %v, not the refusal %q", refusal.arguments, text, refused, refusal.want)
		}
	}
	if text, refused := attach(t, s, worker.SessionID).tool("create_session", map[string]string{"name": "x", "title": "T", "message": "hi"}); !refused || text != "a child session cannot create sessions" {
		t.Fatalf("a child's create_session returned %q, %v", text, refused)
	}

	t.Log("a child whose harness cannot be launched leaves no trace")
	if text, refused := fromA.tool("create_session", map[string]string{"name": "doomed", "title": "Doomed", "message": "hi", "harness": "broken", "model": "m"}); !refused || text != "no such harness binary" {
		t.Fatalf("create_session on a broken harness returned %q, %v", text, refused)
	}
	c.call("harness/list", map[string]any{})
	for _, m := range c.notifications("session/created") {
		if strings.Contains(string(m.Params), `"name":"doomed"`) {
			t.Fatalf("a child whose harness cannot be launched was published: %s", m.Params)
		}
	}
	createChild(map[string]string{"name": "doomed", "title": "Doomed", "message": "hi"})

	t.Log("children outlive a restart")
	before := listWorkspace(c)
	s.Close()
	startedMu.Lock()
	clear(started)
	startedMu.Unlock()
	s, err = New(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c = connect(t, s)
	after := listWorkspace(c)
	if len(after) != len(before) {
		t.Fatalf("after a restart workspace/list listed %+v, not %+v", after, before)
	}
	for i := range before {
		if after[i].SessionID != before[i].SessionID || after[i].Label != before[i].Label ||
			(after[i].Child == nil) != (before[i].Child == nil) || after[i].Child != nil && *after[i].Child != *before[i].Child {
			t.Fatalf("after a restart workspace/list listed %+v, not %+v", after[i], before[i])
		}
	}
	if _, refused := attach(t, s, a).tool("send_message", map[string]string{"to": sub.SessionID, "text": "again"}); refused {
		t.Fatal("send_message to the restored child was refused")
	}
	c.update(sub.SessionID, "prompt")
	if got := startedIn(sub.SessionID); got != dir+"/sub" {
		t.Fatalf("the restored child in sub started in %q", got)
	}

	t.Log("a parent with children cannot be deleted")
	if response := c.request("session/destroy", map[string]string{"session_id": a}); response.Error == nil ||
		response.Error.Code != -32000 || response.Error.Message != "session has child sessions" {
		t.Fatalf("destroying a parent returned %+v", response)
	}
	for _, id := range after[1:] {
		c.call("session/destroy", map[string]string{"session_id": id.SessionID})
	}
	c.call("session/destroy", map[string]string{"session_id": a})
}

// lockedBuffer collects log output written from any goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// A child whose harness cannot be launched answers with the launch error even
// when its row cannot be deleted again, and the failed delete is logged.
func TestFailedLaunchKeepsItsErrorWhenDeleteFails(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
	}}
	st := openStore(t)
	// The parent is never prompted, so the only start is the child's.
	start := func(agent.Options) (agent.Agent, error) {
		st.Close()
		return nil, errors.New("no such harness binary")
	}
	s, err := New(st, cfg, 0, map[string]agent.Harness{"fake": {Start: start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	logged := &lockedBuffer{}
	log.SetOutput(logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	c := connect(t, s)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	var parent struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "permission_mode": "manual",
	}), &parent)
	if text, refused := attach(t, s, parent.SessionID).tool("create_session", map[string]string{"name": "doomed", "title": "Doomed", "message": "hi"}); !refused || text != "no such harness binary" {
		t.Fatalf("create_session with an unlaunchable harness and a closed store returned %q, %v", text, refused)
	}
	if !strings.Contains(logged.String(), "delete after failed launch") {
		t.Fatalf("the failed delete was not logged: %q", logged.String())
	}
}

// A child is published only once its harness is launched. A shutdown that
// races its creation waits for it, then ends the child like any member, and
// the child keeps its first message through a restart.
func TestCreateSessionDuringShutdown(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
	}}
	// The parent is never prompted, so the only start is the child's.
	starting, release := make(chan struct{}), make(chan struct{})
	start := func(o agent.Options) (agent.Agent, error) {
		close(starting)
		<-release
		return fake.Start(o)
	}
	st := openStore(t)
	s, err := New(st, cfg, 0, map[string]agent.Harness{"fake": {Start: start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	c := connect(t, s)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	var parent struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "permission_mode": "manual",
	}), &parent)
	fromParent := attach(t, s, parent.SessionID)

	creating := fromParent.send("tools/call", map[string]any{"name": "create_session",
		"arguments": map[string]string{"name": "late", "title": "Late", "message": "hi"}})
	<-starting
	c.call("harness/list", map[string]any{})
	if created := c.notifications("session/created"); len(created) != 1 || created[0].sessionID() != parent.SessionID {
		t.Fatalf("a child was published while its harness was launching: %+v", created)
	}
	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	close(release)

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	var late struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(fromParent.response(creating).Result, &result)
	if result.IsError || len(result.Content) != 1 || json.Unmarshal([]byte(result.Content[0].Text), &late) != nil || late.SessionID == "" {
		t.Fatalf("create_session racing shutdown returned %+v", result)
	}
	if first := c.next(func(m message) bool { return m.Method != "" && m.sessionID() == late.SessionID }); first.Method != "session/created" {
		t.Fatalf("the page heard %s about the child before session/created", first.Method)
	}
	<-closed
	c.update(late.SessionID, agent.UpdateParked)

	s, err = New(st, cfg, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c = connect(t, s)
	var listed struct {
		Workspaces []struct {
			Sessions []struct {
				SessionID string `json:"session_id"`
			} `json:"sessions"`
		} `json:"workspaces"`
	}
	json.Unmarshal(c.call("workspace/list", map[string]any{}), &listed)
	if sessions := listed.Workspaces[0].Sessions; len(sessions) != 2 || sessions[1].SessionID != late.SessionID {
		t.Fatalf("after a restart workspace/list listed %+v", sessions)
	}
	var got struct {
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": late.SessionID}), &got)
	if !slices.ContainsFunc(got.History, func(u agent.Update) bool {
		return u.Type == agent.UpdatePrompt && u.From == parent.SessionID && u.Text == "hi"
	}) {
		t.Fatalf("after a restart the child's history is %+v", got.History)
	}
}

// Archiving a parent archives its child, stops both, and leaves them
// read-only, unlisted, and archived through a restart until unarchived.
func TestArchive(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}}},
		PermissionModes: []config.Option{{ID: "manual"}},
	}}}
	configuring := make(chan struct{}, 1)
	start := func(o agent.Options) (agent.Agent, error) {
		a, err := fake.Start(o)
		return configuringAgent{a, configuring}, err
	}
	harnesses := map[string]agent.Harness{"fake": {Start: start}}
	st := openStore(t)
	s, err := New(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	workspaceIn := func(c *client) string {
		var workspace struct {
			WorkspaceID string `json:"workspace_id"`
		}
		json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
		return workspace.WorkspaceID
	}
	newSession := func(workspaceID string) string {
		var created struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(c.call("session/create", map[string]string{
			"workspace_id": workspaceID, "harness": "fake", "model": "m", "effort": "low", "permission_mode": "manual",
		}), &created)
		return created.SessionID
	}
	idle := func(id string) {
		for c.update(id, "state").State != "idle" {
		}
	}
	notified := func(method, id string) {
		t.Helper()
		c.next(func(m message) bool { return m.Method == method && m.sessionID() == id })
	}
	archivedIn := func(c *client) map[string]bool {
		t.Helper()
		var listed struct {
			Workspaces []struct {
				Sessions []struct {
					SessionID string `json:"session_id"`
					Archived  bool   `json:"archived"`
				} `json:"sessions"`
			} `json:"workspaces"`
		}
		json.Unmarshal(c.call("workspace/list", map[string]any{}), &listed)
		archived := map[string]bool{}
		for _, w := range listed.Workspaces {
			for _, sess := range w.Sessions {
				archived[sess.SessionID] = sess.Archived
			}
		}
		return archived
	}
	refused := func(response message, code int, text string) {
		t.Helper()
		if response.Error == nil || response.Error.Code != code || response.Error.Message != text {
			t.Fatalf("got %+v, not the refusal %d %q", response.Error, code, text)
		}
	}

	workspace := workspaceIn(c)
	a := newSession(workspace)
	c.call("session/send", map[string]string{"session_id": a, "text": "parent"})
	idle(a)
	fromA := attach(t, s, a)
	text, isError := fromA.tool("create_session", map[string]string{"name": "kid", "title": "Kid", "message": "hello"})
	var kid struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(text), &kid) != nil || isError {
		t.Fatalf("create_session returned %q", text)
	}
	k := kid.SessionID
	idle(k)
	b := newSession(workspaceIn(c))
	c.call("session/send", map[string]string{"session_id": b, "text": "bystander"})
	idle(b)
	fromB := attach(t, s, b)

	t.Log("a session with a prompt held through a configuration is not archived")
	c.forget(b)
	configure := c.send("session/configure", map[string]string{"session_id": b, "effort": "high"})
	<-configuring
	var held struct {
		MessageID string `json:"message_id"`
	}
	json.Unmarshal(c.call("session/send", map[string]string{"session_id": b, "text": "held"}), &held)
	refused(c.request("session/archive", map[string]string{"session_id": b}), sessionBusy, "session is busy")
	if response := c.response(configure); response.Error != nil {
		t.Fatalf("the configuration failed: %+v", response.Error)
	}
	if taken := c.update(b, "prompt"); taken.MessageID != held.MessageID {
		t.Fatalf("the configuration's end took %s, not the held prompt %s", taken.MessageID, held.MessageID)
	}
	idle(b)

	t.Log("a family with a busy member is not archived")
	c.call("session/send", map[string]string{"session_id": a, "text": "approve Bash {}"})
	asked := c.update(a, "approval")
	refused(c.request("session/archive", map[string]string{"session_id": a}), sessionBusy, "session is busy")
	for _, id := range []string{a, k} {
		for _, m := range c.received(id) {
			if m.Method == "session/archived" {
				t.Fatalf("a refused archive told clients %s was archived", id)
			}
		}
	}
	if got := archivedIn(c); got[a] || got[k] {
		t.Fatalf("a refused archive archived %v", got)
	}
	c.call("session/approve", map[string]any{"session_id": a, "request_id": asked.RequestID, "response": json.RawMessage(`{"behavior":"allow"}`)})
	idle(a)

	t.Log("a session in the background is archived and stopped")
	bg := newSession(workspace)
	c.call("session/send", map[string]string{"session_id": bg, "text": "background 60000"})
	for c.update(bg, "state").State != agent.StateBackground {
	}
	c.call("session/archive", map[string]string{"session_id": bg})
	notified("session/archived", bg)
	c.update(bg, "parked")

	t.Log("archiving the parent archives and stops its child")
	c.call("session/archive", map[string]string{"session_id": a})
	for _, id := range []string{a, k} {
		notified("session/archived", id)
		c.update(id, "parked")
	}
	if got := archivedIn(c); !got[a] || !got[k] || got[b] {
		t.Fatalf("workspace/list marked %v", got)
	}

	t.Log("an archived session is read-only")
	refused(c.request("session/send", map[string]string{"session_id": a, "text": "hi"}), operationFailed, "session is archived")
	refused(c.request("session/configure", map[string]string{"session_id": a, "effort": "high"}), operationFailed, "session is archived")
	var settings agent.Settings
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": a}), &settings)
	if settings.Effort != "low" {
		t.Fatalf("a refused configuration changed the settings to %+v", settings)
	}

	t.Log("other sessions cannot list or message it, but can read it")
	if text, _ := fromB.tool("list_sessions", map[string]any{}); strings.Contains(text, a) || strings.Contains(text, k) {
		t.Fatalf("list_sessions listed archived sessions: %s", text)
	}
	if text, isError := fromB.tool("send_message", map[string]string{"to": k, "text": "hi"}); !isError || text != "session is archived" {
		t.Fatalf("send_message to an archived session returned %q, %v", text, isError)
	}
	if text, isError := fromB.tool("read_session", map[string]string{"to": a}); isError || !strings.Contains(text, "User: parent") || !strings.HasSuffix(text, "(parked)") {
		t.Fatalf("read_session on an archived session returned %q, %v", text, isError)
	}
	if text, isError := fromA.tool("create_session", map[string]string{"name": "late", "title": "Late", "message": "hi"}); !isError || text != "session is archived" {
		t.Fatalf("an archived session's create_session returned %q, %v", text, isError)
	}
	refused(c.request("session/unarchive", map[string]string{"session_id": k}), operationFailed, "parent session is archived")

	t.Log("archived sessions stay archived through a restart")
	restarted, err := New(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if got := archivedIn(connect(t, restarted)); !got[a] || !got[k] || got[b] {
		t.Fatalf("after a restart workspace/list marked %v", got)
	}

	t.Log("unarchiving the parent unarchives its child")
	c.call("session/unarchive", map[string]string{"session_id": a})
	notified("session/unarchived", a)
	notified("session/unarchived", k)
	c.call("session/send", map[string]string{"session_id": a, "text": "back"})
	idle(a)

	t.Log("a child is archived alone")
	c.call("session/archive", map[string]string{"session_id": k})
	notified("session/archived", k)
	if got := archivedIn(c); got[a] || !got[k] {
		t.Fatalf("archiving the child marked %v", got)
	}
	c.call("session/unarchive", map[string]string{"session_id": k})
	notified("session/unarchived", k)

	t.Log("an archived session can be deleted, and keeps its workspace")
	c.call("session/archive", map[string]string{"session_id": k})
	c.call("session/destroy", map[string]string{"session_id": k})
	c.call("session/archive", map[string]string{"session_id": a})
	refused(c.request("workspace/remove", map[string]string{"workspace_id": workspace}), operationFailed, "workspace has sessions")
}
