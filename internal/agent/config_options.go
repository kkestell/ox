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
	reasoningDefault = "default"
)

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
	configuration := cloneConfiguration(value.state.configuration)
	history := cloneMessages(value.state.history)
	value.stateMu.Unlock()
	current := currentOptionValue(request.ConfigID, configuration, value.models)
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
		if request.Value != modeCode && request.Value != modePlan {
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
		reset := reasoningDefault
		selections.Reasoning = &reset
	case configReasoning:
		if !reasoningValueAvailable(value.models, configuration.Settings.Model, request.Value) {
			return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(
				jrpc2.InvalidParams, "unknown reasoning value %q", request.Value,
			)
		}
		selected := request.Value
		selections.Reasoning = &selected
	}

	next, err := applySelections(value.activationBase, selections, history, value.models)
	if err != nil {
		return acp.SetSessionConfigOptionResponse{}, jrpc2.Errorf(
			jrpc2.InvalidParams, "%s: %v", request.ConfigID, err,
		)
	}
	options := buildConfigOptions(next, value.models)
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
	configuration := cloneConfiguration(value.state.configuration)
	value.stateMu.Unlock()
	return buildConfigOptions(configuration, value.models)
}

func buildConfigOptions(
	configuration requestConfiguration,
	models []openrouter.Model,
) []acp.SessionConfigOption {
	mode := configuration.Mode
	if mode == "" {
		mode = modeCode
	}
	options := []acp.SessionConfigOption{
		{
			Type: acp.SessionConfigOptionTypeSelect, ID: configMode, Name: "Mode",
			Category: acp.SessionConfigOptionCategoryMode, CurrentValue: mode,
			Options: []acp.SessionConfigSelectOption{
				{Value: modeCode, Name: "Code"},
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
	models = cloneModels(models)
	slices.SortFunc(models, func(left, right openrouter.Model) int {
		return strings.Compare(left.ID, right.ID)
	})
	result := make([]acp.SessionConfigSelectOption, 0, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = model.ID
		}
		result = append(result, acp.SessionConfigSelectOption{Value: model.ID, Name: name})
	}
	return result
}

func currentOptionValue(id string, configuration requestConfiguration, models []openrouter.Model) string {
	for _, option := range buildConfigOptions(configuration, models) {
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

func applySelections(
	base requestConfiguration,
	selections sessionSelections,
	history []openrouter.Message,
	models []openrouter.Model,
) (requestConfiguration, error) {
	if err := validateSelections(selections); err != nil {
		return requestConfiguration{}, err
	}
	configuration := cloneConfiguration(base)
	if selections.Model != "" {
		configuration.Settings.Model = selections.Model
		configuration.Settings.ModelSource = settings.SourceSession
	}
	entry := modelEntry(models, configuration.Settings.Model)
	if entry == nil {
		return requestConfiguration{}, fmt.Errorf("model %q is not in the OpenRouter catalog", configuration.Settings.Model)
	}
	if selections.Reasoning != nil {
		if *selections.Reasoning == reasoningDefault {
			configuration.Settings.Reasoning = nil
		} else {
			reasoning := configuration.Settings.Reasoning
			if reasoning == nil {
				reasoning = &openrouter.Reasoning{}
			} else {
				copy := *reasoning
				reasoning = &copy
			}
			reasoning.Effort = *selections.Reasoning
			configuration.Settings.Reasoning = reasoning
		}
	}
	if selections.Mode == modePlan {
		configuration.Mode = modePlan
		configuration.Tools = planTools(configuration.Tools, configuration.PlanTools)
		configuration.Subagent.Tools = planTools(configuration.Subagent.Tools, configuration.PlanTools)
		allowed := make(map[string]acp.ToolKind, len(configuration.Tools))
		for _, tool := range configuration.Tools {
			allowed[tool.Function.Name] = configuration.ToolKinds[tool.Function.Name]
		}
		configuration.ToolKinds = allowed
	} else {
		configuration.Mode = modeCode
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

func cloneModels(values []openrouter.Model) []openrouter.Model {
	result := make([]openrouter.Model, len(values))
	for index := range values {
		result[index] = values[index]
		result[index].SupportedParameters = slices.Clone(values[index].SupportedParameters)
		result[index].Architecture.InputModalities = slices.Clone(values[index].Architecture.InputModalities)
		result[index].Architecture.OutputModalities = slices.Clone(values[index].Architecture.OutputModalities)
		if values[index].Reasoning != nil {
			reasoning := *values[index].Reasoning
			reasoning.SupportedEfforts = slices.Clone(values[index].Reasoning.SupportedEfforts)
			result[index].Reasoning = &reasoning
		}
	}
	return result
}
