package server

import (
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/store"
)

func TestHistoryMergesTextAndKeepsConversation(t *testing.T) {
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: "/w", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{ID: "s", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	h := &history{store: st, sessionID: "s"}
	replyStarted := time.Date(2026, 11, 18, 12, 0, 1, 0, time.UTC)
	for _, u := range []agent.Update{
		{MessageID: "p", Type: "prompt", Text: "hi"},
		{MessageID: "p", Type: "state", State: "running"},
		{MessageID: "p", Type: "reasoning_delta", Text: "Greet "},
		{MessageID: "p", Type: "reasoning_delta", Text: "them."},
		{MessageID: "p", Type: "text_delta", Text: "hel", Time: replyStarted},
		{MessageID: "p", Type: "state", State: "requires_action"},
		{MessageID: "p", Type: "text_delta", Text: "lo", Time: replyStarted.Add(time.Second)},
	} {
		h.record(u)
	}
	// A fresh history reads only what reached the store, as after oxd is
	// killed mid-reply.
	entries, err := (&history{store: st, sessionID: "s"}).entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[1].Text != "Greet them." || entries[2].Text != "hello" || !entries[2].Time.Equal(replyStarted) {
		t.Fatalf("history mid-reply lacks the reply text so far, timed by its first fragment: %+v", entries)
	}

	resets := time.Date(2026, 11, 18, 17, 0, 0, 0, time.UTC)
	h.record(agent.Update{MessageID: "p", Type: "usage", Usage: []agent.UsageWindow{{IntervalSeconds: 18000, UsedPercent: 12.5, ResetsAt: resets}, {IntervalSeconds: 604800, UsedPercent: 40, ResetsAt: resets.Add(72 * time.Hour)}}})
	h.record(agent.Update{MessageID: "p", Type: "result", Result: &agent.Result{Text: "hello"}})
	h.record(agent.Update{MessageID: "p", Type: "state", State: "idle"})
	h.record(agent.Update{Type: "text_delta", Text: "tick"})
	h.record(agent.Update{MessageID: "q", Type: "text_delta", Text: "tock"})
	h.record(agent.Update{MessageID: "q", Type: "reasoning_delta", Text: "hm"})
	h.record(agent.Update{Type: "parked"})
	// A fresh history reads only what reached the store, as after a restart.
	if entries, err = (&history{store: st, sessionID: "s"}).entries(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Type+":"+e.Text)
	}
	if strings.Join(got, ",") != "prompt:hi,reasoning_delta:Greet them.,text_delta:hello,usage:,result:,text_delta:tick,text_delta:tock,reasoning_delta:hm,parked:" {
		t.Fatalf("unexpected history: %v", got)
	}
	if !entries[2].Time.Equal(replyStarted) {
		t.Fatalf("merged text is not timed by its first fragment: %v", entries[1].Time)
	}
	if u := entries[3].Usage; len(u) != 2 || u[0].IntervalSeconds != 18000 || u[0].UsedPercent != 12.5 || !u[0].ResetsAt.Equal(resets) ||
		u[1].IntervalSeconds != 604800 || u[1].UsedPercent != 40 || !u[1].ResetsAt.Equal(resets.Add(72*time.Hour)) {
		t.Fatalf("usage windows did not survive the restart: %+v", u)
	}
}

// A process whose end the history does not record, as when oxd itself was
// killed, is recorded as ended once, when the session next loads.
func TestUnrecordedEndIsRecordedAtLoad(t *testing.T) {
	st := openStore(t)
	if err := st.AddWorkspace(&store.Workspace{ID: "w", Path: t.TempDir(), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("w", &store.Session{ID: "s", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	h := &history{store: st, sessionID: "s"}
	h.record(agent.Update{MessageID: "p", Type: "prompt", Text: "hi"})
	h.record(agent.Update{MessageID: "p", Type: "tool_use", ToolUseID: "t", ToolName: "Bash"})
	for range 2 {
		s, err := newServer(st, &config.Config{}, 0, nil, MCP{})
		if err != nil {
			t.Fatal(err)
		}
		entries, err := h.entries()
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Type)
		}
		if strings.Join(got, ",") != "prompt,tool_use,parked" {
			t.Fatalf("unexpected history: %v", got)
		}
		s.Close()
	}
}
