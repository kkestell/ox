package config

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestDefaultsMustBeConfiguredChoices(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err != nil {
		t.Fatalf("built-in configuration: %v", err)
	}
	const harnesses = `"harnesses":{"h":{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]},{"id":"plain","name":"Plain","efforts":[]}],"permission_modes":[{"id":"auto","name":"Auto"}]}}`
	const titling = `"titling":{"harness":"h","model":"m","effort":"low"}`
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
		data := `{` + harnesses + `,` + titling + `,"max_child_depth":2}`
		if defaults != "" {
			data = `{` + harnesses + `,"defaults":` + defaults + `,` + titling + `,"max_child_depth":2}`
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
		if err := os.WriteFile(path, []byte(`{`+harnesses+`,`+defaults+`,`+titling+depth+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.HasSuffix(err.Error(), "max_child_depth must be at least 1") {
			t.Errorf("max_child_depth %q: got %v", depth, err)
		}
	}

	path := filepath.Join(t.TempDir(), "config.json")
	data := `{` + harnesses + `,` + defaults + `,` + titling + `,"max_child_depth":2}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a configuration followed by more content loaded")
	}
}

// The configuration names the harness, model, and effort that title sessions,
// from those it offers.
func TestTitling(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("built-in configuration: %v", err)
	}
	if cfg.Titling != (Titling{Harness: "claude", Model: "claude-haiku-5-5", Effort: "low"}) {
		t.Errorf("built-in titling = %+v", cfg.Titling)
	}
	const catalog = `"harnesses":{"h":{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}},` +
		`"defaults":{"harness":"h","model":"m","effort":"low","permission_mode":"auto"},"max_child_depth":2`
	for _, c := range []struct{ titling, err string }{
		{`,"titling":{"harness":"h","model":"m","effort":"low"}`, ""},
		{``, "titling is required: name the harness, model, and effort that title sessions"},
		{`,"titling":{"harness":"","model":"m","effort":"low"}`, "titling is required: name the harness, model, and effort that title sessions"},
		{`,"titling":{"harness":"x","model":"m","effort":"low"}`, `titling: harness "x" is not configured`},
		{`,"titling":{"harness":"h","model":"x","effort":"low"}`, `titling: model "x" is not configured`},
		{`,"titling":{"harness":"h","model":"m","effort":"max"}`, `titling: effort "max" is not one of m's`},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{`+catalog+c.titling+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if c.err == "" && err != nil || c.err != "" && (err == nil || err.Error() != path+": "+c.err) {
			t.Errorf("%s: want %q, got %v", c.titling, c.err, err)
		}
	}
}

// A tailscale_ip must be an IPv4 address within Tailscale's range. Without
// one, Ox serves only its local address.
func TestTailscaleIP(t *testing.T) {
	const catalog = `"harnesses":{"h":{"models":[{"id":"m","name":"M"}],"permission_modes":[{"id":"auto","name":"Auto"}]}},` +
		`"defaults":{"harness":"h","model":"m","effort":"","permission_mode":"auto"},` +
		`"titling":{"harness":"h","model":"m","effort":""},"max_child_depth":2`
	for _, c := range []struct {
		setting string
		ok      bool
		want    netip.Addr
	}{
		{``, true, netip.Addr{}},
		{`,"tailscale_ip":""`, true, netip.Addr{}},
		{`,"tailscale_ip":"100.64.0.1"`, true, netip.MustParseAddr("100.64.0.1")},
		{`,"tailscale_ip":"100.127.255.255"`, true, netip.MustParseAddr("100.127.255.255")},
		{`,"tailscale_ip":"not-an-ip"`, false, netip.Addr{}},
		{`,"tailscale_ip":"100.63.255.255"`, false, netip.Addr{}},
		{`,"tailscale_ip":"100.128.0.0"`, false, netip.Addr{}},
		{`,"tailscale_ip":"::ffff:100.64.0.1"`, false, netip.Addr{}},
		{`,"tailscale_ip":"fd7a:115c:a1e0::1"`, false, netip.Addr{}},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{`+catalog+c.setting+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if (err == nil) != c.ok || err == nil && cfg.TailscaleIP != c.want {
			t.Errorf("%s: want %v, %v; got %v, %v", c.setting, c.ok, c.want, cfg, err)
		}
	}
}

// harness/list serves the catalog as loaded, and the web client reads every
// list in it, for every harness.
func TestCatalogListsArePresent(t *testing.T) {
	load := func(other string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{"harnesses":{"h":{"models":[{"id":"m","name":"M"}],"permission_modes":[{"id":"auto","name":"Auto"}]},` +
			`"other":` + other + `},"defaults":{"harness":"h","model":"m","effort":"","permission_mode":"auto"},` +
			`"titling":{"harness":"h","model":"m","effort":""},"max_child_depth":2}`
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
	for _, c := range []struct{ harness, want string }{
		{`{"models":[{"id":"","name":"M","efforts":[{"id":"low","name":"Low"}]},{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`, "every model needs an id"},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"},{"id":"","name":"High"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`, "every effort level needs an id"},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"},{"id":"","name":"Plan"}]}`, "every permission mode needs an id"},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]},{"id":"m","name":"Other"}],"permission_modes":[{"id":"auto","name":"Auto"}]}`, `duplicate model id "m"`},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"},{"id":"low","name":"Other"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`, `duplicate effort level id "low"`},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"},{"id":"auto","name":"Other"}]}`, `duplicate permission mode id "auto"`},
		{`{"models":[{"id":"m","name":"","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`, `every model needs a name, and "m" has none`},
		{`{"models":[{"id":"m","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`, `every model needs a name, and "m" has none`},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":""}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`, `every effort level needs a name, and "low" has none`},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low"}]}],"permission_modes":[{"id":"auto","name":"Auto"}]}`, `every effort level needs a name, and "low" has none`},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto","name":""}]}`, `every permission mode needs a name, and "auto" has none`},
		{`{"models":[{"id":"m","name":"M","efforts":[{"id":"low","name":"Low"}]}],"permission_modes":[{"id":"auto"}]}`, `every permission mode needs a name, and "auto" has none`},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		data := `{"harnesses":{"h":` + c.harness + `},"defaults":{"harness":"h","model":"m","effort":"low","permission_mode":"auto"},"titling":{"harness":"h","model":"m","effort":"low"},"max_child_depth":2}`
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("loading %s: got error %v, not one saying %q", c.harness, err, c.want)
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

func TestLoadWritesTheDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ox", "config.json")
	first, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, defaultConfig) {
		t.Fatalf("written file differs from the built-in configuration: %v", err)
	}
	for file, mode := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		if info, err := os.Stat(file); err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s: mode %v, %v; want %v", file, info.Mode().Perm(), err, mode)
		}
	}
	second, err := Load(path)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Errorf("reload: %v; configurations equal: %v", err, reflect.DeepEqual(first, second))
	}

	for round := range 50 {
		racePath := filepath.Join(t.TempDir(), "config.json")
		configs := make([]*Config, 8)
		errs := make([]error, len(configs))
		var wg sync.WaitGroup
		for i := range configs {
			wg.Go(func() {
				configs[i], errs[i] = Load(racePath)
			})
		}
		wg.Wait()
		for i := range configs {
			if errs[i] != nil {
				t.Fatalf("round %d, concurrent load %d: %v", round, i, errs[i])
			}
			if !reflect.DeepEqual(configs[0], configs[i]) {
				t.Errorf("round %d, concurrent load %d differs from load 0", round, i)
			}
		}
		if entries, err := os.ReadDir(filepath.Dir(racePath)); err != nil || len(entries) != 1 || entries[0].Name() != "config.json" {
			t.Errorf("round %d, directory after concurrent loads: %v, %v", round, entries, err)
		}
	}

	invalid := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(invalid, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(invalid); err == nil {
		t.Error("an invalid file loaded")
	}
	if data, _ := os.ReadFile(invalid); string(data) != "{" {
		t.Errorf("invalid file rewritten as %q", data)
	}

	locked := t.TempDir()
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if _, err := Load(filepath.Join(locked, "ox", "config.json")); err == nil || !strings.HasPrefix(err.Error(), "write default configuration to") {
		t.Errorf("directory that cannot be created: got %v", err)
	}
}
