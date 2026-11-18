package agent

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/claude"
)

func TestConfigureWaitsForAcknowledgements(t *testing.T) {
	a, input := testAgent()
	decoder := json.NewDecoder(input)
	model, effort, mode := "haiku", "low", "plan"
	result := make(chan error, 1)
	a.reconfigure(Configuration{Model: &model, Effort: &effort, PermissionMode: &mode}, result)

	for _, expected := range []map[string]any{
		{"subtype": "set_model", "model": "haiku"},
		{"subtype": "apply_flag_settings", "settings": map[string]any{"effortLevel": "low"}},
		{"subtype": "set_permission_mode", "mode": "plan"},
	} {
		var wire struct {
			Type      string         `json:"type"`
			RequestID string         `json:"request_id"`
			Request   map[string]any `json:"request"`
		}
		if err := decoder.Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if wire.Type != "control_request" || wire.RequestID == "" || !reflect.DeepEqual(wire.Request, expected) {
			t.Fatalf("unexpected configuration request: %+v", wire)
		}
		if _, err := a.submit(new(recorder), "hello"); !errors.Is(err, ErrBusy) {
			t.Fatalf("prompt accepted during configuration: %v", err)
		}
		overlapping := make(chan error, 1)
		if a.reconfigure(Configuration{Model: &model}, overlapping); !errors.Is(<-overlapping, ErrBusy) {
			t.Fatal("overlapping configuration accepted")
		}
		a.handleOutput(claude.Message{Type: "control_response", Response: claude.ControlResponse{RequestID: "unrelated", Subtype: "success"}})
		select {
		case err := <-result:
			t.Fatalf("configuration finished before its acknowledgement: %v", err)
		default:
		}
		a.handleOutput(claude.Message{Type: "control_response", Response: claude.ControlResponse{RequestID: wire.RequestID, Subtype: "success"}})
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("configuration did not finish")
	}
	if !a.idle() {
		t.Fatal("configuration kept its reservation")
	}
}

func TestConfigureFailureAndEffortReset(t *testing.T) {
	for _, outcome := range []string{"rejected", "timeout", "exited"} {
		t.Run(outcome, func(t *testing.T) {
			a, input := testAgent()
			effort, mode := "", "plan"
			result := make(chan error, 1)
			a.reconfigure(Configuration{Effort: &effort, PermissionMode: &mode}, result)
			var wire struct {
				RequestID string `json:"request_id"`
				Request   struct {
					Settings map[string]any `json:"settings"`
				} `json:"request"`
			}
			if err := json.NewDecoder(input).Decode(&wire); err != nil {
				t.Fatal(err)
			}
			if value, present := wire.Request.Settings["effortLevel"]; !present || value != nil {
				t.Fatalf("empty effort did not reset the override: %+v", wire)
			}
			switch outcome {
			case "rejected":
				a.handleOutput(claude.Message{Type: "control_response", Response: claude.ControlResponse{RequestID: wire.RequestID, Subtype: "error", Error: "Unsupported effort"}})
			case "timeout":
				a.handle(controlTimeout{"stale"})
				a.handle(controlTimeout{wire.RequestID})
			case "exited":
				a.exit(nil)
			}
			err := <-result
			if outcome == "rejected" && (err == nil || !strings.Contains(err.Error(), "Unsupported effort")) {
				t.Fatalf("Claude's refusal was lost: %v", err)
			}
			if outcome == "timeout" && (err == nil || !strings.Contains(err.Error(), "no control response")) {
				t.Fatalf("missing acknowledgement did not fail configuration: %v", err)
			}
			if outcome == "exited" && !errors.Is(err, ErrExited) {
				t.Fatalf("process exit did not fail configuration: %v", err)
			}
			if a.config != nil || a.phase == configuring {
				t.Fatal("failed configuration kept its reservation")
			}
		})
	}
}

func TestConfigureRequiresIdle(t *testing.T) {
	for _, state := range []string{"running", "requires_action", "queued", "prompting"} {
		t.Run(state, func(t *testing.T) {
			a, _ := testAgent()
			switch state {
			case "queued":
				a.phase = queued
			case "prompting":
				a.phase = prompting
			default:
				a.claudeState = state
			}
			result := make(chan error, 1)
			if a.reconfigure(Configuration{}, result); !errors.Is(<-result, ErrBusy) {
				t.Fatalf("configuration accepted in %s", state)
			}
		})
	}
	exited, _ := testAgent()
	close(exited.done)
	if err := exited.Configure(Configuration{}); !errors.Is(err, ErrExited) {
		t.Fatalf("configuration of an exited process: %v", err)
	}
}
