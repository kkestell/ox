package settings

import (
	"errors"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/openrouter"
)

func profile(config ModelConfig) *Config {
	return &Config{Models: map[string]ModelConfig{"vendor/model": config}}
}

func TestResolveAppliesDefaultModelPrecedence(t *testing.T) {
	layer := func(id string) *Config {
		return &Config{
			DefaultModel: pointer(id),
			Models: map[string]ModelConfig{
				"global/model":      {},
				"workspace/model":   {},
				"environment/model": {},
			},
		}
	}
	global := layer("global/model")
	workspace := layer("workspace/model")

	if _, err := Resolve(Merge(nil, nil), ""); !errors.Is(err, ErrNoModels) {
		t.Fatalf("error = %v, want ErrNoModels", err)
	}
	if _, err := Resolve(Merge(nil, &Config{Models: map[string]ModelConfig{"a/b": {}}}), ""); !errors.Is(err, ErrNoDefaultModel) {
		t.Fatalf("error = %v, want ErrNoDefaultModel", err)
	}
	profiles, err := Resolve(Merge(global, nil), "")
	if err != nil || profiles.Default != "global/model" ||
		profiles.DefaultSource != SourceGlobal {
		t.Fatalf("global default = %#v, %v", profiles, err)
	}
	profiles, err = Resolve(Merge(global, workspace), "")
	if err != nil || profiles.Default != "workspace/model" ||
		profiles.DefaultSource != SourceWorkspace {
		t.Fatalf("workspace default = %#v, %v", profiles, err)
	}
	profiles, err = Resolve(Merge(global, workspace), " environment/model ")
	if err != nil || profiles.Default != "environment/model" ||
		profiles.DefaultSource != SourceCLI {
		t.Fatalf("overridden default = %#v, %v", profiles, err)
	}
	if _, err := Resolve(&Config{
		DefaultModel: pointer("  "),
		Models:       map[string]ModelConfig{"vendor/model": {}},
	}, ""); err == nil || !strings.Contains(err.Error(), "default_model") {
		t.Fatalf("blank default error = %v", err)
	}
}

func TestResolveRequiresEverySelectionToBeConfigured(t *testing.T) {
	configured := &Config{
		DefaultModel: pointer("vendor/model"),
		Models:       map[string]ModelConfig{"vendor/model": {}, "other/model": {}},
	}
	for name, test := range map[string]struct {
		config   *Config
		override string
		contains string
	}{
		"unconfigured default": {
			config: &Config{
				DefaultModel: pointer("absent/model"),
				Models:       map[string]ModelConfig{"vendor/model": {}},
			},
			contains: `"absent/model", which is not configured`,
		},
		"unconfigured override": {
			config:   configured,
			override: "absent/model",
			contains: "--model names model",
		},
		"blank model key": {
			config: &Config{
				DefaultModel: pointer("vendor/model"),
				Models:       map[string]ModelConfig{" ": {}},
			},
			contains: `"models" key`,
		},
		"untrimmed model key": {
			config: &Config{
				DefaultModel: pointer("vendor/model"),
				Models:       map[string]ModelConfig{"vendor/model ": {}},
			},
			contains: `"models" key`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Resolve(test.config, test.override); err == nil ||
				!strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want mention of %q", err, test.contains)
			}
		})
	}
	profiles, err := Resolve(configured, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(profiles.IDs(), ",") != "other/model,vendor/model" {
		t.Fatalf("configured IDs = %v", profiles.IDs())
	}
}

func TestResolveAcceptsTheEdgesOfEveryRangeAndRejectsWhatIsOutside(t *testing.T) {
	for name, config := range map[string]ModelConfig{
		"lowest temperature":  {Temperature: pointer(0.0)},
		"highest temperature": {Temperature: pointer(2.0)},
		"one token":           {MaxTokens: pointer(1)},
		"free prompt":         {Provider: &Provider{MaxPrice: &MaxPrice{Prompt: pointer(0.0)}}},
	} {
		if _, err := Resolve(profile(config), "vendor/model"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for name, test := range map[string]struct {
		config   ModelConfig
		contains string
	}{
		"cold": {
			config:   ModelConfig{Temperature: pointer(-0.1)},
			contains: "temperature",
		},
		"hot": {
			config:   ModelConfig{Temperature: pointer(2.1)},
			contains: "temperature",
		},
		"no tokens": {
			config:   ModelConfig{MaxTokens: pointer(0)},
			contains: "max_tokens",
		},
		"negative tokens": {
			config:   ModelConfig{MaxTokens: pointer(-1)},
			contains: "max_tokens",
		},
		"negative price": {
			config: ModelConfig{Provider: &Provider{
				MaxPrice: &MaxPrice{Prompt: pointer(-1.0)},
			}},
			contains: "max_price.prompt",
		},
		"blank effort": {
			config:   ModelConfig{Reasoning: &Reasoning{Effort: pointer(" ")}},
			contains: "reasoning.effort",
		},
		"blank order entry": {
			config:   ModelConfig{Provider: &Provider{Order: []string{"alpha", ""}}},
			contains: "provider.order",
		},
		"blank only entry": {
			config:   ModelConfig{Provider: &Provider{Only: []string{" "}}},
			contains: "provider.only",
		},
		"blank ignore entry": {
			config:   ModelConfig{Provider: &Provider{Ignore: []string{""}}},
			contains: "provider.ignore",
		},
		"blank quantization": {
			config:   ModelConfig{Provider: &Provider{Quantizations: []string{""}}},
			contains: "provider.quantizations",
		},
	} {
		t.Run(name, func(t *testing.T) {
			profiles, err := Resolve(profile(test.config), "vendor/model")
			if err == nil {
				t.Fatalf("resolved = %#v", profiles)
			}
			if !strings.Contains(err.Error(), test.contains) ||
				!strings.Contains(err.Error(), `model "vendor/model"`) {
				t.Fatalf("error = %v, want the model and mention of %q", err, test.contains)
			}
		})
	}
}

func TestSelectMapsOneProfileOntoTheWireTypes(t *testing.T) {
	profiles, err := Resolve(profile(ModelConfig{
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
	}), "vendor/model")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := profiles.Select("vendor/model", SourceSession)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Model != "vendor/model" || resolved.ModelSource != SourceSession {
		t.Fatalf("identity = %#v", resolved)
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

	// Two selections of one profile must not share state, so a session override
	// of one cannot reach the other.
	*resolved.MaxTokens = 1
	resolved.Provider.Order[0] = "omega"
	again, err := profiles.Select("vendor/model", SourceCLI)
	if err != nil {
		t.Fatal(err)
	}
	if *again.MaxTokens != 256 || again.Provider.Order[0] != "alpha" {
		t.Fatalf("second selection = %#v", again)
	}
	if _, err := profiles.Select("absent/model", SourceCLI); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Fatalf("absent selection error = %v", err)
	}
}

func TestResolveDropsAnEmptyReasoningObject(t *testing.T) {
	profiles, err := Resolve(profile(ModelConfig{Reasoning: &Reasoning{}}), "vendor/model")
	if err != nil {
		t.Fatal(err)
	}
	if profiles.DefaultProfile().Reasoning != nil {
		t.Fatalf("reasoning = %#v", profiles.DefaultProfile().Reasoning)
	}
}

func TestValidateChecksCatalogCompatibility(t *testing.T) {
	entry := &openrouter.Model{
		ID: "vendor/model",
		Architecture: openrouter.Architecture{
			InputModalities: []string{"text", "image"},
		},
		SupportedParameters: []string{"temperature", "max_tokens", "tools"},
		Reasoning: &openrouter.ModelReasoning{
			SupportedEfforts: []string{"high", "low"},
		},
	}
	resolved := Resolved{
		Model:       entry.ID,
		Temperature: pointer(0.5),
		MaxTokens:   pointer(256),
		Reasoning:   &openrouter.Reasoning{Effort: "high"},
	}
	if err := Validate(entry, resolved, Compatibility{
		Tools:      true,
		Modalities: []string{"image", "text", "image"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsUnsupportedRequirements(t *testing.T) {
	reasoning := &openrouter.ModelReasoning{SupportedEfforts: []string{"low"}}
	for _, test := range []struct {
		name          string
		entry         *openrouter.Model
		resolved      Resolved
		compatibility Compatibility
		contains      string
	}{
		{
			name:     "temperature",
			entry:    &openrouter.Model{ID: "vendor/model"},
			resolved: Resolved{Temperature: pointer(0.5)},
			contains: "temperature",
		},
		{
			name:     "max tokens",
			entry:    &openrouter.Model{ID: "vendor/model"},
			resolved: Resolved{MaxTokens: pointer(256)},
			contains: "max_tokens",
		},
		{
			name:          "tools",
			entry:         &openrouter.Model{ID: "vendor/model"},
			compatibility: Compatibility{Tools: true},
			contains:      "tool set",
		},
		{
			name: "retained modality",
			entry: &openrouter.Model{
				ID:           "vendor/model",
				Architecture: openrouter.Architecture{InputModalities: []string{"text"}},
			},
			compatibility: Compatibility{Modalities: []string{"text", "audio", "image"}},
			contains:      "retained audio input",
		},
		{
			name:     "reasoning",
			entry:    &openrouter.Model{ID: "vendor/model", Reasoning: reasoning},
			resolved: Resolved{Reasoning: &openrouter.Reasoning{Effort: "high"}},
			contains: "supported efforts are low",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.entry, test.resolved, test.compatibility)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want mention of %q", err, test.contains)
			}
		})
	}
}
