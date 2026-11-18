package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/claude"
)

type recorder []Update

func (r *recorder) Publish(update Update) bool {
	*r = append(*r, update)
	return true
}

func testAgent() (*Agent, *bytes.Buffer) {
	input := new(bytes.Buffer)
	return newAgent(claude.NewInput(input)), input
}

// promptingAgent returns an agent running prompt "prompt" with one subscriber.
func promptingAgent() (*Agent, *bytes.Buffer, *recorder) {
	a, input := testAgent()
	updates := new(recorder)
	a.phase, a.claudeState = prompting, "running"
	a.prompt = &submission{id: "prompt", approvals: make(map[string]json.RawMessage)}
	a.subscribers[updates] = struct{}{}
	return a, input, updates
}

func TestPromptReleasedAfterClaudeTakesIt(t *testing.T) {
	a, _ := testAgent()
	updates := new(recorder)
	a.subscribers[updates] = struct{}{}
	id, err := a.submit("hello")
	if err != nil {
		t.Fatal(err)
	}

	if len(*updates) != 1 || (*updates)[0].Type != "prompt" || (*updates)[0].Text != "hello" || (*updates)[0].MessageID != id {
		t.Fatalf("prompt was not published: %+v", *updates)
	}
	*updates = nil

	a.sessionState("idle")
	if _, err := a.submit("again"); !errors.Is(err, ErrBusy) || len(*updates) != 0 {
		t.Fatalf("idle before Claude took the prompt released it: %v, %+v", err, *updates)
	}

	a.commandStarted(id)
	a.sessionState("running")
	a.sessionState("idle")
	if len(*updates) != 2 || (*updates)[0].State != "running" || (*updates)[1].State != "idle" || (*updates)[1].MessageID != id {
		t.Fatalf("unexpected prompt updates: %+v", *updates)
	}
	if !a.idle() || a.prompt != nil {
		t.Fatal("idle after Claude took the prompt did not release it")
	}
}

func TestInspectReturnsRunningPrompt(t *testing.T) {
	a, _, _ := promptingAgent()
	request := json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Bash"}`)
	a.controlRequest("pending", request)

	var snapshot Snapshot
	a.handle(inspectRequest{func(s Snapshot) { snapshot = s }, make(chan struct{}, 1)})
	if snapshot.State != "running" || snapshot.Prompt == nil || snapshot.Prompt.MessageID != "prompt" ||
		len(snapshot.Prompt.Approvals) != 1 || snapshot.Prompt.Approvals[0].RequestID != "pending" ||
		!bytes.Equal(snapshot.Prompt.Approvals[0].Request, request) {
		t.Fatalf("snapshot missing the running prompt: %+v", snapshot)
	}
}

func TestTurnsClaudeStartsArePublished(t *testing.T) {
	a, _ := testAgent()
	updates := new(recorder)
	a.subscribers[updates] = struct{}{}
	a.sessionState("running")
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"tick"}}}`)
	output(t, a, `{"type":"result","subtype":"success","result":"tick"}`)
	output(t, a, `{"type":"result","subtype":"success","result":""}`)
	a.sessionState("idle")
	a.exit(nil)
	types := []string{}
	for _, u := range *updates {
		if u.MessageID != "" {
			t.Fatalf("self-started turn update has a message ID: %+v", u)
		}
		types = append(types, u.Type)
	}
	if strings.Join(types, ",") != "state,text_delta,result,state,parked" {
		t.Fatalf("unexpected updates: %v", types)
	}
}
