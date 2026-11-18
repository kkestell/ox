package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func configurationProcess() (*process, *bytes.Buffer) {
	input := new(bytes.Buffer)
	return &process{stdin: input, phase: available, claudeState: "idle"}, input
}

func TestConfigureWaitsForAcknowledgements(t *testing.T) {
	p, input := configurationProcess()
	decoder := json.NewDecoder(input)
	model, effort, mode := "haiku", "low", "plan"
	result := make(chan error, 1)
	p.reconfigure(Configuration{Model: &model, Effort: &effort, PermissionMode: &mode}, result)

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
		if _, err := p.submit("client", "hello", nil); !errors.Is(err, ErrBusy) {
			t.Fatalf("prompt accepted during configuration: %v", err)
		}
		overlapping := make(chan error, 1)
		if p.reconfigure(Configuration{Model: &model}, overlapping); !errors.Is(<-overlapping, ErrBusy) {
			t.Fatal("overlapping configuration accepted")
		}
		p.handleOutput(claudeOutput{Type: "control_response", Response: controlResponse{RequestID: "unrelated", Subtype: "success"}})
		select {
		case err := <-result:
			t.Fatalf("configuration finished before its acknowledgement: %v", err)
		default:
		}
		p.handleOutput(claudeOutput{Type: "control_response", Response: controlResponse{RequestID: wire.RequestID, Subtype: "success"}})
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("configuration did not finish")
	}
	if !p.idle() {
		t.Fatal("configuration kept its reservation")
	}
}

func TestConfigureFailureAndEffortReset(t *testing.T) {
	for _, outcome := range []string{"rejected", "timeout", "exited"} {
		t.Run(outcome, func(t *testing.T) {
			p, input := configurationProcess()
			effort, mode := "", "plan"
			result := make(chan error, 1)
			p.reconfigure(Configuration{Effort: &effort, PermissionMode: &mode}, result)
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
				p.handleOutput(claudeOutput{Type: "control_response", Response: controlResponse{RequestID: wire.RequestID, Subtype: "error", Error: "Unsupported effort"}})
			case "timeout":
				p.handle(controlTimeout{"stale"})
				p.handle(controlTimeout{wire.RequestID})
			case "exited":
				p.exit(nil)
			}
			err := <-result
			if outcome == "rejected" && (err == nil || !strings.Contains(err.Error(), "Unsupported effort")) {
				t.Fatalf("Claude's refusal was lost: %v", err)
			}
			if outcome == "timeout" && (err == nil || !strings.Contains(err.Error(), "no control response")) {
				t.Fatalf("missing acknowledgement did not fail configuration: %v", err)
			}
			if outcome == "exited" && !errors.Is(err, ErrNotFound) {
				t.Fatalf("process exit did not fail configuration: %v", err)
			}
			if p.config != nil || p.phase == configuring {
				t.Fatal("failed configuration kept its reservation")
			}
		})
	}
}

func TestConfigureRequiresIdle(t *testing.T) {
	for _, state := range []string{"running", "requires_action", "queued", "prompting"} {
		t.Run(state, func(t *testing.T) {
			p := &process{phase: available, claudeState: state}
			switch state {
			case "queued":
				p.phase, p.claudeState = queued, "idle"
			case "prompting":
				p.phase, p.claudeState = prompting, "idle"
			}
			result := make(chan error, 1)
			if p.reconfigure(Configuration{}, result); !errors.Is(<-result, ErrBusy) {
				t.Fatalf("configuration accepted in %s", state)
			}
		})
	}
	exited := &process{done: make(chan struct{})}
	close(exited.done)
	if err := exited.configure(Configuration{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("configuration of an exited process: %v", err)
	}
}
