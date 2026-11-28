package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/process"
)

func TestApprovalRouting(t *testing.T) {
	a, input, updates, id := promptingAgent(t)
	request := json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Example","input":{"number":9007199254740993},"permission_suggestions":[],"extra":"preserved"}`)
	// Claude's requires_action is the agent's to report, with the approval.
	state(t, a, "requires_action")
	if len(*updates) != 0 {
		t.Fatalf("Claude's requires_action was published: %+v", *updates)
	}
	output(t, a, `{"type":"control_request","request_id":"request","request":`+string(request)+`}`)
	if len(*updates) != 2 || (*updates)[0].State != "requires_action" || (*updates)[1].Type != "approval" || (*updates)[1].MessageID != id || !bytes.Equal((*updates)[1].Request, request) {
		t.Fatalf("approval did not reach the prompt client intact: %+v", *updates)
	}
	*updates = (*updates)[1:]
	if pending := a.Snapshot().Approvals; len(pending) != 1 || !bytes.Equal(pending[0].Request, request) {
		t.Fatalf("the snapshot does not hold the approval's request: %+v", pending)
	}
	response := json.RawMessage(`{"behavior":"allow","updatedInput":{"number":9007199254740993},"extra":"preserved"}`)
	if err := a.Approve("request", response); err != nil {
		t.Fatal(err)
	}
	if len(*updates) != 2 || (*updates)[1].Type != "approval_answered" || (*updates)[1].RequestID != "request" {
		t.Fatalf("answer was not announced: %+v", *updates)
	}
	var wire struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Response  json.RawMessage `json:"response"`
		} `json:"response"`
	}
	if err := json.Unmarshal(input.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Type != "control_response" || wire.Response.Subtype != "success" || wire.Response.RequestID != "request" || !bytes.Equal(wire.Response.Response, response) {
		t.Fatalf("answer changed in transit: %s", input)
	}
	if err := a.Approve("request", response); !errors.Is(err, agent.ErrApprovalNotFound) {
		t.Fatalf("duplicate answer accepted: %v", err)
	}
}

// In auto, the agent answers each request Claude makes itself, echoing its
// input, except a shell command that starts with rm and a question for the
// user, which reach the user as in manual.
func TestAutoApproval(t *testing.T) {
	a, input, updates, _ := promptingAgent(t)
	a.settings.PermissionMode = agent.Auto
	for _, request := range []string{
		`{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"touch x","number":9007199254740993}}`,
		`{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"x"}}`,
		`{"subtype":"can_use_tool","tool_name":"Monitor","input":{"command":"tail -f log"}}`,
		`{"subtype":"can_use_tool","tool_name":"Example","input":"not an object"}`,
	} {
		state(t, a, "requires_action")
		output(t, a, `{"type":"control_request","request_id":"auto","request":`+request+`}`)
		state(t, a, "running")
		if len(*updates) != 0 || len(a.Snapshot().Approvals) != 0 {
			t.Fatalf("an auto-approved request was published: %s: %+v", request, *updates)
		}
		var call, wire struct {
			Input    json.RawMessage `json:"input"`
			Response struct {
				RequestID string `json:"request_id"`
				Response  struct {
					Behavior     string          `json:"behavior"`
					UpdatedInput json.RawMessage `json:"updatedInput"`
				} `json:"response"`
			} `json:"response"`
		}
		json.Unmarshal([]byte(request), &call)
		if call.Input[0] != '{' {
			call.Input = json.RawMessage("{}")
		}
		if err := json.Unmarshal(input.Bytes(), &wire); err != nil {
			t.Fatalf("%s: %s: %v", request, input, err)
		}
		if wire.Response.RequestID != "auto" || wire.Response.Response.Behavior != "allow" || !bytes.Equal(wire.Response.Response.UpdatedInput, call.Input) {
			t.Fatalf("%s was answered %s", request, input)
		}
		input.Reset()
	}
	for _, request := range []string{
		`{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"  rm -rf x"}}`,
		`{"subtype":"can_use_tool","tool_name":"Monitor","input":{"command":"rm log"}}`,
		`{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[]},"requires_user_interaction":true}`,
	} {
		state(t, a, "requires_action")
		output(t, a, `{"type":"control_request","request_id":"asks","request":`+request+`}`)
		if len(*updates) != 2 || (*updates)[0].State != "requires_action" || (*updates)[1].Type != "approval" || input.Len() != 0 {
			t.Fatalf("%s did not wait for the user in auto: %+v, %s", request, *updates, input)
		}
		if err := a.Approve("asks", json.RawMessage(`{"behavior":"deny","message":"no"}`)); err != nil {
			t.Fatal(err)
		}
		state(t, a, "running")
		input.Reset()
		*updates = nil
	}
	a.settings.PermissionMode = agent.Manual
	output(t, a, `{"type":"control_request","request_id":"manual","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"x"}}}`)
	if len(*updates) != 2 || (*updates)[0].State != "requires_action" || (*updates)[1].Type != "approval" || input.Len() != 0 {
		t.Fatalf("a Write did not wait for the user in manual: %+v, %s", *updates, input)
	}
}

func TestApprovalCancellation(t *testing.T) {
	for _, event := range []string{"control_cancel_request", "idle", "exit"} {
		t.Run(event, func(t *testing.T) {
			a, input, updates, _ := promptingAgent(t)
			output(t, a, `{"type":"control_request","request_id":"request","request":{"subtype":"can_use_tool"}}`)
			switch event {
			case "idle":
				state(t, a, "idle")
			case "exit":
				a.Handle(process.Exit{})
			default:
				output(t, a, `{"type":"control_cancel_request","request_id":"request"}`)
			}
			if len(*updates) < 3 || (*updates)[2].Type != "approval_cancelled" || (*updates)[2].RequestID != "request" {
				t.Fatalf("approval not withdrawn before the terminal update: %+v", *updates)
			}
			if err := a.Approve("request", json.RawMessage(`{"behavior":"allow"}`)); !errors.Is(err, agent.ErrApprovalNotFound) || input.Len() != 0 {
				t.Fatalf("late answer accepted: %v, %s", err, input)
			}
		})
	}
}

func TestApprovalSurvivesResult(t *testing.T) {
	a, input, updates, _ := promptingAgent(t)
	output(t, a, `{"type":"control_request","request_id":"background","request":{"subtype":"can_use_tool"}}`)
	output(t, a, `{"type":"result","subtype":"success","result":"Background agent launched"}`)
	if pending := a.Snapshot().Approvals; len(pending) != 1 || pending[0].RequestID != "background" {
		t.Fatalf("result withdrew a background approval: %+v", *updates)
	}
	if err := a.Approve("background", json.RawMessage(`{"behavior":"allow"}`)); err != nil {
		t.Fatalf("background approval could not be answered after a result: %v", err)
	}
	var wire struct {
		Response struct {
			RequestID string `json:"request_id"`
		} `json:"response"`
	}
	if err := json.Unmarshal(input.Bytes(), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Response.RequestID != "background" {
		t.Fatalf("background approval was not forwarded: %s", input)
	}
}

func TestApprovalWithoutPrompt(t *testing.T) {
	a, input, updates := testAgent()
	output(t, a, `{"type":"control_request","request_id":"scheduled","request":{"subtype":"can_use_tool"}}`)
	if len(*updates) != 2 || (*updates)[1].Type != "approval" || (*updates)[1].MessageID != "" {
		t.Fatalf("approval in a turn Claude started was not published: %+v", *updates)
	}
	if a.Parkable() {
		t.Fatal("a pending approval left the agent parkable")
	}
	if err := a.Approve("scheduled", json.RawMessage(`{"behavior":"allow"}`)); err != nil {
		t.Fatalf("approval in a turn Claude started could not be answered: %v", err)
	}
	if !bytes.Contains(input.Bytes(), []byte(`"request_id":"scheduled"`)) {
		t.Fatalf("answer was not forwarded: %s", input)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// A failed answer leaves the approval pending, so the exit that follows a
// broken pipe withdraws it from every client.
func TestFailedAnswerLeavesApprovalPending(t *testing.T) {
	a, updates := agentWriting(failingWriter{}, make(chan agent.Event), nil)
	output(t, a, `{"type":"control_request","request_id":"request","request":{"subtype":"can_use_tool"}}`)
	if err := a.Approve("request", json.RawMessage(`{"behavior":"allow"}`)); err == nil {
		t.Fatal("a failed write was reported as answered")
	}
	if len(a.Snapshot().Approvals) != 1 {
		t.Fatalf("a failed answer removed the approval: %+v", a.Snapshot())
	}
	*updates = nil
	a.Handle(process.Exit{Err: errors.New("signal: broken pipe")})
	if len(*updates) == 0 || (*updates)[0].Type != "approval_cancelled" || (*updates)[0].RequestID != "request" {
		t.Fatalf("exit did not withdraw the approval: %+v", *updates)
	}
}
