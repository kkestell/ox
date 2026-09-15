package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/settings"
)

// TestInstallerSettingsTemplateResolves keeps the starter settings that the
// release archive's install.sh writes valid against the settings vocabulary,
// which rejects unknown keys. A template that no longer resolves would break
// every fresh installation rather than any test that reads settings files.
func TestInstallerSettingsTemplateResolves(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	template := heredoc(t, string(script), "JSON")
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(template), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}

	config, process, err := settings.LoadGlobal(path)
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	if _, err := settings.ResolveProcess(process, settings.ProcessOverrides{}); err != nil {
		t.Fatalf("resolve template process settings: %v", err)
	}
	profiles, err := settings.Resolve(settings.Merge(config, nil), "")
	if err != nil {
		t.Fatalf("resolve template models: %v", err)
	}
	if profiles.DefaultProfile().Model != profiles.Default {
		t.Fatalf("default profile = %q, want %q", profiles.DefaultProfile().Model, profiles.Default)
	}
}

// heredoc returns the body of the first shell heredoc introduced with the given
// quoted delimiter.
func heredoc(t *testing.T, script, delimiter string) string {
	t.Helper()
	_, rest, found := strings.Cut(script, "<<'"+delimiter+"'\n")
	if !found {
		t.Fatalf("no <<'%s' heredoc in install.sh", delimiter)
	}
	body, _, found := strings.Cut(rest, "\n"+delimiter+"\n")
	if !found {
		t.Fatalf("unterminated <<'%s' heredoc in install.sh", delimiter)
	}
	return body
}
