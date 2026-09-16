package agent

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkestell/ox/internal/acp"
)

const (
	maxToolOutputTail = 32 * 1024
	outputFlushPeriod = 100 * time.Millisecond
)

type notifyFunc func(acp.SessionNotification) error

type outputState struct {
	tail  []byte
	dirty bool
}

type eventAdapter struct {
	sessionID        string
	root             string
	notify           notifyFunc
	answerMessageID  string
	thoughtMessageID string
	outputs          map[string]*outputState
	ticker           *time.Ticker
}

func newAdapter(sessionID, root string, notify notifyFunc) *eventAdapter {
	return &eventAdapter{
		sessionID: sessionID,
		root:      root,
		notify:    notify,
		outputs:   make(map[string]*outputState),
		ticker:    time.NewTicker(outputFlushPeriod),
	}
}

func (a *eventAdapter) tick() <-chan time.Time {
	return a.ticker.C
}

func (a *eventAdapter) close() {
	a.ticker.Stop()
}

func (a *eventAdapter) handle(current event) error {
	switch current.kind {
	case eventResponseStart:
		a.answerMessageID = current.messageID
		a.thoughtMessageID = current.thoughtID
	case eventText:
		return a.send(acp.AgentMessageChunk{
			SessionUpdate: acp.SessionUpdateAgentMessageChunk,
			Content:       acp.ContentBlock{Type: "text", Text: current.text},
			MessageID:     a.answerMessageID,
		})
	case eventReasoning:
		return a.send(acp.AgentThoughtChunk{
			SessionUpdate: acp.SessionUpdateAgentThoughtChunk,
			Content:       acp.ContentBlock{Type: "text", Text: current.text},
			MessageID:     a.thoughtMessageID,
		})
	case eventToolPending:
		a.outputs[current.call.ID] = &outputState{}
		return a.send(acp.ToolCall{
			SessionUpdate: acp.SessionUpdateToolCall,
			ToolCallID:    current.call.ID,
			Title:         current.presentation.Title(),
			Name:          current.call.Function.Name,
			Kind:          current.toolKind,
			Status:        acp.ToolCallStatusPending,
			Locations:     toolLocations(a.root, current.target),
			RawInput:      json.RawMessage(current.call.Function.Arguments),
			Meta:          toolPresentationMetadata(current.presentation),
		})
	case eventToolStarted:
		return a.send(acp.ToolCallUpdate{
			SessionUpdate: acp.SessionUpdateToolCallUpdate,
			ToolCallID:    current.call.ID,
			Status:        acp.ToolCallStatusInProgress,
		})
	case eventToolOutput:
		state := a.outputs[current.call.ID]
		if state == nil {
			state = &outputState{}
			a.outputs[current.call.ID] = state
		}
		if current.text != "" {
			state.tail = appendOutput(state.tail, current.text)
			state.dirty = true
		}
	case eventToolCompleted, eventToolFailed:
		if current.text != "" {
			state := a.outputs[current.call.ID]
			if state == nil {
				state = &outputState{}
				a.outputs[current.call.ID] = state
			}
			state.tail = appendOutput(state.tail[:0], current.text)
			state.dirty = true
		}
		if err := a.flush(current.call.ID); err != nil {
			return err
		}
		status := acp.ToolCallStatusCompleted
		if current.kind == eventToolFailed {
			status = acp.ToolCallStatusFailed
		}
		if err := a.send(acp.ToolCallUpdate{
			SessionUpdate: acp.SessionUpdateToolCallUpdate,
			ToolCallID:    current.call.ID,
			Status:        status,
		}); err != nil {
			return err
		}
		delete(a.outputs, current.call.ID)
	case eventPlan:
		return a.send(acp.Plan{
			SessionUpdate: acp.SessionUpdatePlan,
			Entries:       clonePlanEntries(current.plan),
		})
	case eventUsage:
		return a.send(usageUpdate(current.contextOccupancy, current.contextWindow, current.totals))
	case eventOutcome:
		return a.send(current.update)
	}
	return nil
}

func toolLocations(root, target string) []acp.ToolCallLocation {
	if target == "" {
		return nil
	}
	return []acp.ToolCallLocation{{
		Path: filepath.Join(root, filepath.FromSlash(target)),
	}}
}

func (a *eventAdapter) flushDirty() error {
	ids := make([]string, 0, len(a.outputs))
	for id, state := range a.outputs {
		if state.dirty {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := a.flush(id); err != nil {
			return err
		}
	}
	return nil
}

func (a *eventAdapter) flush(id string) error {
	state := a.outputs[id]
	if state == nil || !state.dirty {
		return nil
	}
	err := a.send(acp.ToolCallUpdate{
		SessionUpdate: acp.SessionUpdateToolCallUpdate,
		ToolCallID:    id,
		Content: []acp.ToolCallContent{{
			Type:    "content",
			Content: acp.ContentBlock{Type: "text", Text: outputTail(string(state.tail))},
		}},
	})
	if err != nil {
		return err
	}
	state.dirty = false
	return nil
}

func (a *eventAdapter) send(update any) error {
	return a.notify(acp.SessionNotification{
		SessionID: a.sessionID,
		Update:    update,
	})
}

// outputTail keeps the last maxToolOutputTail bytes of value as valid UTF-8.
// The byte cut can split a rune, so the continuation bytes it leaves at the
// front are dropped. Anything else invalid is replaced in one pass: output
// before a bad byte is still output worth showing.
func outputTail(value string) string {
	if len(value) > maxToolOutputTail {
		value = trimPartialRune(value[len(value)-maxToolOutputTail:])
	}
	if utf8.ValidString(value) {
		return value
	}
	return strings.ToValidUTF8(value, "\uFFFD")
}

func trimPartialRune(value string) string {
	for index := 0; index < len(value) && index < utf8.UTFMax; index++ {
		if utf8.RuneStart(value[index]) {
			return value[index:]
		}
	}
	return value
}

// appendOutput accumulates output while keeping at least the last
// maxToolOutputTail bytes. The buffer may double before it is compacted, so
// copying is amortized over a bound's worth of new output instead of being paid
// for every chunk. A flush bounds it exactly.
func appendOutput(tail []byte, chunk string) []byte {
	tail = append(tail, chunk...)
	if len(tail) > 2*maxToolOutputTail {
		tail = append(tail[:0], tail[len(tail)-maxToolOutputTail:]...)
	}
	return tail
}
