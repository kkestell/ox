package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
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
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ox.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func answer(s *Server, line string) *jsonrpc.Response {
	request, invalid := jsonrpc.Parse([]byte(line))
	if invalid != nil {
		return invalid
	}
	responses := make(chan *jsonrpc.Response, 1)
	s.handle(func(r *jsonrpc.Response) { responses <- r }, request)
	return <-responses
}

// A line that is not a valid request is answered with the error jsonrpc.Parse
// returns, on a plain connection and on a forwarder's, and a request without an
// ID is dropped without ending the connection.
func TestInvalidLines(t *testing.T) {
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{ID: "session-one", Harness: "fake", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	for name, c := range map[string]*client{"plain": connect(t, s), "mcp": attach(t, s, "session-one")} {
		for line, code := range map[string]int{
			"{not json":                                jsonrpc.ParseError,
			`{"jsonrpc":"2.0","method":7}`:             jsonrpc.InvalidRequest,
			`{"jsonrpc":"1.0","id":1,"method":"ping"}`: jsonrpc.InvalidRequest,
		} {
			if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
				t.Fatal(err)
			}
			response := c.next(func(m message) bool { return m.Error != nil && m.Error.Code == code })
			if string(response.ID) != "null" {
				t.Errorf("%s: %s was answered with ID %s", name, line, response.ID)
			}
		}
		unadded := t.TempDir()
		line := `{"jsonrpc":"2.0","method":"ping"}`
		method := "ping"
		if name == "plain" {
			line = `{"jsonrpc":"2.0","method":"workspace/create","params":{"path":"` + unadded + `"}}`
			method = "workspace/list"
		}
		if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
		response := c.request(method, map[string]string{})
		if response.Error != nil {
			t.Errorf("%s: %s after a request without an ID: %+v", name, method, response.Error)
		}
		if name == "plain" {
			var list struct {
				Workspaces []struct {
					Path string `json:"path"`
				} `json:"workspaces"`
			}
			if err := json.Unmarshal(response.Result, &list); err != nil {
				t.Fatal(err)
			}
			for _, w := range list.Workspaces {
				if w.Path == unadded {
					t.Errorf("a request without an ID created a workspace: %s", response.Result)
				}
			}
		}
		// Any reply to the request without an ID would be handled by now.
		time.Sleep(100 * time.Millisecond)
		c.received("")
		if len(c.backlog) != 0 {
			t.Errorf("%s: a request without an ID was answered: %+v", name, c.backlog)
		}
	}
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
	for _, sessionID := range []string{strings.Repeat("g", 32), strings.Repeat("a", 30)} {
		params := `{"session_id":"` + sessionID + `","workspace_id":"missing","harness":"h","model":"m","effort":"low","permission_mode":"plan"}`
		if response := answer(s, request("session/create", params)); response.Error == nil || response.Error.Message != "session_id must be 32 hexadecimal digits" {
			t.Errorf("malformed session_id %s: %+v", sessionID, response.Error)
		}
	}
	if response := answer(s, request("session/configure", `{"session_id":"missing","effort":""}`)); response.Error == nil || response.Error.Code != -32001 {
		t.Fatalf("configuration of one setting did not reach session lookup: %+v", response.Error)
	}
}

func TestClaudeRefusesSettingCommandsBeforeStarting(t *testing.T) {
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{ID: "s", Harness: "claude", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	starts := make(chan struct{}, 1)
	h := claude.Harness
	h.Start = func(o agent.Options) (agent.Agent, error) {
		starts <- struct{}{}
		return fake.Start(o)
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"claude": h}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	send := func(text string) message {
		return c.request("session/send", map[string]string{"session_id": "s", "text": text})
	}
	for _, text := range []string{"/model", "/model sonnet", "  \t/effort\nhigh"} {
		if r := send(text); r.Error == nil || r.Error.Code != jsonrpc.InvalidParams || r.Error.Message != "change the model and effort in the session's settings" {
			t.Fatalf("%q returned %+v", text, r.Error)
		}
	}
	select {
	case <-starts:
		t.Fatal("a prohibited command started a parked session")
	default:
	}
	for _, text := range []string{"/clear", "/compact", "/effortless", "say /model sonnet"} {
		if r := send(text); r.Error != nil {
			t.Fatalf("%q was refused: %+v", text, r.Error)
		}
		for c.update("s", "state").State != "idle" {
		}
	}
	if r := send("/effort high"); r.Error == nil || r.Error.Code != jsonrpc.InvalidParams {
		t.Fatalf("a running session accepted a prohibited command: %+v", r.Error)
	}
}

func TestSendPromptSizeCountsDecodedBytes(t *testing.T) {
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{ID: "s", Harness: "fake", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	send := func(text string) *jsonrpc.Response {
		p, err := json.Marshal(map[string]string{"session_id": "s", "text": text})
		if err != nil {
			t.Fatal(err)
		}
		return answer(s, `{"jsonrpc":"2.0","id":1,"method":"session/send","params":`+string(p)+`}`)
	}
	text := strings.Repeat("é<", (1<<20)/3) + "<"
	if got := send(text + "x"); got.Error == nil || got.Error.Code != jsonrpc.InvalidParams {
		t.Fatalf("a prompt over 1 MiB was not refused: %+v", got.Error)
	}
	if got := send(text); got.Error != nil {
		t.Fatalf("a 1 MiB prompt was refused: %+v", got.Error)
	}
}

// pngBase64 is n bytes of data with PNG's signature, which oxd takes as a
// PNG, as base64.
func pngBase64(n int) string {
	data := make([]byte, n)
	copy(data, "\x89PNG\r\n\x1a\n")
	return base64.StdEncoding.EncodeToString(data)
}

// A prompt's images are stored with the session, named by their IDs in its
// updates and history, and served by session/image until the prompt drops.
func TestPromptImages(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}},
	}}}
	s, err := New(openStore(t), cfg, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
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
	image := func(imageID string) message {
		return c.request("session/image", map[string]string{"session_id": id, "image_id": imageID})
	}
	png := pngBase64(16)

	t.Log("a prompt with text and an image")
	c.call("session/send", map[string]any{"session_id": id, "text": "look", "images": []string{png}})
	queued := c.update(id, "prompt_queued")
	prompt := c.update(id, "prompt")
	if len(queued.Images) != 1 || !slices.Equal(prompt.Images, queued.Images) {
		t.Fatalf("prompt_queued carried %q and prompt %q, not one image ID", queued.Images, prompt.Images)
	}
	imageID := prompt.Images[0]
	var served struct {
		MimeType string `json:"mime_type"`
		Data     string `json:"data"`
	}
	json.Unmarshal(c.call("session/image", map[string]string{"session_id": id, "image_id": imageID}), &served)
	if served.MimeType != "image/png" || served.Data != png {
		t.Fatalf("session/image returned %s with other data", served.MimeType)
	}
	if result := c.update(id, "result"); !strings.HasSuffix(result.Result.Text, "[images: 1]") {
		t.Fatalf("the harness's reply %q reports no image", result.Result.Text)
	}
	var got struct {
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &got)
	if i := slices.IndexFunc(got.History, func(u agent.Update) bool { return u.Type == "prompt" }); i < 0 ||
		!slices.Equal(got.History[i].Images, []string{imageID}) {
		t.Fatalf("session/get's history does not name the image: %+v", got.History)
	}
	for c.update(id, "state").State != "idle" {
	}

	t.Log("a prompt with blank text and an image")
	c.call("session/send", map[string]any{"session_id": id, "text": " ", "images": []string{png}})
	if prompt := c.update(id, "prompt"); len(prompt.Images) != 1 {
		t.Fatalf("an image-only prompt carried %q", prompt.Images)
	}
	for c.update(id, "state").State != "idle" {
	}
	c.forget(id)

	t.Log("bad requests")
	for _, bad := range []struct {
		name   string
		params map[string]any
	}{
		{"blank text and no images", map[string]any{"session_id": id, "text": " "}},
		{"base64 that does not decode", map[string]any{"session_id": id, "text": "x", "images": []string{"not base64!"}}},
		{"text sent as an image", map[string]any{"session_id": id, "text": "x", "images": []string{base64.StdEncoding.EncodeToString([]byte("plain text"))}}},
		{"images over 10 MiB", map[string]any{"session_id": id, "text": "x", "images": []string{pngBase64(5 << 20), pngBase64(5<<20 + 1)}}},
	} {
		if response := c.request("session/send", bad.params); response.Error == nil || response.Error.Code != jsonrpc.InvalidParams {
			t.Errorf("%s: %+v", bad.name, response.Error)
		}
	}
	if received := c.received(id); len(received) != 0 {
		t.Fatalf("a refused prompt started something: %+v", received)
	}

	t.Log("a queued image prompt dropped by an interrupt")
	c.call("session/send", map[string]string{"session_id": id, "text": `approve Bash {}`})
	c.update(id, "approval")
	var sent struct {
		MessageID string `json:"message_id"`
	}
	json.Unmarshal(c.call("session/send", map[string]any{"session_id": id, "text": "later", "images": []string{png}}), &sent)
	var dropped agent.Update
	for dropped.MessageID != sent.MessageID {
		dropped = c.update(id, "prompt_queued")
	}
	c.call("session/interrupt", map[string]string{"session_id": id})
	for c.update(id, "prompt_cancelled").MessageID != sent.MessageID {
	}
	if response := image(dropped.Images[0]); response.Error == nil || response.Error.Code != notFound {
		t.Fatalf("a dropped prompt's image answered %+v", response.Error)
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
	c.call("session/configure", map[string]string{"session_id": id, "permission_mode": "plan"})
	params["permission_mode"] = "obsolete"
	check(true)
	var settings agent.Settings
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &settings)
	if settings.PermissionMode != "plan" {
		t.Fatalf("a retried creation changed existing settings: %+v", settings)
	}
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
	s, err := New(st, cfg, 300*time.Millisecond, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
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

	// A background task that outlasts the park period keeps the session
	// from parking until it ends.
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "effort": "low", "permission_mode": "plan",
	}), &created)
	worker := created.SessionID
	c.call("session/send", map[string]string{"session_id": worker, "text": "background 800"})
	var states []string
	for len(states) == 0 || states[len(states)-1] != "idle" {
		var u agent.Update
		c.next(func(m message) bool {
			if m.Method != "session/update" || m.sessionID() != worker {
				return false
			}
			u = agent.Update{}
			json.Unmarshal(m.Params, &u)
			return u.Type == "state" || u.Type == "parked"
		})
		if u.Type == "parked" {
			t.Fatalf("a session parked with a background task running, after %v", states)
		}
		states = append(states, u.State)
	}
	if !slices.Equal(states, []string{"running", "background", "idle"}) {
		t.Fatalf("a background task published %v", states)
	}

	// A prompt queued during a turn runs its special commands when taken.
	c.call("session/send", map[string]string{"session_id": worker, "text": "one two three four five six"})
	c.call("session/send", map[string]string{"session_id": worker, "text": `approve Bash {"command":"ls"}`})
	if u := c.update(worker, "approval"); !strings.Contains(string(u.Request), `"tool_name":"Bash"`) {
		t.Fatalf("a queued approval request was taken as plain text: %s", u.Request)
	}
	c.call("session/destroy", map[string]string{"session_id": worker})

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
	restarted, err := New(st, cfg, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if n := listed(connect(t, restarted)); n != 0 {
		t.Fatalf("a removed workspace came back after a restart: %d", n)
	}
}

// Settings taken are kept even when storing them fails: every client is told
// what the session now uses, and the session's next process starts with them.
func TestConfigurationTakenButNotStored(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}}},
		PermissionModes: []config.Option{{ID: "plan"}},
	}}}
	st := openStore(t)
	started := make(chan agent.Settings, 2)
	start := func(options agent.Options) (agent.Agent, error) {
		started <- options.Settings
		return fake.Start(options)
	}
	s, err := New(st, cfg, 100*time.Millisecond, map[string]agent.Harness{"fake": {Start: start}}, MCP{})
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
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "effort": "low", "permission_mode": "plan",
	}), &created)
	parked := created.SessionID
	// The background task keeps the running session from parking until it
	// has taken its configuration.
	c.call("session/send", map[string]string{"session_id": id, "text": "background 2000"})
	<-started
	for c.update(id, "state").State != "background" {
	}

	st.Close()
	for _, id := range []string{id, parked} {
		c.call("session/configure", map[string]string{"session_id": id, "effort": "high"})
		configured := c.next(func(m message) bool { return m.Method == "session/configured" && m.sessionID() == id })
		if !strings.Contains(string(configured.Params), `"effort":"high"`) {
			t.Fatalf("clients were not told the settings taken: %s", configured.Params)
		}
	}

	c.update(id, agent.UpdateParked)
	for _, id := range []string{id, parked} {
		c.call("session/send", map[string]string{"session_id": id, "text": "hi"})
		if settings, want := <-started, (agent.Settings{Model: "m", Effort: "high", PermissionMode: "plan"}); settings != want {
			t.Fatalf("the session's next process started with %+v, not the settings it took", settings)
		}
	}
}

// A top-level session is labeled once, with the first nonblank first line of
// the prompts the user sent it, through a restart and a prompt that could not
// be stored.
func TestSessionLabel(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}},
	}}}
	harnesses := map[string]agent.Harness{"fake": {Start: fake.Start}}
	st := openStore(t)
	s, err := New(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	type labeled struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
	}
	create := func() string {
		var created labeled
		json.Unmarshal(c.call("session/create", map[string]string{
			"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "m", "permission_mode": "manual",
		}), &created)
		event := c.next(func(m message) bool { return m.Method == "session/created" && m.sessionID() == created.SessionID })
		json.Unmarshal(event.Params, &created)
		if created.Label != created.SessionID[:8] {
			t.Fatalf("session/created labeled %s %q", created.SessionID, created.Label)
		}
		return created.SessionID
	}
	a, b := create(), create()
	labelOf := func(c *client, id string) string {
		t.Helper()
		label := c.next(func(m message) bool { return m.Method == "session/labeled" && m.sessionID() == id })
		var params labeled
		json.Unmarshal(label.Params, &params)
		return params.Label
	}
	listed := func(c *client, id string) string {
		t.Helper()
		var list struct {
			Workspaces []struct {
				Sessions []labeled `json:"sessions"`
			} `json:"workspaces"`
		}
		json.Unmarshal(c.call("workspace/list", struct{}{}), &list)
		for _, w := range list.Workspaces {
			for _, sess := range w.Sessions {
				if sess.SessionID == id {
					return sess.Label
				}
			}
		}
		t.Fatalf("workspace/list did not list %s", id)
		return ""
	}
	idle := func(c *client, id string) {
		for c.update(id, "state").State != "idle" {
		}
	}

	// A no-break space is blank to Go and not to SQLite's trim.
	c.call("session/send", map[string]any{"session_id": a, "text": "\u00a0", "images": []string{pngBase64(16)}})
	idle(c, a)
	for _, m := range c.received(a) {
		if m.Method == "session/labeled" {
			t.Fatalf("an image-only prompt labeled A: %s", m.Params)
		}
	}
	if got := listed(c, a); got != a[:8] {
		t.Fatalf("an image-only prompt labeled A %q", got)
	}

	c.call("session/send", map[string]string{"session_id": a, "text": "\u0085\nActual title\nmore"})
	if got := labelOf(c, a); got != "Actual title" {
		t.Fatalf("session/labeled labeled A %q", got)
	}
	idle(c, a)
	c.call("session/send", map[string]string{"session_id": a, "text": "second"})
	idle(c, a)
	if got := listed(c, a); got != "Actual title" {
		t.Fatalf("a later prompt relabeled A %q", got)
	}

	t.Log("a restart labels A from its stored history")
	s.Close()
	s, err = New(st, cfg, 0, harnesses, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c = connect(t, s)
	if got := listed(c, a); got != "Actual title" {
		t.Fatalf("after a restart, A is labeled %q", got)
	}

	t.Log("a prompt that could not be stored labels B")
	st.Close()
	c.call("session/send", map[string]string{"session_id": b, "text": "Unsaved title"})
	if got := labelOf(c, b); got != "Unsaved title" {
		t.Fatalf("session/labeled labeled B %q", got)
	}
	if got := listed(c, b); got != "Unsaved title" {
		t.Fatalf("workspace/list labeled B %q", got)
	}
}

// A process that exits during a configuration is replaced by one started with
// the requested settings, so the configuration succeeds.
func TestConfigurationOutlivesItsProcess(t *testing.T) {
	dir := t.TempDir()
	args, exit := filepath.Join(dir, "args"), filepath.Join(dir, "exit")
	// Acknowledge the model but hold the effort control until told to exit.
	script := logStarts(args) + `
read initialize
printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'
read prompt
uuid=${prompt##*'"uuid":"'}; uuid=${uuid%%'"'*}
printf '{"type":"command_lifecycle","state":"started","command_uuid":"%s"}\n' "$uuid"
printf '%s\n' '{"type":"system","subtype":"session_state_changed","state":"idle"}'
read model
request=${model#*'"request_id":"'}; request=${request%%'"'*}
printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s"}}\n' "$request"
read effort
printf '%s\n' '{"type":"system","subtype":"status","permissionMode":"default"}'
while [ ! -e '` + exit + `' ]; do sleep .02; done
`
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	efforts := []config.Option{{ID: "low"}, {ID: "high"}}
	cfg := &config.Config{Harnesses: map[string]config.Harness{"claude": {
		Models:          []config.Model{{ID: "m", Efforts: efforts}, {ID: "new", Efforts: efforts}},
		PermissionModes: []config.Option{{ID: "plan"}, {ID: "manual"}},
	}}}
	s, err := New(openStore(t), cfg, 0, map[string]agent.Harness{"claude": claude.Harness}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	var workspace struct {
		ID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": dir}), &workspace)
	var created struct {
		ID string `json:"session_id"`
	}
	json.Unmarshal(c.call("session/create", map[string]string{
		"workspace_id": workspace.ID, "harness": "claude", "model": "m", "effort": "low", "permission_mode": "plan",
	}), &created)
	id := created.ID
	c.call("session/send", map[string]string{"session_id": id, "text": "hi"})
	for c.update(id, "state").State != "idle" {
	}

	configure := c.send("session/configure", map[string]string{"session_id": id, "model": "new", "effort": "high"})
	checkSettings := func(data []byte, want agent.Settings) {
		t.Helper()
		var got agent.Settings
		if err := json.Unmarshal(data, &got); err != nil || got != want {
			t.Fatalf("settings %+v, want %+v (%v)", got, want, err)
		}
	}
	nextConfigured := func() []byte {
		return c.next(func(m message) bool { return m.Method == "session/configured" && m.sessionID() == id }).Params
	}
	checkSettings(nextConfigured(), agent.Settings{Model: "new", Effort: "low", PermissionMode: "plan"})
	beforeExit := agent.Settings{Model: "new", Effort: "low", PermissionMode: "manual"}
	checkSettings(nextConfigured(), beforeExit)
	checkSettings(c.call("session/get", map[string]string{"session_id": id}), beforeExit)
	if err := os.WriteFile(exit, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := c.response(configure); r.Error != nil {
		t.Fatalf("a configuration whose process exited failed: %+v", r.Error)
	}
	afterExit := agent.Settings{Model: "new", Effort: "high", PermissionMode: "manual"}
	checkSettings(nextConfigured(), afterExit)
	c.update(id, "parked")
	checkSettings(c.call("session/get", map[string]string{"session_id": id}), afterExit)
	c.forget(id)
	c.call("session/send", map[string]string{"session_id": id, "text": "again"})
	c.update(id, "prompt")
	if lines := starts(t, args); len(lines) != 2 || !strings.Contains(lines[1], "--model=new --permission-mode=manual --effort=high") {
		t.Fatalf("the next process did not use the current settings: %q", lines)
	}
}

// lostNotice is the wording of the notice that Claude could not resume the
// conversation.
const lostNotice = "could not resume the earlier conversation"

// lost checks that the send that found the conversation gone succeeded, and
// that the prompt was then dropped with the lost notice and the session
// parked.
func lost(t *testing.T, c *client, sent message) {
	t.Helper()
	if sent.Error != nil {
		t.Fatalf("the send failed: %+v", sent.Error)
	}
	var reply struct {
		MessageID string `json:"message_id"`
	}
	json.Unmarshal(sent.Result, &reply)
	if cancelled := c.update("s", "prompt_cancelled"); cancelled.MessageID != reply.MessageID || cancelled.Text != "hi" {
		t.Fatalf("the prompt was not dropped: %+v", cancelled)
	}
	if notice := c.update("s", "exited"); notice.MessageID != reply.MessageID || !strings.Contains(notice.Error, lostNotice) {
		t.Fatalf("the transcript has no notice: %+v", notice)
	}
	c.update("s", "parked")
}

// One session starting or stopping Claude holds up no request to another: a
// send replies before Claude has answered initialize, and a destroy replies
// while Claude, ignoring SIGTERM, is still running.
func TestStartAndStopHoldUpNoOtherSession(t *testing.T) {
	dir := t.TempDir()
	gate, pidFile := filepath.Join(dir, "gate"), filepath.Join(dir, "pid")
	// A stand-in for Claude that ignores SIGTERM and answers initialize only
	// once the gate exists.
	script := "#!/bin/sh\ntrap '' TERM\necho $$ > " + pidFile + "\nread line\n" +
		"while [ ! -e " + gate + " ]; do sleep .02; done\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		takePrompt +
		"exec cat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"session-a", "session-b"} {
		if err := st.AddSession("w", &store.Session{ID: id, Harness: "claude", Model: "m", PermissionMode: "manual", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"claude": claude.Harness}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	pid := func() int {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		return pid
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidFile); err == nil {
			exec.Command("kill", "-9", strings.TrimSpace(string(data))).Run()
		}
	})
	c := connect(t, s)
	fromA := attach(t, s, "session-a")
	// stateOfB is B's state as A's list_sessions reports it, or empty when it
	// is not listed.
	stateOfB := func() string {
		t.Helper()
		text, refused := fromA.tool("list_sessions", map[string]any{})
		var sessions []struct {
			SessionID string `json:"session_id"`
			State     string `json:"state"`
		}
		if err := json.Unmarshal([]byte(text), &sessions); refused || err != nil {
			t.Fatalf("list_sessions returned %q", text)
		}
		for _, l := range sessions {
			if l.SessionID == "session-b" {
				return l.State
			}
		}
		return ""
	}

	var sent struct {
		MessageID string `json:"message_id"`
	}
	json.Unmarshal(c.call("session/send", map[string]string{"session_id": "session-b", "text": "hi"}), &sent)
	var got struct {
		State  string        `json:"state"`
		Queued []agent.Input `json:"queued"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": "session-b"}), &got)
	if got.State != "running" || len(got.Queued) != 1 || got.Queued[0].MessageID != sent.MessageID {
		t.Fatalf("a starting session does not show its prompt pending: %+v", got)
	}
	if state := stateOfB(); state != "running" {
		t.Fatalf("list_sessions listed the starting session as %q", state)
	}

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if taken := c.update("session-b", "prompt"); taken.MessageID != sent.MessageID {
		t.Fatalf("the prompt held while Claude started was not taken: %+v", taken)
	}

	c.call("session/destroy", map[string]string{"session_id": "session-b"})
	if state := stateOfB(); state != "" {
		t.Fatalf("the destroyed session is listed as %q", state)
	}
	if err := syscall.Kill(pid(), 0); err != nil {
		t.Fatalf("Claude exited before the destroy and list_sessions answered: %v", err)
	}
}

// A session destroyed while Claude starts or takes a configuration ends with
// session/destroyed: nothing its process held, such as the prompt or the
// configuration, reaches clients after it.
func TestDestroyWhileStartingOrConfiguring(t *testing.T) {
	dir := t.TempDir()
	started, configuring := filepath.Join(dir, "started"), filepath.Join(dir, "configuring")
	// A stand-in for Claude that answers initialize once the started gate
	// exists, takes a prompt, and marks the configuration it reads.
	script := "#!/bin/sh\nread line\n" +
		"while [ ! -e " + started + " ]; do sleep .02; done\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		takePrompt +
		`echo '{"type":"system","subtype":"session_state_changed","state":"idle"}'` + "\n" +
		"read line\ntouch " + configuring + "\n" +
		"exec cat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := &config.Config{Harnesses: map[string]config.Harness{"claude": {
		Models:          []config.Model{{ID: "m", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}}},
		PermissionModes: []config.Option{{ID: "manual"}},
	}}}
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: dir, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"session-d", "session-b"} {
		if err := st.AddSession("w", &store.Session{ID: id, Harness: "claude", Model: "m", Effort: "low", PermissionMode: "manual", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(st, cfg, 0, map[string]agent.Harness{"claude": claude.Harness}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	endsDestroyed := func(id string) {
		t.Helper()
		c.call("workspace/list", map[string]any{})
		received := c.received(id)
		if len(received) == 0 || received[len(received)-1].Method != "session/destroyed" {
			t.Fatalf("session %s's last notification is not session/destroyed: %+v", id, received)
		}
	}

	// Starting: Claude has not answered initialize.
	c.call("session/send", map[string]string{"session_id": "session-d", "text": "hi"})
	c.call("session/destroy", map[string]string{"session_id": "session-d"})
	endsDestroyed("session-d")

	// Configuring: Claude has read the configuration and not acknowledged it.
	if err := os.WriteFile(started, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c.call("session/send", map[string]string{"session_id": "session-b", "text": "hi"})
	for c.update("session-b", "state").State != "idle" {
	}
	configure := c.send("session/configure", map[string]string{"session_id": "session-b", "effort": "high"})
	for {
		if _, err := os.Stat(configuring); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.call("session/destroy", map[string]string{"session_id": "session-b"})
	c.response(configure)
	endsDestroyed("session-b")
}

// A resume token whose conversation Claude no longer has, such as one whose
// transcript Claude deleted, drops one prompt with a notice in the
// transcript; the next send starts a new conversation.
func TestUnresumableConversationStartsAnew(t *testing.T) {
	st, _, c, args := unresumableSession(t)

	lost(t, c, c.request("session/send", map[string]string{"session_id": "s", "text": "hi"}))
	var got struct {
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": "s"}), &got)
	if len(got.History) != 2 || !strings.Contains(got.History[0].Error, lostNotice) || got.History[1].Type != "parked" {
		t.Fatalf("the notice was not recorded: %+v", got.History)
	}

	c.call("session/send", map[string]string{"session_id": "s", "text": "hi"})
	c.update("s", "prompt")
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
	st, _, c, args := unresumableSession(t)
	st.Close()
	lost(t, c, c.request("session/send", map[string]string{"session_id": "s", "text": "hi"}))
	c.call("session/send", map[string]string{"session_id": "s", "text": "hi"})
	c.update("s", "prompt")
	if lines := starts(t, args); len(lines) != 2 || strings.Contains(lines[1], "--resume") {
		t.Fatalf("the next send did not start a new conversation: %q", lines)
	}
}

func TestMessageToUnresumableConversationLeavesOneNotice(t *testing.T) {
	_, s, c, args := unresumableSession(t)
	sender := attach(t, s, "sender-session")
	if text, refused := sender.tool("send_message", map[string]string{"to": "s", "text": "hi"}); refused {
		t.Fatal(text)
	}
	queued := c.update("s", "prompt_queued")
	if u := c.update("s", "prompt_cancelled"); u.MessageID != queued.MessageID {
		t.Fatalf("the message was not cancelled: %+v", u)
	}
	c.update("s", "parked")
	var got struct {
		Queued  []agent.Input  `json:"queued"`
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": "s"}), &got)
	if len(got.Queued) != 0 || len(got.History) != 2 || got.History[0].Type != "exited" || !strings.Contains(got.History[0].Error, lostNotice) ||
		got.History[1].Type != "parked" {
		t.Fatalf("a failed resume for a message left extra notices or pending messages: %+v", got)
	}
	if lines := starts(t, args); len(lines) != 1 {
		t.Fatalf("the process start was retried: %q", lines)
	}
}

// takePrompt is a step of a stand-in claude script: it reads a prompt and
// takes it, as Claude reports with command_lifecycle.
const takePrompt = `read prompt
uuid=${prompt##*'"uuid":"'}; uuid=${uuid%%'"'*}
printf '{"type":"command_lifecycle","state":"started","command_uuid":"%s"}\n' "$uuid"
`

// logStarts is the head of a stand-in claude script: it appends each start's
// arguments to the file args as one line, which starts reads.
func logStarts(args string) string {
	return "#!/bin/sh\nprintf '%s\\n' \"$(echo \"$@\" | tr '\\n' ' ')\" >> " + args + "\n"
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
func unresumableSession(t *testing.T) (*store.Store, *Server, *client, string) {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	// A stand-in for Claude, which fails a resume as Claude does when it has
	// no transcript for the ID.
	script := logStarts(args) +
		`case "$*" in *--resume=*) echo '{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["No conversation found with session ID: gone"]}'; exit 1;; esac` + "\n" +
		"read line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		takePrompt +
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
	if err := st.AddSession("w", &store.Session{ID: "sender-session", Harness: "claude", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"claude": claude.Harness}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return st, s, connect(t, s), args
}

// A session keeps its conversation's resume token even when storing it fails,
// so the next process resumes the conversation.
func TestResumeTokenKeptWhenNotStored(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	// A stand-in for Claude that answers one prompt and exits.
	script := logStarts(args) +
		"read line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		"read prompt\n" +
		`echo '{"type":"result","subtype":"success","session_id":"11111111-1111-4111-8111-111111111111","is_error":false,"result":"done"}'` + "\n"
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
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"claude": claude.Harness}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)

	st.Close()
	c.call("session/send", map[string]string{"session_id": "s", "text": "hi"})
	c.update("s", "exited")
	c.update("s", "parked")
	c.forget("s")
	c.call("session/send", map[string]string{"session_id": "s", "text": "again"})
	c.update("s", "result")
	lines := starts(t, args)
	if len(lines) != 2 || !strings.Contains(lines[1], "--resume=") {
		t.Fatalf("the next process did not resume the conversation: %q", lines)
	}
}

// A mode Claude changes itself, as it does when a plan is approved, reaches
// every client, the store, and the session's later processes.
func TestClaudeModeChangeIsAdopted(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	// A stand-in for Claude that leaves plan mode once it has read a prompt,
	// answers it, and exits.
	script := logStarts(args) +
		"read line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		"read prompt\n" +
		`echo '{"type":"system","subtype":"status","status":null,"permissionMode":"default"}'` + "\n" +
		`echo '{"type":"result","subtype":"success","session_id":"11111111-1111-4111-8111-111111111111","is_error":false,"result":"done"}'` + "\n"
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
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"claude": claude.Harness}, MCP{})
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

	c.update("s", "parked")
	c.forget("s")
	c.call("session/send", map[string]string{"session_id": "s", "text": "again"})
	c.update("s", "result")
	lines := starts(t, args)
	if len(lines) != 2 || !strings.Contains(lines[1], "--resume=11111111-1111-4111-8111-111111111111") ||
		!strings.Contains(lines[1], "--permission-mode=manual") {
		t.Fatalf("the next process did not resume in the mode Claude chose: %q", lines)
	}
}

// An approval reaches every client and a reloaded snapshot, takes one answer,
// which reaches the harness untouched, and is refused once answered.
func TestApprovalAnsweredOnce(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models:          []config.Model{{ID: "m"}},
		PermissionModes: []config.Option{{ID: "manual"}},
	}}}
	s, err := New(openStore(t), cfg, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c, other := connect(t, s), connect(t, s)
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
	if seen := other.update(id, "approval"); seen.RequestID != asked.RequestID {
		t.Fatalf("another client saw a different approval: %+v", seen)
	}
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
	if answered := other.update(id, "approval_answered"); answered.RequestID != asked.RequestID {
		t.Fatalf("another client was not told the approval was answered: %+v", answered)
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
	s, err := New(openStore(t), cfg, 0, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
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
			if busy := c.request("session/configure", map[string]string{"session_id": id, "model": "m"}); busy.Error == nil || busy.Error.Code != sessionBusy {
				t.Fatalf("a configuration ran while a prompt was untaken: %+v", busy.Error)
			}
			var snapshot struct {
				Queued []agent.Input `json:"queued"`
			}
			json.Unmarshal(c.call("session/get", map[string]string{"session_id": id}), &snapshot)
			if len(snapshot.Queued) != 1 || snapshot.Queued[0].MessageID != sent.MessageID || snapshot.Queued[0].Text != "steer" {
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
	if first, viaParent := add(filepath.Dir(dir)), add(link+"/.."); first != viaParent {
		t.Fatalf("the path through a symlink followed by .. named another workspace: %s, %s", first, viaParent)
	}
	c.drain()
	var announced int
	for _, m := range c.backlog {
		if m.Method == "workspace/created" {
			announced++
		}
	}
	if announced != 2 {
		t.Fatalf("%d workspaces were announced for two directories", announced)
	}
}

// A workspace's directory is listed and read through its root, which nothing
// leads outside of.
func TestWorkspaceFiles(t *testing.T) {
	s, err := New(openStore(t), &config.Config{}, 0, nil, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	dir, outside := t.TempDir(), t.TempDir()
	for _, folder := range []string{"src", "src/nested", ".git", "empty"} {
		if err := os.Mkdir(filepath.Join(dir, folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		".DS_Store": "", ".gitignore": "ignored.log\n", "ignored.log": "", "src/main.go": "package main\n", "src/.git": "",
		"bin.dat": "a\x00b", "big.txt": strings.Repeat("x", 1<<20+1), "latin1.txt": "caf\xe9",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		"inside": "src", "outside": outside, "absolute": filepath.Join(dir, "src"), "notes": ".gitignore",
		"leak": filepath.Join(outside, "secret"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("nested", filepath.Join(dir, "src", "sibling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("src", "nested"), filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	var w struct {
		ID string `json:"workspace_id"`
	}
	json.Unmarshal(c.call("workspace/create", map[string]string{"path": dir}), &w)

	list := func(path string) map[string]bool {
		var result struct {
			Entries []folderEntry `json:"entries"`
		}
		raw := c.call("workspace/list_folder", map[string]string{"workspace_id": w.ID, "path": path})
		if err := json.Unmarshal(raw, &result); err != nil || result.Entries == nil {
			t.Fatalf("listing %q returned %s", path, raw)
		}
		folders := map[string]bool{}
		for _, e := range result.Entries {
			folders[e.Name] = e.Folder
		}
		return folders
	}
	want := map[string]bool{
		"src": true, "empty": true, "inside": true, "alias": true, "outside": false, "absolute": false, "notes": false,
		".gitignore": false, "ignored.log": false, "bin.dat": false, "big.txt": false, "latin1.txt": false,
		"pipe": false, "leak": false,
	}
	if got := list(""); !maps.Equal(got, want) {
		t.Errorf("root listed %v, want %v", got, want)
	}
	srcWant := map[string]bool{"main.go": false, "nested": true, "sibling": true}
	if got := list("src"); !maps.Equal(got, srcWant) {
		t.Errorf("src listed %v, want %v", got, srcWant)
	}
	// ".." after a link leaves the link's target, so a link beside the target is
	// classified in the target's parent.
	if got := list("alias/.."); !maps.Equal(got, srcWant) {
		t.Errorf("alias/.. listed %v, want %v", got, srcWant)
	}
	if got := list("empty"); len(got) != 0 {
		t.Errorf("empty folder listed %v", got)
	}

	refused := func(method string, code int, workspaceID, path, message string) {
		t.Helper()
		response := c.request(method, map[string]string{"workspace_id": workspaceID, "path": path})
		if response.Error == nil || response.Error.Code != code || !strings.Contains(response.Error.Message, message) {
			t.Errorf("%s %q in %q: %.200s %+v, want error %d %q", method, path, workspaceID, response.Result, response.Error, code, message)
		}
	}
	refused("workspace/list_folder", -32602, w.ID, "../x", "")
	refused("workspace/list_folder", -32602, w.ID, outside, "")
	refused("workspace/list_folder", -32000, w.ID, "outside", "")
	refused("workspace/list_folder", -32000, w.ID, ".gitignore", "")
	refused("workspace/list_folder", -32001, "missing", "", "")

	read := func(path string) string {
		var result struct {
			Text *string `json:"text"`
		}
		raw := c.call("workspace/read_file", map[string]string{"workspace_id": w.ID, "path": path})
		if err := json.Unmarshal(raw, &result); err != nil || result.Text == nil {
			t.Fatalf("reading %q returned %s", path, raw)
		}
		return *result.Text
	}
	if got := read("src/main.go"); got != "package main\n" {
		t.Errorf("src/main.go read %q", got)
	}
	if got := read("notes"); got != "ignored.log\n" {
		t.Errorf("notes read %q", got)
	}
	refused("workspace/read_file", -32000, w.ID, "bin.dat", "file is binary")
	refused("workspace/read_file", -32000, w.ID, "big.txt", "file is larger than 1 MiB")
	refused("workspace/read_file", -32000, w.ID, "latin1.txt", "file is not UTF-8 text")
	refused("workspace/read_file", -32000, w.ID, "src", "not a regular file")
	refused("workspace/read_file", -32000, w.ID, "pipe", "not a regular file")
	refused("workspace/read_file", -32000, w.ID, "leak", "")
	refused("workspace/read_file", -32000, w.ID, "missing.txt", "no such file or directory")
	refused("workspace/read_file", -32602, w.ID, "", "path must be relative to the workspace")
	refused("workspace/read_file", -32602, w.ID, "../x", "path must be relative to the workspace")
	refused("workspace/read_file", -32602, w.ID, filepath.Join(outside, "secret"), "path must be relative to the workspace")
	refused("workspace/read_file", -32001, "missing", "notes", "workspace not found")
}

// configuringAgent reports each configuration it starts on started.
type configuringAgent struct {
	agent.Agent
	started chan<- struct{}
}

func (a configuringAgent) Configure(patch agent.SettingsPatch, done func(error)) {
	a.Agent.Configure(patch, done)
	a.started <- struct{}{}
}

// Claude in one session lists the others and sends them messages, which
// reach the recipient as prompts with their sender, queued in order.
func TestSendMessage(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{
		"fake":   {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
		"broken": {Models: []config.Model{{ID: "m"}}, PermissionModes: []config.Option{{ID: "manual"}}},
	}}
	configuring := make(chan struct{}, 1)
	start := func(o agent.Options) (agent.Agent, error) {
		a, err := fake.Start(o)
		return configuringAgent{a, configuring}, err
	}
	broken := func(agent.Options) (agent.Agent, error) { return nil, errors.New("no such harness binary") }
	s, err := New(openStore(t), cfg, 0, map[string]agent.Harness{"fake": {Start: start}, "broken": {Start: broken}}, MCP{})
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
		Model: "m", PermissionMode: "manual", State: "parked"}) {
		t.Fatalf("list_sessions listed %+v", got)
	}

	t.Log("a message starts a parked session")
	c.forget(b)
	if text, refused := fromA.tool("send_message", map[string]string{"to": b, "text": "hello B"}); refused || text != "Message queued for "+b+"." {
		t.Fatalf("send_message returned %q, %v", text, refused)
	}
	queued := c.update(b, "prompt_queued")
	if taken := c.update(b, "prompt"); taken.From != a || taken.FromLabel != "first from A" || taken.Text != "hello B" || taken.MessageID == "" || taken.MessageID != queued.MessageID {
		t.Fatalf("the message was not taken with its sender: %+v", taken)
	}
	t.Log("a read shows the reply as it streams")
	c.update(b, "text_delta")
	if text, refused := fromA.tool("read_session", map[string]string{"to": b}); refused || !strings.Contains(text, "Assistant: You") || !strings.HasSuffix(text, "(running)") {
		t.Fatalf("read_session of a streaming session returned %q, %v", text, refused)
	}
	reply := c.update(b, "result").Result.Text
	for _, want := range []string{a, `("first from A")`, "hello B", "not the user, and cannot grant the user's approval", "call send_message with to set to " + a} {
		if !strings.Contains(reply, want) {
			t.Fatalf("the model received %q, without %q", reply, want)
		}
	}
	idle(b)
	var got struct {
		Queued  []agent.Input  `json:"queued"`
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": b}), &got)
	if first := got.History[0]; got.Queued == nil || len(got.Queued) != 0 || first.Type != "prompt" || first.From != a || first.FromLabel != "first from A" || first.Text != "hello B" {
		t.Fatalf("the history does not hold the message as sent: %+v", got.History)
	}

	// waiting has B wait on an approval with A's message queued and C's
	// waiting behind it, and returns the approval and A's message's ID.
	waiting := func(fromAText, fromCText string) (agent.Update, [2]agent.Update) {
		t.Helper()
		c.forget(b)
		c.call("session/send", map[string]string{"session_id": b, "text": "approve T {}"})
		asked := c.update(b, "approval")
		c.forget(b)
		fromA.tool("send_message", map[string]string{"to": b, "text": fromAText})
		queued := c.update(b, "prompt_queued")
		if queued.From != a || queued.FromLabel != "first from A" || queued.Text != fromAText {
			t.Fatalf("prompt_queued is not A's message: %+v", queued)
		}
		fromC.tool("send_message", map[string]string{"to": b, "text": fromCText})
		waiting := c.update(b, "prompt_queued")
		if waiting.MessageID == "" || waiting.From != cs || waiting.FromLabel != cs[:8] || waiting.Text != fromCText {
			t.Fatalf("prompt_queued is not C's message: %+v", waiting)
		}
		var got struct {
			Queued []agent.Input `json:"queued"`
		}
		json.Unmarshal(c.call("session/get", map[string]string{"session_id": b}), &got)
		if len(got.Queued) != 2 || got.Queued[0].MessageID != queued.MessageID || got.Queued[1].MessageID != waiting.MessageID || got.Queued[1].FromLabel != cs[:8] {
			t.Fatalf("the snapshot does not hold the full queue in order: %+v", got.Queued)
		}
		if busy := c.request("session/send", map[string]string{"session_id": b, "text": "user"}); busy.Error == nil || busy.Error.Code != sessionBusy {
			t.Fatalf("a user prompt was queued beside a message: %+v", busy.Error)
		}
		if text, refused := fromA.tool("read_session", map[string]string{"to": b}); refused || !strings.Contains(text, "Tool: T {}") || !strings.HasSuffix(text, "(waiting for approval: T, 2 queued)") {
			t.Fatalf("read_session of a waiting session returned %q, %v", text, refused)
		}
		c.forget(b)
		return asked, [2]agent.Update{queued, waiting}
	}

	t.Log("messages wait their turn in order")
	asked, waitingInputs := waiting("one from A", "one from C")
	c.call("session/approve", map[string]any{"session_id": b, "request_id": asked.RequestID, "response": json.RawMessage(`{"behavior":"allow"}`)})
	for _, want := range waitingInputs {
		if taken := c.update(b, "prompt"); taken.MessageID != want.MessageID || taken.From != want.From || taken.Text != want.Text || taken.FromLabel != want.FromLabel {
			t.Fatalf("the next message %+v was not taken: %+v", want, taken)
		}
	}
	for _, m := range c.received(b) {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		if u.Type == "prompt_queued" {
			t.Fatalf("a message was announced again when taken: %+v", u)
		}
	}
	c.call("session/interrupt", map[string]string{"session_id": b})
	idle(b)

	t.Log("Stop drops every message Claude has not taken")
	_, dropped := waiting("two from A", "two from C")
	c.call("session/interrupt", map[string]string{"session_id": b})
	for _, want := range dropped {
		if cancelled := c.update(b, "prompt_cancelled"); cancelled.MessageID != want.MessageID || cancelled.From != want.From || cancelled.FromLabel != want.FromLabel {
			t.Fatalf("prompt_cancelled does not name %+v: %+v", want, cancelled)
		}
	}
	idle(b)
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": b}), &got)
	if len(got.Queued) != 0 {
		t.Fatalf("messages outlived Stop: %+v", got.Queued)
	}
	for _, u := range got.History {
		if strings.HasPrefix(u.Text, "two from") {
			t.Fatalf("a dropped message is in the history: %+v", u)
		}
	}

	if text, refused := fromA.tool("read_session", map[string]string{"to": b}); refused || !strings.Contains(text, "Tool: T {} (failed)") || !strings.Contains(text, "Error: Interrupted") {
		t.Fatalf("read_session of an interrupted session returned %q, %v", text, refused)
	}

	// configureB sends B a configuration and returns its request's ID once
	// the configuration runs.
	configureB := func() string {
		t.Helper()
		id := c.send("session/configure", map[string]string{"session_id": b, "model": "m"})
		select {
		case <-configuring:
		case <-time.After(c.timeout):
			t.Fatal("the configuration did not start")
		}
		return id
	}
	isPrompt := func(m message) bool {
		var u agent.Update
		json.Unmarshal(m.Params, &u)
		return m.Method == "session/update" && m.sessionID() == b && u.Type == "prompt"
	}

	t.Log("inputs sent during a configuration wait for it, in order")
	c.forget(b)
	configure := configureB()
	var prompt struct {
		MessageID string `json:"message_id"`
	}
	json.Unmarshal(c.call("session/send", map[string]string{"session_id": b, "text": "user during configuration"}), &prompt)
	if queued := c.update(b, "prompt_queued"); queued.MessageID != prompt.MessageID {
		t.Fatalf("the user prompt sent during configuration was not queued: %+v", queued)
	}
	fromA.tool("send_message", map[string]string{"to": b, "text": "during configuration"})
	sent := c.update(b, "prompt_queued")
	if first := c.next(func(m message) bool { return string(m.ID) == configure || isPrompt(m) }); string(first.ID) != configure || first.Error != nil {
		t.Fatalf("an input was taken before the configuration succeeded: %+v", first)
	}
	for _, want := range []string{prompt.MessageID, sent.MessageID} {
		if taken := c.update(b, "prompt"); taken.MessageID != want {
			t.Fatalf("%s was not taken next after the configuration: %+v", want, taken)
		}
	}
	idle(b)

	t.Log("Stop during a configuration drops the inputs sent during it")
	c.forget(b)
	configure = configureB()
	fromA.tool("send_message", map[string]string{"to": b, "text": "dropped during configuration"})
	held := c.update(b, "prompt_queued")
	c.call("session/interrupt", map[string]string{"session_id": b})
	if cancelled := c.update(b, "prompt_cancelled"); cancelled.MessageID != held.MessageID {
		t.Fatalf("Stop during a configuration did not drop the message: %+v", cancelled)
	}
	if response := c.response(configure); response.Error != nil {
		t.Fatalf("the configuration failed: %+v", response.Error)
	}
	c.call("session/get", map[string]string{"session_id": b})
	for _, m := range c.received(b) {
		if isPrompt(m) {
			t.Fatalf("an input dropped during a configuration was taken: %s", m.Params)
		}
	}

	t.Log("a read leaves a parked session parked")
	c.forget(cs)
	if text, refused := fromA.tool("read_session", map[string]string{"to": cs}); refused || text != "(parked)" {
		t.Fatalf("read_session of C returned %q, %v", text, refused)
	}
	c.call("session/get", map[string]string{"session_id": cs})
	if received := c.received(cs); len(received) != 0 {
		t.Fatalf("a read reached C: %v", received)
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
	for _, refusal := range []struct {
		arguments map[string]string
		reason    string
	}{
		{map[string]string{}, "to is required"},
		{map[string]string{"to": a}, "cannot read your own session"},
		{map[string]string{"to": "nobody"}, "session not found"},
	} {
		if text, refused := fromA.tool("read_session", refusal.arguments); !refused || text != refusal.reason {
			t.Errorf("read_session %v returned %q, %v", refusal.arguments, text, refused)
		}
	}
	c.call("session/get", map[string]string{"session_id": b})
	if received := c.received(b); len(received) != 0 {
		t.Fatalf("a refused message reached B: %v", received)
	}

	t.Log("a message to a session that cannot be launched is refused")
	c.forget(d)
	if text, refused := fromA.tool("send_message", map[string]string{"to": d, "text": "hello D"}); !refused || text != "no such harness binary" {
		t.Fatalf("send_message to a session that cannot be launched returned %q, %v", text, refused)
	}
	c.call("session/get", map[string]string{"session_id": d})
	if received := c.received(d); len(received) != 0 {
		t.Fatalf("a refused message reached D: %v", received)
	}

	sessions := list()
	if got := sessions[b]; got.Label != "approve T {}" || got.State != "idle" {
		t.Fatalf("B, whose first user prompt followed a message, is listed as %+v", got)
	}
	if got := sessions[cs]; got.Label != cs[:8] || got.State != "parked" {
		t.Fatalf("C is listed as %+v", got)
	}
	c.call("session/destroy", map[string]string{"session_id": a})
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": b}), &got)
	if first := got.History[0]; first.FromLabel != "first from A" {
		t.Fatalf("deleting the sender lost its label in the recipient's history: %+v", first)
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
	script := logStarts(args) +
		"read line\n" +
		`echo '{"type":"control_response","response":{"subtype":"success","request_id":"initialize"}}'` + "\n" +
		"read message\n" +
		"while [ ! -e " + exit + " ]; do sleep 0.05; done\necho 'tool runtime failed' >&2\nexit 1\n"
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
	s, err := New(st, &config.Config{}, 0, map[string]agent.Harness{"claude": claude.Harness}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c := connect(t, s)
	mcp := attach(t, s, sender)

	var queued []agent.Update
	for _, text := range []string{"first", "second"} {
		mcp.tool("send_message", map[string]string{"to": recipient, "text": text})
		u := c.update(recipient, "prompt_queued")
		if u.Text != text || u.MessageID == "" {
			t.Fatalf("the message was not queued: %+v", u)
		}
		queued = append(queued, u)
	}
	if err := os.WriteFile(exit, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, want := range queued {
		if u := c.update(recipient, "prompt_cancelled"); u.MessageID != want.MessageID {
			t.Fatalf("exit did not cancel %+v: %+v", want, u)
		}
	}
	const reason = "Claude exited: exit status 1\ntool runtime failed"
	if notice := c.update(recipient, "exited"); notice.Error != reason {
		t.Fatalf("the exit notice omitted Claude's stderr: %+v", notice)
	}
	c.update(recipient, "parked")
	var got struct {
		Queued  []agent.Input  `json:"queued"`
		History []agent.Update `json:"history"`
	}
	json.Unmarshal(c.call("session/get", map[string]string{"session_id": recipient}), &got)
	if len(got.History) != 2 || got.History[0].Error != reason {
		t.Fatalf("the exit reason was not recorded: %+v", got.History)
	}
	if got.Queued == nil || len(got.Queued) != 0 {
		t.Fatalf("messages outlived the exit: %+v", got.Queued)
	}
	if lines := starts(t, args); len(lines) != 1 {
		t.Fatalf("Claude was started again for a waiting message: %q", lines)
	}
}

func TestModelChangeKeepsCurrentEffort(t *testing.T) {
	cfg := &config.Config{Harnesses: map[string]config.Harness{"fake": {
		Models: []config.Model{
			{ID: "a", Efforts: []config.Option{{ID: "low"}, {ID: "high"}}},
			{ID: "b", Efforts: []config.Option{{ID: "medium"}, {ID: "high"}}},
		},
		PermissionModes: []config.Option{{ID: "plan"}},
	}}}
	s, err := New(openStore(t), cfg, time.Minute, map[string]agent.Harness{"fake": {Start: fake.Start}}, MCP{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c1, c2 := connect(t, s), connect(t, s)
	var workspace struct {
		WorkspaceID string `json:"workspace_id"`
	}
	json.Unmarshal(c1.call("workspace/create", map[string]string{"path": t.TempDir()}), &workspace)
	var created struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(c1.call("session/create", map[string]string{
		"workspace_id": workspace.WorkspaceID, "harness": "fake", "model": "a", "effort": "low", "permission_mode": "plan",
	}), &created)
	id := created.SessionID
	settings := func() string {
		var got agent.Settings
		json.Unmarshal(c1.call("session/get", map[string]string{"session_id": id}), &got)
		return got.Model + "/" + got.Effort
	}

	c1.call("session/configure", map[string]string{"session_id": id, "model": "b"})
	if got := settings(); got != "b/medium" {
		t.Fatalf("a model alone left %s, not b/medium", got)
	}
	c2.call("session/configure", map[string]string{"session_id": id, "effort": "high"})
	c1.call("session/configure", map[string]string{"session_id": id, "model": "a"})
	if got := settings(); got != "a/high" {
		t.Fatalf("a model alone after another page's effort change left %s, not a/high", got)
	}
}
