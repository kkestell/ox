package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestConfigurationPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		options   []startOption
		wantModel string
	}{
		{
			name: "global",
			options: []startOption{
				withEnvironment("OX_MODEL", ""),
				withGlobalConfig(`{"model":"global/model"}`),
			},
			wantModel: "global/model",
		},
		{
			name: "workspace over global",
			options: []startOption{
				withEnvironment("OX_MODEL", ""),
				withGlobalConfig(`{"model":"global/model"}`),
				withWorkspaceConfig(`{"model":"workspace/model"}`),
			},
			wantModel: "workspace/model",
		},
		{
			name: "environment over workspace and global",
			options: []startOption{
				withEnvironment("OX_MODEL", "environment/model"),
				withGlobalConfig(`{"model":"global/model"}`),
				withWorkspaceConfig(`{"model":"workspace/model"}`),
			},
			wantModel: "environment/model",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := startModel(t, sse(evFinishReason("stop")))
			options := append(test.options, withModel(model))
			child, session := startSession(t, options...)
			prompt(t, child, session, test.name)
			if got := model.requestFor(test.name).Model; got != test.wantModel {
				t.Fatalf("model = %q, want %q", got, test.wantModel)
			}
		})
	}
}

func TestGlobalConfigurationFallsBackToHome(t *testing.T) {
	model := startModel(t, sse(evFinishReason("stop")))
	child, session := startSession(t,
		withModel(model),
		withEnvironment("OX_MODEL", ""),
		withEnvironment("XDG_CONFIG_HOME", ""),
		withFile(filepath.Join(".config", "ox", "config.json"), `{"model":"home/model"}`),
	)
	prompt(t, child, session, "home fallback")
	if got := model.requestFor("home fallback").Model; got != "home/model" {
		t.Fatalf("model = %q, want home/model", got)
	}
}

func TestWorkspaceConfigurationDoesNotWalkToAParent(t *testing.T) {
	child := start(t,
		withEnvironment("OX_MODEL", ""),
		withWorkspaceConfig(`{"model":"parent/model"}`),
		withFile(filepath.Join("child", ".keep"), ""),
	)
	initialize(t, child)

	childWorkspace := filepath.Join(child.cwd, "child")
	responseError := child.requestError("session/new", newSessionRequest(childWorkspace))
	if responseError.Code != -32603 {
		t.Fatalf("error code = %d, want -32603", responseError.Code)
	}
	if !strings.Contains(responseError.Message, filepath.Join(childWorkspace, ".ox", "config.json")) {
		t.Errorf("error = %q, want child workspace path", responseError.Message)
	}
}

func TestSessionsResolveConfigurationForTheirOwnWorkspaces(t *testing.T) {
	model := startModel(t)
	model.queueFor("first prompt", sse(evFinishReason("stop")))
	model.queueFor("second prompt", sse(evFinishReason("stop")))
	child := start(t,
		withModel(model),
		withEnvironment("OX_MODEL", ""),
		withFile(filepath.Join("first", ".ox", "config.json"), `{"model":"first/model"}`),
		withFile(filepath.Join("second", ".ox", "config.json"), `{"model":"second/model"}`),
	)
	initialize(t, child)
	first := newSession(t, child, filepath.Join(child.cwd, "first"))
	second := newSession(t, child, filepath.Join(child.cwd, "second"))

	prompt(t, child, first, "first prompt")
	prompt(t, child, second, "second prompt")
	if got := model.requestFor("first prompt").Model; got != "first/model" {
		t.Errorf("first session model = %q, want first/model", got)
	}
	if got := model.requestFor("second prompt").Model; got != "second/model" {
		t.Errorf("second session model = %q, want second/model", got)
	}
}

func TestSessionConfigurationIsFrozen(t *testing.T) {
	model := startModel(t)
	model.queueFor("old session", sse(evFinishReason("stop")))
	model.queueFor("new session", sse(evFinishReason("stop")))
	child := start(t,
		withModel(model),
		withEnvironment("OX_MODEL", ""),
		withWorkspaceConfig(`{"model":"old/model"}`),
	)
	initialize(t, child)
	older := newSession(t, child, child.cwd)

	path := filepath.Join(child.cwd, ".ox", "config.json")
	if err := os.WriteFile(path, []byte(`{"model":"new/model"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	newer := newSession(t, child, child.cwd)

	prompt(t, child, older, "old session")
	prompt(t, child, newer, "new session")
	if got := model.requestFor("old session").Model; got != "old/model" {
		t.Errorf("old session model = %q, want old/model", got)
	}
	if got := model.requestFor("new session").Model; got != "new/model" {
		t.Errorf("new session model = %q, want new/model", got)
	}
}

func TestWorkspaceConfigurationUsesCanonicalDirectory(t *testing.T) {
	model := startModel(t, sse(evFinishReason("stop")))
	child := start(t,
		withModel(model),
		withEnvironment("OX_MODEL", ""),
		withFile(filepath.Join("real", ".ox", "config.json"), `{"model":"canonical/model"}`),
	)
	link := filepath.Join(child.cwd, "link")
	if err := os.Symlink(filepath.Join(child.cwd, "real"), link); err != nil {
		t.Fatal(err)
	}
	initialize(t, child)
	session := newSession(t, child, link)

	prompt(t, child, session, "canonical workspace")
	if got := model.requestFor("canonical workspace").Model; got != "canonical/model" {
		t.Fatalf("model = %q, want canonical/model", got)
	}
}

func TestInvalidConfigurationFailsOnlyThatSession(t *testing.T) {
	model := startModel(t)
	held := model.holdFor("running prompt", frames(evText("running")))
	child := start(t,
		withModel(model),
		withEnvironment("OX_MODEL", ""),
		withFile(filepath.Join("good", ".ox", "config.json"), `{"model":"good/model"}`),
		withFile(filepath.Join("bad", ".ox", "config.json"), `{"unknown":true}`),
	)
	initialize(t, child)
	good := newSession(t, child, filepath.Join(child.cwd, "good"))
	running := child.begin("session/prompt", acp.PromptRequest{
		SessionID: good,
		Prompt:    textPrompt("running prompt"),
	})
	held.await(t)

	responseError := child.requestError("session/new", newSessionRequest(filepath.Join(child.cwd, "bad")))
	if responseError.Code != -32603 || !strings.Contains(responseError.Message, filepath.Join("bad", ".ox", "config.json")) {
		t.Fatalf("bad session error = %#v", responseError)
	}
	held.finish(sse(evFinishReason("stop")))
	_ = promptResponse(t, child.result(child.await(running)))
	_ = updates(t, child, good)
}

func TestBadConfigurationFilesNameTheirPathAndOxRecovers(t *testing.T) {
	tests := []struct {
		name     string
		relative string
		content  string
		want     string
	}{
		{name: "malformed global", relative: filepath.Join("config", "ox", "config.json"), content: `{"model":`, want: "parse configuration file"},
		{name: "unknown workspace key", relative: filepath.Join(".ox", "config.json"), content: `{"modle":"test/model"}`, want: `unknown field "modle"`},
		{name: "wrong model type", relative: filepath.Join(".ox", "config.json"), content: `{"model":1}`, want: "cannot unmarshal number"},
		{name: "blank model", relative: filepath.Join(".ox", "config.json"), content: `{"model":"   "}`, want: `"model" must not be blank`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			child := start(t,
				withEnvironment("OX_MODEL", ""),
				withFile(test.relative, test.content),
			)
			initialize(t, child)

			responseError := child.requestError("session/new", newSessionRequest(child.cwd))
			path := filepath.Join(child.cwd, test.relative)
			if responseError.Code != -32603 || !strings.Contains(responseError.Message, path) || !strings.Contains(responseError.Message, test.want) {
				t.Fatalf("configuration error = %#v, want path and %q", responseError, test.want)
			}

			if err := os.WriteFile(path, []byte(`{"model":"fixed/model"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			_ = newSession(t, child, child.cwd)
		})
	}
}

func TestHarmlessWorkspaceFilesFallThroughToGlobalConfiguration(t *testing.T) {
	for _, content := range []string{"", " \n\t"} {
		name := "empty"
		if content != "" {
			name = "whitespace"
		}
		t.Run(name, func(t *testing.T) {
			model := startModel(t, sse(evFinishReason("stop")))
			child, session := startSession(t,
				withModel(model),
				withEnvironment("OX_MODEL", ""),
				withGlobalConfig(`{"model":"global/model"}`),
				withWorkspaceConfig(content),
			)
			prompt(t, child, session, name)
			if got := model.requestFor(name).Model; got != "global/model" {
				t.Fatalf("model = %q, want global/model", got)
			}
		})
	}
}

func TestConfigurationReadFailuresNameTheirPath(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *process) string
	}{
		{
			name: ".ox is a file",
			setup: func(t *testing.T, child *process) string {
				path := filepath.Join(child.cwd, ".ox")
				if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(path, "config.json")
			},
		},
		{
			name: "config is a directory",
			setup: func(t *testing.T, child *process) string {
				path := filepath.Join(child.cwd, ".ox", "config.json")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			child := start(t, withEnvironment("OX_MODEL", ""))
			path := test.setup(t, child)
			initialize(t, child)
			responseError := child.requestError("session/new", newSessionRequest(child.cwd))
			if responseError.Code != -32603 || !strings.Contains(responseError.Message, path) || !strings.Contains(responseError.Message, "read configuration file") {
				t.Fatalf("configuration error = %#v, want read error naming %s", responseError, path)
			}
		})
	}
}

func TestNoUsableGlobalBaseNamesOnlyWorkspaceConfiguration(t *testing.T) {
	child := start(t,
		withEnvironment("OX_MODEL", ""),
		withEnvironment("XDG_CONFIG_HOME", "relative"),
		withEnvironment("HOME", ""),
	)
	initialize(t, child)
	responseError := child.requestError("session/new", newSessionRequest(child.cwd))
	workspacePath := filepath.Join(child.cwd, ".ox", "config.json")
	if responseError.Code != -32603 || !strings.Contains(responseError.Message, workspacePath) {
		t.Fatalf("configuration error = %#v, want workspace path", responseError)
	}
	if strings.Contains(responseError.Message, filepath.Join("relative", "ox")) {
		t.Errorf("configuration error = %q, want no relative global path", responseError.Message)
	}
}
