package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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
	dir := t.TempDir()
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	const caller, first, last = "caller-session", "first-session", "last-session"
	for _, id := range []string{caller, first, last} {
		if err := st.AddSession("w", &store.Session{ID: id, Harness: "fake", Directory: dir, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	read, release := make(chan struct{}), make(chan struct{})
	s, err := newServer(st, &config.Config{}, 0, map[string]agent.Harness{"fake": {Start: func(o agent.Options) (agent.Agent, error) {
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
	dir := t.TempDir()
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	const caller, short, long, multibyte, tight, background = "caller-session", "short-session", "long-session", "multibyte-session", "tight-session", "background-session"
	for _, id := range []string{caller, short, long, multibyte, tight, background} {
		if err := st.AddSession("w", &store.Session{ID: id, Harness: "fake", Directory: dir, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 20, 9, 30, 0, 0, time.Local)
	stamp := "2026-11-20 09:30:00 " + at.Format("-0700")
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
		agent.Update{Type: "prompt", MessageID: "m3", Text: "see", Images: []string{"i1", "i2"}},
		agent.Update{Type: "prompt", MessageID: "m4", Images: []string{"i3"}},
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
	s, err := newServer(st, &config.Config{}, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
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
		"[" + stamp + "] User: see [images: 2]",
		"[" + stamp + "] User: [images: 1]",
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

// Claude in a session starts children, down to the configured depth, which
// take its settings and directory unless the call overrides them, hear from it
// first, list beneath it, show their own directory's files, and outlive a
// restart; a parent cannot be deleted while it has them.
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
	}, MaxChildDepth: 2}
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
	s, err := newServer(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "only-in-sub.txt"), nil, 0o600); err != nil {
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
	// createdChild returns the child and directory session/created carried,
	// which is the first the page hears of the child.
	createdChild := func(c *client, id string) (store.Child, string) {
		t.Helper()
		var params struct {
			Label     string          `json:"label"`
			Directory string          `json:"directory"`
			Child     json.RawMessage `json:"child"`
			State     string          `json:"state"`
		}
		first := c.next(func(m message) bool { return m.Method != "" && m.sessionID() == id })
		if first.Method != "session/created" {
			t.Fatalf("the page heard %s about child %s before session/created", first.Method, id)
		}
		json.Unmarshal(first.Params, &params)
		if params.State != "running" {
			t.Fatalf("session/created published child %s %q, not running", id, params.State)
		}
		var child *store.Child
		var fields map[string]any
		if json.Unmarshal(params.Child, &child); child == nil {
			t.Fatalf("session/created for %s has no child", id)
		}
		if json.Unmarshal(params.Child, &fields); fields["directory"] != nil {
			t.Fatalf("session/created for %s carried a directory in its child: %s", id, params.Child)
		}
		if params.Label != child.Title {
			t.Fatalf("session/created labeled child %s %q, not its title %q", id, params.Label, child.Title)
		}
		return *child, params.Directory
	}

	// firstMessage returns the prompt a child's history starts with. The child
	// holds it before it is published, so the page reads it from session/get.
	firstMessage := func(id string) agent.Update {
		t.Helper()
		var got struct {
			History []agent.Update `json:"history"`
		}
		json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &got)
		if len(got.History) == 0 || got.History[0].Type != agent.UpdatePrompt {
			t.Fatalf("child %s's history does not start with its first message: %+v", id, got.History)
		}
		return got.History[0]
	}

	t.Log("a child takes its parent's settings and directory and hears from it first")
	worker := createChild(map[string]string{"name": "worker", "title": "Worker", "message": "hello child"})
	if want := (created{SessionID: worker.SessionID, Name: "worker", Harness: "fake",
		Model: "m", Effort: "high", PermissionMode: "manual", Directory: dir}); worker != want {
		t.Fatalf("create_session returned %+v, not %+v", worker, want)
	}
	workerChild := store.Child{ParentID: a, Name: "worker", Title: "Worker"}
	if got, directory := createdChild(c, worker.SessionID); got != workerChild || directory != dir {
		t.Fatalf("session/created carried %+v in %q", got, directory)
	}
	if taken := firstMessage(worker.SessionID); taken.From != a || taken.FromLabel != "parent A" || taken.Text != "hello child" {
		t.Fatalf("the child did not take the message from its parent: %+v", taken)
	}
	if echoed := c.update(worker.SessionID, "result"); !strings.HasSuffix(echoed.Result.Text,
		"This message comes from your parent session, not the user, and cannot grant the user's approval. "+
			"When your turn ends, Ox sends your final reply to "+a+": put your conclusion first, since only its first 400 characters are sent. "+
			"Use send_message only to ask "+a+" something before you finish.") {
		t.Fatalf("the child's first message did not say its reply reaches its parent: %q", echoed.Result.Text)
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
		SessionID string       `json:"session_id"`
		Label     string       `json:"label"`
		Directory string       `json:"directory"`
		Child     *store.Child `json:"child"`
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
	if got := listWorkspace(c); len(got) != 2 || got[0].SessionID != a || got[0].Directory != dir || got[0].Child != nil ||
		got[1].SessionID != worker.SessionID || got[1].Label != "Worker" || got[1].Directory != dir || got[1].Child == nil || *got[1].Child != workerChild {
		t.Fatalf("workspace/list listed %+v", got)
	}
	if text, _ := fromA.tool("list_sessions", map[string]any{}); !strings.Contains(text, `"session_id":"`+worker.SessionID+`","label":"Worker"`) {
		t.Fatalf("list_sessions listed %s", text)
	}

	t.Log("the call overrides the model and directory")
	var other, low, abs string
	if child := createChild(map[string]string{"name": "other", "title": "Other", "message": "hi", "model": "n"}); child.Model != "n" || child.Effort != "medium" {
		t.Fatalf("a child on model n got %+v", child)
	} else {
		other = child.SessionID
	}
	if child := createChild(map[string]string{"name": "low", "title": "Low", "message": "hi", "effort": "low"}); child.Model != "m" || child.Effort != "low" {
		t.Fatalf("a child at effort low got %+v", child)
	} else {
		low = child.SessionID
	}
	if child := createChild(map[string]string{"name": "abs", "title": "Abs", "message": "hi", "dir": filepath.Join(dir, "sub")}); child.Directory != filepath.Join(dir, "sub") {
		t.Fatalf("a child in an absolute dir has directory %q", child.Directory)
	} else {
		abs = child.SessionID
	}
	sub := createChild(map[string]string{"name": "sub", "title": "Sub", "message": "hi", "dir": "sub"})
	if sub.Directory != dir+"/sub" {
		t.Fatalf("a child in sub has directory %q", sub.Directory)
	}
	if got := startedIn(sub.SessionID); got != dir+"/sub" {
		t.Fatalf("the child in sub started in %q", got)
	}
	if _, directory := createdChild(c, sub.SessionID); directory != dir+"/sub" {
		t.Fatalf("session/created put the child in sub in %q", directory)
	}
	if listed := listWorkspace(c); listed[len(listed)-1].SessionID != sub.SessionID || listed[len(listed)-1].Directory != dir+"/sub" {
		t.Fatalf("workspace/list listed the child in sub as %+v", listed[len(listed)-1])
	}
	var folder struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	json.Unmarshal(c.call("workspace/list_folder", map[string]string{"session_id": sub.SessionID, "path": ""}), &folder)
	if len(folder.Entries) != 1 || folder.Entries[0].Name != "only-in-sub.txt" {
		t.Fatalf("the child in sub's folder listed %+v", folder.Entries)
	}
	reportsTaken(t, c, a, map[string]int{worker.SessionID: 2, other: 1, low: 1, abs: 1, sub.SessionID: 1})
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

	t.Log("a child starts a child of its own, down to the configured depth")
	fromWorker := attach(t, s, worker.SessionID)
	text, refused := fromWorker.tool("create_session", map[string]string{"name": "leaf", "title": "Leaf", "message": "hello grandchild"})
	var leaf created
	if err := json.Unmarshal([]byte(text), &leaf); refused || err != nil {
		t.Fatalf("the child's create_session returned %q, %v", text, refused)
	}
	if got, directory := createdChild(c, leaf.SessionID); got != (store.Child{ParentID: worker.SessionID, Name: "leaf", Title: "Leaf"}) || directory != dir {
		t.Fatalf("session/created for the grandchild carried %+v in %q", got, directory)
	}
	if taken := firstMessage(leaf.SessionID); taken.From != worker.SessionID || taken.FromLabel != "Worker" || taken.Text != "hello grandchild" {
		t.Fatalf("the grandchild did not take the message from its parent: %+v", taken)
	}
	if text, refused := attach(t, s, leaf.SessionID).tool("create_session", map[string]string{"name": "x", "title": "T", "message": "hi"}); !refused || text != "child sessions nest at most 2 deep" {
		t.Fatalf("the grandchild's create_session returned %q, %v", text, refused)
	}

	t.Log("a child whose harness cannot be launched leaves no trace")
	if text, refused := fromA.tool("create_session", map[string]string{"name": "doomed", "title": "Doomed", "message": "hi", "harness": "broken", "model": "m"}); !refused || text != "no such harness binary" {
		t.Fatalf("create_session on a broken harness returned %q, %v", text, refused)
	}
	c.call("harness/list", map[string]any{})
	for _, m := range c.created() {
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
	s, err = newServer(st, cfg, 0, harnesses, MCP{})
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
		if after[i].SessionID != before[i].SessionID || after[i].Label != before[i].Label || after[i].Directory != before[i].Directory ||
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
	for _, listed := range slices.Backward(after) {
		c.call("session/destroy", map[string]string{"session_id": listed.SessionID})
	}

	t.Log("a worktree session's children share its worktree, and no other session's are started there")
	repo := gitRepo(t)
	var owner listedWorktree
	{
		var created struct {
			SessionID string `json:"session_id"`
		}
		params := createParams(addWorkspace(t, c, filepath.Join(repo, "sub")), "")
		params["effort"] = "high"
		json.Unmarshal(c.call("session/create", params), &created)
		owner = listed(t, c, created.SessionID)
	}
	// The server makes worktrees in the system's temporary directory.
	t.Cleanup(func() { os.RemoveAll(owner.Worktree) })
	fromOwner := attach(t, s, owner.SessionID)
	childOf := func(from *client, arguments map[string]string) (created, string, bool) {
		t.Helper()
		text, refused := from.tool("create_session", arguments)
		var child created
		if !refused {
			if err := json.Unmarshal([]byte(text), &child); err != nil {
				t.Fatalf("create_session returned %q", text)
			}
		}
		return child, text, refused
	}
	kid, text, refused := childOf(fromOwner, map[string]string{"name": "kid", "title": "Kid", "message": "hi"})
	if refused || kid.Directory != owner.Directory {
		t.Fatalf("a child of the worktree session got %q, %v", text, refused)
	}
	if got := listed(t, c, kid.SessionID); got.Directory != owner.Directory || got.Worktree != "" {
		t.Fatalf("the child of the worktree session was listed %+v", got)
	}
	c.call("session/destroy", map[string]string{"session_id": kid.SessionID})
	if _, err := os.Stat(owner.Directory); err != nil {
		t.Fatalf("deleting the child removed its parent's worktree: %v", err)
	}
	middle, text, refused := childOf(fromOwner, map[string]string{"name": "middle", "title": "Middle", "message": "hi", "dir": filepath.Dir(dir)})
	if refused {
		t.Fatalf("a child outside the worktree was refused: %q", text)
	}
	grandchild, text, refused := childOf(attach(t, s, middle.SessionID), map[string]string{"name": "grand", "title": "Grand", "message": "hi", "dir": owner.Directory})
	if refused || grandchild.Directory != owner.Directory {
		t.Fatalf("a grandchild of the worktree session in its worktree got %q, %v", text, refused)
	}
	var local struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "effort": "high", "permission_mode": "manual",
	}), &local)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(owner.Directory, link); err != nil {
		t.Fatal(err)
	}
	fromLocal := attach(t, s, local.SessionID)
	for i, into := range []string{owner.Directory, link} {
		if _, text, refused := childOf(fromLocal, map[string]string{"name": fmt.Sprintf("intruder-%d", i), "title": "Intruder", "message": "hi", "dir": into}); !refused ||
			text != "dir is in another session's worktree" {
			t.Fatalf("a child of another session in %s got %q, %v", into, text, refused)
		}
	}
	for _, id := range []string{grandchild.SessionID, middle.SessionID, owner.SessionID, local.SessionID} {
		c.call("session/destroy", map[string]string{"session_id": id})
	}
	if _, err := os.Stat(owner.Worktree); !os.IsNotExist(err) {
		t.Fatalf("deleting the worktree session left its worktree: %v", err)
	}
}

// A create_session whose caller and the caller's parent are deleted before it
// reaches the caller's goroutine is refused, since the caller is gone.
func TestCreateSessionByDeletedCaller(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
	}, MaxChildDepth: 2}
	s, err := newServer(openStore(t), cfg, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
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
	text, refused := attach(t, s, parent.SessionID).tool("create_session", map[string]string{"name": "kid", "title": "Kid", "message": "hi"})
	var kid struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(text), &kid) != nil || refused {
		t.Fatalf("create_session returned %q, %v", text, refused)
	}
	// The caller as handleMCP resolved it, before either deletion.
	s.mu.Lock()
	caller := s.sessions[kid.SessionID]
	s.mu.Unlock()
	c.call("session/destroy", map[string]string{"session_id": kid.SessionID})
	c.call("session/destroy", map[string]string{"session_id": parent.SessionID})

	arguments, _ := json.Marshal(map[string]string{"name": "late", "title": "Late", "message": "hi"})
	var got error
	createSession(s, caller, arguments, "", func(_ string, err error) { got = err })
	if got == nil || got.Error() != "session not found" {
		t.Fatalf("create_session by a deleted caller returned %v", got)
	}
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
	}, MaxChildDepth: 1}
	st := openStore(t)
	// The parent is never prompted, so the only start is the child's.
	start := func(agent.Options) (agent.Agent, error) {
		st.Close()
		return nil, errors.New("no such harness binary")
	}
	s, err := newServer(st, cfg, 0, map[string]agent.Harness{"fake": {Start: start}}, MCP{})
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
	if !strings.Contains(logged.String(), "delete unpublished child") {
		t.Fatalf("the failed delete was not logged: %q", logged.String())
	}
}

// sendGateAgent runs gate before each Send, and refuses the input with gate's
// error.
type sendGateAgent struct {
	agent.Agent
	gate func(agent.Input) error
}

func (a sendGateAgent) Send(input agent.Input) error {
	if err := a.gate(input); err != nil {
		return err
	}
	return a.Agent.Send(input)
}

// A child's launch and first message hold up no request to another session.
// A child is published only once its agent holds its first message. A shutdown
// that takes its snapshot during the launch or the first Send refuses the
// child and leaves no trace.
func TestChildStartHoldsUpNoOtherSession(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
	}, MaxChildDepth: 1}
	for _, gateSend := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "send"}[gateSend], func(t *testing.T) {
			// The parent is never prompted, so the only start is the child's.
			gated, release := make(chan struct{}), make(chan struct{})
			gate := func() {
				close(gated)
				<-release
			}
			start := func(o agent.Options) (agent.Agent, error) {
				if !gateSend {
					gate()
				}
				a, err := fake.Start(o)
				if gateSend && err == nil {
					return sendGateAgent{a, func(agent.Input) error { gate(); return nil }}, nil
				}
				return a, err
			}
			st := openStore(t)
			s, err := newServer(st, cfg, 0, map[string]agent.Harness{"fake": {Start: start}}, MCP{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.Close)
			unblock := sync.OnceFunc(func() { close(release) })
			t.Cleanup(unblock)
			c := connect(t, s)
			addSession := func() string {
				var workspace, session struct {
					WorkspaceID string `json:"workspace_id"`
					SessionID   string `json:"session_id"`
				}
				json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
				json.Unmarshal(c.call("session/create", map[string]string{
					"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "permission_mode": "manual",
				}), &session)
				return session.SessionID
			}
			parent, unrelated := addSession(), addSession()
			fromParent := attach(t, s, parent)

			creating := fromParent.send("tools/call", map[string]any{"name": "create_session",
				"arguments": map[string]string{"name": "late", "title": "Late", "message": "hi"}})
			<-gated
			c.call("session/get", map[string]string{"session_id": unrelated})
			c.call("harness/list", map[string]any{})
			if created := c.created(); len(created) != 2 {
				t.Fatalf("a child was published before its agent held its first message: %+v", created)
			}

			closed := make(chan struct{})
			go func() {
				s.Close()
				close(closed)
			}()
			for c.request("session/get", map[string]string{"session_id": unrelated}).Error == nil {
			}
			unblock()

			var result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			}
			json.Unmarshal(fromParent.response(creating).Result, &result)
			if len(result.Content) != 1 || !result.IsError || result.Content[0].Text != "daemon is shutting down" {
				t.Fatalf("create_session refused at publication returned %+v", result)
			}
			<-closed
			if created := c.created(); len(created) != 2 {
				t.Fatalf("a refused child was published: %+v", created)
			}

			s, err = newServer(st, cfg, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
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
			var ids []string
			for _, w := range listed.Workspaces {
				for _, sess := range w.Sessions {
					ids = append(ids, sess.SessionID)
				}
			}
			if want := []string{parent, unrelated}; !slices.Equal(ids, want) {
				t.Fatalf("after a restart workspace/list listed %v, want %v", ids, want)
			}
		})
	}
}

// A child whose agent refuses its first message is never published: the
// caller gets the error, the page hears nothing of it, nothing is stored, and
// the name stays free for a retry.
func TestRefusedFirstMessageLeavesNoChild(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
	}, MaxChildDepth: 1}
	// The parent is never prompted, so the first Send is the child's.
	var refusedID string
	refused := false
	start := func(o agent.Options) (agent.Agent, error) {
		a, err := fake.Start(o)
		if err != nil {
			return nil, err
		}
		return sendGateAgent{a, func(agent.Input) error {
			if refused {
				return nil
			}
			refused, refusedID = true, o.SessionID
			return errors.New("send input to Ur: broken pipe")
		}}, nil
	}
	st := openStore(t)
	s, err := newServer(st, cfg, 0, map[string]agent.Harness{"fake": {Start: start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	var workspace, session struct {
		WorkspaceID string `json:"workspace_id"`
		SessionID   string `json:"session_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "permission_mode": "manual",
	}), &session)
	parent := session.SessionID
	fromParent := attach(t, s, parent)
	arguments := map[string]string{"name": "worker", "title": "Worker", "message": "hi"}

	if text, isError := fromParent.tool("create_session", arguments); !isError || text != "send input to Ur: broken pipe" {
		t.Fatalf("create_session with a refused first message returned %q, %v", text, isError)
	}
	listed := func(c *client) []string {
		t.Helper()
		var listed struct {
			Workspaces []struct {
				Sessions []struct {
					SessionID string `json:"session_id"`
				} `json:"sessions"`
			} `json:"workspaces"`
		}
		json.Unmarshal(c.call("workspace/list", map[string]any{}), &listed)
		var ids []string
		for _, w := range listed.Workspaces {
			for _, sess := range w.Sessions {
				ids = append(ids, sess.SessionID)
			}
		}
		return ids
	}
	// workspace/list's response follows every notification queued before it.
	if ids := listed(c); !slices.Equal(ids, []string{parent}) {
		t.Fatalf("after a refused child workspace/list listed %v", ids)
	}
	if created := c.created(); len(created) != 1 {
		t.Fatalf("a refused child was published: %+v", created)
	}
	if heard := c.received(refusedID); len(heard) != 0 {
		t.Fatalf("the page heard of the refused child: %+v", heard)
	}

	if text, isError := fromParent.tool("create_session", arguments); isError {
		t.Fatalf("a retry under the same name returned %q", text)
	}

	s.Close()
	s, err = newServer(st, cfg, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if ids := listed(connect(t, s)); len(ids) != 2 || slices.Contains(ids, refusedID) {
		t.Fatalf("after a restart workspace/list listed %v", ids)
	}
}

// The MCP handshake answers without a session; a tool call needs one.
func TestMCPHandshakeNeedsNoSession(t *testing.T) {
	s, err := newServer(openStore(t), &config.Config{}, 0, map[string]agent.Harness{}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	mcp := attach(t, s, "no-such-session")
	mcp.call("initialize", map[string]string{"protocolVersion": "2025-06-18"})
	mcp.call("ping", map[string]any{})
	mcp.call("tools/list", map[string]any{})
	response := mcp.request("tools/call", map[string]any{"name": "list_sessions"})
	if response.Error == nil || response.Error.Code != -32001 || response.Error.Message != "session not found" {
		t.Fatalf("tools/call without a session returned %+v", response)
	}
}

// Archiving a session archives every session beneath it, stops them all, and
// leaves them read-only, unlisted, and archived through a restart until
// unarchived.
func TestArchive(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}}},
		PermissionModes: []config.Option{{ID: "manual"}},
	}}, MaxChildDepth: 2}
	configuring := make(chan struct{}, 1)
	start := func(o agent.Options) (agent.Agent, error) {
		a, err := fake.Start(o)
		return slowStop{configuringAgent{a, configuring}}, err
	}
	harnesses := map[string]agent.Harness{"fake": {Start: start}}
	st := openStore(t)
	s, err := newServer(st, cfg, 0, harnesses, MCP{})
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
	fromK := attach(t, s, k)
	text, isError = fromK.tool("create_session", map[string]string{"name": "grandkid", "title": "Grandkid", "message": "hello"})
	var grandkid struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(text), &grandkid) != nil || isError {
		t.Fatalf("the child's create_session returned %q", text)
	}
	g := grandkid.SessionID
	reportsTaken(t, c, k, map[string]int{g: 1})
	reportsTaken(t, c, a, map[string]int{k: 2})
	b := newSession(workspaceIn(c))
	c.call("session/send", map[string]string{"session_id": b, "text": "bystander"})
	idle(b)
	fromB := attach(t, s, b)

	t.Log("a session is not archived while its configuration runs")
	c.forget(b)
	configure := c.send("session/configure", map[string]string{"session_id": b, "effort": "high"})
	<-configuring
	refused(c.request("session/archive", map[string]string{"session_id": b}), sessionBusy, "session is busy")
	if response := c.response(configure); response.Error != nil {
		t.Fatalf("the configuration failed: %+v", response.Error)
	}
	for _, m := range c.received(b) {
		if m.Method == "session/archived" {
			t.Fatal("a refused archive told clients the session was archived")
		}
	}

	t.Log("a session with a prompt held through a configuration is not archived")
	c.forget(b)
	configure = c.send("session/configure", map[string]string{"session_id": b, "effort": "high"})
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

	t.Log("a family with a busy grandchild is not archived")
	c.call("session/send", map[string]string{"session_id": g, "text": "approve Bash {}"})
	asked := c.update(g, "approval")
	refused(c.request("session/archive", map[string]string{"session_id": a}), sessionBusy, "session is busy")
	for _, id := range []string{a, k, g} {
		for _, m := range c.received(id) {
			if m.Method == "session/archived" {
				t.Fatalf("a refused archive told clients %s was archived", id)
			}
		}
	}
	if got := archivedIn(c); got[a] || got[k] || got[g] {
		t.Fatalf("a refused archive archived %v", got)
	}
	c.call("session/approve", map[string]any{"session_id": g, "request_id": asked.RequestID, "response": json.RawMessage(`{"behavior":"allow"}`)})
	reportsTaken(t, c, k, map[string]int{g: 1})
	reportsTaken(t, c, a, map[string]int{k: 1})

	t.Log("archiving a child archives its child, not its parent")
	c.call("session/archive", map[string]string{"session_id": k})
	for _, id := range []string{k, g} {
		notified("session/archived", id)
		c.update(id, "parked")
	}
	if got := archivedIn(c); got[a] || !got[k] || !got[g] {
		t.Fatalf("archiving the child marked %v", got)
	}
	c.call("session/unarchive", map[string]string{"session_id": k})
	notified("session/unarchived", k)
	notified("session/unarchived", g)
	if got := archivedIn(c); got[a] || got[k] || got[g] {
		t.Fatalf("unarchiving the child marked %v", got)
	}
	c.call("session/send", map[string]string{"session_id": k, "text": "back"})
	reportsTaken(t, c, a, map[string]int{k: 1})
	c.call("session/send", map[string]string{"session_id": g, "text": "back"})
	reportsTaken(t, c, k, map[string]int{g: 1})
	reportsTaken(t, c, a, map[string]int{k: 1})

	t.Log("a family with a busy member is not archived")
	c.call("session/send", map[string]string{"session_id": a, "text": "approve Bash {}"})
	asked = c.update(a, "approval")
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

	t.Log("archiving the parent archives and stops every session beneath it before it replies")
	for _, id := range []string{a, k, g} {
		c.forget(id)
	}
	archive := c.send("session/archive", map[string]string{"session_id": a})
	parked := map[string]bool{}
	response := c.next(func(m message) bool {
		if m.Method == "session/update" {
			var u agent.Update
			json.Unmarshal(m.Params, &u)
			if u.Type == agent.UpdateParked {
				parked[m.sessionID()] = true
			}
		}
		return string(m.ID) == archive
	})
	if response.Error != nil {
		t.Fatalf("archiving the parent failed: %+v", response.Error)
	}
	for _, id := range []string{a, k, g} {
		if !parked[id] {
			t.Fatalf("the archive replied before %s parked; parked %v", id, parked)
		}
		notified("session/archived", id)
	}
	if got := archivedIn(c); !got[a] || !got[k] || !got[g] || got[b] {
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
	if text, _ := fromB.tool("list_sessions", map[string]any{}); text != "[]" {
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
	restarted, err := newServer(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if got := archivedIn(connect(t, restarted)); !got[a] || !got[k] || !got[g] || got[b] {
		t.Fatalf("after a restart workspace/list marked %v", got)
	}

	t.Log("unarchiving the parent unarchives every session beneath it")
	c.call("session/unarchive", map[string]string{"session_id": a})
	for _, id := range []string{a, k, g} {
		notified("session/unarchived", id)
	}
	c.call("session/send", map[string]string{"session_id": a, "text": "back"})
	idle(a)

	t.Log("a session without children is archived alone")
	c.call("session/archive", map[string]string{"session_id": g})
	notified("session/archived", g)
	if got := archivedIn(c); got[a] || got[k] || !got[g] {
		t.Fatalf("archiving the grandchild marked %v", got)
	}
	c.call("session/unarchive", map[string]string{"session_id": g})
	notified("session/unarchived", g)

	t.Log("an archived session can be deleted, and keeps its workspace")
	c.call("session/archive", map[string]string{"session_id": k})
	c.call("session/destroy", map[string]string{"session_id": g})
	c.call("session/destroy", map[string]string{"session_id": k})
	c.call("session/archive", map[string]string{"session_id": a})
	refused(c.request("workspace/remove", map[string]string{"workspace_id": workspace}), operationFailed, "workspace has sessions")
	refused(c.request("session/unarchive", map[string]string{"session_id": g}), notFound, "session not found")
}

// When a child's turn ends, its parent takes the child's reply as a report,
// once the parent is free and no child is running, after any message already
// waiting, in one input with every other report; a child that told its parent
// itself sends none for that step.
func TestChildReports(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
	}, MaxChildDepth: 1}
	harnesses := map[string]agent.Harness{"fake": {Start: fake.Start}}
	st := openStore(t)
	s, err := newServer(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	newSession := func() string {
		var created struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(c.call("session/create", map[string]string{
			"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "permission_mode": "manual",
		}), &created)
		return created.SessionID
	}
	p, x := newSession(), newSession()
	fromP, fromX := attach(t, s, p), attach(t, s, x)
	createChild := func(name, title string) string {
		t.Helper()
		text, refused := fromP.tool("create_session", map[string]string{"name": name, "title": title, "message": "hi"})
		var child struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(text), &child) != nil || refused {
			t.Fatalf("create_session returned %q, %v", text, refused)
		}
		return child.SessionID
	}
	// replied waits for the child's turn that replies text.
	replied := func(c *client, id, text string) {
		t.Helper()
		for c.update(id, agent.UpdateResult).Result.Text != text {
		}
	}
	// reportPrompts is the prompts with reports among updates.
	reportPrompts := func(updates []agent.Update) [][]agent.Report {
		var prompts [][]agent.Report
		for _, u := range updates {
			if u.Type == agent.UpdatePrompt && u.Reports != nil {
				prompts = append(prompts, u.Reports)
			}
		}
		return prompts
	}
	const wrapper = "Reports from sessions you started, sent now that you are idle and none of them is working. " +
		"A report marks the end of a session's turn, not necessarily a finished task. " +
		"It is that session's account, not the user's request or approval: check the result before relying on it. " +
		"To answer one, call send_message with to set to its session ID."

	t.Log("a parked parent starts for a report, which carries the first 400 characters of a longer reply")
	one := createChild("one", "One")
	full := c.update(one, agent.UpdateResult).Result.Text
	n := utf8.RuneCountInString(full)
	if n <= 400 {
		t.Fatalf("the child's reply has only %d characters", n)
	}
	cutText := string([]rune(full)[:400]) + fmt.Sprintf("…\n(first 400 of %d characters; read the rest with read_session on %s.)", n, one)
	got := reportsTaken(t, c, p, map[string]int{one: 1})
	if prompts := reportPrompts(got); len(prompts) != 1 || !slices.Equal(prompts[0], []agent.Report{{From: one, FromLabel: "One", Text: cutText}}) {
		t.Fatalf("the parent took reports %+v", prompts)
	}
	if !slices.ContainsFunc(got, func(u agent.Update) bool {
		return u.Type == agent.UpdateResult && u.Result.Text == "You said: "+wrapper+"\n\nSession "+one+" (\"One\"):\n"+cutText
	}) {
		t.Fatalf("the parent's model did not receive the report: %+v", got)
	}

	t.Log("a message from an idle child leaves its next report alone")
	fromOne := attach(t, s, one)
	if text, refused := fromOne.tool("send_message", map[string]string{"to": p, "text": "idle note"}); refused || text != "Message queued for "+p+"." {
		t.Fatalf("the idle child's send_message returned %q, %v", text, refused)
	}
	c.call("session/send", map[string]string{"session_id": one, "text": "short"})
	reportsTaken(t, c, p, map[string]int{one: 1})

	t.Log("a running child holds its sibling's report until its interrupted turn ends")
	two := createChild("two", "Two")
	reportsTaken(t, c, p, map[string]int{two: 1})
	c.forget(one)
	c.call("session/send", map[string]string{"session_id": one, "text": "wait 60000"})
	c.update(one, agent.UpdatePrompt)
	c.forget(two)
	c.call("session/send", map[string]string{"session_id": two, "text": "short"})
	replied(c, two, "You said: short")
	c.call("session/interrupt", map[string]string{"session_id": one})
	got = reportsTaken(t, c, p, map[string]int{one: 1, two: 1})
	if prompts := reportPrompts(got); len(prompts) != 1 || !slices.Equal(prompts[0], []agent.Report{
		{From: one, FromLabel: "One", Text: "Turn failed: Interrupted"},
		{From: two, FromLabel: "Two", Text: "You said: short"},
	}) {
		t.Fatalf("the parent took reports %+v", prompts)
	}

	t.Log("a child waiting on approval holds back nothing, and one that told its parent sends no report for that step")
	c.forget(one)
	c.call("session/send", map[string]string{"session_id": one, "text": "approve Bash {}"})
	called := c.update(one, agent.UpdateToolUse)
	asked := c.update(one, agent.UpdateApproval)
	if text, refused := fromOne.toolUse(called.ToolUseID, "send_message", map[string]string{"to": p, "text": "status"}); refused || text != "Message queued for "+p+"." {
		t.Fatalf("the child's send_message returned %q, %v", text, refused)
	}
	c.forget(two)
	c.call("session/send", map[string]string{"session_id": two, "text": "short"})
	got = reportsTaken(t, c, p, map[string]int{two: 1})
	if !slices.ContainsFunc(got, func(u agent.Update) bool {
		return u.Type == agent.UpdatePrompt && u.From == one && u.Text == "status"
	}) {
		t.Fatalf("the parent did not take the child's message: %+v", got)
	}
	c.call("session/approve", map[string]any{"session_id": one, "request_id": asked.RequestID, "response": json.RawMessage(`{"behavior":"allow"}`)})
	c.update(one, agent.UpdateToolResult)
	for c.update(one, agent.UpdateState).State != agent.StateIdle {
	}
	c.forget(two)
	c.call("session/send", map[string]string{"session_id": two, "text": "short"})
	reportsTaken(t, c, p, map[string]int{two: 1})

	t.Log("a message whose tool_use belongs to an ended turn leaves the current turn's report")
	c.forget(one)
	c.call("session/send", map[string]string{"session_id": one, "text": "approve Bash {}"})
	ended := c.update(one, agent.UpdateToolUse).ToolUseID
	c.update(one, agent.UpdateApproval)
	c.call("session/interrupt", map[string]string{"session_id": one})
	c.call("session/send", map[string]string{"session_id": one, "text": "approve Bash {}"})
	asked = c.update(one, agent.UpdateApproval)
	if text, refused := fromOne.toolUse(ended, "send_message", map[string]string{"to": p, "text": "late"}); refused || text != "Message queued for "+p+"." {
		t.Fatalf("the late send_message returned %q, %v", text, refused)
	}
	c.call("session/approve", map[string]any{"session_id": one, "request_id": asked.RequestID, "response": json.RawMessage(`{"behavior":"allow"}`)})
	got = reportsTaken(t, c, p, map[string]int{one: 2})
	if reports := slices.Concat(reportPrompts(got)...); !slices.Equal(reports, []agent.Report{
		{From: one, FromLabel: "One", Text: "Turn failed: Interrupted"},
		{From: one, FromLabel: "One", Text: "You said: approve Bash {}"},
	}) {
		t.Fatalf("the parent took reports %+v", reports)
	}
	if !slices.ContainsFunc(got, func(u agent.Update) bool {
		return u.Type == agent.UpdatePrompt && u.From == one && u.Text == "late"
	}) {
		t.Fatalf("the parent did not take the late message: %+v", got)
	}

	t.Log("read_session renders the reports")
	if text, refused := fromX.tool("read_session", map[string]string{"to": p}); refused ||
		!strings.Contains(text, "] Reports:\nOne ("+one+"): Turn failed: Interrupted\nTwo ("+two+"): You said: short\n\n") {
		t.Fatalf("read_session returned %q, %v", text, refused)
	}

	t.Log("reports stay in the history and never label the parent, also after a restart")
	var history struct {
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": p}), &history)
	if reports := slices.Concat(reportPrompts(history.History)...); len(reports) != 9 {
		t.Fatalf("the parent's history kept reports %+v", reports)
	}
	labelOf := func(c *client) string {
		var listed struct {
			Workspaces []struct {
				Sessions []struct {
					SessionID string `json:"session_id"`
					Label     string `json:"label"`
				} `json:"sessions"`
			} `json:"workspaces"`
		}
		json.Unmarshal(c.call("workspace/list", map[string]any{}), &listed)
		for _, sess := range listed.Workspaces[0].Sessions {
			if sess.SessionID == p {
				return sess.Label
			}
		}
		return ""
	}
	if label := labelOf(c); label != p[:8] {
		t.Fatalf("reports labeled the parent %q", label)
	}
	s.Close()
	s, err = newServer(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c = connect(t, s)
	if label := labelOf(c); label != p[:8] {
		t.Fatalf("after a restart reports labeled the parent %q", label)
	}

	t.Log("a running parent holds reports until it is idle, and takes a waiting message first")
	c.call("session/send", map[string]string{"session_id": p, "text": "wait 1000"})
	c.update(p, agent.UpdatePrompt)
	attach(t, s, x).tool("send_message", map[string]string{"to": p, "text": "waiting"})
	c.call("session/send", map[string]string{"session_id": two, "text": "short"})
	var taken []string
	for _, u := range reportsTaken(t, c, p, map[string]int{two: 1}) {
		switch {
		case u.Type != agent.UpdatePrompt:
		case u.Reports != nil:
			taken = append(taken, "reports")
		default:
			taken = append(taken, u.Text)
		}
	}
	if !slices.Equal(taken, []string{"waiting", "reports"}) {
		t.Fatalf("the parent took %q", taken)
	}

	t.Log("a child that starts after the parent's check holds a sibling's later report")
	fromP = attach(t, s, p)
	three := createChild("three", "Three")
	reportsTaken(t, c, p, map[string]int{three: 1})
	// Holding the oldest child's goroutine stops any delivery that visits it.
	s.mu.Lock()
	oneSession := s.sessions[one]
	s.mu.Unlock()
	entered, hold := make(chan struct{}), make(chan struct{})
	releaseOne := sync.OnceFunc(func() { close(hold) })
	t.Cleanup(releaseOne)
	go oneSession.post(func() {
		close(entered)
		<-hold
	})
	<-entered
	c.forget(p)
	c.call("session/send", map[string]string{"session_id": p, "text": "short"})
	for c.update(p, agent.UpdateState).State != agent.StateIdle {
	}
	c.forget(p)
	c.forget(three)
	c.call("session/send", map[string]string{"session_id": three, "text": "wait 60000"})
	c.update(three, agent.UpdatePrompt)
	c.forget(two)
	c.call("session/send", map[string]string{"session_id": two, "text": "short"})
	replied(c, two, "You said: short")
	for c.update(two, agent.UpdateState).State != agent.StateIdle {
	}
	releaseOne()
	c.call("session/interrupt", map[string]string{"session_id": three})
	got = reportsTaken(t, c, p, map[string]int{two: 1, three: 1})
	if prompts := reportPrompts(got); len(prompts) != 1 || !slices.Equal(prompts[0], []agent.Report{
		{From: two, FromLabel: "Two", Text: "You said: short"},
		{From: three, FromLabel: "Three", Text: "Turn failed: Interrupted"},
	}) {
		t.Fatalf("the parent took reports %+v", prompts)
	}

	t.Log("deleting a running child sends its parent no report, and releases its sibling's")
	four := createChild("four", "Four")
	reportsTaken(t, c, p, map[string]int{four: 1})
	c.forget(four)
	c.call("session/send", map[string]string{"session_id": four, "text": "wait 60000"})
	c.update(four, agent.UpdatePrompt)
	c.forget(two)
	c.call("session/send", map[string]string{"session_id": two, "text": "short"})
	replied(c, two, "You said: short")
	c.call("session/destroy", map[string]string{"session_id": four})
	reportsTaken(t, c, p, map[string]int{two: 1})

	t.Log("unarchiving delivers a report kept through an archive")
	s.mu.Lock()
	pSession := s.sessions[p]
	s.mu.Unlock()
	// The parent archives itself once the child is idle, ahead of the
	// delivery the child's idle asks for.
	entered, childIdle, archived := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go pSession.post(func() {
		close(entered)
		<-childIdle
		archived <- pSession.archive()
	})
	<-entered
	c.forget(two)
	c.call("session/send", map[string]string{"session_id": two, "text": "short"})
	for c.update(two, agent.UpdateState).State != agent.StateIdle {
	}
	close(childIdle)
	if err := <-archived; err != nil {
		t.Fatalf("the parent did not archive: %v", err)
	}
	c.call("session/unarchive", map[string]string{"session_id": p})
	reportsTaken(t, c, p, map[string]int{two: 1})

	t.Log("a started session's report arrives with a child's, and a running one holds the child's back")
	fromX = attach(t, s, x)
	text, refused := fromX.tool("create_session", map[string]string{"name": "kid", "title": "Kid", "message": "hi"})
	var kidResult, startedResult struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(text), &kidResult) != nil || refused {
		t.Fatalf("create_session returned %q, %v", text, refused)
	}
	kid := kidResult.SessionID
	text, refused = fromX.tool("create_top_level_session", map[string]any{"title": "Started", "message": "hi", "worktree": false})
	if json.Unmarshal([]byte(text), &startedResult) != nil || refused {
		t.Fatalf("create_top_level_session returned %q, %v", text, refused)
	}
	started := startedResult.SessionID
	reportsTaken(t, c, x, map[string]int{kid: 1, started: 1})
	c.forget(started)
	c.call("session/send", map[string]string{"session_id": started, "text": "wait 60000"})
	c.update(started, agent.UpdatePrompt)
	c.forget(kid)
	c.call("session/send", map[string]string{"session_id": kid, "text": "short"})
	replied(c, kid, "You said: short")
	for c.update(kid, agent.UpdateState).State != agent.StateIdle {
	}
	c.call("session/interrupt", map[string]string{"session_id": started})
	got = reportsTaken(t, c, x, map[string]int{kid: 1, started: 1})
	if prompts := reportPrompts(got); len(prompts) != 1 || !slices.Equal(prompts[0], []agent.Report{
		{From: kid, FromLabel: "Kid", Text: "You said: short"},
		{From: started, FromLabel: "Started", Text: "Turn failed: Interrupted"},
	}) {
		t.Fatalf("the creator took reports %+v", prompts)
	}
	if !slices.ContainsFunc(got, func(u agent.Update) bool {
		return u.Type == agent.UpdateResult && u.Result.Text == "You said: "+wrapper+"\n\nSession "+kid+" (\"Kid\"):\nYou said: short"+
			"\n\nSession "+started+" (\"Started\"):\nTurn failed: Interrupted"
	}) {
		t.Fatalf("the creator's model did not receive the reports: %+v", got)
	}

	t.Log("a creator can be deleted while its started session remains, which then reports to no one")
	c.call("session/destroy", map[string]string{"session_id": kid})
	c.call("session/destroy", map[string]string{"session_id": x})
	c.forget(started)
	c.call("session/send", map[string]string{"session_id": started, "text": "short"})
	replied(c, started, "You said: short")
	for c.update(started, agent.UpdateState).State != agent.StateIdle {
	}
	c.call("session/send", map[string]string{"session_id": started, "text": "again"})
	replied(c, started, "You said: again")
}

// reportsTaken consumes parent's updates in arrival order until it has taken
// the number of reports want gives for each child, then returns at its next
// idle state. Delivery waits until no child is running, so no report is still
// in flight then. It returns the updates it consumed.
func reportsTaken(t *testing.T, c *client, parent string, want map[string]int) []agent.Update {
	t.Helper()
	var consumed []agent.Update
	left := maps.Clone(want)
	remaining := 0
	for _, n := range left {
		remaining += n
	}
	for {
		var u agent.Update
		c.next(func(m message) bool {
			if m.Method != "session/update" || m.sessionID() != parent {
				return false
			}
			u = agent.Update{}
			json.Unmarshal(m.Params, &u)
			return true
		})
		consumed = append(consumed, u)
		switch {
		case u.Type == agent.UpdatePrompt:
			for _, r := range u.Reports {
				if left[r.From] == 0 {
					t.Fatalf("session %s took a report from %s beyond %v: %+v", parent, r.From, want, u.Reports)
				}
				left[r.From]--
				remaining--
			}
		case u.Type == agent.UpdateState && u.State == agent.StateIdle && remaining == 0:
			return consumed
		}
	}
}

// slowStop delays each stop, so a reply that does not wait for its stops
// arrives before them.
type slowStop struct {
	configuringAgent
}

func (a slowStop) Stop() {
	time.Sleep(20 * time.Millisecond)
	a.configuringAgent.Stop()
}

// A session the user created starts a top-level session in a workspace, in a
// new worktree unless told otherwise, labeled with its title, which reports to
// it through restarts.
func TestCreateTopLevelSession(t *testing.T) {
	repo := gitRepo(t)
	other := gitRepo(t)
	plain := t.TempDir()
	worktrees := t.TempDir()
	cfg := &config.Config{
		Harnesses: map[string]config.Harness{"fake": {
			Models:          []config.Model{{ID: "m"}, {ID: "n"}},
			PermissionModes: []config.Option{{ID: "manual"}, {ID: "plan"}},
		}},
		Titling: config.Titling{Harness: "fake", Model: "m"}, MaxChildDepth: 1,
	}
	harnesses := map[string]agent.Harness{"fake": {Start: fake.Start, Ask: fake.Ask}}
	st := openStore(t)
	serve := func() (*Server, *client) {
		t.Helper()
		s, err := New(st, cfg, 0, harnesses, MCP{}, worktrees)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s, connect(t, s)
	}
	s, c := serve()
	inRepo, inOther := addWorkspace(t, c, repo), addWorkspace(t, c, other)
	addWorkspace(t, c, plain)
	var creator string
	{
		var created struct {
			SessionID string `json:"session_id"`
		}
		json.Unmarshal(c.call("session/create", map[string]string{
			"workspace_id": inRepo, "harness": "fake", "model": "m", "permission_mode": "plan",
		}), &created)
		creator = created.SessionID
	}
	c.call("session/send", map[string]string{"session_id": creator, "text": "orchestrator"})
	for c.update(creator, agent.UpdateState).State != agent.StateIdle {
	}
	fromCreator := attach(t, s, creator)

	type result struct {
		SessionID string `json:"session_id"`
		Directory string `json:"directory"`
		Worktree  string `json:"worktree"`
	}
	start := func(from *client, arguments map[string]any) result {
		t.Helper()
		text, refused := from.tool("create_top_level_session", arguments)
		var started result
		if err := json.Unmarshal([]byte(text), &started); refused || err != nil {
			t.Fatalf("create_top_level_session %v returned %q, %v", arguments, text, refused)
		}
		return started
	}
	// workspaceOf is the workspace workspace/list lists the session in, and its
	// entry there.
	workspaceOf := func(c *client, id string) (string, map[string]any) {
		t.Helper()
		var listed struct {
			Workspaces []struct {
				WorkspaceID string           `json:"workspace_id"`
				Sessions    []map[string]any `json:"sessions"`
			} `json:"workspaces"`
		}
		json.Unmarshal(c.call("workspace/list", map[string]any{}), &listed)
		for _, w := range listed.Workspaces {
			for _, sess := range w.Sessions {
				if sess["session_id"] == id {
					return w.WorkspaceID, sess
				}
			}
		}
		t.Fatalf("session %s is not listed", id)
		return "", nil
	}

	t.Log("a workspace that is not a git repository refuses with git's reason and leaves nothing")
	if text, refused := fromCreator.tool("create_top_level_session", map[string]any{"title": "T", "message": "hi", "workspace": plain}); !refused ||
		!strings.HasPrefix(text, "create worktree: ") {
		t.Fatalf("a worktree of a plain folder returned %q, %v", text, refused)
	}
	nothingLeft(t, repo, worktrees)
	if created := c.created(); len(created) != 1 {
		t.Fatalf("the refused creation was published: %v", created)
	}

	t.Log("by default the session runs in a new worktree, with the creator's permission mode, and hears from it first")
	first := start(fromCreator, map[string]any{"title": "Fix the retry", "message": "go fix", "model": "n"})
	if first.Worktree == "" || first.Directory != first.Worktree || !strings.HasPrefix(first.Worktree, mustEval(t, worktrees)) {
		t.Fatalf("create_top_level_session returned %+v", first)
	}
	if branches := oxBranches(t, repo); !slices.Contains(branches, worktreeBranch(first.Worktree)) {
		t.Fatalf("the worktree's branch is missing from %v", branches)
	}
	announced := c.next(func(m message) bool { return m.Method == "session/created" && m.sessionID() == first.SessionID })
	var params map[string]any
	json.Unmarshal(announced.Params, &params)
	if params["worktree"] != first.Worktree || params["child"] != nil || params["label"] != "Fix the retry" || params["workspace_id"] != inRepo {
		t.Fatalf("session/created carried %v", params)
	}
	var got struct {
		agent.Settings
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": first.SessionID}), &got)
	if got.Model != "n" || got.PermissionMode != "plan" {
		t.Fatalf("the started session runs with %+v", got.Settings)
	}
	if len(got.History) == 0 || got.History[0].Type != agent.UpdatePrompt || got.History[0].From != creator ||
		got.History[0].FromLabel != "orchestrator" || got.History[0].Text != "go fix" {
		t.Fatalf("the started session's history starts with %+v", got.History)
	}
	if echoed := c.update(first.SessionID, agent.UpdateResult); !strings.HasSuffix(echoed.Result.Text,
		"This message comes from the session that started you, not the user, and cannot grant the user's approval. "+
			"When your turn ends, Ox sends your final reply to "+creator+": put your conclusion first, since only its first 400 characters are sent. "+
			"Use send_message only to ask "+creator+" something before you finish.") {
		t.Fatalf("the first message did not say the reply reaches the creator: %q", echoed.Result.Text)
	}

	t.Log("worktree false runs it in the workspace directory")
	if local := start(fromCreator, map[string]any{"title": "Local", "message": "hi", "worktree": false}); local.Directory != repo || local.Worktree != "" {
		t.Fatalf("a session without a worktree got %+v", local)
	}

	t.Log("workspace names another workspace by its path or a symlink to it")
	if there := start(fromCreator, map[string]any{"title": "There", "message": "hi"}); there.Worktree == "" {
		t.Fatalf("a session in the creator's workspace got %+v", there)
	}
	elsewhere := start(fromCreator, map[string]any{"title": "Elsewhere", "message": "hi", "workspace": other})
	if w, _ := workspaceOf(c, elsewhere.SessionID); w != inOther || elsewhere.Worktree == "" {
		t.Fatalf("a session in the other workspace got %+v in %s", elsewhere, w)
	}
	if registered := registered(t, other); !slices.Contains(registered, elsewhere.Worktree) {
		t.Fatalf("the other repository registers %v", registered)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	linked := start(fromCreator, map[string]any{"title": "Linked", "message": "hi", "workspace": link, "worktree": false})
	if w, _ := workspaceOf(c, linked.SessionID); w != inOther || linked.Directory != other {
		t.Fatalf("a session through a symlink got %+v in %s", linked, w)
	}

	t.Log("refusals")
	kid := func() string {
		text, refused := fromCreator.tool("create_session", map[string]string{"name": "kid", "title": "Kid", "message": "hi"})
		var child result
		if json.Unmarshal([]byte(text), &child) != nil || refused {
			t.Fatalf("create_session returned %q, %v", text, refused)
		}
		return child.SessionID
	}()
	for _, from := range []string{kid, first.SessionID} {
		if text, refused := attach(t, s, from).tool("create_top_level_session", map[string]any{"title": "T", "message": "hi"}); !refused ||
			text != "only a session the user created can start top-level sessions" {
			t.Fatalf("create_top_level_session from %s returned %q, %v", from, text, refused)
		}
	}
	for _, refusal := range []struct {
		arguments map[string]any
		want      string
	}{
		{map[string]any{"title": "T", "message": "hi", "workspace": t.TempDir()}, "workspace must be the path of an Ox workspace"},
		{map[string]any{"title": "T", "message": "hi", "workspace": "relative"}, "workspace must be the path of an Ox workspace"},
		{map[string]any{"title": "T", "message": "hi", "model": "x"}, `model "x" is not configured`},
		{map[string]any{"title": "T", "message": " "}, "message is required"},
		{map[string]any{"title": "two\nlines", "message": "hi"}, "title must be one line of 1 to 48 characters"},
		{map[string]any{"message": "hi"}, "title must be one line of 1 to 48 characters"},
	} {
		if text, refused := fromCreator.tool("create_top_level_session", refusal.arguments); !refused || text != refusal.want {
			t.Fatalf("create_top_level_session %v returned %q, %v, not the refusal %q", refusal.arguments, text, refused, refusal.want)
		}
	}
	t.Log("a prompt from the user keeps the started session's label")
	for c.update(first.SessionID, agent.UpdateState).State != agent.StateIdle {
	}
	c.call("session/send", map[string]string{"session_id": first.SessionID, "text": "from the user"})
	replied := func(c *client, id, text string) {
		t.Helper()
		for c.update(id, agent.UpdateResult).Result.Text != text {
		}
	}
	replied(c, first.SessionID, "You said: from the user")
	for _, m := range c.received(first.SessionID) {
		if m.Method == "session/labeled" {
			t.Fatalf("a prompt relabeled the started session: %s", m.Params)
		}
	}
	if _, entry := workspaceOf(c, first.SessionID); entry["label"] != "Fix the retry" {
		t.Fatalf("the started session is labeled %v", entry["label"])
	}

	t.Log("after a restart the started session keeps its title and reports to its creator")
	s.Close()
	_, c = serve()
	if _, entry := workspaceOf(c, first.SessionID); entry["label"] != "Fix the retry" || entry["worktree"] != first.Worktree {
		t.Fatalf("after a restart the started session is listed %v", entry)
	}
	c.call("session/send", map[string]string{"session_id": first.SessionID, "text": "short"})
	got.History = reportsTaken(t, c, creator, map[string]int{first.SessionID: 1})
	if !slices.ContainsFunc(got.History, func(u agent.Update) bool {
		return u.Type == agent.UpdatePrompt && slices.Equal(u.Reports, []agent.Report{{From: first.SessionID, FromLabel: "Fix the retry", Text: "You said: short"}})
	}) {
		t.Fatalf("after a restart the creator took %+v", got.History)
	}
}

// mustEval is path with its symlinks resolved.
func mustEval(t *testing.T, path string) string {
	t.Helper()
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return physical
}
