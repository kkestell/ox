package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalPathUsesXDGThenHome(t *testing.T) {
	if got := GlobalPath("/var/config/user", "/home/user"); got !=
		"/var/config/user/ox/settings.json" {
		t.Fatalf("XDG path = %q", got)
	}
	if got := GlobalPath("relative", "/home/user"); got !=
		"/home/user/.config/ox/settings.json" {
		t.Fatalf("home path = %q", got)
	}
	if got := GlobalPath("", "relative"); got != "" {
		t.Fatalf("unusable path = %q", got)
	}
}

func TestWorkspacePathDoesNotWalkUpwards(t *testing.T) {
	if got := WorkspacePath("/work/project"); got !=
		"/work/project/.ox/settings.json" {
		t.Fatalf("workspace path = %q", got)
	}
}

func TestLoadTreatsAbsentAndBlankLayersAsHarmless(t *testing.T) {
	directory := t.TempDir()
	for name, path := range map[string]string{
		"no path":      "",
		"missing file": filepath.Join(directory, "missing.json"),
	} {
		config, err := Load(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if config != nil {
			t.Fatalf("%s: layer = %#v", name, config)
		}
	}
	for name, body := range map[string]string{
		"empty file":      "",
		"whitespace only": " \n\t ",
	} {
		config, err := Load(write(t, filepath.Join(t.TempDir(), "settings.json"), body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if config == nil || *config != (Config{}) {
			t.Fatalf("%s: layer = %#v", name, config)
		}
	}
}

func TestLoadDecodesTheAllowlistedVocabulary(t *testing.T) {
	config, err := Load(write(t, filepath.Join(t.TempDir(), "settings.json"), `{
		"model": "vendor/model",
		"max_tokens": 512,
		"temperature": 0.25,
		"reasoning": {"enabled": true, "effort": "high", "exclude": false},
		"provider": {
			"order": ["alpha", "beta"],
			"only": ["alpha"],
			"ignore": ["gamma"],
			"quantizations": ["fp8"],
			"sort": "throughput",
			"data_collection": "deny",
			"allow_fallbacks": false,
			"max_price": {"prompt": 1.5, "completion": 2}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if *config.Model != "vendor/model" || *config.MaxTokens != 512 ||
		*config.Temperature != 0.25 {
		t.Fatalf("scalars = %#v", config)
	}
	if !*config.Reasoning.Enabled || *config.Reasoning.Effort != "high" ||
		*config.Reasoning.Exclude {
		t.Fatalf("reasoning = %#v", config.Reasoning)
	}
	provider := config.Provider
	if strings.Join(provider.Order, ",") != "alpha,beta" ||
		strings.Join(provider.Only, ",") != "alpha" ||
		strings.Join(provider.Ignore, ",") != "gamma" ||
		strings.Join(provider.Quantizations, ",") != "fp8" ||
		*provider.Sort != "throughput" ||
		*provider.DataCollection != "deny" ||
		*provider.AllowFallbacks {
		t.Fatalf("provider = %#v", provider)
	}
	if *provider.MaxPrice.Prompt != 1.5 || *provider.MaxPrice.Completion != 2 {
		t.Fatalf("max price = %#v", provider.MaxPrice)
	}
}

func TestLoadRejectsUnknownKeysAndWrongTypesNamingTheFile(t *testing.T) {
	for name, body := range map[string]string{
		"malformed json":       `{"model":`,
		"unknown key":          `{"models": "vendor/model"}`,
		"unknown provider key": `{"provider": {"require_parameters": true}}`,
		"unknown nested key":   `{"reasoning": {"effort_level": "high"}}`,
		"unknown price key":    `{"provider": {"max_price": {"image": 1}}}`,
		"wrong scalar type":    `{"temperature": "hot"}`,
		"wrong list type":      `{"provider": {"order": "alpha"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := write(t, filepath.Join(t.TempDir(), "settings.json"), body)
			config, err := Load(path)
			if err == nil {
				t.Fatalf("layer = %#v", config)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %v, want mention of %q", err, path)
			}
		})
	}
}

func write(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
