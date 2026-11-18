package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/kkestell/ox/internal/agent"
)

// history records a session's conversation as updates in a JSON Lines file,
// returned to clients by session/get. It keeps prompts, assistant text,
// results, and exits, merging consecutive text fragments into one entry. It
// subscribes to each of the session's processes, so the running agent's
// goroutine uses it; while the session is parked, its lock holder does.
type history struct {
	path    string
	pending *agent.Update // text not yet written
}

func (h *history) Publish(update agent.Update) bool {
	if update.Type == "text_delta" {
		if h.pending != nil && h.pending.MessageID == update.MessageID {
			h.pending.Text += update.Text
			return true
		}
		h.flush()
		h.pending = &update
		return true
	}
	h.flush()
	switch update.Type {
	case "prompt", "result", "exited":
		h.append(update)
	}
	return true
}

func (h *history) flush() {
	if h.pending != nil {
		h.append(*h.pending)
		h.pending = nil
	}
}

func (h *history) append(update agent.Update) {
	err := os.MkdirAll(filepath.Dir(h.path), 0o700)
	if err == nil {
		var f *os.File
		if f, err = os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			err = json.NewEncoder(f).Encode(update)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		log.Printf("record session history: %v", err)
	}
}

// entries returns the recorded updates, including text not yet written.
func (h *history) entries() ([]agent.Update, error) {
	updates := []agent.Update{}
	f, err := os.Open(h.path)
	if errors.Is(err, fs.ErrNotExist) {
		return h.withPending(updates), nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	lines.Buffer(nil, 64<<20)
	for lines.Scan() {
		var update agent.Update
		if err := json.Unmarshal(lines.Bytes(), &update); err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}
	if err := lines.Err(); err != nil {
		return nil, err
	}
	return h.withPending(updates), nil
}

func (h *history) withPending(updates []agent.Update) []agent.Update {
	if h.pending != nil {
		updates = append(updates, *h.pending)
	}
	return updates
}
