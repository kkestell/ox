package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
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

	c.call("session/send", map[string]string{"session_id": id, "text": "hi"})
	if busy := c.request("session/send", map[string]string{"session_id": id, "text": "again"}); busy.Error == nil || busy.Error.Code != sessionBusy {
		t.Fatalf("a running session took a second prompt: %+v", busy.Error)
	}
	for c.update(id, "state").State != "idle" {
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
	if running.State != "idle" || running.Effort != "high" || len(running.History) != 3 || running.History[1].Text != "You said: hi" {
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
