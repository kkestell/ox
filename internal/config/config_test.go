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
		data := `{` + harnesses + `,"max_child_depth":2}`
		if defaults != "" {
			data = `{` + harnesses + `,"defaults":` + defaults + `,"max_child_depth":2}`
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); (err == nil) != valid {
			t.Errorf("defaults %s: valid %v, got %v", defaults, valid, err)
		}
	}

	const defaults = `"defaults":{"harness":"h","model":"m","effort":"low","permission_mode":"auto"}`
	for _, depth := range []string{`,"max_child_depth":0`, ``} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{`+harnesses+`,`+defaults+depth+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.HasSuffix(err.Error(), "max_child_depth must be at least 1") {
			t.Errorf("max_child_depth %q: got %v", depth, err)
		}
	}

	path := filepath.Join(t.TempDir(), "config.json")
	data := `{` + harnesses + `,` + defaults + `,"max_child_depth":2}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a configuration followed by more content loaded")
	}
}

// A nonempty tailscale_ip must be an IPv4 address within Tailscale's CGNAT
// range; omitting it, or leaving it empty, loads with no tailnet address.
func TestTailscaleIP(t *testing.T) {
	const harnesses = `"harnesses":{"h":{"models":[{"id":"m","name":"M"}],"permission_modes":[{"id":"auto","name":"Auto"}]}},` +
		`"defaults":{"harness":"h","model":"m","effort":"","permission_mode":"auto"},"max_child_depth":2`
	for ip, valid := range map[string]bool{
		"":                true,
		"100.101.102.103": true,
		"100.64.0.1":      true,
		"100.127.255.255": true,
		"not-an-ip":       false,
		"100.63.255.255":  false, // just outside the range
		"100.128.0.0":     false, // just outside the range
		"192.168.1.1":     false, // outside Tailscale's range
		"::1":             false, // IPv6
		"2001:db8::1":     false, // IPv6
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{` + harnesses
		if ip != "" {
			data += `,"tailscale_ip":"` + ip + `"`
		}
		data += `}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if (err == nil) != valid {
			t.Errorf("tailscale_ip %q: valid %v, got %v", ip, valid, err)
			continue
		}
		if valid && cfg.TailscaleIP != ip {
			t.Errorf("tailscale_ip %q loaded as %q", ip, cfg.TailscaleIP)
		}
		if valid && ip != "" {
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "tailscale_ip") {
				t.Errorf("Config marshaled with tailscale_ip set emits it: %s", data)
			}
		}
	}
	if cfg, err := Load(filepath.Join(t.TempDir(), "missing.json")); err != nil || cfg.TailscaleIP != "" {
		t.Errorf("the built-in configuration has tailscale_ip %q, %v", cfg.TailscaleIP, err)
	}
}

// harness/list serves the catalog as loaded, and the web client reads every
// list in it, for every harness.
func TestCatalogListsArePresent(t *testing.T) {
	load := func(other string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{"harnesses":{"h":{"models":[{"id":"m","name":"M"}],"permission_modes":[{"id":"auto","name":"Auto"}]},` +
			`"other":` + other + `},"defaults":{"harness":"h","model":"m","effort":"","permission_mode":"auto"},"max_child_depth":2}`
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

// Providers serves Ur the provider order of each model that sets one.
func TestProviders(t *testing.T) {
	h := Harness{Models: []Model{
		{ID: "pinned", Providers: []string{"a/x", "b/y"}},
		{ID: "free"},
	}}
	providers := h.Providers()
	if len(providers) != 1 || strings.Join(providers["pinned"], ",") != "a/x,b/y" {
		t.Errorf("Providers() = %v", providers)
	}
}

// A choice needs a name and an ID that identifies exactly one option in its own list.
func TestChoiceIDsAndNames(t *testing.T) {
	for _, harness := range []string{
		`{"models":[{"id":"","name":"M","efforts":[{"id":"low","name":"Low"}]},{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"},{"id":"","name":"High"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"},{"id":"","name":"Plan"}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]},{"id":"m","name":"Other"}],"permission_modes":[{"id":"auto","name":"Auto"}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"},{"id":"low","name":"Other"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"},{"id":"auto","name":"Other"}]}`,
		`{"models":[{"id":"m","name":"","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`,
		`{"models":[{"id":"m","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":""}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":""}]}`,
		`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto"}]}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{"harnesses":{"h":` + harness + `},"defaults":{"harness":"h","model":"m","effort":"low","permission_mode":"auto"},"max_child_depth":2}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("a choice with a missing ID or name, or a duplicate ID, loaded: %s", harness)
		}
	}
}

// A session moving to a model keeps its effort when the model offers it, or
// takes the model's first, or none for a model without effort levels.
func TestEffort(t *testing.T) {
	h := Harness{Models: []Model{
		{ID: "m", Efforts: []Option{{ID: "low"}, {ID: "high"}}},
		{ID: "flat", Efforts: []Option{}},
	}}
	for _, c := range []struct{ model, current, want string }{
		{"m", "high", "high"},
		{"m", "medium", "low"},
		{"m", "", "low"},
		{"flat", "high", ""},
	} {
		if got := h.Effort(c.model, c.current); got != c.want {
			t.Errorf("Effort(%q, %q) = %q, not %q", c.model, c.current, got, c.want)
		}
	}
}
