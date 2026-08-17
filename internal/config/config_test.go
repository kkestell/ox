package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalPath(t *testing.T) {
	tests := []struct {
		name string
		xdg  string
		home string
		want string
	}{
		{
			name: "absolute XDG path",
			xdg:  filepath.Join(string(filepath.Separator), "xdg"),
			home: filepath.Join(string(filepath.Separator), "home"),
			want: filepath.Join(string(filepath.Separator), "xdg", "ox", "config.json"),
		},
		{
			name: "home fallback",
			home: filepath.Join(string(filepath.Separator), "home"),
			want: filepath.Join(string(filepath.Separator), "home", ".config", "ox", "config.json"),
		},
		{
			name: "relative XDG uses home",
			xdg:  "relative",
			home: filepath.Join(string(filepath.Separator), "home"),
			want: filepath.Join(string(filepath.Separator), "home", ".config", "ox", "config.json"),
		},
		{name: "relative home is unusable", home: "relative"},
		{name: "no base directory"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := GlobalPath(test.xdg, test.home); got != test.want {
				t.Fatalf("GlobalPath(%q, %q) = %q, want %q", test.xdg, test.home, got, test.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content *string
		wantNil bool
		want    *string
		wantErr string
	}{
		{name: "absent", wantNil: true},
		{name: "empty", content: pointer("")},
		{name: "whitespace", content: pointer(" \n\t")},
		{name: "model", content: pointer(`{"model":"test/model"}`), want: pointer("test/model")},
		{name: "malformed", content: pointer(`{"model":`), wantErr: "parse configuration file"},
		{name: "unknown key", content: pointer(`{"temperature":1}`), wantErr: `unknown field "temperature"`},
		{name: "wrong type", content: pointer(`{"model":1}`), wantErr: "cannot unmarshal number"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, test.name, "config.json")
			if test.content != nil {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(*test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			got, err := load(path)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) || !strings.Contains(err.Error(), path) {
					t.Fatalf("load error = %v, want path and %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.wantNil {
				if got != nil {
					t.Fatalf("load = %#v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("load = nil, want a configuration")
			}
			if test.want == nil && got.Model != nil {
				t.Fatalf("model = %q, want nil", *got.Model)
			}
			if test.want != nil && (got.Model == nil || *got.Model != *test.want) {
				t.Fatalf("model = %#v, want %q", got.Model, *test.want)
			}
		})
	}
}

func TestLoadReadFailureNamesPath(t *testing.T) {
	path := t.TempDir()
	_, err := load(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "read configuration file") {
		t.Fatalf("load error = %v, want a read error naming %s", err, path)
	}
}

func TestResolvePrecedence(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.json")
	workspaceFile := workspacePath(dir)
	writeConfig(t, globalPath, `{"model":"global/model"}`)

	tests := []struct {
		name       string
		override   string
		workspace  *string
		wantModel  string
		wantSource Source
	}{
		{name: "global", wantModel: "global/model", wantSource: SourceGlobal},
		{name: "workspace", workspace: pointer(`{"model":"workspace/model"}`), wantModel: "workspace/model", wantSource: SourceWorkspace},
		{name: "environment", override: " environment/model ", workspace: pointer(`{"model":"workspace/model"}`), wantModel: "environment/model", wantSource: SourceEnvironment},
		{name: "blank environment falls through", override: " \t", workspace: pointer(`{"model":"workspace/model"}`), wantModel: "workspace/model", wantSource: SourceWorkspace},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := os.RemoveAll(filepath.Dir(workspaceFile)); err != nil {
				t.Fatal(err)
			}
			if test.workspace != nil {
				writeConfig(t, workspaceFile, *test.workspace)
			}
			got, err := Resolve(Environment{GlobalPath: globalPath, ModelOverride: test.override}, dir)
			if err != nil {
				t.Fatal(err)
			}
			if got.Model != test.wantModel || got.ModelSource != test.wantSource {
				t.Fatalf("Resolve = %#v, want model %q from %q", got, test.wantModel, test.wantSource)
			}
		})
	}
}

func TestResolveRejectsBlankModels(t *testing.T) {
	for _, layer := range []string{"global", "workspace"} {
		t.Run(layer, func(t *testing.T) {
			dir := t.TempDir()
			globalPath := filepath.Join(dir, "global.json")
			path := globalPath
			if layer == "workspace" {
				path = workspacePath(dir)
			}
			writeConfig(t, path, `{"model":"   "}`)

			_, err := Resolve(Environment{GlobalPath: globalPath, ModelOverride: "override/model"}, dir)
			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `"model" must not be blank`) {
				t.Fatalf("Resolve error = %v, want blank-model error naming %s", err, path)
			}
		})
	}
}

func TestResolveNotConfiguredNamesConsultedPaths(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.json")
	_, err := Resolve(Environment{GlobalPath: globalPath}, dir)
	if err == nil {
		t.Fatal("Resolve succeeded without a model")
	}
	for _, want := range []string{"OX_MODEL", globalPath, workspacePath(dir)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %s", err, want)
		}
	}
}

func TestResolveWithoutGlobalPathNamesOnlyWorkspace(t *testing.T) {
	dir := t.TempDir()
	_, err := Resolve(Environment{}, dir)
	if err == nil {
		t.Fatal("Resolve succeeded without a model")
	}
	if !strings.Contains(err.Error(), workspacePath(dir)) {
		t.Errorf("error = %q, want workspace path", err)
	}
	if strings.Contains(err.Error(), `.config`) {
		t.Errorf("error = %q, want no relative global path", err)
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pointer(value string) *string {
	return &value
}
