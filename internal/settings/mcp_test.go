package settings_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ox/internal/settings"
)

func TestMCPSettings(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	if got, err := settings.LoadMCPServers(home, workspace); err != nil || len(got) != 0 {
		t.Fatalf("missing files: %v %v", got, err)
	}
	global, local := settings.GlobalPath(home), filepath.Join(workspace, ".ox/settings.json")
	write(t, global, `{"mcpServers":{"one":{"command":"server","args":["--stdio"],"env":{"A":"B"}}}}`)
	inherited, err := settings.LoadMCPServers(home, workspace)
	if err != nil || inherited["one"].Env["A"] != "B" {
		t.Fatalf("global: %v %v", inherited, err)
	}
	write(t, local, `{"mode":"auto"}`)
	got, err := settings.LoadMCPServers(home, workspace)
	if err != nil || !reflect.DeepEqual(got, inherited) {
		t.Fatalf("inherit: %v %v", got, err)
	}
	write(t, local, `{"mcpServers":{"two":{"command":"other"}}}`)
	got, err = settings.LoadMCPServers(home, workspace)
	if err != nil || len(got) != 1 || got["two"].Command != "other" {
		t.Fatalf("replace: %v %v", got, err)
	}
	if _, err := settings.Save(home, workspace, settings.Settings{}); err != nil {
		t.Fatal(err)
	}
	saved, err := settings.LoadMCPServers(home, workspace)
	if err != nil || !reflect.DeepEqual(saved, got) {
		t.Fatalf("save lost MCP settings: %v %v", saved, err)
	}
	write(t, local, `{"mcpServers":{}}`)
	if got, err := settings.LoadMCPServers(home, workspace); err != nil || len(got) != 0 {
		t.Fatalf("disable: %v %v", got, err)
	}
}

func TestInvalidMCPSettings(t *testing.T) {
	for _, definition := range []string{`null`, `[]`, `{}`, `{"command":""}`, `{"command":"x","extra":1}`, `{"command":"x","args":null}`, `{"command":"x","args":[null]}`, `{"command":"x","args":[2]}`, `{"command":"x","env":{"A":null}}`, `{"command":"x","env":{"A":2}}`, `{"command":"x","env":{"A=B":"c"}}`, `{"command":"x","env":{"":"c"}}`, `{"command":"x","env":{"A":"\u0000"}}`} {
		t.Run(definition, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			path := settings.GlobalPath(home)
			write(t, path, `{"mcpServers":{"bad":`+definition+`}}`)
			_, err := settings.LoadMCPServers(home, workspace)
			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `"bad"`) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	for _, raw := range []string{`null`, `[]`, `{"mcpServers":null}`, `{"mcpServers":[]}`, `{"mcpServers":{" ":{"command":"x"}}}`} {
		home, workspace := t.TempDir(), t.TempDir()
		write(t, settings.GlobalPath(home), raw)
		if _, err := settings.LoadMCPServers(home, workspace); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}
