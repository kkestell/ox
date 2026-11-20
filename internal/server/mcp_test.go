package server

import (
	"encoding/json"
	"fmt"
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

func TestReadSession(t *testing.T) {
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	const caller, short, long, multibyte, tight = "caller-session", "short-session", "long-session", "multibyte-session", "tight-session"
	for _, id := range []string{caller, short, long, multibyte, tight} {
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
	if got, refused := read(map[string]any{"to": 5}); !refused || got != "invalid arguments" {
		t.Errorf("undecodable arguments returned %q, %v", got, refused)
	}
	for _, cursor := range []int{0, 8} {
		if got, refused := read(map[string]any{"to": long, "cursor": cursor}); !refused || got != "cursor out of range" {
			t.Errorf("cursor %d returned %q, %v", cursor, got, refused)
		}
	}
}
