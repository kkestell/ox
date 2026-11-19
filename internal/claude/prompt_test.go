package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

type recorder []agent.Update

func (r *recorder) record(update agent.Update) {
	*r = append(*r, update)
}

// agentWriting returns an agent that writes to input and runs proc, whose
// exit arrives on events.
func agentWriting(input io.Writer, events chan agent.Event, proc *process) (*Agent, *recorder) {
	updates := new(recorder)
	options := agent.Options{OnUpdate: updates.record, OnConfigured: func(agent.Settings) {}, OnError: func(err error) { panic(err) }}
	return newAgent(newWriter(input), events, proc, "claude-session", options), updates
}

func testAgent() (*Agent, *bytes.Buffer, *recorder) {
	input := new(bytes.Buffer)
	a, updates := agentWriting(input, make(chan agent.Event), nil)
	return a, input, updates
}

// stoppableAgent is a testAgent whose process waits to be stopped and
// delivers its exit as Claude's does.
func stoppableAgent(t *testing.T) (*Agent, *bytes.Buffer, *recorder) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return agentRunning(t, cmd)
}

// agentRunning is a testAgent whose process is the started cmd, delivering
// its exit as Claude's does.
func agentRunning(t *testing.T, cmd *exec.Cmd) (*Agent, *bytes.Buffer, *recorder) {
	t.Cleanup(func() { cmd.Process.Kill() })
	events := make(chan agent.Event, 1)
	go func() { events <- processExit{cmd.Wait()} }()
	input := new(bytes.Buffer)
	a, updates := agentWriting(input, events, &process{cmd: cmd})
	return a, input, updates
}

// promptingAgent returns an agent running a prompt Claude has taken, with
// its input and updates so far cleared.
func promptingAgent(t *testing.T) (*Agent, *bytes.Buffer, *recorder, string) {
	a, input, updates := testAgent()
	id, err := a.Send("hello")
	if err != nil {
		t.Fatal(err)
	}
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+id+`","state":"started"}`)
	output(t, a, `{"type":"system","subtype":"session_state_changed","state":"running"}`)
	input.Reset()
	*updates = nil
	return a, input, updates, id
}

func output(t *testing.T, a *Agent, line string) {
	t.Helper()
	var m message
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatal(err)
	}
	a.Handle(m)
}

func state(t *testing.T, a *Agent, state string) {
	t.Helper()
	output(t, a, `{"type":"system","subtype":"session_state_changed","state":"`+state+`"}`)
}

func TestPromptReleasedAfterClaudeTakesIt(t *testing.T) {
	a, input, updates := testAgent()
	id, err := a.Send("hello")
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(input.Bytes(), &wire); err != nil || wire["uuid"] != id || wire["priority"] != "next" {
		t.Fatalf("prompt not written for steering under its ID: %s", input)
	}

	if len(*updates) != 2 || (*updates)[0].Type != "prompt" || (*updates)[0].Text != "hello" || (*updates)[0].MessageID != id ||
		(*updates)[1].State != "running" || (*updates)[1].MessageID != id || a.Snapshot().State != "running" {
		t.Fatalf("a written prompt was not published as running: %+v", *updates)
	}
	*updates = nil

	state(t, a, "idle")
	if _, err := a.Send("again"); !errors.Is(err, agent.ErrBusy) || len(*updates) != 0 || a.Snapshot().State != "running" {
		t.Fatalf("idle before Claude took the prompt released it: %v, %+v", err, *updates)
	}
	input.Reset()
	if err := a.Interrupt(); err != nil || !strings.Contains(input.String(), `"subtype":"interrupt"`) {
		t.Fatalf("an interrupt before Claude took the prompt was not sent: %v, %q", err, input.String())
	}

	// A turn Claude starts itself while the prompt waits is not the prompt's.
	output(t, a, `{"type":"command_lifecycle","command_uuid":"11111111-1111-4111-8111-111111111111","state":"started"}`)
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"tick"}}}`)
	state(t, a, "idle")
	if _, err := a.Send("again"); !errors.Is(err, agent.ErrBusy) || len(*updates) != 1 || (*updates)[0].MessageID != "" {
		t.Fatalf("Claude's own turn took the waiting prompt's identity: %v, %+v", err, *updates)
	}
	*updates = nil

	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+id+`","state":"started"}`)
	state(t, a, "running")
	state(t, a, "idle")
	if len(*updates) != 1 || (*updates)[0].State != "idle" || (*updates)[0].MessageID != id {
		t.Fatalf("unexpected prompt updates: %+v", *updates)
	}
	if _, err := a.Send("next"); err != nil {
		t.Fatalf("the released agent refused a prompt: %v", err)
	}
}

func TestSnapshotReturnsStateAndApprovals(t *testing.T) {
	a, _, _, _ := promptingAgent(t)
	request := `{"subtype":"can_use_tool","tool_name":"Bash"}`
	output(t, a, `{"type":"control_request","request_id":"pending","request":`+request+`}`)

	snapshot := a.Snapshot()
	if snapshot.State != "running" || len(snapshot.Approvals) != 1 || snapshot.Approvals[0].RequestID != "pending" ||
		string(snapshot.Approvals[0].Request) != request {
		t.Fatalf("snapshot missing the running state or approval: %+v", snapshot)
	}
}

func TestTurnsClaudeStartsArePublished(t *testing.T) {
	a, _, updates := testAgent()
	state(t, a, "running")
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"tick"}}}`)
	output(t, a, `{"type":"result","subtype":"success","result":"tick"}`)
	output(t, a, `{"type":"result","subtype":"success","result":""}`)
	state(t, a, "idle")
	a.Handle(processExit{})
	types := []string{}
	for _, u := range *updates {
		if u.MessageID != "" {
			t.Fatalf("self-started turn update has a message ID: %+v", u)
		}
		types = append(types, u.Type)
	}
	if strings.Join(types, ",") != "state,text_delta,result,state,exited" {
		t.Fatalf("unexpected updates: %v", types)
	}
}

func TestTopLevelToolCallsArePublished(t *testing.T) {
	a, _, updates, id := promptingAgent(t)
	output(t, a, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`)
	output(t, a, `{"type":"assistant","parent_tool_use_id":"t0","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{}}]}}`)
	output(t, a, `{"type":"user","parent_tool_use_id":"t0","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"sub"}]}}`)
	output(t, a, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]}]}}`)
	if len(*updates) != 2 {
		t.Fatalf("expected the top-level call and result only: %+v", *updates)
	}
	use, result := (*updates)[0], (*updates)[1]
	if use.Type != "tool_use" || use.ToolUseID != "t1" || use.ToolName != "Bash" || string(use.Input) != `{"command":"ls"}` || use.MessageID != id {
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
	a.Handle(processExit{errors.New("exit status 1")})
	if !a.Exited() || len(*updates) != 1 || (*updates)[0].Type != "exited" || !strings.Contains((*updates)[0].Error, "exit status 1") {
		t.Fatalf("an unexpected exit looked like parking: %+v", *updates)
	}

	a, _, updates = stoppableAgent(t)
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	if !a.Exited() || len(*updates) != 0 {
		t.Fatalf("a requested stop was reported as a failure: %+v", *updates)
	}
}

// Stop finishes even when Claude ignores SIGTERM: Ox kills it.
func TestStopKillsClaudeIgnoringSIGTERM(t *testing.T) {
	saved := killAfter
	killAfter = 100 * time.Millisecond
	t.Cleanup(func() { killAfter = saved })
	cmd := exec.Command("sh", "-c", "trap '' TERM; echo ready; exec sleep 60")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	a, _, _ := agentRunning(t, cmd)
	stopped := make(chan error, 1)
	go func() { stopped <- a.Stop() }()
	select {
	case err := <-stopped:
		if err != nil || !a.Exited() {
			t.Fatalf("stop failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop waited for a Claude that ignores SIGTERM")
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
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+id+`","state":"started"}`)
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
	state(t, a, "idle")
	if len(*updates) != 3 || (*updates)[0].MessageID != "" || (*updates)[1].MessageID != "" ||
		(*updates)[2].State != "idle" || (*updates)[2].MessageID != id {
		t.Fatalf("Claude's own turn was attributed to the prompt: %+v", *updates)
	}
}
