package server

import (
	"encoding/json"
	"log"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/store"
)

// history is a session's recorded updates. The store is its only copy, so
// every recorded update survives oxd being killed.
type history struct {
	store     *store.Store
	sessionID string
}

func (h *history) record(update agent.Update) {
	switch update.Type {
	case agent.UpdatePrompt, agent.UpdateTextDelta, agent.UpdateToolUse, agent.UpdateToolResult, agent.UpdateResult, agent.UpdateExited, agent.UpdateParked:
		h.append(update)
	}
}

// recordEnd records that the session's last process has ended when the
// history does not say so, as after oxd itself exited without stopping it.
func (h *history) recordEnd() {
	last, err := h.store.LastEvent(h.sessionID)
	var update agent.Update
	if err == nil && last != nil {
		if err = json.Unmarshal(last, &update); err == nil && update.Type != agent.UpdateParked {
			h.append(agent.Update{Type: agent.UpdateParked, Time: time.Now()})
		}
	}
	if err != nil {
		log.Printf("record session %s history: %v", h.sessionID, err)
	}
}

func (h *history) append(update agent.Update) {
	at := update.Time
	update.Time = time.Time{}
	data, err := json.Marshal(update)
	if err == nil {
		err = h.store.AddEvent(h.sessionID, store.Event{CreatedAt: at, Data: data})
	}
	if err != nil {
		log.Printf("record session %s history: %v", h.sessionID, err)
	}
}

// entries returns the recorded updates, with consecutive text_delta
// fragments of one message joined into one timed by its first fragment, as
// the page joins them live.
func (h *history) entries() ([]agent.Update, error) {
	events, err := h.store.Events(h.sessionID)
	if err != nil {
		return nil, err
	}
	updates := make([]agent.Update, 0, len(events))
	for _, e := range events {
		var update agent.Update
		if err := json.Unmarshal(e.Data, &update); err != nil {
			return nil, err
		}
		update.Time = e.CreatedAt
		if n := len(updates); n > 0 && update.Type == agent.UpdateTextDelta {
			if last := &updates[n-1]; last.Type == agent.UpdateTextDelta && last.MessageID == update.MessageID {
				last.Text += update.Text
				continue
			}
		}
		updates = append(updates, update)
	}
	return updates, nil
}
