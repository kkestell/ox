package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/agent"
)

type outcome struct {
	applied agent.Settings
	err     error
}

func replyTo(result chan outcome) func(agent.Settings, error) {
	return func(applied agent.Settings, err error) { result <- outcome{applied, err} }
}

// controlRequestID reads the next control request written to Claude.
func controlRequestID(t *testing.T, input *bytes.Buffer) string {
	t.Helper()
	var wire struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(input).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	return wire.RequestID
}

func acknowledge(t *testing.T, a *Agent, requestID, subtype string) {
	t.Helper()
	output(t, a, `{"type":"control_response","response":{"subtype":"`+subtype+`","request_id":"`+requestID+`","error":"refused"}}`)
}

func TestConfigureWaitsForAcknowledgements(t *testing.T) {
	a, input, _ := testAgent()
	decoder := json.NewDecoder(input)
	config := agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"}
	result := make(chan outcome, 1)
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
		if _, err := a.Send(prompt("hello")); !errors.Is(err, agent.ErrBusy) {
			t.Fatalf("prompt accepted during configuration: %v", err)
		}
		overlapping := make(chan outcome, 1)
		if a.Configure(config, replyTo(overlapping)); !errors.Is((<-overlapping).err, agent.ErrBusy) {
			t.Fatal("overlapping configuration accepted")
		}
		acknowledge(t, a, "unrelated", "success")
		select {
		case r := <-result:
			t.Fatalf("configuration finished before its acknowledgement: %v", r.err)
		default:
		}
		acknowledge(t, a, wire.RequestID, "success")
	}
	select {
	case r := <-result:
		if r.err != nil || r.applied != config {
			t.Fatalf("acknowledged settings were not applied: %+v", r)
		}
	default:
		t.Fatal("configuration did not finish")
	}
	if _, err := a.Send(prompt("hello")); err != nil {
		t.Fatalf("configuration kept its reservation: %v", err)
	}
}

func TestConfigureSendsOnlyChangedSettings(t *testing.T) {
	a, input, _ := testAgent()
	result := make(chan outcome, 1)
	a.Configure(agent.Settings{Effort: "high"}, replyTo(result))
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
	acknowledge(t, a, wire.RequestID, "success")
	if r := <-result; r.err != nil {
		t.Fatal(r.err)
	}

	a.Configure(agent.Settings{Effort: "high"}, replyTo(result))
	if r := <-result; r.err != nil || input.Len() != 0 {
		t.Fatalf("configuration without a change was sent to Claude: %v, %s", r.err, input)
	}
	if _, err := a.Send(prompt("hello")); err != nil {
		t.Fatalf("configuration without a change kept a reservation: %v", err)
	}
}

func TestConfigureFailure(t *testing.T) {
	for _, ending := range []string{"rejected", "exited"} {
		t.Run(ending, func(t *testing.T) {
			a, input, _ := testAgent()
			result := make(chan outcome, 1)
			a.Configure(agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"}, replyTo(result))
			requestID := controlRequestID(t, input)
			switch ending {
			case "rejected":
				output(t, a, `{"type":"control_response","response":{"subtype":"error","request_id":"`+requestID+`","error":"Unsupported model"}}`)
				if _, err := a.Send(prompt("hello")); err != nil {
					t.Fatalf("failed configuration kept its reservation: %v", err)
				}
			case "exited":
				a.Handle(processExit{})
			}
			err := (<-result).err
			if ending == "rejected" && (err == nil || !strings.Contains(err.Error(), "Unsupported model")) {
				t.Fatalf("Claude's refusal was lost: %v", err)
			}
			if ending == "exited" && !errors.Is(err, agent.ErrExited) {
				t.Fatalf("process exit did not fail configuration: %v", err)
			}
		})
	}
}

// A refusal after an acknowledgement leaves the process with the settings it
// took, which the session must adopt.
func TestPartialConfigurationReportsAppliedSettings(t *testing.T) {
	a, input, _ := testAgent()
	result := make(chan outcome, 1)
	a.Configure(agent.Settings{Model: "sonnet", Effort: "high"}, replyTo(result))
	acknowledge(t, a, controlRequestID(t, input), "success")
	acknowledge(t, a, controlRequestID(t, input), "error")
	if r := <-result; r.err == nil || r.applied != (agent.Settings{Model: "sonnet"}) {
		t.Fatalf("a partial configuration reported %+v", r)
	}
}

// A timed-out control request may still be applied, so the process stops and
// its exit ends the configuration; a late acknowledgement changes nothing.
func TestConfigureTimeoutStopsProcess(t *testing.T) {
	a, input, _ := stoppableAgent(t)
	result := make(chan outcome, 1)
	a.Configure(agent.Settings{PermissionMode: "plan"}, replyTo(result))
	requestID := controlRequestID(t, input)

	a.Handle(controlTimeout{"stale"})
	a.Handle(controlTimeout{requestID})
	exit := <-a.Events()
	acknowledge(t, a, requestID, "success")
	select {
	case r := <-result:
		t.Fatalf("configuration ended before the process exited: %v", r.err)
	default:
	}
	if _, err := a.Send(prompt("hello")); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("stopping process was released: %v", err)
	}
	a.Handle(exit)
	if r := <-result; !errors.Is(r.err, agent.ErrExited) || r.applied != (agent.Settings{}) {
		t.Fatalf("process exit did not end configuration unchanged: %+v", r)
	}
}

func TestConfigureRequiresIdle(t *testing.T) {
	for _, busy := range []string{"running", "requires_action", "prompt not taken", "prompt taken"} {
		t.Run(busy, func(t *testing.T) {
			a, _, _ := testAgent()
			switch busy {
			case "prompt not taken":
				if _, err := a.Send(prompt("hello")); err != nil {
					t.Fatal(err)
				}
			case "prompt taken":
				a, _, _, _ = promptingAgent(t)
			default:
				state(t, a, busy)
			}
			result := make(chan outcome, 1)
			if a.Configure(agent.Settings{Model: "haiku"}, replyTo(result)); !errors.Is((<-result).err, agent.ErrBusy) {
				t.Fatalf("configuration accepted in %s", busy)
			}
		})
	}
}

// Claude changes its own permission mode, such as leaving plan mode once a
// plan is approved, and reports it on system lines, with manual as default.
func TestClaudeReportsPermissionMode(t *testing.T) {
	input := new(bytes.Buffer)
	var adopted []agent.Settings
	a := newAgent(newWriter(input), make(chan agent.Event), nil, "claude-session", agent.Options{
		Settings:     agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"},
		OnUpdate:     func(agent.Update) {},
		OnConfigured: func(s agent.Settings) { adopted = append(adopted, s) },
	})

	output(t, a, `{"type":"system","subtype":"status","status":null,"permissionMode":"default"}`)
	if want := []agent.Settings{{Model: "haiku", Effort: "low", PermissionMode: "manual"}}; !reflect.DeepEqual(adopted, want) {
		t.Fatalf("a change to default was adopted as %+v", adopted)
	}
	output(t, a, `{"type":"system","subtype":"init","permissionMode":"default"}`)
	output(t, a, `{"type":"system","subtype":"session_state_changed","state":"idle"}`)
	if len(adopted) != 1 {
		t.Fatalf("a report of the current mode changed settings: %+v", adopted)
	}

	result := make(chan outcome, 1)
	a.Configure(agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"}, replyTo(result))
	var wire struct {
		RequestID string         `json:"request_id"`
		Request   map[string]any `json:"request"`
	}
	if err := json.NewDecoder(input).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if wire.Request["subtype"] != "set_permission_mode" || wire.Request["mode"] != "plan" {
		t.Fatalf("configuration did not leave the mode Claude reported: %+v", wire)
	}
	acknowledge(t, a, wire.RequestID, "success")
	output(t, a, `{"type":"system","subtype":"status","status":null,"permissionMode":"plan"}`)
	if r := <-result; r.err != nil || len(adopted) != 1 {
		t.Fatalf("the report that followed a configuration changed settings: %+v, %+v", r, adopted)
	}
}
