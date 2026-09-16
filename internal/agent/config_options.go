package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/creachadair/jrpc2"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/settings"
)

const (
	configMode       = "mode"
	configModel      = "model"
	configReasoning  = "reasoning"
	modeCode         = "code"
	modePlan         = "plan"
	modeAuto         = "auto"
	reasoningDefault = "default"
)

func validMode(value string) bool {
	return value == modeCode || value == modeAuto || value == modePlan
}

func (a *Agent) SetSessionConfigOption(
	ctx context.Context,
	request acp.SetSessionConfigOptionRequest,
) (acp.SetSessionConfigOptionResponse, error) {
	if err := request.Validate(); err != nil {
		return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	value := a.findSession(request.SessionID)
	if value == nil {
		return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "unknown session")
	}
	release, err := value.claimConfigChange()
	if err != nil {
		return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(jrpc2.InvalidParams, "%v", err)
	}
	defer release()

	value.configMu.Lock()
	defer value.configMu.Unlock()
	value.stateMu.Lock()
	selections := cloneSelections(value.state.selections)
	mode := value.state.mode()
	configuration := cloneConfiguration(value.state.configuration)
	history := cloneMessages(value.state.history)
	value.stateMu.Unlock()
	current := currentOptionValue(request.ConfigID, mode, configuration, value.models)
	if current == "" {
		return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(
			jrpc2.InvalidParams, "unknown session configuration option %q", request.ConfigID,
		)
	}
	if current == request.Value {
		return acp.SetSessionConfigOptionResponse{ConfigOptions: a.configOptions(value)}, nil
	}

	switch request.ConfigID {
	case configMode:
		if !validMode(request.Value) {
			return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(
				jrpc2.InvalidParams, "unknown mode %q", request.Value,
			)
		}
		if request.Value == modeCode {
			selections.Mode = ""
		} else {
			selections.Mode = request.Value
		}
	case configModel:
		if modelEntry(value.models, request.Value) == nil {
			return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(
				jrpc2.InvalidParams, "unknown model %q", request.Value,
			)
		}
		selections.Model = request.Value
		// A profile carries its own reasoning, so a model change drops the
		// override rather than carrying one model's effort onto another.
		selections.Reasoning = nil
	case configReasoning:
		if !reasoningValueAvailable(value.models, configuration.Settings.Model, request.Value) {
			return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(
				jrpc2.InvalidParams, "unknown reasoning value %q", request.Value,
			)
		}
		if request.Value == reasoningDefault {
			selections.Reasoning = nil
		} else {
			selected := request.Value
			selections.Reasoning = &selected
		}
	}

	next, err := applySelections(
		value.activationBase, selections, history, value.models, value.profiles,
	)
	if err != nil {
		return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(
			jrpc2.InvalidParams, "%s: %v", request.ConfigID, err,
		)
	}
	options := buildConfigOptions(selectedMode(selections), next, value.models)
	change := optionChanged{
		Selections: selections, Configuration: next, Options: options,
	}
	if err := a.commit(value, recordOptionChanged, change); err != nil {
		return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("persist session configuration option: %w", err)
	}
	update := acp.ConfigOptionUpdate{
		SessionUpdate: acp.SessionUpdateConfigOptionUpdate,
		ConfigOptions: cloneConfigOptions(options),
	}
	if err := jrpc2.ServerFromContext(ctx).Notify(ctx, "session/update", acp.SessionNotification{
		SessionID: value.id,
		Update:    update,
	}); err != nil {
		return acp.SetSessionConfigOptionResponse{}, fmt.Errorf("send configuration option update: %w", err)
	}
	return acp.SetSessionConfigOptionResponse{ConfigOptions: options}, nil
}

func (a *Agent) configOptions(value *session) []acp.SessionConfigOption {
	value.stateMu.Lock()
	mode := value.state.mode()
	configuration := cloneConfiguration(value.state.configuration)
	value.stateMu.Unlock()
	return buildConfigOptions(mode, configuration, value.models)
}

func selectedMode(selections sessionSelections) string {
	if selections.Mode == "" {
		return modeCode
	}
	return selections.Mode
}

func buildConfigOptions(
	mode string,
	configuration requestConfiguration,
	models []openrouter.Model,
) []acp.SessionConfigOption {
	options := []acp.SessionConfigOption{
		{
			Type: acp.SessionConfigOptionTypeSelect, ID: configMode, Name: "Mode",
			Category: acp.SessionConfigOptionCategoryMode, CurrentValue: mode,
			Options: []acp.SessionConfigSelectOption{
				{Value: modeCode, Name: "Code"},
				{Value: modeAuto, Name: "Auto"},
				{Value: modePlan, Name: "Plan"},
			},
		},
		{
			Type: acp.SessionConfigOptionTypeSelect, ID: configModel, Name: "Model",
			Category:     acp.SessionConfigOptionCategoryModel,
			CurrentValue: configuration.Settings.Model,
			Options:      modelOptions(models),
		},
	}
	entry := modelEntry(models, configuration.Settings.Model)
	if entry != nil && entry.Reasoning != nil && len(entry.Reasoning.SupportedEfforts) > 0 {
		current := reasoningDefault
		if configuration.Settings.Reasoning != nil && configuration.Settings.Reasoning.Effort != "" {
			current = configuration.Settings.Reasoning.Effort
		}
		reasoning := []acp.SessionConfigSelectOption{{Value: reasoningDefault, Name: "Default"}}
		for _, effort := range entry.Reasoning.SupportedEfforts {
			if effort == "" {
				continue
			}
			reasoning = append(reasoning, acp.SessionConfigSelectOption{
				Value: effort, Name: strings.ToUpper(effort[:1]) + effort[1:],
			})
		}
		options = append(options, acp.SessionConfigOption{
			Type: acp.SessionConfigOptionTypeSelect, ID: configReasoning,
			Name: "Reasoning", Category: acp.SessionConfigOptionCategoryThoughtLevel,
			CurrentValue: current, Options: reasoning,
		})
	}
	return options
}

func modelOptions(models []openrouter.Model) []acp.SessionConfigSelectOption {
	result := make([]acp.SessionConfigSelectOption, 0, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = model.ID
		}
		result = append(result, acp.SessionConfigSelectOption{Value: model.ID, Name: name})
	}
	// Sorting the rendered options leaves the catalog untouched, so it can stay
	// frozen rather than be copied for every option list.
	slices.SortFunc(result, func(left, right acp.SessionConfigSelectOption) int {
		return strings.Compare(left.Value, right.Value)
	})
	return result
}

func currentOptionValue(
	id, mode string,
	configuration requestConfiguration,
	models []openrouter.Model,
) string {
	for _, option := range buildConfigOptions(mode, configuration, models) {
		if option.ID == id {
			return option.CurrentValue
		}
	}
	return ""
}

func reasoningValueAvailable(models []openrouter.Model, model, value string) bool {
	if value == reasoningDefault {
		return true
	}
	entry := modelEntry(models, model)
	return entry != nil && entry.Reasoning != nil && slices.Contains(entry.Reasoning.SupportedEfforts, value)
}

func modelEntry(models []openrouter.Model, id string) *openrouter.Model {
	for index := range models {
		if models[index].ID == id {
			return &models[index]
		}
	}
	return nil
}

// applySelections folds a session's durable choices over the configuration its
// activation resolved. Choosing a model replaces the whole request profile, so
// no provider routing, sampling, or output limit survives from the model that
// was selected before it.
func applySelections(
	base requestConfiguration,
	selections sessionSelections,
	history []openrouter.Message,
	models []openrouter.Model,
	profiles settings.Profiles,
) (requestConfiguration, error) {
	if err := validateSelections(selections); err != nil {
		return requestConfiguration{}, err
	}
	configuration := cloneConfiguration(base)
	if selections.Model != "" {
		profile, err := profiles.Select(selections.Model, settings.SourceSession)
		if err != nil {
			return requestConfiguration{}, err
		}
		configuration.Settings = profile
	}
	entry := modelEntry(models, configuration.Settings.Model)
	if entry == nil {
		return requestConfiguration{}, fmt.Errorf("model %q is not in the OpenRouter catalog", configuration.Settings.Model)
	}
	// An omitted reasoning selection leaves the profile's own reasoning in place,
	// which is what "default" means once a model carries its own settings.
	if selections.Reasoning != nil && *selections.Reasoning != reasoningDefault {
		reasoning := &openrouter.Reasoning{}
		if configuration.Settings.Reasoning != nil {
			selected := *configuration.Settings.Reasoning
			reasoning = &selected
		}
		reasoning.Effort = *selections.Reasoning
		configuration.Settings.Reasoning = reasoning
	}
	configuration.ContextWindow = entry.ContextWindow()
	if configuration.ContextWindow <= 0 {
		return requestConfiguration{}, fmt.Errorf(
			"model %q has no positive context length in the OpenRouter catalog; choose a model with a published context length",
			entry.ID,
		)
	}
	if err := settings.Validate(entry, configuration.Settings, settings.Compatibility{
		Tools:      len(configuration.Tools) > 0,
		Modalities: retainedModalities(history),
	}); err != nil {
		return requestConfiguration{}, err
	}
	return configuration, nil
}

// applyMode narrows a frozen turn configuration to what the session's current
// mode permits. Only plan changes the declared tool set, so code and auto
// return the configuration unchanged.
func applyMode(configuration requestConfiguration, mode string) requestConfiguration {
	if mode != modePlan {
		return configuration
	}
	configuration.Tools = planTools(configuration.Tools, configuration.PlanTools)
	kinds := make(map[string]acp.ToolKind, len(configuration.Tools))
	for _, tool := range configuration.Tools {
		kinds[tool.Function.Name] = configuration.ToolKinds[tool.Function.Name]
	}
	configuration.ToolKinds = kinds
	return configuration
}

func planTools(tools []openrouter.Tool, allowed map[string]bool) []openrouter.Tool {
	return slices.DeleteFunc(cloneTools(tools), func(tool openrouter.Tool) bool {
		return !allowed[tool.Function.Name]
	})
}

func constrainedToolSet(base toolSet, declarations []openrouter.Tool) toolSet {
	allowed := make(map[string]struct{}, len(declarations))
	for _, declaration := range declarations {
		allowed[declaration.Function.Name] = struct{}{}
	}
	tools := slices.DeleteFunc(slices.Clone(base.tools), func(tool Tool) bool {
		_, ok := allowed[tool.Name]
		return !ok
	})
	// The tools are a subset of an already-validated set, so no name can
	// collide here.
	result, err := newToolSet(tools)
	if err != nil {
		panic(err)
	}
	return result
}

func retainedModalities(history []openrouter.Message) []string {
	var result []string
	for _, message := range history {
		for _, content := range message.Content {
			switch content.Type {
			case "image_url":
				result = append(result, "image")
			case "input_audio":
				result = append(result, "audio")
			}
		}
	}
	return result
}
