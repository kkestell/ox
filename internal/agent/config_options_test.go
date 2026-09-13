package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
)

// configOptionSession registers a session whose catalog offers one model with
// reasoning efforts and one without, so every refusal the method can produce is
// reachable.
func configOptionSession(t *testing.T) (*Agent, *session) {
	t.Helper()
	instance, err := New(Config{Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	models := []openrouter.Model{
		{
			ID: "test/model", Name: "Test Model", ContextLength: 1000,
			Reasoning: &openrouter.ModelReasoning{SupportedEfforts: []string{"low", "high"}},
		},
		{ID: "plain/model", Name: "Plain Model", ContextLength: 1000},
	}
	configuration := requestConfiguration{
		Mode:          modeCode,
		Settings:      settings.Resolved{Model: "test/model"},
		ContextWindow: 1000,
	}
	value := durableTestSession(t, instance, configuration, "turn")
	value.activationBase = cloneConfiguration(configuration)
	value.models = models
	instance.sessionsMu.Lock()
	instance.sessions[value.id] = value
	instance.sessionsMu.Unlock()
	return instance, value
}

func TestSetSessionConfigOptionRefusesUnknownTargets(t *testing.T) {
	instance, value := configOptionSession(t)
	tests := []struct {
		name      string
		sessionID string
		configID  string
		value     string
		want      string
	}{
		{
			name: "unknown session", sessionID: "missing",
			configID: configMode, value: modePlan, want: "unknown session",
		},
		{
			name: "unknown option", sessionID: value.id,
			configID: "temperature", value: "0.5",
			want: `unknown session configuration option "temperature"`,
		},
		{
			name: "unknown mode", sessionID: value.id,
			configID: configMode, value: "review", want: `unknown mode "review"`,
		},
		{
			name: "unknown model", sessionID: value.id,
			configID: configModel, value: "absent/model",
			want: `unknown model "absent/model"`,
		},
		{
			name: "unknown reasoning value", sessionID: value.id,
			configID: configReasoning, value: "extreme",
			want: `unknown reasoning value "extreme"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := instance.SetSessionConfigOption(
				context.Background(),
				acp.SetSessionConfigOptionRequest{
					SessionID: test.sessionID, ConfigID: test.configID, Value: test.value,
				},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// A model without reasoning support has no reasoning option at all, so setting
// one is an unknown option rather than an unknown value.
func TestSetSessionConfigOptionRefusesReasoningForAModelWithoutIt(t *testing.T) {
	instance, value := configOptionSession(t)
	value.stateMu.Lock()
	value.state.configuration.Settings.Model = "plain/model"
	value.stateMu.Unlock()

	_, err := instance.SetSessionConfigOption(
		context.Background(),
		acp.SetSessionConfigOptionRequest{
			SessionID: value.id, ConfigID: configReasoning, Value: "low",
		},
	)
	if err == nil || !strings.Contains(err.Error(), `unknown session configuration option "reasoning"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestSetSessionConfigOptionReturnsOptionsWhenTheValueIsUnchanged(t *testing.T) {
	instance, value := configOptionSession(t)
	before := len(value.snapshot().records)

	response, err := instance.SetSessionConfigOption(
		context.Background(),
		acp.SetSessionConfigOptionRequest{
			SessionID: value.id, ConfigID: configMode, Value: modeCode,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if optionValue(response.ConfigOptions, configMode) != modeCode {
		t.Fatalf("options = %#v", response.ConfigOptions)
	}
	if after := len(value.snapshot().records); after != before {
		t.Fatalf("records = %d, want %d", after, before)
	}
}

func TestSetSessionConfigOptionPersistenceFailureLeavesTheConfigurationAlone(t *testing.T) {
	instance, value := configOptionSession(t)
	value.log.close()

	_, err := instance.SetSessionConfigOption(
		context.Background(),
		acp.SetSessionConfigOptionRequest{
			SessionID: value.id, ConfigID: configModel, Value: "plain/model",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "persist session configuration option") {
		t.Fatalf("error = %v", err)
	}
	state := value.snapshot()
	if state.configuration.Settings.Model != "test/model" {
		t.Fatalf("model = %q, want unchanged", state.configuration.Settings.Model)
	}
	if state.selections.Model != "" {
		t.Fatalf("selections = %#v, want unchanged", state.selections)
	}
}

func optionValue(options []acp.SessionConfigOption, id string) string {
	for _, option := range options {
		if option.ID == id {
			return option.CurrentValue
		}
	}
	return ""
}
