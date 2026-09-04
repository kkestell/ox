package settings

import (
	"strings"
	"testing"
)

func TestMergeFoldsWorkspaceOverGlobalPerField(t *testing.T) {
	global := &Config{
		Model:       pointer("global/model"),
		MaxTokens:   pointer(256),
		Temperature: pointer(0.2),
		Reasoning: &Reasoning{
			Enabled: pointer(true),
			Effort:  pointer("low"),
			Exclude: pointer(true),
		},
		Provider: &Provider{
			Order:    []string{"alpha"},
			Ignore:   []string{"gamma"},
			Sort:     pointer("price"),
			MaxPrice: &MaxPrice{Prompt: pointer(1.0), Completion: pointer(2.0)},
		},
	}
	workspace := &Config{
		Temperature: pointer(0.9),
		Reasoning:   &Reasoning{Effort: pointer("high")},
		Provider: &Provider{
			Ignore:   []string{},
			MaxPrice: &MaxPrice{Completion: pointer(3.0)},
		},
	}

	merged := Merge(global, workspace)
	if *merged.Model != "global/model" || merged.modelSource != SourceGlobal {
		t.Fatalf("model = %v from %q", merged.Model, merged.modelSource)
	}
	if *merged.MaxTokens != 256 || *merged.Temperature != 0.9 {
		t.Fatalf("scalars = %#v", merged)
	}
	// The workspace set one reasoning field; the global layer's others survive.
	if *merged.Reasoning.Effort != "high" || !*merged.Reasoning.Enabled ||
		!*merged.Reasoning.Exclude {
		t.Fatalf("reasoning = %#v", merged.Reasoning)
	}
	if strings.Join(merged.Provider.Order, ",") != "alpha" ||
		*merged.Provider.Sort != "price" {
		t.Fatalf("provider = %#v", merged.Provider)
	}
	// An explicit empty list clears the global one rather than falling through.
	if merged.Provider.Ignore == nil || len(merged.Provider.Ignore) != 0 {
		t.Fatalf("ignore = %#v", merged.Provider.Ignore)
	}
	if *merged.Provider.MaxPrice.Prompt != 1.0 ||
		*merged.Provider.MaxPrice.Completion != 3.0 {
		t.Fatalf("max price = %#v", merged.Provider.MaxPrice)
	}

	if global.Temperature == nil || *global.Temperature != 0.2 ||
		len(global.Provider.Ignore) != 1 {
		t.Fatalf("merge mutated the global layer: %#v", global)
	}
}

func TestMergeKeepsALayerTheOtherOmitsEntirely(t *testing.T) {
	global := &Config{
		Model:     pointer("global/model"),
		Reasoning: &Reasoning{Effort: pointer("low")},
		Provider:  &Provider{Order: []string{"alpha"}},
	}
	merged := Merge(global, &Config{Model: pointer("workspace/model")})
	if *merged.Model != "workspace/model" || merged.modelSource != SourceWorkspace {
		t.Fatalf("model = %v from %q", merged.Model, merged.modelSource)
	}
	if *merged.Reasoning.Effort != "low" ||
		strings.Join(merged.Provider.Order, ",") != "alpha" {
		t.Fatalf("merged = %#v", merged)
	}

	workspaceOnly := Merge(nil, global)
	if *workspaceOnly.Model != "global/model" ||
		workspaceOnly.modelSource != SourceWorkspace {
		t.Fatalf("workspace-only merge = %#v", workspaceOnly)
	}
}

func TestMergeOfTwoAbsentLayersSetsNothing(t *testing.T) {
	merged := Merge(nil, nil)
	if *merged != (Config{}) {
		t.Fatalf("merged = %#v", merged)
	}
}

func pointer[T any](value T) *T {
	return &value
}
