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

func ptr(s string) *string { return &s }

func replyTo(result chan error) func(error) { return func(err error) { result <- err } }

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
	input := new(bytes.Buffer)
	var applied []agent.Settings
	a := newAgent(newWriter(input), make(chan agent.Event), nil, agent.Options{
		OnUpdate: func(agent.Update) {}, OnConfigured: func(s agent.Settings) { applied = append(applied, s) },
	})
	decoder := json.NewDecoder(input)
	patch := agent.SettingsPatch{Model: ptr("haiku"), Effort: ptr("low"), PermissionMode: ptr("plan")}
	result := make(chan error, 1)
	a.Configure(patch, replyTo(result))

	expectedSettings := []agent.Settings{
		{Model: "haiku"},
		{Model: "haiku", Effort: "low"},
		{Model: "haiku", Effort: "low", PermissionMode: "plan"},
	}
	for i, expected := range []map[string]any{
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
		if err := a.Send(prompt("hello")); !errors.Is(err, agent.ErrBusy) {
			t.Fatalf("prompt accepted during configuration: %v", err)
		}
		overlapping := make(chan error, 1)
		if a.Configure(patch, replyTo(overlapping)); !errors.Is(<-overlapping, agent.ErrBusy) {
			t.Fatal("overlapping configuration accepted")
		}
		acknowledge(t, a, "unrelated", "success")
		select {
		case err := <-result:
			t.Fatalf("configuration finished before its acknowledgement: %v", err)
		default:
		}
		if len(applied) != i {
			t.Fatalf("unacknowledged settings were reported: %+v", applied)
		}
		acknowledge(t, a, wire.RequestID, "success")
		if !reflect.DeepEqual(applied, expectedSettings[:i+1]) {
			t.Fatalf("acknowledged settings were not reported: %+v", applied)
		}
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("configuration did not finish")
	}
	if err := a.Send(prompt("hello")); err != nil {
		t.Fatalf("configuration kept its reservation: %v", err)
	}
}

func TestConfigureSendsOnlyChangedSettings(t *testing.T) {
	a, input, _ := testAgent()
	result := make(chan error, 1)
	patch := agent.SettingsPatch{Effort: ptr("high")}
	a.Configure(patch, replyTo(result))
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
	if err := <-result; err != nil {
		t.Fatal(err)
	}

	a.Configure(patch, replyTo(result))
	if err := <-result; err != nil || input.Len() != 0 {
		t.Fatalf("configuration without a change was sent to Claude: %v, %s", err, input)
	}
	if err := a.Send(prompt("hello")); err != nil {
		t.Fatalf("configuration without a change kept a reservation: %v", err)
	}
}

func TestConfigureFailure(t *testing.T) {
	for _, ending := range []string{"rejected", "exited"} {
		t.Run(ending, func(t *testing.T) {
			a, input, _ := testAgent()
			result := make(chan error, 1)
			a.Configure(agent.SettingsPatch{Model: ptr("haiku")}, replyTo(result))
			requestID := controlRequestID(t, input)
			switch ending {
			case "rejected":
				output(t, a, `{"type":"control_response","response":{"subtype":"error","request_id":"`+requestID+`","error":"Unsupported model"}}`)
				if err := a.Send(prompt("hello")); err != nil {
					t.Fatalf("failed configuration kept its reservation: %v", err)
				}
			case "exited":
				a.Handle(processExit{})
			}
			err := <-result
			if ending == "rejected" && (err == nil || !strings.Contains(err.Error(), "Unsupported model")) {
				t.Fatalf("Claude's refusal was lost: %v", err)
			}
			if ending == "exited" && !errors.Is(err, agent.ErrExited) {
				t.Fatalf("process exit did not fail configuration: %v", err)
			}
		})
	}
}

func TestPartialConfigurationReportsAppliedSettings(t *testing.T) {
	input := new(bytes.Buffer)
	var applied []agent.Settings
	a := newAgent(newWriter(input), make(chan agent.Event), nil, agent.Options{
		OnUpdate: func(agent.Update) {}, OnConfigured: func(s agent.Settings) { applied = append(applied, s) },
	})
	result := make(chan error, 1)
	a.Configure(agent.SettingsPatch{Model: ptr("sonnet"), Effort: ptr("high")}, replyTo(result))
	acknowledge(t, a, controlRequestID(t, input), "success")
	acknowledge(t, a, controlRequestID(t, input), "error")
	if err := <-result; err == nil || !reflect.DeepEqual(applied, []agent.Settings{{Model: "sonnet"}}) {
		t.Fatalf("a partial configuration reported %v, %+v", err, applied)
	}
}

func TestConfigureTimeoutStopsProcess(t *testing.T) {
	a, input, _ := stoppableAgent(t)
	result := make(chan error, 1)
	a.Configure(agent.SettingsPatch{PermissionMode: ptr("plan")}, replyTo(result))
	requestID := controlRequestID(t, input)

	a.Handle(controlTimeout{"stale"})
	a.Handle(controlTimeout{requestID})
	exit := <-a.Events()
	acknowledge(t, a, requestID, "success")
	select {
	case err := <-result:
		t.Fatalf("configuration ended before the process exited: %v", err)
	default:
	}
	if err := a.Send(prompt("hello")); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("stopping process was released: %v", err)
	}
	a.Handle(exit)
	if err := <-result; !errors.Is(err, agent.ErrExited) {
		t.Fatalf("process exit did not end configuration: %v", err)
	}
}

func TestConfigureRequiresIdle(t *testing.T) {
	for _, busy := range []string{"running", "requires_action", "prompt not taken", "prompt taken"} {
		t.Run(busy, func(t *testing.T) {
			a, _, _ := testAgent()
			switch busy {
			case "prompt not taken":
				if err := a.Send(prompt("hello")); err != nil {
					t.Fatal(err)
				}
			case "prompt taken":
				a, _, _, _ = promptingAgent(t)
			default:
				state(t, a, busy)
			}
			result := make(chan error, 1)
			if a.Configure(agent.SettingsPatch{Model: ptr("haiku")}, replyTo(result)); !errors.Is(<-result, agent.ErrBusy) {
				t.Fatalf("configuration accepted in %s", busy)
			}
		})
	}
}

func TestClaudeReportsPermissionMode(t *testing.T) {
	input := new(bytes.Buffer)
	var adopted []agent.Settings
	a := newAgent(newWriter(input), make(chan agent.Event), nil, agent.Options{
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

	result := make(chan error, 1)
	a.Configure(agent.SettingsPatch{PermissionMode: ptr("plan")}, replyTo(result))
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
	if err := <-result; err != nil || len(adopted) != 2 || adopted[1].PermissionMode != "plan" {
		t.Fatalf("the mode change was not reported exactly once: %v, %+v", err, adopted)
	}
}
