package config

import (
	"os"
	"path/filepath"
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
}
