package settings

// Merge folds workspace over global into a new Config, mutating neither input.
// Every setting is workspace-wins-per-field: a field the workspace sets replaces
// the global one, a field it omits falls through, and the nested reasoning,
// provider, and max_price objects merge per field rather than wholesale. Slices
// keep the nil-versus-empty distinction, so `"ignore": []` in the workspace
// clears a global list instead of falling through to it.
func Merge(global, workspace *Config) *Config {
	if global == nil {
		global = &Config{}
	}
	if workspace == nil {
		workspace = &Config{}
	}
	merged := &Config{
		Model:       pick(workspace.Model, global.Model),
		MaxTokens:   pick(workspace.MaxTokens, global.MaxTokens),
		Temperature: pick(workspace.Temperature, global.Temperature),
		Reasoning:   mergeReasoning(global.Reasoning, workspace.Reasoning),
		Provider:    mergeProvider(global.Provider, workspace.Provider),
	}
	switch {
	case workspace.Model != nil:
		merged.modelSource = SourceWorkspace
	case global.Model != nil:
		merged.modelSource = SourceGlobal
	}
	return merged
}

func mergeReasoning(global, workspace *Reasoning) *Reasoning {
	if global == nil {
		return workspace
	}
	if workspace == nil {
		return global
	}
	return &Reasoning{
		Enabled: pick(workspace.Enabled, global.Enabled),
		Effort:  pick(workspace.Effort, global.Effort),
		Exclude: pick(workspace.Exclude, global.Exclude),
	}
}

func mergeProvider(global, workspace *Provider) *Provider {
	if global == nil {
		return workspace
	}
	if workspace == nil {
		return global
	}
	return &Provider{
		Order:          pickSlice(workspace.Order, global.Order),
		Only:           pickSlice(workspace.Only, global.Only),
		Ignore:         pickSlice(workspace.Ignore, global.Ignore),
		Quantizations:  pickSlice(workspace.Quantizations, global.Quantizations),
		Sort:           pick(workspace.Sort, global.Sort),
		DataCollection: pick(workspace.DataCollection, global.DataCollection),
		AllowFallbacks: pick(workspace.AllowFallbacks, global.AllowFallbacks),
		MaxPrice:       mergeMaxPrice(global.MaxPrice, workspace.MaxPrice),
	}
}

func mergeMaxPrice(global, workspace *MaxPrice) *MaxPrice {
	if global == nil {
		return workspace
	}
	if workspace == nil {
		return global
	}
	return &MaxPrice{
		Prompt:     pick(workspace.Prompt, global.Prompt),
		Completion: pick(workspace.Completion, global.Completion),
	}
}

func pick[T any](workspace, global *T) *T {
	if workspace != nil {
		return workspace
	}
	return global
}

// pickSlice prefers the workspace slice when it is non-nil, so an explicit empty
// array overrides rather than falls through.
func pickSlice[T any](workspace, global []T) []T {
	if workspace != nil {
		return workspace
	}
	return global
}
