package claude

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/children"
	"github.com/kkestell/ox/internal/process"
)

type recorder []agent.Update

func (r *recorder) record(update agent.Update) {
	*r = append(*r, update)
}

// stopReply is an interrupt's answer: whether it came, its error, and how
// many updates were recorded when it did.
type stopReply struct {
	answered bool
	err      error
	after    int
}

func interrupt(a *Agent, updates *recorder) *stopReply {
	r := &stopReply{}
	a.Interrupt(func(err error) { r.answered, r.err, r.after = true, err, len(*updates) })
	return r
}

// startingAgent returns an agent that writes to input and runs proc, whose
// exit arrives on events, before Claude has answered initialize.
func startingAgent(input io.Writer, events chan agent.Event, proc *process.Process) (*Agent, *recorder) {
	updates := new(recorder)
	options := agent.Options{OnUpdate: updates.record, OnConfigured: func(agent.Settings) {}, OnError: func(err error) { panic(err) }}
	return newAgent(newWriter(input), events, make(chan struct{}), proc, options), updates
}

// agentWriting is a startingAgent that Claude has answered.
func agentWriting(input io.Writer, events chan agent.Event, proc *process.Process) (*Agent, *recorder) {
	a, updates := startingAgent(input, events, proc)
	a.Handle(initialized)
	return a, updates
}

// initialized is Claude's answer to initialize.
var initialized = message{Type: "control_response", Response: controlReply{Subtype: "success", RequestID: "initialize"}}

func testAgent() (*Agent, *bytes.Buffer, *recorder) {
	input := new(bytes.Buffer)
	a, updates := agentWriting(input, make(chan agent.Event), nil)
	return a, input, updates
}

// stoppableAgent is a testAgent whose process waits to be stopped and
// delivers its exit as Claude's does.
func stoppableAgent(t *testing.T) (*Agent, *bytes.Buffer, *recorder) {
	t.Helper()
	a, input, updates := stoppableStartingAgent(t)
	a.Handle(initialized)
	return a, input, updates
}

// stoppableStartingAgent is a stoppableAgent before Claude has answered
// initialize.
func stoppableStartingAgent(t *testing.T) (*Agent, *bytes.Buffer, *recorder) {
	t.Helper()
	return agentRunning(t, exec.Command("sleep", "60"))
}

// agentRunning is a starting testAgent whose process is cmd, started as
// Claude's is.
func agentRunning(t *testing.T, cmd *exec.Cmd) (*Agent, *bytes.Buffer, *recorder) {
	t.Helper()
	events := make(chan agent.Event, 1)
	proc, _, err := process.Start(cmd, "claude", "ox-session", syscall.SIGKILL, parse, nil, events, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	input := new(bytes.Buffer)
	a, updates := startingAgent(input, events, proc)
	return a, input, updates
}

// prompt is the user's prompt text as an input.
func prompt(text string) agent.Input {
	return agent.Input{MessageID: newUUID(), Text: text, Content: text}
}

func sendTestPrompt(a *Agent, text string) (string, error) {
	p := prompt(text)
	return p.MessageID, a.Send(p)
}

// promptingAgent returns an agent running a prompt Claude has taken, with
// its input and updates so far cleared.
func promptingAgent(t *testing.T) (*Agent, *bytes.Buffer, *recorder, string) {
	t.Helper()
	a, input, updates := testAgent()
	id, err := sendTestPrompt(a, "hello")
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

// written returns the IDs of the prompts written to input, oldest first, and
// empties it.
func written(t *testing.T, input *bytes.Buffer) []string {
	t.Helper()
	var ids []string
	decoder := json.NewDecoder(input)
	for decoder.More() {
		var wire struct {
			Type string `json:"type"`
			UUID string `json:"uuid"`
		}
		if err := decoder.Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if wire.Type == "user" {
			ids = append(ids, wire.UUID)
		}
	}
	input.Reset()
	return ids
}

// wrotePrompt decodes the one prompt written to input.
func wrotePrompt(t *testing.T, input *bytes.Buffer) (wire struct {
	UUID     string `json:"uuid"`
	Priority string `json:"priority"`
	Message  struct {
		Content string `json:"content"`
	} `json:"message"`
}) {
	t.Helper()
	if err := json.Unmarshal(input.Bytes(), &wire); err != nil {
		t.Fatalf("no single prompt written: %q: %v", input, err)
	}
	return wire
}

// queuedIDs returns the IDs of the inputs a reports untaken, oldest first.
func queuedIDs(a *Agent) []string {
	ids := []string{}
	for _, input := range a.Snapshot().Queued {
		ids = append(ids, input.MessageID)
	}
	return ids
}

func TestPromptReleasedAfterClaudeTakesIt(t *testing.T) {
	a, input, updates := testAgent()
	id, err := sendTestPrompt(a, "hello")
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(input.Bytes(), &wire); err != nil || wire["uuid"] != id || wire["priority"] != "next" {
		t.Fatalf("prompt not written for steering under its ID: %s", input)
	}

	if len(*updates) != 2 || (*updates)[0].Type != "prompt_queued" || (*updates)[0].MessageID != id ||
		(*updates)[1].State != "running" || (*updates)[1].MessageID != id || a.Snapshot().State != "running" {
		t.Fatalf("a written prompt was not published as queued and running: %+v", *updates)
	}
	*updates = nil

	state(t, a, "idle")
	if len(*updates) != 0 || a.Snapshot().State != "running" || !slices.Equal(queuedIDs(a), []string{id}) {
		t.Fatalf("idle before Claude took the prompt released it: %+v, %+v", a.Snapshot(), *updates)
	}
	input.Reset()
	stop := interrupt(a, updates)
	if stop.answered || !strings.Contains(input.String(), `"subtype":"interrupt"`) {
		t.Fatalf("an interrupt before Claude took the prompt was not sent, or answered before its turn ended: %+v, %q", stop, input.String())
	}

	// A turn Claude starts itself while the prompt waits is not the prompt's.
	output(t, a, `{"type":"command_lifecycle","command_uuid":"11111111-1111-4111-8111-111111111111","state":"started"}`)
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"tick"}}}`)
	if stop.answered {
		t.Fatalf("the interrupt was answered before Claude reported idle: %+v", stop)
	}
	state(t, a, "idle")
	if len(*updates) != 1 || (*updates)[0].MessageID != "" || a.Snapshot().State != "running" || !slices.Equal(queuedIDs(a), []string{id}) {
		t.Fatalf("Claude's own turn took the waiting prompt's identity: %+v, %+v", a.Snapshot(), *updates)
	}
	if !stop.answered || stop.err != nil || stop.after != len(*updates) {
		t.Fatalf("Claude's idle did not answer the interrupt after its updates: %+v, %+v", stop, *updates)
	}
	*updates = nil

	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+id+`","state":"started"}`)
	if len(*updates) != 1 || (*updates)[0].Type != "prompt" || (*updates)[0].MessageID != id {
		t.Fatalf("the taken prompt was not published: %+v", *updates)
	}
	*updates = nil
	state(t, a, "running")
	state(t, a, "idle")
	if len(*updates) != 1 || (*updates)[0].State != "idle" || (*updates)[0].MessageID != id {
		t.Fatalf("unexpected prompt updates: %+v", *updates)
	}
	if err := a.Send(prompt("next")); err != nil {
		t.Fatalf("the released agent refused a prompt: %v", err)
	}
}

// A prompt with images writes an image block for each, then its text unless
// the text is blank.
func TestPromptWritesImages(t *testing.T) {
	png := agent.Image{ID: "a", MimeType: "image/png", Data: []byte("png bytes")}
	jpeg := agent.Image{ID: "b", MimeType: "image/jpeg", Data: []byte("jpeg bytes")}
	type block struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Source struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	image := func(i agent.Image) block {
		b := block{Type: "image"}
		b.Source.Type, b.Source.MediaType, b.Source.Data = "base64", i.MimeType, base64.StdEncoding.EncodeToString(i.Data)
		return b
	}
	for _, c := range []struct {
		name   string
		text   string
		images []agent.Image
		want   []block
	}{
		{"text and two images", "compare these", []agent.Image{png, jpeg}, []block{image(png), image(jpeg), {Type: "text", Text: "compare these"}}},
		{"an image and blank text", " ", []agent.Image{png}, []block{image(png)}},
	} {
		a, input, _ := testAgent()
		if err := a.Send(agent.Input{MessageID: newUUID(), Text: c.text, Images: c.images, Content: c.text}); err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Message struct {
				Content []block `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(input.Bytes(), &wire); err != nil {
			t.Fatalf("%s: the prompt's content is not blocks: %s: %v", c.name, input, err)
		}
		if !reflect.DeepEqual(wire.Message.Content, c.want) {
			t.Errorf("%s: wrote %+v, want %+v", c.name, wire.Message.Content, c.want)
		}
	}
}

func TestPromptSentWhileClaudeWorks(t *testing.T) {
	a, input, updates, first := promptingAgent(t)
	steer := agent.Input{MessageID: newUUID(), Text: "steer", From: "other", FromLabel: "Other", Content: "wrapped steer"}
	id := steer.MessageID
	if err := a.Send(steer); err != nil {
		t.Fatal(err)
	}
	if wire := wrotePrompt(t, input); wire.UUID != id || wire.Priority != "next" || wire.Message.Content != "wrapped steer" {
		t.Fatalf("prompt not written for steering under its ID with its content: %s", input)
	}
	input.Reset()
	if len(*updates) != 1 || (*updates)[0].Type != "prompt_queued" || (*updates)[0].MessageID != id {
		t.Fatalf("a prompt sent while Claude works was not published as queued: %+v", *updates)
	}
	if q := (*updates)[0]; q.Text != "steer" || q.From != "other" || q.FromLabel != "Other" {
		t.Fatalf("prompt_queued lost the input's text or attribution: %+v", q)
	}
	*updates = nil

	// Inputs sent behind the untaken one wait, unwritten, in order.
	held := prompt("again")
	message := agent.Input{MessageID: newUUID(), Text: "note", From: "third", FromLabel: "Third", Content: "wrapped note"}
	for _, in := range []agent.Input{held, message} {
		if err := a.Send(in); err != nil {
			t.Fatalf("an input behind an untaken one was refused: %v", err)
		}
	}
	if len(*updates) != 2 || (*updates)[0].Type != "prompt_queued" || (*updates)[0].MessageID != held.MessageID ||
		(*updates)[1].Type != "prompt_queued" || (*updates)[1].MessageID != message.MessageID || (*updates)[1].From != "third" || input.Len() != 0 {
		t.Fatalf("inputs behind an untaken one were not published once each, or were written: %+v, %q", *updates, input)
	}
	if queued := a.Snapshot().Queued; !reflect.DeepEqual(queued, []agent.Input{steer, held, message}) {
		t.Fatalf("the snapshot does not report every untaken input in order: %+v", queued)
	}
	*updates = nil

	state(t, a, "requires_action")
	state(t, a, "idle")
	if len(*updates) != 2 || (*updates)[0].State != "requires_action" || (*updates)[1].State != "running" || len(a.Snapshot().Queued) != 3 {
		t.Fatalf("a stale idle released a queued prompt or left it idle: %+v", *updates)
	}
	*updates = nil

	state(t, a, "running")
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+id+`","state":"started"}`)
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"steered"}}}`)
	if len(*updates) != 2 || (*updates)[0].Type != "prompt" || (*updates)[0].MessageID != id || (*updates)[1].MessageID != id {
		t.Fatalf("the taken prompt did not take over the turn: %+v", *updates)
	}
	if p := (*updates)[0]; p.Text != "steer" || p.From != "other" || p.FromLabel != "Other" {
		t.Fatalf("prompt lost the input's text or attribution: %+v", p)
	}
	if ids := written(t, input); !slices.Equal(ids, []string{held.MessageID}) || !slices.Equal(queuedIDs(a), []string{held.MessageID, message.MessageID}) {
		t.Fatalf("taking the first input did not write the second alone: %v, %v", ids, queuedIDs(a))
	}
	*updates = nil
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+held.MessageID+`","state":"started"}`)
	if ids := written(t, input); len(*updates) != 1 || (*updates)[0].Type != "prompt" || (*updates)[0].MessageID != held.MessageID ||
		!slices.Equal(ids, []string{message.MessageID}) || !slices.Equal(queuedIDs(a), []string{message.MessageID}) {
		t.Fatalf("taking the second input did not write the third: %v, %+v", ids, *updates)
	}
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+message.MessageID+`","state":"started"}`)
	if ids := written(t, input); len(ids) != 0 || len(a.Snapshot().Queued) != 0 {
		t.Fatalf("taking the last input wrote %v or left %+v", ids, a.Snapshot().Queued)
	}
	*updates = nil

	last, err := sendTestPrompt(a, "more")
	if err != nil {
		t.Fatalf("a prompt was refused once Claude took the queued ones: %v", err)
	}
	if ids := written(t, input); !slices.Equal(ids, []string{last}) {
		t.Fatalf("a prompt with nothing untaken was not written at once: %v", ids)
	}
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+last+`","state":"started"}`)
	output(t, a, `{"type":"result","subtype":"success","result":"done"}`)
	state(t, a, "idle")
	release := (*updates)[len(*updates)-1]
	if release.State != "idle" || release.MessageID != last || release.MessageID == first {
		t.Fatalf("the release does not name the latest taken prompt: %+v", *updates)
	}

	// Claude's own turn has no client prompt to hold the agent.
	a, _, updates = testAgent()
	state(t, a, "running")
	*updates = nil
	if err := a.Send(prompt("steer")); err != nil || len(*updates) != 1 || (*updates)[0].Type != "prompt_queued" {
		t.Fatalf("a prompt sent during Claude's own turn changed the state: %v, %+v", err, *updates)
	}

}

func TestStopDropsQueuedPrompt(t *testing.T) {
	a, input, updates, first := promptingAgent(t)
	id, err := sendTestPrompt(a, "steer")
	if err != nil {
		t.Fatal(err)
	}
	held := []agent.Input{prompt("one"), prompt("two")}
	for _, in := range held {
		if err := a.Send(in); err != nil {
			t.Fatal(err)
		}
	}
	input.Reset()
	*updates = nil
	if stop := interrupt(a, updates); stop.answered || !strings.Contains(input.String(), `"cancel_queued":true`) {
		t.Fatalf("an interrupt did not drop the written prompt, or was answered at once: %+v, %q", stop, input.String())
	}
	if !cancelled(*updates, []string{held[0].MessageID, held[1].MessageID}) || !slices.Equal(queuedIDs(a), []string{id}) {
		t.Fatalf("an interrupt did not drop the held inputs at once, oldest first: %+v, %v", *updates, queuedIDs(a))
	}
	*updates = nil
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+first+`","state":"cancelled"}`)
	if len(*updates) != 0 {
		t.Fatalf("cancelling the taken prompt published updates: %+v", *updates)
	}
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+id+`","state":"cancelled"}`)
	if len(*updates) != 1 || (*updates)[0].Type != "prompt_cancelled" || (*updates)[0].MessageID != id || (*updates)[0].Text != "steer" {
		t.Fatalf("the dropped prompt was not published: %+v", *updates)
	}
	if queued := a.Snapshot().Queued; len(queued) != 0 {
		t.Fatalf("the snapshot reports a dropped input: %+v", queued)
	}
	*updates = nil
	state(t, a, "idle")
	if len(*updates) != 1 || (*updates)[0].Type != "state" || (*updates)[0].MessageID != id {
		t.Fatalf("the release did not name the dropped prompt %s rather than %s: %+v", id, first, *updates)
	}
	input.Reset()
	next, err := sendTestPrompt(a, "next")
	if ids := written(t, input); err != nil || !slices.Equal(ids, []string{next}) {
		t.Fatalf("idle did not release the agent after the dropped prompt: %v, %v", err, ids)
	}

	// An input sent after the interrupt is written once Claude drops the
	// written one.
	a, input, updates, _ = promptingAgent(t)
	if id, err = sendTestPrompt(a, "steer"); err != nil {
		t.Fatal(err)
	}
	stop := interrupt(a, updates)
	input.Reset()
	late, err := sendTestPrompt(a, "late")
	if err != nil || input.Len() != 0 {
		t.Fatalf("an input sent before Claude dropped the written one was written: %v, %q", err, input)
	}
	*updates = nil
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+id+`","state":"cancelled"}`)
	if ids := written(t, input); !cancelled(*updates, []string{id}) || !slices.Equal(ids, []string{late}) || !slices.Equal(queuedIDs(a), []string{late}) {
		t.Fatalf("Claude's cancelled did not write the input sent after the interrupt: %v, %+v", ids, *updates)
	}
	// Claude can take that input before it reports idle, which ends the
	// stopped turn.
	output(t, a, `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_tools"}`)
	if stop.answered {
		t.Fatalf("the interrupt was answered at the stopped turn's result: %+v", stop)
	}
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+late+`","state":"started"}`)
	if !stop.answered || stop.err != nil || stop.after != len(*updates) {
		t.Fatalf("taking the next input did not answer the interrupt: %+v, %+v", stop, *updates)
	}

	a, input, updates, first = promptingAgent(t)
	if stop := interrupt(a, updates); stop.answered || !strings.Contains(input.String(), `"subtype":"interrupt"`) || strings.Contains(input.String(), "cancel_queued") {
		t.Fatalf("an interrupt with nothing queued changed: %+v, %q", stop, input.String())
	}
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+first+`","state":"cancelled"}`)
	if len(*updates) != 0 {
		t.Fatalf("cancelling the taken prompt with nothing queued published updates: %+v", *updates)
	}
	state(t, a, "idle")
	if len(*updates) != 1 || (*updates)[0].Type != "state" || (*updates)[0].MessageID != first {
		t.Fatalf("the release did not name the interrupted prompt %s: %+v", first, *updates)
	}

	// Claude can take the written prompt after the interrupt, only to stop
	// it, which does not end the stopped turn.
	a, _, updates = testAgent()
	if id, err = sendTestPrompt(a, "hello"); err != nil {
		t.Fatal(err)
	}
	stop = interrupt(a, updates)
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+id+`","state":"started"}`)
	output(t, a, `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"}`)
	if stop.answered {
		t.Fatalf("taking the interrupted prompt answered the interrupt: %+v", stop)
	}
	state(t, a, "idle")
	if !stop.answered || stop.after != len(*updates) {
		t.Fatalf("idle did not answer the interrupt after its update: %+v, %+v", stop, *updates)
	}

	a, _, updates = stoppableAgent(t)
	ids := sendHeld(t, a)
	*updates = nil
	a.Stop()
	if len(*updates) != 3 || !cancelled((*updates)[:2], ids) || (*updates)[0].Text != "hello" ||
		(*updates)[2].Type != "exited" || (*updates)[2].MessageID != ids[0] || (*updates)[2].Error != "Claude was stopped" {
		t.Fatalf("a stop did not drop the untaken inputs, oldest first, then end the written one: %+v", *updates)
	}
	if queued := a.Snapshot().Queued; len(queued) != 0 {
		t.Fatalf("the snapshot reports an input after exit: %+v", queued)
	}
	if err := a.Send(prompt("late")); !errors.Is(err, agent.ErrExited) || len(*updates) != 3 {
		t.Fatalf("an agent that has ended accepted an input: %v, %+v", err, *updates)
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

// A turn Claude starts itself is published, including one a resumed
// conversation starts before Claude has answered initialize.
func TestTurnsClaudeStartsArePublished(t *testing.T) {
	for _, starting := range []bool{false, true} {
		var a *Agent
		var updates *recorder
		if starting {
			a, updates = startingAgent(io.Discard, make(chan agent.Event), nil)
		} else {
			a, _, updates = testAgent()
		}
		state(t, a, "running")
		output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"tick"}}}`)
		output(t, a, `{"type":"result","subtype":"success","result":"tick"}`)
		output(t, a, `{"type":"result","subtype":"success","result":""}`)
		state(t, a, "idle")
		if starting {
			a.Handle(initialized)
		}
		a.Handle(process.Exit{})
		types := []string{}
		for _, u := range *updates {
			if u.MessageID != "" {
				t.Fatalf("starting %v: self-started turn update has a message ID: %+v", starting, u)
			}
			types = append(types, u.Type)
		}
		if strings.Join(types, ",") != "state,text_delta,result,state,exited" {
			t.Fatalf("starting %v: unexpected updates: %v", starting, types)
		}
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

	// A call an interrupt stopped has no tool_result, so it shows stopped.
	output(t, a, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"sleep 30"}}]}}`)
	stop := interrupt(a, updates)
	*updates = nil
	output(t, a, `{"type":"control_response","response":{"subtype":"success","request_id":"interrupt"}}`)
	output(t, a, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t3","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected."}]}}`)
	output(t, a, `{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`)
	output(t, a, `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_tools"}`)
	state(t, a, "idle")
	if !stop.answered || stop.err != nil {
		t.Fatalf("the interrupt was not answered at idle: %+v", stop)
	}
	for _, u := range *updates {
		if u.Type == "tool_result" {
			t.Fatalf("the stopped call got a tool_result: %+v", *updates)
		}
	}
	if u := (*updates)[0]; u.Type != "result" || u.Result.Text != "Interrupted" {
		t.Fatalf("the stopped turn did not end with Interrupted: %+v", *updates)
	}

	// The next turn's error results are published again.
	next, err := sendTestPrompt(a, "again")
	if err != nil {
		t.Fatal(err)
	}
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+next+`","state":"started"}`)
	*updates = nil
	output(t, a, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t4","is_error":true,"content":"failed"}]}}`)
	if len(*updates) != 1 || (*updates)[0].Type != "tool_result" || (*updates)[0].ToolUseID != "t4" || !(*updates)[0].Result.IsError {
		t.Fatalf("an error result after the stopped turn was not published: %+v", *updates)
	}
}

func TestThinkingIsPublished(t *testing.T) {
	a, _, updates, id := promptingAgent(t)
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":""}}}`)
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Two plus two."}}}`)
	output(t, a, `{"type":"stream_event","parent_tool_use_id":"t0","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"sub"}}}`)
	output(t, a, `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"abc"}}}`)
	output(t, a, `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Two plus two.","signature":"abc"}]}}`)
	if len(*updates) != 1 {
		t.Fatalf("expected the top-level thinking only: %+v", *updates)
	}
	if u := (*updates)[0]; u.Type != "reasoning_delta" || u.Text != "Two plus two." || u.MessageID != id {
		t.Fatalf("unexpected reasoning update: %+v", u)
	}
}

func TestUsageIsPublished(t *testing.T) {
	a, _, updates, id := promptingAgent(t)
	output(t, a, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{`+
		`"seven_day":{"utilization":0.39,"resetsAt":1794193200,"status":"allowed"},`+
		`"five_hour":{"utilization":0.01,"resetsAt":1793767800,"status":"allowed"},`+
		`"seven_day_opus":{"utilization":0.5,"resetsAt":1794193200,"status":"allowed"}}}}`)
	output(t, a, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1793767800}}`)
	if len(*updates) != 1 {
		t.Fatalf("expected one usage update: %+v", *updates)
	}
	u := (*updates)[0]
	want := []agent.UsageWindow{
		{IntervalSeconds: 18000, UsedPercent: 1, ResetsAt: time.Unix(1793767800, 0)},
		{IntervalSeconds: 604800, UsedPercent: 39, ResetsAt: time.Unix(1794193200, 0)},
	}
	if u.Type != "usage" || u.MessageID != id || len(u.Usage) != len(want) {
		t.Fatalf("unexpected usage update: %+v", u)
	}
	for i, w := range want {
		got := u.Usage[i]
		if got.IntervalSeconds != w.IntervalSeconds || math.Abs(got.UsedPercent-w.UsedPercent) > 1e-9 || !got.ResetsAt.Equal(w.ResetsAt) {
			t.Fatalf("window %d is %+v, want %+v", i, got, w)
		}
	}
}

// Claude exits on its own only on failure, which clients see even when no
// prompt is active; an exit Ox asked for is plain parking.
func TestUnexpectedExitIsReported(t *testing.T) {
	a, _, updates := testAgent()
	a.Handle(process.Exit{Err: errors.New("exit status 1"), Stderr: "an unexpected diagnostic"})
	if !a.Exited() || len(*updates) != 1 || (*updates)[0].Type != "exited" || !strings.Contains((*updates)[0].Error, "exit status 1\nan unexpected diagnostic") {
		t.Fatalf("an unexpected exit looked like parking: %+v", *updates)
	}

	a, _, updates = testAgent()
	id, err := sendTestPrompt(a, "hello")
	if err != nil {
		t.Fatal(err)
	}
	a.Handle(process.Exit{Err: errors.New("exit status 1")})
	var exits []agent.Update
	for _, u := range *updates {
		if u.Type == "exited" {
			exits = append(exits, u)
		}
	}
	if len(exits) != 1 || exits[0].MessageID != id {
		t.Fatalf("an unexpected exit during a prompt was not reported once, naming it: %+v", *updates)
	}

	a, _, updates = testAgent()
	id, err = sendTestPrompt(a, "hello")
	if err != nil {
		t.Fatal(err)
	}
	a.Handle(plainText("API Error: Connection refused"))
	a.Handle(process.Exit{Err: errors.New("exit status 1")})
	exits = nil
	for _, u := range *updates {
		if u.Type == "exited" {
			exits = append(exits, u)
		}
	}
	if len(exits) != 1 || exits[0].MessageID != id || !strings.Contains(exits[0].Error, "exit status 1") || !strings.Contains(exits[0].Error, "API Error: Connection refused") {
		t.Fatalf("an exit after startup dropped Claude's plain-text line: %+v", *updates)
	}

	a, _, updates = stoppableAgent(t)
	a.Stop()
	if !a.Exited() || len(*updates) != 0 {
		t.Fatalf("a requested stop was reported as a failure: %+v", *updates)
	}

	// Claude's exit answers an interrupt waiting for its turn to end.
	a, _, updates = testAgent()
	state(t, a, "running")
	stop := interrupt(a, updates)
	a.Handle(process.Exit{Err: errors.New("exit status 1")})
	if !stop.answered || stop.err != nil || stop.after != len(*updates) {
		t.Fatalf("an exit did not answer the waiting interrupt after its update: %+v, %+v", stop, *updates)
	}
}

// Stop ends the agent at once, even when Claude ignores SIGTERM: Ox kills it
// in the background, and children.WaitAll waits for that.
func TestStopKillsClaudeIgnoringSIGTERM(t *testing.T) {
	saved := killAfter
	killAfter = 500 * time.Millisecond
	t.Cleanup(func() { killAfter = saved })
	cmd := exec.Command("sh", "-c", "trap '' TERM; echo ready; exec sleep 60")
	a, _, _ := agentRunning(t, cmd)
	if e := <-a.Events(); e != plainText("ready") {
		t.Fatalf("Claude's first line was %+v", e)
	}
	a.Handle(initialized)
	a.Stop()
	if !a.Exited() {
		t.Fatal("Stop did not end the agent")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("Stop waited for Claude to exit: %v", err)
	}
	select {
	case <-a.Reaped():
		t.Fatal("Reaped closed while Claude was running")
	default:
	}
	select {
	case <-a.Reaped():
	case <-time.After(5 * time.Second):
		t.Fatal("Reaped stayed open past the kill of a Claude that ignores SIGTERM")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("Reaped closed while Claude was running")
	}
	waited := make(chan struct{})
	go func() {
		children.WaitAll()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("WaitAll waited past the kill of a Claude that ignores SIGTERM")
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Fatal("WaitAll returned while Claude was running")
	}
}

// Inputs sent while Claude starts are held, and written one at a time once
// Claude has answered initialize with the session's flags.
func TestInputWaitsForStartup(t *testing.T) {
	input := new(bytes.Buffer)
	a, updates := startingAgent(input, make(chan agent.Event), nil)
	held := agent.Input{MessageID: newUUID(), Text: "steer", From: "other", FromLabel: "Other", Content: "wrapped steer"}
	if err := a.Send(held); err != nil {
		t.Fatal(err)
	}
	if len(*updates) != 2 || (*updates)[0].Type != "prompt_queued" || (*updates)[0].MessageID != held.MessageID || (*updates)[0].From != "other" ||
		(*updates)[1].State != "running" || (*updates)[1].MessageID != held.MessageID || input.Len() != 0 {
		t.Fatalf("held input was not published as queued and running, or was written: %+v, %q", *updates, input)
	}
	second := prompt("again")
	if err := a.Send(second); err != nil {
		t.Fatalf("a second input was refused while Claude starts: %v", err)
	}
	if len(*updates) != 3 || (*updates)[2].Type != "prompt_queued" || (*updates)[2].MessageID != second.MessageID || input.Len() != 0 {
		t.Fatalf("the second held input was not published once, or was written: %+v, %q", *updates, input)
	}
	if snapshot := a.Snapshot(); snapshot.State != "running" || !reflect.DeepEqual(snapshot.Queued, []agent.Input{held, second}) {
		t.Fatalf("the snapshot does not report the held inputs: %+v", snapshot)
	}
	result := make(chan error, 1)
	if a.Configure(agent.SettingsPatch{Model: new("haiku")}, replyTo(result)); !errors.Is(<-result, agent.ErrBusy) {
		t.Fatal("a configuration ran while Claude starts")
	}
	if a.Parkable() {
		t.Fatal("a starting agent is parkable")
	}
	state(t, a, "idle")
	if a.Snapshot().State != "running" || input.Len() != 0 {
		t.Fatalf("Claude's idle during startup released the held input: %+v, %q", a.Snapshot(), input)
	}
	*updates = nil

	a.Handle(initialized)
	if wire := wrotePrompt(t, input); wire.UUID != held.MessageID || wire.Priority != "next" || wire.Message.Content != "wrapped steer" {
		t.Fatalf("the first held input alone was not written with its content once Claude answered: %q", input)
	}
	input.Reset()
	if len(*updates) != 0 || !slices.Equal(queuedIDs(a), []string{held.MessageID, second.MessageID}) {
		t.Fatalf("writing the held input republished it or released it: %+v", *updates)
	}
	output(t, a, `{"type":"command_lifecycle","command_uuid":"`+held.MessageID+`","state":"started"}`)
	if len(*updates) != 1 || (*updates)[0].Type != "prompt" || (*updates)[0].MessageID != held.MessageID {
		t.Fatalf("Claude did not take the held input: %+v", *updates)
	}
	if ids := written(t, input); !slices.Equal(ids, []string{second.MessageID}) || !slices.Equal(queuedIDs(a), []string{second.MessageID}) {
		t.Fatalf("taking the first held input did not write the second: %v, %v", ids, queuedIDs(a))
	}

	// Interrupt drops the held inputs, oldest first, which leaves the agent
	// idle, and writes nothing.
	input = new(bytes.Buffer)
	a, updates = startingAgent(input, make(chan agent.Event), nil)
	ids := sendHeld(t, a)
	*updates = nil
	if stop := interrupt(a, updates); !stop.answered || stop.err != nil {
		t.Fatalf("an interrupt with nothing to stop was not answered at once: %+v", stop)
	}
	if !cancelledToIdle(*updates, ids) || input.Len() != 0 || a.Snapshot().State != "idle" || len(a.Snapshot().Queued) != 0 {
		t.Fatalf("an interrupt while Claude starts did not drop the held inputs alone: %+v, %q, %+v", *updates, input, a.Snapshot())
	}
	*updates = nil
	a.Handle(initialized)
	if len(*updates) != 0 || input.Len() != 0 || !a.Idle() {
		t.Fatalf("Claude's answer did not release the agent: %+v, %q", *updates, input)
	}
	if err := a.Send(prompt("next")); err != nil {
		t.Fatalf("the started agent refused a prompt: %v", err)
	}

	// A resumed process can run a turn of its own before Claude answers.
	// Interrupt drops the held inputs and stops that turn.
	input = new(bytes.Buffer)
	a, updates = startingAgent(input, make(chan agent.Event), nil)
	state(t, a, "running")
	ids = sendHeld(t, a)
	*updates = nil
	stop := interrupt(a, updates)
	if !cancelled(*updates, ids) || !strings.Contains(input.String(), `"subtype":"interrupt"`) || strings.Contains(input.String(), "cancel_queued") {
		t.Fatalf("an interrupt during a startup turn did not drop the held inputs and stop the turn: %+v, %q", *updates, input)
	}
	*updates = nil
	output(t, a, `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_tools"}`)
	state(t, a, "idle")
	if len(*updates) != 2 || (*updates)[0].Type != "result" || (*updates)[0].Result.Text != "Interrupted" || (*updates)[1].State != "idle" {
		t.Fatalf("the stopped startup turn did not end as interrupted and idle: %+v", *updates)
	}
	if !stop.answered || stop.after != 2 {
		t.Fatalf("the stopped startup turn's idle did not answer the interrupt: %+v", stop)
	}

	// A startup turn that fails publishes its error, and the start goes on.
	a, updates = startingAgent(io.Discard, make(chan agent.Event), nil)
	state(t, a, "running")
	*updates = nil
	output(t, a, `{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529 overloaded","terminal_reason":"model_error"}`)
	state(t, a, "idle")
	if len(*updates) != 2 || (*updates)[0].Type != "result" || !(*updates)[0].Result.IsError ||
		(*updates)[0].Result.Text != "API Error: 529 overloaded" || (*updates)[1].State != "idle" {
		t.Fatalf("the failed startup turn did not publish its error: %+v", *updates)
	}
	a.Handle(initialized)
	if !a.Idle() || a.Exited() {
		t.Fatalf("a failed startup turn ended the start: %+v", a.Snapshot())
	}

	// Held inputs Claude's pipe refuses are dropped once Claude answers, and
	// the exit that follows reports the process.
	var failures []error
	a = newAgent(newWriter(failingWriter{}), make(chan agent.Event), make(chan struct{}), nil, agent.Options{
		OnUpdate: updates.record, OnError: func(err error) { failures = append(failures, err) },
	})
	ids = sendHeld(t, a)
	*updates = nil
	a.Handle(initialized)
	if !cancelledToIdle(*updates, ids) || len(a.Snapshot().Queued) != 0 || len(failures) != 1 {
		t.Fatalf("held inputs that could not be written were not dropped: %+v, %v", *updates, failures)
	}

	// Stop drops the held inputs without reporting a failure.
	a, _, updates = stoppableStartingAgent(t)
	ids = sendHeld(t, a)
	*updates = nil
	a.Stop()
	if !a.Exited() || !cancelled(*updates, ids) {
		t.Fatalf("a stop while Claude starts did not drop the held inputs alone: %+v", *updates)
	}

	// A failed start drops the held inputs and names the latest.
	a, updates = startingAgent(io.Discard, make(chan agent.Event), nil)
	ids = sendHeld(t, a)
	*updates = nil
	a.Handle(process.Exit{Err: errors.New("exit status 1")})
	if len(*updates) != 3 || !cancelled((*updates)[:2], ids) || (*updates)[2].Type != "exited" || (*updates)[2].MessageID != ids[1] {
		t.Fatalf("a failed start did not drop the held inputs and name the latest: %+v", *updates)
	}
}

// sendHeld sends two prompts, hello and again, and returns their IDs.
func sendHeld(t *testing.T, a *Agent) []string {
	t.Helper()
	var ids []string
	for _, text := range []string{"hello", "again"} {
		id, err := sendTestPrompt(a, text)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// cancelled reports that updates are exactly prompt_cancelled for ids, in
// order.
func cancelled(updates []agent.Update, ids []string) bool {
	if len(updates) != len(ids) {
		return false
	}
	for i, u := range updates {
		if u.Type != "prompt_cancelled" || u.MessageID != ids[i] {
			return false
		}
	}
	return true
}

// cancelledToIdle reports that updates are exactly prompt_cancelled for ids,
// in order, then the idle state that dropping the last untaken input leaves.
func cancelledToIdle(updates []agent.Update, ids []string) bool {
	last := len(updates) - 1
	return last >= 0 && cancelled(updates[:last], ids) && updates[last].Type == "state" && updates[last].State == "idle"
}

// A background subagent's findings arrive in a turn Claude starts after the
// prompt's result. That turn is not the prompt's, though the prompt holds the
// session until Claude is idle.
func TestTurnAfterPromptResultIsClaudes(t *testing.T) {
	a, _, updates := testAgent()
	id, err := sendTestPrompt(a, "start a background agent")
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
	result := make(chan error, 1)
	if a.Configure(agent.SettingsPatch{Model: new("haiku")}, replyTo(result)); !errors.Is(<-result, agent.ErrBusy) {
		t.Fatal("the prompt released the session before Claude was idle")
	}
	state(t, a, "idle")
	if len(*updates) != 3 || (*updates)[0].MessageID != "" || (*updates)[1].MessageID != "" ||
		(*updates)[2].State != "idle" || (*updates)[2].MessageID != id {
		t.Fatalf("Claude's own turn was attributed to the prompt: %+v", *updates)
	}
}

// A turn that ends while a background task Claude started still runs leaves
// the session in background, which is idle for settings, until the task ends.
func TestBackgroundTasksAfterTurn(t *testing.T) {
	a, input, updates, id := promptingAgent(t)
	tasks := func(list string) {
		output(t, a, `{"type":"system","subtype":"background_tasks_changed","tasks":`+list+`}`)
	}
	tasks(`[{"task_id":"b0"}]`)
	tasks(`[]`)
	tasks(`[{"task_id":"b1"}]`)
	output(t, a, `{"type":"result","subtype":"success","result":""}`)
	if len(*updates) != 0 {
		t.Fatalf("background tasks during a turn changed the state: %+v", *updates)
	}

	state(t, a, "idle")
	if len(*updates) != 1 || (*updates)[0].State != agent.StateBackground || (*updates)[0].MessageID != id {
		t.Fatalf("a turn ending with a background task did not publish background for its prompt: %+v", *updates)
	}
	if got := a.Snapshot().State; got != agent.StateBackground {
		t.Fatalf("snapshot state %q, want background", got)
	}

	result := make(chan error, 1)
	a.Configure(agent.SettingsPatch{Model: new("haiku")}, replyTo(result))
	acknowledge(t, a, controlRequestID(t, input), "success")
	if err := <-result; err != nil {
		t.Fatalf("configuration refused in background: %v", err)
	}

	*updates = nil
	state(t, a, "running")
	state(t, a, "idle")
	if len(*updates) != 2 || (*updates)[0].State != agent.StateRunning || (*updates)[1].State != agent.StateBackground {
		t.Fatalf("a turn Claude started with a background task running did not end in background: %+v", *updates)
	}

	*updates = nil
	tasks(`[]`)
	if len(*updates) != 1 || (*updates)[0].State != agent.StateIdle || (*updates)[0].MessageID != "" {
		t.Fatalf("the last background task's end did not publish idle: %+v", *updates)
	}
}
