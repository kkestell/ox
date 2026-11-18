package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/kkestell/ox/internal/claude"
)

func TestApprovalRouting(t *testing.T) {
	a, input, updates := promptingAgent()
	request := json.RawMessage(`{"subtype":"can_use_tool","tool_name":"Example","input":{"number":9007199254740993},"permission_suggestions":[],"extra":"preserved"}`)
	a.handleOutput(claude.Message{Type: "control_request", RequestID: "request", Request: request})
	if len(*updates) != 1 || (*updates)[0].Type != "approval" || (*updates)[0].MessageID != "prompt" || !bytes.Equal((*updates)[0].Request, request) {
		t.Fatalf("approval did not reach the prompt client intact: %+v", *updates)
	}
	response := json.RawMessage(`{"behavior":"allow","updatedInput":{"number":9007199254740993},"extra":"preserved"}`)
	if err := a.answer("request", response); err != nil {
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
	if err := a.answer("request", response); !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("duplicate answer accepted: %v", err)
	}
}

func TestApprovalCancellation(t *testing.T) {
	for _, event := range []string{"control_cancel_request", "idle", "exit"} {
		t.Run(event, func(t *testing.T) {
			a, input, updates := promptingAgent()
			a.controlRequest("request", json.RawMessage(`{"subtype":"can_use_tool"}`))
			switch event {
			case "idle":
				a.sessionState("idle")
			case "exit":
				a.exit(nil)
			default:
				a.handleOutput(claude.Message{Type: event, Subtype: "success", RequestID: "request"})
			}
			if len(*updates) < 2 || (*updates)[1].Type != "approval_cancelled" || (*updates)[1].RequestID != "request" {
				t.Fatalf("approval not withdrawn before the terminal update: %+v", *updates)
			}
			if err := a.answer("request", json.RawMessage(`{"behavior":"allow"}`)); !errors.Is(err, ErrApprovalNotFound) || input.Len() != 0 {
				t.Fatalf("late answer accepted: %v, %s", err, input)
			}
		})
	}
}

func TestApprovalSurvivesResult(t *testing.T) {
	a, input, updates := promptingAgent()
	a.controlRequest("background", json.RawMessage(`{"subtype":"can_use_tool"}`))
	a.handleOutput(claude.Message{Type: "result", Subtype: "success", Result: "Background agent launched"})
	if len(*updates) != 2 || (*updates)[1].Type != "result" {
		t.Fatalf("result withdrew a background approval: %+v", *updates)
	}
	if err := a.answer("background", json.RawMessage(`{"behavior":"allow"}`)); err != nil {
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
	a, input := testAgent()
	updates := new(recorder)
	a.subscribers[updates] = struct{}{}
	a.controlRequest("scheduled", json.RawMessage(`{"subtype":"can_use_tool"}`))
	if len(*updates) != 1 || (*updates)[0].Type != "approval" || (*updates)[0].MessageID != "" {
		t.Fatalf("approval in a turn Claude started was not published: %+v", *updates)
	}
	if a.parkable() {
		t.Fatal("a pending approval left the agent parkable")
	}
	if err := a.answer("scheduled", json.RawMessage(`{"behavior":"allow"}`)); err != nil {
		t.Fatalf("approval in a turn Claude started could not be answered: %v", err)
	}
	if !bytes.Contains(input.Bytes(), []byte(`"request_id":"scheduled"`)) {
		t.Fatalf("answer was not forwarded: %s", input)
	}
}
