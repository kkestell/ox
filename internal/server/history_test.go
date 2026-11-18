package server

import (
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
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
		{MessageID: "p", Type: "text_delta", Text: "hel", Time: replyStarted},
		{MessageID: "p", Type: "text_delta", Text: "lo", Time: replyStarted.Add(time.Second)},
	} {
		h.Publish(u)
	}
	entries, err := h.entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].Text != "hello" {
		t.Fatalf("history mid-reply lacks the pending text: %+v", entries)
	}

	h.Publish(agent.Update{MessageID: "p", Type: "result", Result: &agent.Result{Text: "hello"}})
	h.Publish(agent.Update{MessageID: "p", Type: "state", State: "idle"})
	h.Publish(agent.Update{Type: "text_delta", Text: "tick"})
	h.Publish(agent.Update{Type: "parked"})
	if entries, err = h.entries(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Type+":"+e.Text)
	}
	if len(got) != 4 || got[0] != "prompt:hi" || got[1] != "text_delta:hello" || got[2] != "result:" || got[3] != "text_delta:tick" {
		t.Fatalf("unexpected history: %v", got)
	}
	if !entries[1].Time.Equal(replyStarted) {
		t.Fatalf("merged text is not timed by its first fragment: %v", entries[1].Time)
	}
}
