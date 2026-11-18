package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

type approvalInput struct{ bytes.Buffer }

func (*approvalInput) Close() error { return nil }

func approvalProcess() (*process, *approvalInput, *[]Update) {
	input := new(approvalInput)
	updates := new([]Update)
	return &process{
		stdin: input, state: "running",
		prompt: &submission{
			id: "prompt", clientID: "owner", started: true,
			approvals: make(map[string]struct{}),
			notify:    func(update Update) { *updates = append(*updates, update) },
		},
	}, input, updates
}

func TestApprovalRouting(t *testing.T) {
	p, input, updates := approvalProcess()
	request := json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Example","input":{"number":9007199254740993},"permission_suggestions":[],"extra":"preserved"}`)
	p.handleOutput(claudeOutput{Type: "control_request", RequestID: "request", Request: request})
	if len(*updates) != 1 || (*updates)[0].Type != "approval" || (*updates)[0].MessageID != "prompt" || !bytes.Equal((*updates)[0].Request, request) {
		t.Fatalf("approval did not reach the prompt client intact: %+v", *updates)
	}
	response := json.RawMessage(`{"behavior":"allow","updatedInput":{"number":9007199254740993},"extra":"preserved"}`)
	if err := p.approve("other", "request", response); !errors.Is(err, ErrApprovalOwner) || input.Len() != 0 {
		t.Fatalf("another client answered the request: %v, %s", err, input)
	}
	if err := p.approve("owner", "request", response); err != nil {
		t.Fatal(err)
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
	if err := p.approve("owner", "request", response); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("duplicate answer accepted: %v", err)
	}
}

func TestApprovalCancellation(t *testing.T) {
	for _, event := range []string{"control_cancel_request", "idle", "exit"} {
		t.Run(event, func(t *testing.T) {
			p, input, updates := approvalProcess()
			p.requestApproval("request", json.RawMessage(`{"subtype":"can_use_tool"}`))
			switch event {
			case "idle":
				p.setState("idle")
			case "exit":
				p.finish(nil)
			default:
				p.handleOutput(claudeOutput{Type: event, Subtype: "success", RequestID: "request"})
			}
			if len(*updates) < 2 || (*updates)[1].Type != "approval_cancelled" || (*updates)[1].RequestID != "request" {
				t.Fatalf("approval not withdrawn before the terminal update: %+v", *updates)
			}
			if err := p.approve("owner", "request", json.RawMessage(`{"behavior":"allow"}`)); !errors.Is(err, ErrApprovalNotFound) || input.Len() != 0 {
				t.Fatalf("late answer accepted: %v, %s", err, input)
			}
		})
	}
}

func TestApprovalSurvivesResult(t *testing.T) {
	p, input, updates := approvalProcess()
	p.requestApproval("background", json.RawMessage(`{"subtype":"can_use_tool"}`))
	p.handleOutput(claudeOutput{Type: "result", Subtype: "success", Result: "Background agent launched"})
	if len(*updates) != 2 || (*updates)[1].Type != "result" {
		t.Fatalf("result withdrew a background approval: %+v", *updates)
	}
	if err := p.approve("owner", "background", json.RawMessage(`{"behavior":"allow"}`)); err != nil {
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

func TestApprovalClientDisconnect(t *testing.T) {
	p, input, updates := approvalProcess()
	p.requestApproval("pending", json.RawMessage(`{"subtype":"can_use_tool"}`))
	p.disconnect("other")
	if input.Len() != 0 {
		t.Fatal("unrelated disconnect affected the prompt")
	}
	p.disconnect("owner")
	p.requestApproval("later", json.RawMessage(`{"subtype":"can_use_tool"}`))
	decoder := json.NewDecoder(input)
	for _, id := range []string{"pending", "later"} {
		var wire struct {
			Response struct {
				RequestID string `json:"request_id"`
				Response  struct {
					Behavior  string `json:"behavior"`
					Message   string `json:"message"`
					Interrupt bool   `json:"interrupt"`
				} `json:"response"`
			} `json:"response"`
		}
		if err := decoder.Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if wire.Response.RequestID != id || wire.Response.Response.Behavior != "deny" || !wire.Response.Response.Interrupt || wire.Response.Response.Message == "" {
			t.Fatalf("disconnected approval left waiting: %+v", wire)
		}
	}
	if len(*updates) != 1 {
		t.Fatalf("approval forwarded to disconnected client: %+v", *updates)
	}
}
