package claude

import (
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/agent"
)

func replyTo(result chan error) func(error) {
	return func(err error) { result <- err }
}

func TestConfigureWaitsForAcknowledgements(t *testing.T) {
	a, input, _ := testAgent()
	decoder := json.NewDecoder(input)
	config := agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"}
	result := make(chan error, 1)
	a.Configure(config, replyTo(result))

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
		if _, err := a.Send("hello"); !errors.Is(err, agent.ErrBusy) {
			t.Fatalf("prompt accepted during configuration: %v", err)
		}
		overlapping := make(chan error, 1)
		if a.Configure(config, replyTo(overlapping)); !errors.Is(<-overlapping, agent.ErrBusy) {
			t.Fatal("overlapping configuration accepted")
		}
		a.handleOutput(Message{Type: "control_response", Response: ControlResponse{RequestID: "unrelated", Subtype: "success"}})
		select {
		case err := <-result:
			t.Fatalf("configuration finished before its acknowledgement: %v", err)
		default:
		}
		a.handleOutput(Message{Type: "control_response", Response: ControlResponse{RequestID: wire.RequestID, Subtype: "success"}})
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
	if a.settings != config {
		t.Fatalf("acknowledged settings were not recorded: %+v", a.settings)
	}
}

func TestConfigureSendsOnlyChangedSettings(t *testing.T) {
	a, input, _ := testAgent()
	a.settings = agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"}
	result := make(chan error, 1)
	a.Configure(agent.Settings{Model: "haiku", Effort: "high", PermissionMode: "plan"}, replyTo(result))
	var wire struct {
		RequestID string         `json:"request_id"`
		Request   map[string]any `json:"request"`
	}
	if err := json.NewDecoder(input).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if wire.Request["subtype"] != "apply_flag_settings" || input.Len() != 0 {
		t.Fatalf("unchanged settings were sent: %+v, %s", wire, input)
	}
	a.handleOutput(Message{Type: "control_response", Response: ControlResponse{RequestID: wire.RequestID, Subtype: "success"}})
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	a.Configure(a.settings, replyTo(result))
	if err := <-result; err != nil || input.Len() != 0 || !a.idle() {
		t.Fatalf("configuration without a change was sent to Claude: %v, %s", err, input)
	}
}

func TestConfigureFailure(t *testing.T) {
	for _, outcome := range []string{"rejected", "exited"} {
		t.Run(outcome, func(t *testing.T) {
			a, input, _ := testAgent()
			result := make(chan error, 1)
			a.Configure(agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"}, replyTo(result))
			var wire struct {
				RequestID string `json:"request_id"`
			}
			if err := json.NewDecoder(input).Decode(&wire); err != nil {
				t.Fatal(err)
			}
			switch outcome {
			case "rejected":
				a.handleOutput(Message{Type: "control_response", Response: ControlResponse{RequestID: wire.RequestID, Subtype: "error", Error: "Unsupported model"}})
			case "exited":
				a.exit(nil)
			}
			err := <-result
			if outcome == "rejected" && (err == nil || !strings.Contains(err.Error(), "Unsupported model")) {
				t.Fatalf("Claude's refusal was lost: %v", err)
			}
			if outcome == "exited" && !errors.Is(err, agent.ErrExited) {
				t.Fatalf("process exit did not fail configuration: %v", err)
			}
			if a.config != nil || a.phase == configuring {
				t.Fatal("failed configuration kept its reservation")
			}
		})
	}
}

// A timed-out control request may still be applied, so the process stops and
// its exit ends the configuration; a late acknowledgement changes nothing.
func TestConfigureTimeoutStopsProcess(t *testing.T) {
	a, input, _ := testAgent()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	a.proc = &Process{Input: a.input, cmd: cmd}
	a.settings = agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "manual"}
	result := make(chan error, 1)
	a.Configure(agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"}, replyTo(result))
	var wire struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(input).Decode(&wire); err != nil {
		t.Fatal(err)
	}

	a.Handle(controlTimeout{"stale"})
	a.Handle(controlTimeout{wire.RequestID})
	if err := cmd.Wait(); err == nil {
		t.Fatal("the process was not stopped")
	}
	a.handleOutput(Message{Type: "control_response", Response: ControlResponse{RequestID: wire.RequestID, Subtype: "success"}})
	select {
	case err := <-result:
		t.Fatalf("configuration ended before the process exited: %v", err)
	default:
	}
	if a.idle() || a.settings.PermissionMode != "manual" {
		t.Fatalf("stopping process was released or changed: %v, %+v", a.phase, a.settings)
	}
	a.Handle(processExit{})
	if err := <-result; !errors.Is(err, agent.ErrExited) {
		t.Fatalf("process exit did not end configuration: %v", err)
	}
}

func TestConfigureRequiresIdle(t *testing.T) {
	for _, state := range []string{"running", "requires_action", "queued", "prompting"} {
		t.Run(state, func(t *testing.T) {
			a, _, _ := testAgent()
			switch state {
			case "queued":
				a.phase = queued
			case "prompting":
				a.phase = prompting
			default:
				a.claudeState = state
			}
			result := make(chan error, 1)
			if a.Configure(agent.Settings{Model: "haiku"}, replyTo(result)); !errors.Is(<-result, agent.ErrBusy) {
				t.Fatalf("configuration accepted in %s", state)
			}
		})
	}
}
