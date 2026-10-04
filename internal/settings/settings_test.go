package settings_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ox/internal/catalog"
	"ox/internal/openroutertest"
	"ox/internal/settings"
	"ox/internal/transcript"
)

const model = openroutertest.DefaultModel

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
		t.Fatal(err)
	}
}

func TestGlobalSettingsDefaultValidateAndPinProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cat := openroutertest.ParsedCatalog()
	if _, err := settings.Load(path, cat); err == nil || !strings.Contains(err.Error(), "~deepseek/deepseek-flash-latest is not in the model catalog") {
		t.Errorf("without a file, the built-in model is required: %v", err)
	}
	write(t, path, `{"model":"`+model+`","effort":"high","mode":"auto","servers":[],"favorites":[]}`)
	loaded, err := settings.Load(path, cat)
	if err != nil || loaded != (settings.Settings{Model: model, Effort: catalog.EffortHigh, Mode: transcript.ModeAuto}) {
		t.Errorf("loaded %+v, %v", loaded, err)
	}
	pinned := "openrouter:z-ai/glm-5.3-flash"
	write(t, path, `{"model":"`+model+`","models":{"`+pinned+`":{"providers":["b","a"]}}}`)
	if _, err := settings.Load(path, cat); err != nil {
		t.Fatal(err)
	}
	for _, m := range cat {
		want := []string(nil)
		if m.QualifiedID() == pinned {
			want = []string{"b", "a"}
		}
		if !reflect.DeepEqual(m.Providers, want) {
			t.Errorf("%s providers = %v", m.ID, m.Providers)
		}
	}
	for _, test := range []struct{ text, want string }{
		{`{"model":"a/b"}`, "model a/b is not in the model catalog"},
		{`{"model":"openrouter:"}`, "is not in the model catalog"},
		{`not json`, "invalid character"},
		{`{"provider":"openrouter"}`, "use a provider-qualified model ID"},
		{`{"model":"` + model + `","effort":"xhigh"}`, "effort xhigh is not supported"},
		{`{"model":"` + model + `","effort":"loud"}`, "unknown effort loud"},
		{`{"model":"` + model + `","mode":"loud"}`, "unknown mode loud"},
		{`{"model":"` + model + `","models":{"openrouter:a/b":{"providers":["a"]}}}`, "model openrouter:a/b in models is not in the OpenRouter model catalog"},
		{`{"model":"` + model + `","models":{"` + pinned + `":{"providers":[]}}}`, "lists no providers"},
	} {
		write(t, path, test.text)
		_, err := settings.Load(path, openroutertest.ParsedCatalog())
		if err == nil || !strings.HasPrefix(err.Error(), path+": ") || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: %v", test.text, err)
		}
	}
}

func TestWorkspaceSettingsReplaceGlobalKeys(t *testing.T) {
	cat := openroutertest.ParsedCatalog()
	global := settings.Settings{Model: model, Effort: catalog.EffortHigh, Mode: transcript.ModeAuto}
	workspace := t.TempDir()
	if unchanged, err := global.ForWorkspace(workspace, cat); err != nil || unchanged != global {
		t.Errorf("without a file: %+v, %v", unchanged, err)
	}
	path := filepath.Join(workspace, ".ox/settings.json")
	write(t, path, `{"mode":"ask"}`)
	if got, _ := global.ForWorkspace(workspace, cat); got != (settings.Settings{Model: model, Effort: catalog.EffortHigh, Mode: transcript.ModeAsk}) {
		t.Errorf("mode only: %+v", got)
	}
	for _, test := range []struct{ text, want string }{
		{`null`, "settings must be a JSON object"},
		{`{"models":{}}`, "models can be set only in the global settings file"},
		{`{"model":"openrouter:acme/plain"}`, "effort high is not supported"},
		{`{"provider":"openai"}`, "provider is no longer supported"},
	} {
		write(t, path, test.text)
		_, err := global.ForWorkspace(workspace, cat)
		if err == nil || !strings.HasPrefix(err.Error(), path+": ") || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: %v", test.text, err)
		}
	}
}

func TestSavingNullSettingsReturnsAnError(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	path := filepath.Join(workspace, ".ox/settings.json")
	write(t, path, "null")
	selected := settings.Settings{Model: model, Effort: catalog.EffortLow, Mode: transcript.ModeAuto}
	if _, err := settings.Save(home, workspace, selected); err == nil || !strings.Contains(err.Error(), "settings must be a JSON object") {
		t.Fatalf("save = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "null" {
		t.Fatalf("invalid settings changed: %q, %v", data, err)
	}
}

func TestSavingKeepsOtherFieldsAndPrefersTheWorkspaceFile(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	global := settings.GlobalPath(home)
	write(t, global, `{"servers":[{"name":"Alpha","command":"alpha"}],"favorites":["openrouter:a/b"]}`)
	selected := settings.Settings{Model: model, Effort: catalog.EffortLow, Mode: transcript.ModeAuto}
	if wroteGlobal, err := settings.Save(home, workspace, selected); err != nil || !wroteGlobal {
		t.Fatalf("global save: %v, %v", wroteGlobal, err)
	}
	var saved map[string]any
	data, _ := os.ReadFile(global)
	json.Unmarshal(data, &saved)
	if saved["favorites"].([]any)[0] != "openrouter:a/b" || saved["effort"] != "low" || saved["mode"] != "auto" {
		t.Errorf("global = %s", data)
	}
	local := filepath.Join(workspace, ".ox/settings.json")
	write(t, local, `{"model":"openrouter:z-ai/glm-5.3-flash","other":42}`)
	if wroteGlobal, err := settings.Save(home, workspace, selected); err != nil || wroteGlobal {
		t.Fatalf("workspace save: %v, %v", wroteGlobal, err)
	}
	data, _ = os.ReadFile(local)
	json.Unmarshal(data, &saved)
	if saved["other"] != 42.0 || saved["model"] != model {
		t.Errorf("workspace = %s", data)
	}
}
