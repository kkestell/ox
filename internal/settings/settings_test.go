package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/lsp"
)

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
		if config == nil || config.DefaultModel != nil || config.Models != nil {
			t.Fatalf("%s: layer = %#v", name, config)
		}
	}
}

func TestLoadDecodesTheAllowlistedVocabulary(t *testing.T) {
	config, err := LoadWorkspace(write(t, filepath.Join(t.TempDir(), "settings.json"), `{
		"default_model": "vendor/model",
		"models": {
			"other/model": {},
			"vendor/model": {
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
			}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if *config.DefaultModel != "vendor/model" || len(config.Models) != 2 {
		t.Fatalf("layer = %#v", config)
	}
	model := config.Models["vendor/model"]
	if *model.MaxTokens != 512 || *model.Temperature != 0.25 {
		t.Fatalf("scalars = %#v", model)
	}
	if !*model.Reasoning.Enabled || *model.Reasoning.Effort != "high" ||
		*model.Reasoning.Exclude {
		t.Fatalf("reasoning = %#v", model.Reasoning)
	}
	provider := model.Provider
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
		"malformed json":          `{"default_model":`,
		"unknown key":             `{"model": "vendor/model"}`,
		"top-level request field": `{"max_tokens": 512}`,
		"unknown profile key":     `{"models": {"vendor/model": {"models": {}}}}`,
		"unknown provider key":    `{"models": {"vendor/model": {"provider": {"require_parameters": true}}}}`,
		"unknown nested key":      `{"models": {"vendor/model": {"reasoning": {"effort_level": "high"}}}}`,
		"unknown price key":       `{"models": {"vendor/model": {"provider": {"max_price": {"image": 1}}}}}`,
		"wrong scalar type":       `{"models": {"vendor/model": {"temperature": "hot"}}}`,
		"wrong list type":         `{"models": {"vendor/model": {"provider": {"order": "alpha"}}}}`,
		"wrong profile type":      `{"models": {"vendor/model": "profile"}}`,
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
		"default_model":"vendor/model",
		"models":{"vendor/model":{}},
		"process":{"log_level":"debug","openrouter_base_url":"http://localhost:8080/api/v1","trace":"trace.jsonl"}
	}`)
	model, process, err := LoadGlobal(path)
	if err != nil {
		t.Fatal(err)
	}
	if *model.DefaultModel != "vendor/model" || *process.LogLevel != "debug" ||
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

func TestResolveLanguageServers(t *testing.T) {
	resolved, err := ResolveProcess(&Process{
		LanguageServers: map[string]LanguageServer{
			"typescript": {
				Command:    pointer("typescript-language-server"),
				Args:       []string{"--stdio"},
				Extensions: []string{".TS", "tsx"},
			},
			"gopls": {Command: pointer(" gopls "), Extensions: []string{"go"}},
		},
	}, ProcessOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	want := []lsp.Definition{
		{Name: "gopls", Command: "gopls", Extensions: []string{"go"}},
		{
			Name: "typescript", Command: "typescript-language-server",
			Args: []string{"--stdio"}, Extensions: []string{"ts", "tsx"},
		},
	}
	if !reflect.DeepEqual(resolved.LanguageServers, want) {
		t.Fatalf("language servers = %#v", resolved.LanguageServers)
	}

	for name, servers := range map[string]map[string]LanguageServer{
		"blank name":       {"  ": {Command: pointer("gopls"), Extensions: []string{"go"}}},
		"missing command":  {"gopls": {Extensions: []string{"go"}}},
		"blank command":    {"gopls": {Command: pointer(" "), Extensions: []string{"go"}}},
		"blank argument":   {"gopls": {Command: pointer("gopls"), Args: []string{""}, Extensions: []string{"go"}}},
		"no extensions":    {"gopls": {Command: pointer("gopls")}},
		"blank extension":  {"gopls": {Command: pointer("gopls"), Extensions: []string{" . "}}},
		"path extension":   {"gopls": {Command: pointer("gopls"), Extensions: []string{"a/b"}}},
		"shared extension": {"gopls": {Command: pointer("gopls"), Extensions: []string{"go"}}, "other": {Command: pointer("other"), Extensions: []string{".GO"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ResolveProcess(&Process{LanguageServers: servers}, ProcessOverrides{}); err == nil {
				t.Fatal("invalid language server was accepted")
			}
		})
	}
}

// A later change to the settings value must not reach an already-resolved
// definition, which every session activation reads.
func TestResolvedLanguageServersDoNotAliasSettings(t *testing.T) {
	arguments := []string{"--stdio"}
	process := &Process{LanguageServers: map[string]LanguageServer{
		"gopls": {Command: pointer("gopls"), Args: arguments, Extensions: []string{"go"}},
	}}
	resolved, err := ResolveProcess(process, ProcessOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	arguments[0] = "mutated"
	if resolved.LanguageServers[0].Args[0] != "--stdio" {
		t.Fatalf("resolved arguments = %#v", resolved.LanguageServers[0].Args)
	}
}
