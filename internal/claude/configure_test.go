package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/agent"
)

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
	updates := new(recorder)
	a := newAgent(newWriter(input), make(chan agent.Event), make(chan struct{}), nil, agent.Options{
		OnUpdate: updates.record, OnConfigured: func(s agent.Settings) { applied = append(applied, s) },
	})
	a.Handle(initialized)
	*updates = nil
	decoder := json.NewDecoder(input)
	patch := agent.SettingsPatch{Model: new("haiku"), Effort: new("low"), PermissionMode: new("plan")}
	result := make(chan error, 1)
	a.Configure(patch, replyTo(result))
	held := prompt("hello")

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
		// A prompt sent during the configuration is accepted, and the
		// requests that follow it show it is not written.
		if i == 1 {
			if err := a.Send(held); err != nil {
				t.Fatalf("prompt refused during configuration: %v", err)
			}
			if len(*updates) != 2 || (*updates)[0].Type != "prompt_queued" || (*updates)[0].MessageID != held.MessageID || (*updates)[1].State != "running" {
				t.Fatalf("a prompt sent during configuration was not published: %+v", *updates)
			}
			state(t, a, "idle")
			if a.Snapshot().State != "running" {
				t.Fatal("Claude's idle during configuration released the held prompt")
			}
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
	var wire struct {
		Type string `json:"type"`
		UUID string `json:"uuid"`
	}
	if err := decoder.Decode(&wire); err != nil || wire.Type != "user" || wire.UUID != held.MessageID || decoder.More() {
		t.Fatalf("the last acknowledgement did not write the prompt sent during configuration alone: %v, %+v", err, wire)
	}
}

func TestConfigureSendsOnlyChangedSettings(t *testing.T) {
	a, input, _ := testAgent()
	result := make(chan error, 1)
	patch := agent.SettingsPatch{Effort: new("high")}
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
			a, input, updates := testAgent()
			result := make(chan error, 1)
			a.Configure(agent.SettingsPatch{Model: new("haiku")}, replyTo(result))
			requestID := controlRequestID(t, input)
			input.Reset()
			id, err := sendTestPrompt(a, "hello")
			if err != nil {
				t.Fatal(err)
			}
			*updates = nil
			switch ending {
			case "rejected":
				output(t, a, `{"type":"control_response","response":{"subtype":"error","request_id":"`+requestID+`","error":"Unsupported model"}}`)
				if ids := written(t, input); !slices.Equal(ids, []string{id}) {
					t.Fatalf("a failed configuration did not write the prompt held during it: %v", ids)
				}
			case "exited":
				a.Handle(processExit{})
				if len(*updates) != 2 || !cancelled((*updates)[:1], []string{id}) || (*updates)[1].Type != "exited" || input.Len() != 0 {
					t.Fatalf("the exit did not drop the held prompt and end alone: %+v, %q", *updates, input)
				}
			}
			err = <-result
			if ending == "rejected" && (err == nil || !strings.Contains(err.Error(), "Unsupported model")) {
				t.Fatalf("Claude's refusal was lost: %v", err)
			}
			if ending == "exited" && !errors.Is(err, agent.ErrExited) {
				t.Fatalf("process exit did not fail configuration: %v", err)
			}
		})
	}
}

// An interrupt during a configuration drops the inputs held for it, which
// leaves the agent idle, and writes nothing.
func TestInterruptDuringConfiguration(t *testing.T) {
	a, input, updates := testAgent()
	result := make(chan error, 1)
	a.Configure(agent.SettingsPatch{Model: new("haiku")}, replyTo(result))
	requestID := controlRequestID(t, input)
	input.Reset()
	ids := sendHeld(t, a)
	*updates = nil
	if stop := interrupt(a, updates); !stop.answered || input.Len() != 0 || !cancelledToIdle(*updates, ids) {
		t.Fatalf("an interrupt during configuration wrote %q or did not drop the held inputs: %+v, %+v", input, stop, *updates)
	}
	*updates = nil
	acknowledge(t, a, requestID, "success")
	if err := <-result; err != nil || input.Len() != 0 || len(*updates) != 0 || !a.idle() {
		t.Fatalf("the configuration's end after an interrupt wrote %q, published, or did not release the agent: %v, %+v", input, err, *updates)
	}
}

// A turn Claude starts during a configuration keeps the agent running after
// the acknowledgement, until Claude reports idle.
func TestConfigurationKeepsReportedState(t *testing.T) {
	a, input, updates := testAgent()
	result := make(chan error, 1)
	a.Configure(agent.SettingsPatch{Model: new("haiku")}, replyTo(result))
	requestID := controlRequestID(t, input)
	state(t, a, "running")
	*updates = nil
	// Stop during the configuration stops the turn Claude started.
	stop := interrupt(a, updates)
	if stop.answered || !strings.Contains(input.String(), `"subtype":"interrupt"`) {
		t.Fatalf("an interrupt of Claude's turn during a configuration wrote %q: %+v", input, stop)
	}
	input.Reset()
	acknowledge(t, a, requestID, "success")
	if err := <-result; err != nil || len(*updates) != 0 || a.Snapshot().State != "running" || a.Parkable() || stop.answered {
		t.Fatalf("the acknowledgement did not keep Claude's running state: %v, %+v, %+v, %+v", err, *updates, a.Snapshot(), stop)
	}
	if a.Configure(agent.SettingsPatch{Model: new("sonnet")}, replyTo(result)); !errors.Is(<-result, agent.ErrBusy) {
		t.Fatal("a running agent accepted a configuration")
	}
	if again := interrupt(a, updates); again.answered || !strings.Contains(input.String(), `"subtype":"interrupt"`) {
		t.Fatalf("an interrupt of the running agent wrote %q: %+v", input, again)
	}
	input.Reset()
	state(t, a, "idle")
	if len(*updates) != 1 || (*updates)[0].State != "idle" || !a.Parkable() {
		t.Fatalf("Claude's idle did not release the agent: %+v", *updates)
	}
	if !stop.answered || stop.err != nil || stop.after != 1 {
		t.Fatalf("Claude's idle did not answer the interrupt after its update: %+v", stop)
	}
	if idle := interrupt(a, updates); !idle.answered || input.Len() != 0 {
		t.Fatalf("an interrupt of the idle agent wrote %q or waited: %+v", input, idle)
	}
}

func TestPartialConfigurationReportsAppliedSettings(t *testing.T) {
	input := new(bytes.Buffer)
	var applied []agent.Settings
	a := newAgent(newWriter(input), make(chan agent.Event), make(chan struct{}), nil, agent.Options{
		OnUpdate: func(agent.Update) {}, OnConfigured: func(s agent.Settings) { applied = append(applied, s) },
	})
	a.Handle(initialized)
	result := make(chan error, 1)
	a.Configure(agent.SettingsPatch{Model: new("sonnet"), Effort: new("high")}, replyTo(result))
	acknowledge(t, a, controlRequestID(t, input), "success")
	acknowledge(t, a, controlRequestID(t, input), "error")
	if err := <-result; err == nil || !reflect.DeepEqual(applied, []agent.Settings{{Model: "sonnet"}}) {
		t.Fatalf("a partial configuration reported %v, %+v", err, applied)
	}
}

func TestConfigureTimeoutStopsProcess(t *testing.T) {
	a, input, updates := stoppableAgent(t)
	var reported []agent.Settings
	a.onConfigured = func(s agent.Settings) { reported = append(reported, s) }
	result := make(chan error, 1)
	a.Configure(agent.SettingsPatch{Model: new("haiku"), Effort: new("low"), PermissionMode: new("plan")}, replyTo(result))
	first := controlRequestID(t, input)
	acknowledge(t, a, first, "success")
	second := controlRequestID(t, input)

	a.Handle(controlTimeout{first})
	a.Handle(controlTimeout{"stale"})
	acknowledge(t, a, second, "success")
	if len(reported) != 2 {
		t.Fatalf("a timeout for an acknowledged request stopped the configuration: %+v", reported)
	}
	third := controlRequestID(t, input)

	a.Handle(controlTimeout{third})
	exit := <-a.Events()
	acknowledge(t, a, third, "success")
	select {
	case err := <-result:
		t.Fatalf("configuration ended before the process exited: %v", err)
	default:
	}
	if len(reported) != 2 {
		t.Fatalf("an acknowledgement after the timeout reported settings: %+v", reported)
	}
	id, err := sendTestPrompt(a, "hello")
	if err != nil || input.Len() != 0 {
		t.Fatalf("a prompt sent to a stopping process was refused or written: %v, %q", err, input)
	}
	*updates = nil
	a.Handle(exit)
	if err := <-result; !errors.Is(err, agent.ErrExited) {
		t.Fatalf("process exit did not end configuration: %v", err)
	}
	if !a.Exited() || !cancelled(*updates, []string{id}) {
		t.Fatalf("the exit did not drop the held prompt alone, or the exit Ox asked for was reported: %+v", *updates)
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
			if a.Configure(agent.SettingsPatch{Model: new("haiku")}, replyTo(result)); !errors.Is(<-result, agent.ErrBusy) {
				t.Fatalf("configuration accepted in %s", busy)
			}
		})
	}
}

func TestClaudeReportsPermissionMode(t *testing.T) {
	input := new(bytes.Buffer)
	var adopted []agent.Settings
	a := newAgent(newWriter(input), make(chan agent.Event), make(chan struct{}), nil, agent.Options{
		Settings:     agent.Settings{Model: "haiku", Effort: "low", PermissionMode: "plan"},
		OnUpdate:     func(agent.Update) {},
		OnConfigured: func(s agent.Settings) { adopted = append(adopted, s) },
	})
	a.Handle(initialized)

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
	a.Configure(agent.SettingsPatch{PermissionMode: new("plan")}, replyTo(result))
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
