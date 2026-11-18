package agent

import (
	"bytes"
	"encoding/json"
	"errors"
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
	id, err := a.submit(updates, "hello")
	if err != nil {
		t.Fatal(err)
	}

	a.sessionState("idle")
	if _, err := a.submit(updates, "again"); !errors.Is(err, ErrBusy) || len(*updates) != 0 {
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

func TestAttachReturnsStateBeforeUpdates(t *testing.T) {
	a, _, _ := promptingAgent()
	request := json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Bash"}`)
	a.controlRequest("pending", request)

	late := new(recorder)
	var snapshot Snapshot
	a.handle(attachRequest{late, func(s Snapshot) {
		snapshot = s
		if len(*late) != 0 {
			t.Fatal("update delivered before the snapshot")
		}
	}, make(chan struct{}, 1)})
	if snapshot.State != "running" || snapshot.Prompt == nil || snapshot.Prompt.MessageID != "prompt" ||
		len(snapshot.Prompt.Approvals) != 1 || snapshot.Prompt.Approvals[0].RequestID != "pending" ||
		!bytes.Equal(snapshot.Prompt.Approvals[0].Request, request) {
		t.Fatalf("snapshot missing the running prompt: %+v", snapshot)
	}

	a.sessionState("idle")
	if len(*late) != 2 || (*late)[0].Type != "approval_cancelled" || (*late)[1].State != "idle" {
		t.Fatalf("attached subscriber missed updates: %+v", *late)
	}
}
