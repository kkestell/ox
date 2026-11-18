package agents

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func configurationProcess(t *testing.T) (*process, *json.Decoder) {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { reader.Close(); writer.Close() })
	return &process{stdin: writer, state: "idle", done: make(chan struct{})}, json.NewDecoder(reader)
}

func configurationResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("configuration did not finish")
		return nil
	}
}

func TestConfigureWaitsForAcknowledgements(t *testing.T) {
	p, decoder := configurationProcess(t)
	model, effort, mode := "haiku", "low", "plan"
	result := make(chan error, 1)
	go func() { result <- p.configure(Configuration{Model: &model, Effort: &effort, PermissionMode: &mode}) }()

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
		if _, err := p.send("client", "hello", nil); !errors.Is(err, ErrBusy) {
			t.Fatalf("prompt accepted during configuration: %v", err)
		}
		if err := p.configure(Configuration{Model: &model}); !errors.Is(err, ErrBusy) {
			t.Fatalf("overlapping configuration accepted: %v", err)
		}
		p.handleOutput(claudeOutput{Type: "control_response", Response: controlResponse{RequestID: "unrelated", Subtype: "success"}})
		select {
		case err := <-result:
			t.Fatalf("configuration finished before its acknowledgement: %v", err)
		default:
		}
		p.handleOutput(claudeOutput{Type: "control_response", Response: controlResponse{RequestID: wire.RequestID, Subtype: "success"}})
	}
	if err := configurationResult(t, result); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureFailureAndEffortReset(t *testing.T) {
	for _, outcome := range []string{"rejected", "exited"} {
		t.Run(outcome, func(t *testing.T) {
			p, decoder := configurationProcess(t)
			effort, mode := "", "plan"
			result := make(chan error, 1)
			go func() { result <- p.configure(Configuration{Effort: &effort, PermissionMode: &mode}) }()
			var wire struct {
				RequestID string `json:"request_id"`
				Request   struct {
					Settings map[string]any `json:"settings"`
				} `json:"request"`
			}
			if err := decoder.Decode(&wire); err != nil {
				t.Fatal(err)
			}
			if value, present := wire.Request.Settings["effortLevel"]; !present || value != nil {
				t.Fatalf("empty effort did not reset the override: %+v", wire)
			}
			if outcome == "rejected" {
				p.handleOutput(claudeOutput{Type: "control_response", Response: controlResponse{RequestID: wire.RequestID, Subtype: "error", Error: "Unsupported effort"}})
			} else {
				p.finish(nil)
				close(p.done)
			}
			err := configurationResult(t, result)
			if outcome == "rejected" && (err == nil || !strings.Contains(err.Error(), "Unsupported effort")) {
				t.Fatalf("Claude's refusal was lost: %v", err)
			}
			if outcome == "exited" && !errors.Is(err, ErrNotFound) {
				t.Fatalf("process exit did not fail configuration: %v", err)
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.configuring || p.control != nil {
				t.Fatal("failed configuration kept its reservation")
			}
		})
	}
}

func TestConfigureRequiresIdle(t *testing.T) {
	for _, state := range []string{"running", "requires_action", "idle", "exited"} {
		t.Run(state, func(t *testing.T) {
			p := &process{state: state}
			if state == "idle" {
				p.prompt = &submission{}
			}
			want := ErrBusy
			if state == "exited" {
				want = ErrNotFound
			}
			if err := p.configure(Configuration{}); !errors.Is(err, want) {
				t.Fatalf("unexpected configuration result with state %s: %v", state, err)
			}
		})
	}
}
