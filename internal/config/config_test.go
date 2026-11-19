package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsMustBeConfiguredChoices(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err != nil {
		t.Fatalf("built-in configuration: %v", err)
	}
	const harnesses = `"harnesses":{"h":{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]},{"id":"plain","name":"Plain","efforts":[]}],"permission_modes":[{"id":"auto","name":"Auto"}]}}`
	for defaults, valid := range map[string]bool{
		`{"harness":"h","model":"m","effort":"low","permission_mode":"auto"}`:  true,
		`{"harness":"h","model":"plain","effort":"","permission_mode":"auto"}`: true,
		``: false,
		`{"harness":"x","model":"m","effort":"low","permission_mode":"auto"}`:     false,
		`{"harness":"h","model":"x","effort":"low","permission_mode":"auto"}`:     false,
		`{"harness":"h","model":"m","effort":"","permission_mode":"auto"}`:        false,
		`{"harness":"h","model":"plain","effort":"low","permission_mode":"auto"}`: false,
		`{"harness":"h","model":"m","effort":"low","permission_mode":"x"}`:        false,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{` + harnesses + `}`
		if defaults != "" {
			data = `{` + harnesses + `,"defaults":` + defaults + `}`
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); (err == nil) != valid {
			t.Errorf("defaults %s: valid %v, got %v", defaults, valid, err)
		}
	}

	path := filepath.Join(t.TempDir(), "config.json")
	data := `{` + harnesses + `,"defaults":{"harness":"h","model":"m","effort":"low","permission_mode":"auto"}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a configuration followed by more content loaded")
	}
}

// harness/list serves the catalog as loaded, and the web client reads every
// list in it, for every harness.
func TestCatalogListsArePresent(t *testing.T) {
	load := func(other string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{"harnesses":{"h":{"models":[{"id":"m","name":"M"}],"permission_modes":[{"id":"auto","name":"Auto"}]},` +
			`"other":` + other + `},"defaults":{"harness":"h","model":"m","effort":"","permission_mode":"auto"}}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load(path)
	}
	for _, other := range []string{
		`{"models":[{"id":"m","name":"M","efforts":[]}]}`,
		`{"permission_modes":[{"id":"auto","name":"Auto"}]}`,
	} {
		if _, err := load(other); err == nil {
			t.Errorf("a harness without a list loaded: %s", other)
		}
	}
	cfg, err := load(`{"models":[{"id":"m","name":"M"}],"permission_modes":[{"id":"auto","name":"Auto"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(cfg.Harnesses); strings.Contains(string(data), "null") {
		t.Errorf("a model without effort levels is served null: %s", data)
	}
}
