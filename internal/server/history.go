package server

import (
	"encoding/json"
	"log"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/store"
)

// history records a session's conversation as events in the store, returned
// to clients by session/get. It keeps prompts, assistant text, tool calls and
// their results, turn results, and exits, merging consecutive text fragments
// into one event timed by the first. The session's goroutine owns it.
type history struct {
	store     *store.Store
	sessionID string
	pending   *agent.Update // text not yet written
}

func (h *history) record(update agent.Update) {
	if update.Type == "text_delta" {
		if h.pending != nil && h.pending.MessageID == update.MessageID {
			h.pending.Text += update.Text
			return
		}
		h.flush()
		h.pending = &update
		return
	}
	h.flush()
	switch update.Type {
	case "prompt", "tool_use", "tool_result", "result", "exited":
		h.append(update)
	}
}

func (h *history) flush() {
	if h.pending != nil {
		h.append(*h.pending)
		h.pending = nil
	}
}

// append stores the update, its time in the event's own column.
func (h *history) append(update agent.Update) {
	at := update.Time
	update.Time = time.Time{}
	data, err := json.Marshal(update)
	if err == nil {
		err = h.store.AddEvent(h.sessionID, store.Event{CreatedAt: at, Data: data})
	}
	if err != nil {
		log.Printf("record session history: %v", err)
	}
}

// entries returns the recorded updates, including text not yet written.
func (h *history) entries() ([]agent.Update, error) {
	events, err := h.store.Events(h.sessionID)
	if err != nil {
		return nil, err
	}
	updates := make([]agent.Update, 0, len(events)+1)
	for _, e := range events {
		var update agent.Update
		if err := json.Unmarshal(e.Data, &update); err != nil {
			return nil, err
		}
		update.Time = e.CreatedAt
		updates = append(updates, update)
	}
	if h.pending != nil {
		updates = append(updates, *h.pending)
	}
	return updates, nil
}
