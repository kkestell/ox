package settings

import (
	"strings"
	"testing"
)

func TestMergeFoldsWorkspaceOverGlobalPerField(t *testing.T) {
	global := &Config{
		DefaultModel: pointer("global/model"),
		Models: map[string]ModelConfig{
			"global/model": {
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
			},
		},
	}
	workspace := &Config{
		Models: map[string]ModelConfig{
			"global/model": {
				Temperature: pointer(0.9),
				Reasoning:   &Reasoning{Effort: pointer("high")},
				Provider: &Provider{
					Ignore:   []string{},
					MaxPrice: &MaxPrice{Completion: pointer(3.0)},
				},
			},
		},
	}

	merged := Merge(global, workspace)
	if *merged.DefaultModel != "global/model" || merged.defaultSource != SourceGlobal {
		t.Fatalf("default model = %v from %q", merged.DefaultModel, merged.defaultSource)
	}
	profile := merged.Models["global/model"]
	if *profile.MaxTokens != 256 || *profile.Temperature != 0.9 {
		t.Fatalf("scalars = %#v", profile)
	}
	// The workspace set one reasoning field; the global layer's others survive.
	if *profile.Reasoning.Effort != "high" || !*profile.Reasoning.Enabled ||
		!*profile.Reasoning.Exclude {
		t.Fatalf("reasoning = %#v", profile.Reasoning)
	}
	if strings.Join(profile.Provider.Order, ",") != "alpha" ||
		*profile.Provider.Sort != "price" {
		t.Fatalf("provider = %#v", profile.Provider)
	}
	// An explicit empty list clears the global one rather than falling through.
	if profile.Provider.Ignore == nil || len(profile.Provider.Ignore) != 0 {
		t.Fatalf("ignore = %#v", profile.Provider.Ignore)
	}
	if *profile.Provider.MaxPrice.Prompt != 1.0 ||
		*profile.Provider.MaxPrice.Completion != 3.0 {
		t.Fatalf("max price = %#v", profile.Provider.MaxPrice)
	}

	original := global.Models["global/model"]
	if original.Temperature == nil || *original.Temperature != 0.2 ||
		len(original.Provider.Ignore) != 1 {
		t.Fatalf("merge mutated the global layer: %#v", original)
	}
}

func TestMergeKeepsAProfileOnlyOneLayerDefines(t *testing.T) {
	global := &Config{
		DefaultModel: pointer("global/model"),
		Models: map[string]ModelConfig{
			"global/model": {Reasoning: &Reasoning{Effort: pointer("low")}},
			"shared/model": {Provider: &Provider{Order: []string{"alpha"}}},
		},
	}
	workspace := &Config{
		DefaultModel: pointer("workspace/model"),
		Models: map[string]ModelConfig{
			"workspace/model": {MaxTokens: pointer(64)},
			"shared/model":    {Temperature: pointer(0.5)},
		},
	}

	merged := Merge(global, workspace)
	if *merged.DefaultModel != "workspace/model" || merged.defaultSource != SourceWorkspace {
		t.Fatalf("default model = %v from %q", merged.DefaultModel, merged.defaultSource)
	}
	if len(merged.Models) != 3 {
		t.Fatalf("merged profiles = %#v", merged.Models)
	}
	if *merged.Models["global/model"].Reasoning.Effort != "low" {
		t.Fatalf("global-only profile = %#v", merged.Models["global/model"])
	}
	if *merged.Models["workspace/model"].MaxTokens != 64 {
		t.Fatalf("workspace-only profile = %#v", merged.Models["workspace/model"])
	}
	shared := merged.Models["shared/model"]
	if *shared.Temperature != 0.5 || strings.Join(shared.Provider.Order, ",") != "alpha" {
		t.Fatalf("shared profile = %#v", shared)
	}

	workspaceOnly := Merge(nil, global)
	if *workspaceOnly.DefaultModel != "global/model" ||
		workspaceOnly.defaultSource != SourceWorkspace {
		t.Fatalf("workspace-only merge = %#v", workspaceOnly)
	}
}

func TestMergeOfTwoAbsentLayersSetsNothing(t *testing.T) {
	merged := Merge(nil, nil)
	if merged.DefaultModel != nil || merged.Models != nil || merged.defaultSource != "" {
		t.Fatalf("merged = %#v", merged)
	}
}

// TestMergeResultSharesNothingWithItsInputs pins the non-mutation contract as a
// property of the merge rather than of its callers. A result that aliased an
// input would let a later change to one session's settings reach another's.
func TestMergeResultSharesNothingWithItsInputs(t *testing.T) {
	enabled := true
	effort := "high"
	prompt := 1.0
	build := func() *Config {
		return &Config{
			DefaultModel: pointer("author/model"),
			Models: map[string]ModelConfig{
				"author/model": {
					MaxTokens: pointer(1024),
					Reasoning: &Reasoning{Enabled: &enabled, Effort: &effort},
					Provider: &Provider{
						Order:    []string{"alpha"},
						MaxPrice: &MaxPrice{Prompt: &prompt},
					},
				},
			},
		}
	}

	for _, test := range []struct {
		name      string
		global    *Config
		workspace *Config
	}{
		{name: "both layers", global: build(), workspace: build()},
		{name: "workspace only", workspace: build()},
		{name: "global only", global: build()},
	} {
		t.Run(test.name, func(t *testing.T) {
			merged := Merge(test.global, test.workspace)
			profile := merged.Models["author/model"]
			if merged.DefaultModel == nil || profile.Reasoning == nil || profile.Provider == nil {
				t.Fatalf("merged = %#v", merged)
			}

			*merged.DefaultModel = "changed/model"
			*profile.MaxTokens = 1
			*profile.Reasoning.Enabled = false
			*profile.Reasoning.Effort = "low"
			profile.Provider.Order[0] = "omega"
			*profile.Provider.MaxPrice.Prompt = 999
			delete(merged.Models, "author/model")

			for name, input := range map[string]*Config{
				"global":    test.global,
				"workspace": test.workspace,
			} {
				if input == nil {
					continue
				}
				original, present := input.Models["author/model"]
				if !present {
					t.Fatalf("%s lost its profile", name)
				}
				if *input.DefaultModel != "author/model" || *original.MaxTokens != 1024 {
					t.Fatalf("%s scalars changed: %q, %d", name, *input.DefaultModel, *original.MaxTokens)
				}
				if !*original.Reasoning.Enabled || *original.Reasoning.Effort != "high" {
					t.Fatalf("%s reasoning changed: %#v", name, *original.Reasoning)
				}
				if original.Provider.Order[0] != "alpha" || *original.Provider.MaxPrice.Prompt != 1 {
					t.Fatalf("%s provider changed: %#v", name, *original.Provider)
				}
			}
		})
	}
}

// TestMergeKeepsTheEmptySliceOverride guards the distinction the clone could
// have flattened: an explicit empty workspace list clears a global one.
func TestMergeKeepsTheEmptySliceOverride(t *testing.T) {
	merged := Merge(
		&Config{Models: map[string]ModelConfig{
			"test/model": {Provider: &Provider{Order: []string{"alpha"}, Only: []string{"beta"}}},
		}},
		&Config{Models: map[string]ModelConfig{
			"test/model": {Provider: &Provider{Order: []string{}}},
		}},
	)
	provider := merged.Models["test/model"].Provider
	if provider.Order == nil || len(provider.Order) != 0 {
		t.Fatalf("cleared list = %#v, want an empty non-nil slice", provider.Order)
	}
	if len(provider.Only) != 1 || provider.Only[0] != "beta" {
		t.Fatalf("omitted list = %#v, want the global value", provider.Only)
	}
}

func pointer[T any](value T) *T {
	return &value
}
