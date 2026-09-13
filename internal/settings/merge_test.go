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

// TestMergeResultSharesNothingWithItsInputs pins the non-mutation contract as a
// property of the merge rather than of its callers. A result that aliased an
// input would let a later change to one session's settings reach another's.
func TestMergeResultSharesNothingWithItsInputs(t *testing.T) {
	enabled := true
	effort := "high"
	prompt := 1.0
	build := func() *Config {
		return &Config{
			Model:     pointerTo("author/model"),
			MaxTokens: pointerTo(1024),
			Reasoning: &Reasoning{Enabled: &enabled, Effort: &effort},
			Provider: &Provider{
				Order:    []string{"alpha"},
				MaxPrice: &MaxPrice{Prompt: &prompt},
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
			if merged.Model == nil || merged.Reasoning == nil || merged.Provider == nil {
				t.Fatalf("merged = %#v", merged)
			}

			*merged.Model = "changed/model"
			*merged.MaxTokens = 1
			*merged.Reasoning.Enabled = false
			*merged.Reasoning.Effort = "low"
			merged.Provider.Order[0] = "omega"
			*merged.Provider.MaxPrice.Prompt = 999

			for name, input := range map[string]*Config{
				"global":    test.global,
				"workspace": test.workspace,
			} {
				if input == nil {
					continue
				}
				if *input.Model != "author/model" || *input.MaxTokens != 1024 {
					t.Fatalf("%s scalars changed: %q, %d", name, *input.Model, *input.MaxTokens)
				}
				if !*input.Reasoning.Enabled || *input.Reasoning.Effort != "high" {
					t.Fatalf("%s reasoning changed: %#v", name, *input.Reasoning)
				}
				if input.Provider.Order[0] != "alpha" || *input.Provider.MaxPrice.Prompt != 1 {
					t.Fatalf("%s provider changed: %#v", name, *input.Provider)
				}
			}
		})
	}
}

// TestMergeKeepsTheEmptySliceOverride guards the distinction the clone could
// have flattened: an explicit empty workspace list clears a global one.
func TestMergeKeepsTheEmptySliceOverride(t *testing.T) {
	merged := Merge(
		&Config{Provider: &Provider{Order: []string{"alpha"}, Only: []string{"beta"}}},
		&Config{Provider: &Provider{Order: []string{}}},
	)
	if merged.Provider.Order == nil || len(merged.Provider.Order) != 0 {
		t.Fatalf("cleared list = %#v, want an empty non-nil slice", merged.Provider.Order)
	}
	if len(merged.Provider.Only) != 1 || merged.Provider.Only[0] != "beta" {
		t.Fatalf("omitted list = %#v, want the global value", merged.Provider.Only)
	}
}

func pointerTo[T any](value T) *T {
	return &value
}
