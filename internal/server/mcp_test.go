package server

import (
	"encoding/json"
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

func TestListSessionsCapturesLabelWithOwnerRead(t *testing.T) {
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
	for _, id := range []string{first, last} {
		c.call("session/send", map[string]string{"session_id": id, "text": "label for " + id})
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
	}
	if response.Error != nil || json.Unmarshal(response.Result, &result) != nil || result.IsError || len(result.Content) != 1 ||
		json.Unmarshal([]byte(result.Content[0].Text), &sessions) != nil || len(sessions) != 2 ||
		sessions[0].SessionID != first || sessions[0].Label != "label for "+first {
		t.Fatalf("listing lost the label captured before deletion: %+v (%s)", sessions, response.Result)
	}
}
