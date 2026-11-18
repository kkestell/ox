package server

import (
	"path/filepath"
	"testing"

	"github.com/kkestell/ox/internal/agent"
)

func TestHistoryMergesTextAndKeepsConversation(t *testing.T) {
	h := &history{path: filepath.Join(t.TempDir(), "sessions", "s.jsonl")}
	for _, u := range []agent.Update{
		{MessageID: "p", Type: "prompt", Text: "hi"},
		{MessageID: "p", Type: "state", State: "running"},
		{MessageID: "p", Type: "text_delta", Text: "hel"},
		{MessageID: "p", Type: "text_delta", Text: "lo"},
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
}
