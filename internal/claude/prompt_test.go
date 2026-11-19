package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/agent"
)

type recorder []agent.Update

func (r *recorder) record(update agent.Update) {
	*r = append(*r, update)
}

func testAgent() (*Agent, *bytes.Buffer, *recorder) {
	input := new(bytes.Buffer)
	updates := new(recorder)
	options := agent.Options{OnUpdate: updates.record, OnError: func(err error) { panic(err) }}
	return newAgent(newWriter(input), make(chan agent.Event), "claude-session", options), input, updates
}

func promptingAgent() (*Agent, *bytes.Buffer, *recorder) {
	a, input, updates := testAgent()
	a.phase, a.state = prompting, "running"
	a.prompt, a.turn = &submission{id: "prompt"}, "prompt"
	return a, input, updates
}

func output(t *testing.T, a *Agent, line string) {
	t.Helper()
	var m message
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatal(err)
	}
	a.Handle(m)
}

func TestPromptReleasedAfterClaudeTakesIt(t *testing.T) {
	a, input, updates := testAgent()
	id, err := a.Send("hello")
	if err != nil {
		t.Fatal(err)
	}

	if len(*updates) != 2 || (*updates)[0].Type != "prompt" || (*updates)[0].Text != "hello" || (*updates)[0].MessageID != id ||
		(*updates)[1].State != "running" || (*updates)[1].MessageID != id || a.Snapshot().State != "running" {
		t.Fatalf("a written prompt was not published as running: %+v", *updates)
	}
	*updates = nil

	a.sessionState("idle")
	if _, err := a.Send("again"); !errors.Is(err, agent.ErrBusy) || len(*updates) != 0 || a.Snapshot().State != "running" {
		t.Fatalf("idle before Claude took the prompt released it: %v, %+v", err, *updates)
	}
	input.Reset()
	if err := a.Interrupt(); err != nil || !strings.Contains(input.String(), `"subtype":"interrupt"`) {
		t.Fatalf("an interrupt before Claude took the prompt was not sent: %v, %q", err, input.String())
	}

	a.commandStarted(id)
	a.sessionState("running")
	a.sessionState("idle")
	if len(*updates) != 1 || (*updates)[0].State != "idle" || (*updates)[0].MessageID != id {
		t.Fatalf("unexpected prompt updates: %+v", *updates)
	}
	if !a.idle() || a.prompt != nil {
		t.Fatal("idle after Claude took the prompt did not release it")
	}
}

func TestSnapshotReturnsRunningPrompt(t *testing.T) {
	a, _, _ := promptingAgent()
	request := json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Bash"}`)
	a.controlRequest("pending", request)

	snapshot := a.Snapshot()
	if snapshot.State != "running" || snapshot.Prompt == nil || snapshot.Prompt.MessageID != "prompt" ||
		len(snapshot.Approvals) != 1 || snapshot.Approvals[0].RequestID != "pending" ||
		!bytes.Equal(snapshot.Approvals[0].Request, request) {
		t.Fatalf("snapshot missing the running prompt: %+v", snapshot)
	}
}

func TestTurnsClaudeStartsArePublished(t *testing.T) {
	a, _, updates := testAgent()
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
	if strings.Join(types, ",") != "state,text_delta,result,state,exited,parked" {
		t.Fatalf("unexpected updates: %v", types)
	}
}

func TestTopLevelToolCallsArePublished(t *testing.T) {
	a, _, updates := promptingAgent()
	output(t, a, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`)
	output(t, a, `{"type":"assistant","parent_tool_use_id":"t0","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{}}]}}`)
	output(t, a, `{"type":"user","parent_tool_use_id":"t0","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"sub"}]}}`)
	output(t, a, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]}]}}`)
	if len(*updates) != 2 {
		t.Fatalf("expected the top-level call and result only: %+v", *updates)
	}
	use, result := (*updates)[0], (*updates)[1]
	if use.Type != "tool_use" || use.ToolUseID != "t1" || use.ToolName != "Bash" || string(use.Input) != `{"command":"ls"}` || use.MessageID != "prompt" {
		t.Fatalf("unexpected tool_use: %+v", use)
	}
	if result.Type != "tool_result" || result.ToolUseID != "t1" || result.Result.Text != "a\nb" || !result.Result.IsError {
		t.Fatalf("unexpected tool_result: %+v", result)
	}
}

// Claude exits on its own only on failure, which clients see even when no
// prompt is active; an exit Ox asked for is plain parking.
func TestUnexpectedExitIsReported(t *testing.T) {
	a, _, updates := testAgent()
	a.exit(errors.New("exit status 1"))
	if len(*updates) != 2 || (*updates)[0].Type != "exited" || !strings.Contains((*updates)[0].Error, "exit status 1") ||
		(*updates)[1].Type != "parked" {
		t.Fatalf("an unexpected exit looked like parking: %+v", *updates)
	}

	a, _, updates = testAgent()
	a.stopping = true
	a.exit(errors.New("signal: terminated"))
	if len(*updates) != 1 || (*updates)[0].Type != "parked" {
		t.Fatalf("a requested stop was reported as a failure: %+v", *updates)
	}
}

// A background subagent's findings arrive in a turn Claude starts after the
// prompt's result. That turn is not the prompt's, though the prompt holds the
// session until Claude is idle.
func TestTurnAfterPromptResultIsClaudes(t *testing.T) {
	a, _, updates := testAgent()
	id, err := a.Send("start a background agent")
	if err != nil {
		t.Fatal(err)
	}
	a.commandStarted(id)
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"launched"}}}`)
	output(t, a, `{"type":"result","subtype":"success","result":"launched"}`)
	for _, u := range *updates {
		if u.MessageID != id {
			t.Fatalf("the prompt's turn was not attributed to it: %+v", u)
		}
	}
	*updates = nil
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"found it"}}}`)
	output(t, a, `{"type":"result","subtype":"success","result":"found it"}`)
	output(t, a, `{"type":"result","subtype":"success","result":""}`)
	if _, err := a.Send("again"); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("the prompt released the session before Claude was idle: %v", err)
	}
	a.sessionState("idle")
	if len(*updates) != 3 || (*updates)[0].MessageID != "" || (*updates)[1].MessageID != "" ||
		(*updates)[2].State != "idle" || (*updates)[2].MessageID != id {
		t.Fatalf("Claude's own turn was attributed to the prompt: %+v", *updates)
	}
}
