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
		config, err := LoadWorkspace(path)
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
		config, err := LoadWorkspace(write(t, filepath.Join(t.TempDir(), "settings.json"), body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if config == nil || *config != (Config{}) {
			t.Fatalf("%s: layer = %#v", name, config)
		}
	}
}

func TestLoadDecodesTheAllowlistedVocabulary(t *testing.T) {
	config, err := LoadWorkspace(write(t, filepath.Join(t.TempDir(), "settings.json"), `{
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
			config, err := LoadWorkspace(path)
			if err == nil {
				t.Fatalf("layer = %#v", config)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %v, want mention of %q", err, path)
			}
		})
	}
}

func TestGlobalProcessSettingsAndWorkspaceExclusion(t *testing.T) {
	path := write(t, filepath.Join(t.TempDir(), "settings.json"), `{
		"model":"vendor/model",
		"process":{"log_level":"debug","openrouter_base_url":"http://localhost:8080/api/v1","trace":"trace.jsonl"}
	}`)
	model, process, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if *model.Model != "vendor/model" || *process.LogLevel != "debug" ||
		*process.OpenRouterBaseURL != "http://localhost:8080/api/v1" || *process.Trace != "trace.jsonl" {
		t.Fatalf("global settings = %#v, %#v", model, process)
	}
	if _, err := LoadWorkspace(path); err == nil || !strings.Contains(err.Error(), `unknown field "process"`) {
		t.Fatalf("workspace process error = %v", err)
	}
}

func TestResolveProcessPrecedenceDefaultsAndValidation(t *testing.T) {
	resolved, err := ResolveProcess(nil, ProcessOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.LogLevel != DefaultLogLevel || resolved.OpenRouterBaseURL != DefaultOpenRouterBaseURL || resolved.Trace != "" {
		t.Fatalf("defaults = %#v", resolved)
	}

	global := &Process{
		LogLevel:          pointer("warn"),
		OpenRouterBaseURL: pointer("https://global.example/api/v1"),
		Trace:             pointer("global.jsonl"),
	}
	resolved, err = ResolveProcess(global, ProcessOverrides{
		LogLevel:          pointer("DEBUG"),
		OpenRouterBaseURL: pointer("http://127.0.0.1:8080/api/v1"),
		Trace:             pointer("cli.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.LogLevel != "debug" || resolved.OpenRouterBaseURL != "http://127.0.0.1:8080/api/v1" || resolved.Trace != "cli.jsonl" {
		t.Fatalf("overrides = %#v", resolved)
	}

	for name, process := range map[string]*Process{
		"log level":       {LogLevel: pointer("verbose")},
		"blank trace":     {Trace: pointer("  ")},
		"relative URL":    {OpenRouterBaseURL: pointer("api/v1")},
		"URL credentials": {OpenRouterBaseURL: pointer("https://key@example.com/api/v1")},
		"URL query":       {OpenRouterBaseURL: pointer("https://example.com/api/v1?q=1")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ResolveProcess(process, ProcessOverrides{}); err == nil {
				t.Fatal("invalid process setting was accepted")
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
