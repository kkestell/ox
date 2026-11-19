package server

import (
	"encoding/json"
	"log"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/store"
)

type history struct {
	store     *store.Store
	sessionID string
	pending   *agent.Update
}

func (h *history) record(update agent.Update) {
	if update.Type == agent.UpdateTextDelta {
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
	case agent.UpdatePrompt, agent.UpdateToolUse, agent.UpdateToolResult, agent.UpdateResult, agent.UpdateExited, agent.UpdateParked:
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

func (h *history) flush() {
	if h.pending != nil {
		h.append(*h.pending)
		h.pending = nil
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
