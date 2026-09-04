package agent

import (
	"encoding/json"
	"path/filepath"
	"sort"
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
	tail   string
	dirty  bool
	parent string
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
			SessionUpdate: "agent_message_chunk",
			Content:       acp.ContentBlock{Type: "text", Text: current.text},
			MessageID:     a.answerMessageID,
		})
	case eventReasoning:
		return a.send(acp.AgentThoughtChunk{
			SessionUpdate: "agent_thought_chunk",
			Content:       acp.ContentBlock{Type: "text", Text: current.text},
			MessageID:     a.thoughtMessageID,
		})
	case eventToolPending:
		a.outputs[current.call.ID] = &outputState{parent: current.parent}
		meta := toolEventMetadata(current.parent, current.delegates)
		return a.send(acp.ToolCall{
			SessionUpdate: "tool_call",
			ToolCallID:    current.call.ID,
			Title:         current.title,
			Name:          current.call.Function.Name,
			Kind:          current.toolKind,
			Status:        acp.ToolCallStatusPending,
			Locations:     toolLocations(a.root, current.target),
			RawInput:      json.RawMessage(current.call.Function.Arguments),
			Meta:          meta,
		})
	case eventToolStarted:
		return a.send(acp.ToolCallUpdate{
			SessionUpdate: "tool_call_update",
			ToolCallID:    current.call.ID,
			Status:        acp.ToolCallStatusInProgress,
			Meta:          toolEventMetadata(current.parent, false),
		})
	case eventToolOutput:
		state := a.outputs[current.call.ID]
		if state == nil {
			state = &outputState{parent: current.parent}
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
				state = &outputState{parent: current.parent}
				a.outputs[current.call.ID] = state
			}
			state.tail = outputTail(current.text)
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
			SessionUpdate: "tool_call_update",
			ToolCallID:    current.call.ID,
			Status:        status,
			Meta:          toolEventMetadata(current.parent, current.delegates),
		}); err != nil {
			return err
		}
		delete(a.outputs, current.call.ID)
	case eventUsage:
		return a.send(acp.UsageUpdate{
			SessionUpdate: "usage_update",
			Used:          uint64(current.usage.TotalTokens),
			Size:          uint64(current.context),
			Cost: &acp.Cost{
				Amount:   current.totalCost,
				Currency: "USD",
			},
		})
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
		SessionUpdate: "tool_call_update",
		ToolCallID:    id,
		Content: []acp.ToolCallContent{{
			Type:    "content",
			Content: acp.ContentBlock{Type: "text", Text: state.tail},
		}},
		Meta: toolEventMetadata(state.parent, false),
	})
	if err != nil {
		return err
	}
	state.dirty = false
	return nil
}

func toolEventMetadata(parent string, delegates bool) acp.Metadata {
	if parent == "" && !delegates {
		return nil
	}
	meta := make(acp.Metadata, 2)
	if parent != "" {
		meta[acp.MetaParentToolCallID] = parent
	}
	if delegates {
		meta[acp.MetaSubagent] = true
	}
	return meta
}

func (a *eventAdapter) send(update any) error {
	return a.notify(acp.SessionNotification{
		SessionID: a.sessionID,
		Update:    update,
	})
}

func outputTail(value string) string {
	if len(value) <= maxToolOutputTail {
		return value
	}
	value = value[len(value)-maxToolOutputTail:]
	for !utf8.ValidString(value) {
		value = value[1:]
	}
	return value
}

func appendOutput(tail, chunk string) string {
	if len(chunk) >= maxToolOutputTail {
		return outputTail(chunk)
	}
	return outputTail(tail + chunk)
}
