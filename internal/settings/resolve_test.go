package settings

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveAppliesModelPrecedence(t *testing.T) {
	global := &Config{Model: pointer("global/model")}
	workspace := &Config{Model: pointer("workspace/model")}

	if _, err := Resolve(Merge(nil, nil), ""); !errors.Is(err, ErrNoModel) {
		t.Fatalf("error = %v, want ErrNoModel", err)
	}
	resolved, err := Resolve(Merge(global, nil), "")
	if err != nil || resolved.Model != "global/model" ||
		resolved.ModelSource != SourceGlobal {
		t.Fatalf("global model = %#v, %v", resolved, err)
	}
	resolved, err = Resolve(Merge(global, workspace), "")
	if err != nil || resolved.Model != "workspace/model" ||
		resolved.ModelSource != SourceWorkspace {
		t.Fatalf("workspace model = %#v, %v", resolved, err)
	}
	resolved, err = Resolve(Merge(global, workspace), " environment/model ")
	if err != nil || resolved.Model != "environment/model" ||
		resolved.ModelSource != SourceEnvironment {
		t.Fatalf("overridden model = %#v, %v", resolved, err)
	}
	if _, err := Resolve(&Config{Model: pointer("  ")}, ""); err == nil ||
		!strings.Contains(err.Error(), "model") {
		t.Fatalf("blank model error = %v", err)
	}
}

func TestResolveAcceptsTheEdgesOfEveryRangeAndRejectsWhatIsOutside(t *testing.T) {
	for name, config := range map[string]*Config{
		"lowest temperature":  {Temperature: pointer(0.0)},
		"highest temperature": {Temperature: pointer(2.0)},
		"one token":           {MaxTokens: pointer(1)},
		"free prompt":         {Provider: &Provider{MaxPrice: &MaxPrice{Prompt: pointer(0.0)}}},
	} {
		config.Model = pointer("vendor/model")
		if _, err := Resolve(config, ""); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for name, test := range map[string]struct {
		config   *Config
		contains string
	}{
		"cold": {
			config:   &Config{Temperature: pointer(-0.1)},
			contains: "temperature",
		},
		"hot": {
			config:   &Config{Temperature: pointer(2.1)},
			contains: "temperature",
		},
		"no tokens": {
			config:   &Config{MaxTokens: pointer(0)},
			contains: "max_tokens",
		},
		"negative tokens": {
			config:   &Config{MaxTokens: pointer(-1)},
			contains: "max_tokens",
		},
		"negative price": {
			config: &Config{Provider: &Provider{
				MaxPrice: &MaxPrice{Prompt: pointer(-1.0)},
			}},
			contains: "max_price.prompt",
		},
		"blank effort": {
			config:   &Config{Reasoning: &Reasoning{Effort: pointer(" ")}},
			contains: "reasoning.effort",
		},
		"blank order entry": {
			config:   &Config{Provider: &Provider{Order: []string{"alpha", ""}}},
			contains: "provider.order",
		},
		"blank only entry": {
			config:   &Config{Provider: &Provider{Only: []string{" "}}},
			contains: "provider.only",
		},
		"blank ignore entry": {
			config:   &Config{Provider: &Provider{Ignore: []string{""}}},
			contains: "provider.ignore",
		},
		"blank quantization": {
			config:   &Config{Provider: &Provider{Quantizations: []string{""}}},
			contains: "provider.quantizations",
		},
	} {
		t.Run(name, func(t *testing.T) {
			test.config.Model = pointer("vendor/model")
			resolved, err := Resolve(test.config, "")
			if err == nil {
				t.Fatalf("resolved = %#v", resolved)
			}
			if !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want mention of %q", err, test.contains)
			}
		})
	}
}

func TestResolveMapsTheSettingsOntoTheWireTypes(t *testing.T) {
	resolved, err := Resolve(&Config{
		Model:       pointer("vendor/model"),
		MaxTokens:   pointer(256),
		Temperature: pointer(0.5),
		Reasoning: &Reasoning{
			Enabled: pointer(true),
			Effort:  pointer("high"),
			Exclude: pointer(false),
		},
		Provider: &Provider{
			Order:          []string{"alpha"},
			Sort:           pointer("throughput"),
			DataCollection: pointer("deny"),
			AllowFallbacks: pointer(false),
			MaxPrice:       &MaxPrice{Prompt: pointer(1.5)},
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if *resolved.MaxTokens != 256 || *resolved.Temperature != 0.5 {
		t.Fatalf("scalars = %#v", resolved)
	}
	if resolved.Reasoning.Effort != "high" || !*resolved.Reasoning.Enabled ||
		*resolved.Reasoning.Exclude {
		t.Fatalf("reasoning = %#v", resolved.Reasoning)
	}
	if strings.Join(resolved.Provider.Order, ",") != "alpha" ||
		resolved.Provider.Sort != "throughput" ||
		resolved.Provider.DataCollection != "deny" ||
		*resolved.Provider.AllowFallbacks ||
		*resolved.Provider.MaxPrice.Prompt != 1.5 {
		t.Fatalf("provider = %#v", resolved.Provider)
	}
	// require_parameters is not part of the settings vocabulary; the client owns
	// it, and resolution must leave it unset.
	if resolved.Provider.RequireParameters != nil {
		t.Fatalf("require_parameters = %v", *resolved.Provider.RequireParameters)
	}
}

func TestResolveDropsAnEmptyReasoningObject(t *testing.T) {
	resolved, err := Resolve(&Config{
		Model:     pointer("vendor/model"),
		Reasoning: &Reasoning{},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Reasoning != nil {
		t.Fatalf("reasoning = %#v", resolved.Reasoning)
	}
}
