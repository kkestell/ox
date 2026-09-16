package agent

import (
	"context"
	"reflect"
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
		Settings:      settings.Resolved{Model: "test/model"},
		ContextWindow: 1000,
	}
	value := durableTestSession(t, instance, configuration, "turn")
	value.activationBase = configuration
	value.models = models
	value.profiles = testProfiles(t, "test/model", "plain/model")
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

// Auto mode is advertised beside code and plan and exposes code's whole tool
// set, so the only difference between them is whether a call is asked about.
func TestAutoModeIsAdvertisedAndKeepsTheCodeToolSet(t *testing.T) {
	_, value := configOptionSession(t)
	models := []openrouter.Model{{
		ID: "test/model", Name: "Test Model", ContextLength: 1000,
		SupportedParameters: []string{"tools"},
	}}
	base := value.activationBase
	base.Tools = []openrouter.Tool{
		{Type: "function", Function: openrouter.ToolFunction{Name: "read_file"}},
		{Type: "function", Function: openrouter.ToolFunction{Name: "shell"}},
	}
	base.PlanTools = map[string]bool{"read_file": true}

	var offered []string
	for _, option := range buildConfigOptions(modeCode, base, models)[0].Options {
		offered = append(offered, option.Value)
	}
	if !reflect.DeepEqual(offered, []string{modeCode, modeAuto, modePlan}) {
		t.Fatalf("mode options = %#v", offered)
	}

	profiles := testProfiles(t, "test/model")
	code, err := applySelections(base, sessionSelections{}, nil, models, profiles)
	if err != nil {
		t.Fatal(err)
	}
	auto, err := applySelections(base, sessionSelections{Mode: modeAuto}, nil, models, profiles)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applyMode(auto, modeAuto).Tools, code.Tools) {
		t.Fatalf("auto configuration = %#v, code tools = %#v", auto, code.Tools)
	}
	if err := validateSelections(sessionSelections{Mode: modeAuto}); err != nil {
		t.Fatalf("auto selection is invalid: %v", err)
	}
	if err := validateConfiguration(auto); err != nil {
		t.Fatalf("auto configuration is invalid: %v", err)
	}

	plan, err := applySelections(base, sessionSelections{Mode: modePlan}, nil, models, profiles)
	if err != nil {
		t.Fatal(err)
	}
	narrowed := applyMode(plan, modePlan)
	if len(narrowed.Tools) != 1 || narrowed.Tools[0].Function.Name != "read_file" {
		t.Fatalf("plan tools = %#v", narrowed.Tools)
	}
}
