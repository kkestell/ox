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
				withModelOverride(""),
				withGlobalConfig(`{"model":"global/model"}`),
			},
			wantModel: "global/model",
		},
		{
			name: "workspace over global",
			options: []startOption{
				withModelOverride(""),
				withGlobalConfig(`{"model":"global/model"}`),
				withWorkspaceConfig(`{"model":"workspace/model"}`),
			},
			wantModel: "workspace/model",
		},
		{
			name: "CLI over workspace and global",
			options: []startOption{
				withModelOverride("environment/model"),
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

func TestProcessConfigurationPrecedenceAndGlobalTrace(t *testing.T) {
	model := startModel(t, sse(evFinishReason("stop")))
	child, session := startSession(t,
		withGlobalConfig(`{
			"process": {
				"log_level": "warn",
				"openrouter_base_url": "`+model.server.URL+`/api/v1",
				"trace": "global-trace.jsonl"
			}
		}`),
		withoutFlag("--log-level"),
		withoutFlag("--openrouter-base-url"),
	)
	prompt(t, child, session, "global process")
	child.stop()
	if _, err := os.Stat(filepath.Join(child.cwd, "global-trace.jsonl")); err != nil {
		t.Fatalf("global trace: %v", err)
	}
	if strings.Contains(child.stderr.String(), `level=INFO msg="ox starting"`) {
		t.Fatalf("global warn level emitted info log: %s", child.stderr.String())
	}
}

func TestCLIProcessConfigurationOverridesGlobal(t *testing.T) {
	model := startModel(t, sse(evFinishReason("stop")))
	child, session := startSession(t,
		withModel(model),
		withGlobalConfig(`{
			"process": {
				"log_level": "error",
				"openrouter_base_url": "https://global.invalid/api/v1",
				"trace": "global-trace.jsonl"
			}
		}`),
		withLogLevel("debug"),
		withArguments("--trace", "cli-trace.jsonl"),
	)
	prompt(t, child, session, "CLI process")
	child.stop()
	if _, err := os.Stat(filepath.Join(child.cwd, "cli-trace.jsonl")); err != nil {
		t.Fatalf("CLI trace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(child.cwd, "global-trace.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("global trace unexpectedly used: %v", err)
	}
	if !strings.Contains(child.stderr.String(), `level=INFO msg="ox starting"`) {
		t.Fatalf("CLI debug level was not used: %s", child.stderr.String())
	}
}

func TestInvalidGlobalProcessConfigurationFailsStartup(t *testing.T) {
	for name, content := range map[string]string{
		"malformed": `{"model":`,
		"invalid":   `{"process":{"log_level":"verbose"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := runCommand(t, "", nil, withGlobalConfig(content))
			if result.ExitCode != 1 || !strings.Contains(result.Stderr, "configure process") {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestWorkspaceCannotSetProcessConfiguration(t *testing.T) {
	child := start(t, withWorkspaceConfig(`{"process":{"log_level":"error"}}`))
	initialize(t, child)
	responseError := child.requestError("session/new", newSessionRequest(child.cwd))
	if responseError.Code != -32603 || !strings.Contains(responseError.Message, `unknown field "process"`) {
		t.Fatalf("workspace process error = %#v", responseError)
	}
}

func TestLegacyConfigurationEnvironmentIsIgnored(t *testing.T) {
	model := startModel(t, sse(evFinishReason("stop")))
	child, session := startSession(t,
		withModel(model),
		withEnvironment("OX_MODEL", "legacy/model"),
		withEnvironment("OX_LOG_LEVEL", "error"),
		withEnvironment("OX_OPENROUTER_BASE_URL", "https://legacy.invalid/api/v1"),
		withEnvironment("OX_KEYRING_DISABLED", ""),
		withEnvironment("OPENROUTER_API_KEY", "legacy-secret"),
	)
	prompt(t, child, session, "legacy environment")
	if request := model.requestFor("legacy environment"); request.Model != "test/model" || request.Authorization != "Bearer test-key" {
		t.Fatalf("request used legacy environment: %#v", request)
	}
	child.stop()
	if !strings.Contains(child.stderr.String(), `level=INFO msg="ox starting"`) || strings.Contains(child.stderr.String(), "legacy-secret") {
		t.Fatalf("stderr = %s", child.stderr.String())
	}
}

func TestGlobalConfigurationFallsBackToHome(t *testing.T) {
	model := startModel(t, sse(evFinishReason("stop")))
	child, session := startSession(t,
		withModel(model),
		withModelOverride(""),
		withEnvironment("XDG_CONFIG_HOME", ""),
		withFile(filepath.Join(".config", "ox", "settings.json"), `{"model":"home/model"}`),
	)
	prompt(t, child, session, "home fallback")
	if got := model.requestFor("home fallback").Model; got != "home/model" {
		t.Fatalf("model = %q, want home/model", got)
	}
}

func TestWorkspaceConfigurationDoesNotWalkToAParent(t *testing.T) {
	child := start(t,
		withModelOverride(""),
		withWorkspaceConfig(`{"model":"parent/model"}`),
		withFile(filepath.Join("child", ".keep"), ""),
	)
	initialize(t, child)

	childWorkspace := filepath.Join(child.cwd, "child")
	responseError := child.requestError("session/new", newSessionRequest(childWorkspace))
	if responseError.Code != -32603 {
		t.Fatalf("error code = %d, want -32603", responseError.Code)
	}
	if !strings.Contains(responseError.Message, filepath.Join(childWorkspace, ".ox", "settings.json")) {
		t.Errorf("error = %q, want child workspace path", responseError.Message)
	}
}

func TestSessionsResolveConfigurationForTheirOwnWorkspaces(t *testing.T) {
	model := startModel(t)
	model.queueFor("first prompt", sse(evFinishReason("stop")))
	model.queueFor("second prompt", sse(evFinishReason("stop")))
	child := start(t,
		withModel(model),
		withModelOverride(""),
		withFile(filepath.Join("first", ".ox", "settings.json"), `{"model":"first/model"}`),
		withFile(filepath.Join("second", ".ox", "settings.json"), `{"model":"second/model"}`),
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
		withModelOverride(""),
		withWorkspaceConfig(`{"model":"old/model"}`),
	)
	initialize(t, child)
	older := newSession(t, child, child.cwd)

	path := filepath.Join(child.cwd, ".ox", "settings.json")
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
		withModelOverride(""),
		withFile(filepath.Join("real", ".ox", "settings.json"), `{"model":"canonical/model"}`),
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
		withModelOverride(""),
		withFile(filepath.Join("good", ".ox", "settings.json"), `{"model":"good/model"}`),
		withFile(filepath.Join("bad", ".ox", "settings.json"), `{"unknown":true}`),
	)
	initialize(t, child)
	good := newSession(t, child, filepath.Join(child.cwd, "good"))
	running := child.begin("session/prompt", acp.PromptRequest{
		SessionID: good,
		Prompt:    textPrompt("running prompt"),
	})
	held.await(t)

	responseError := child.requestError("session/new", newSessionRequest(filepath.Join(child.cwd, "bad")))
	if responseError.Code != -32603 || !strings.Contains(responseError.Message, filepath.Join("bad", ".ox", "settings.json")) {
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
		{name: "unknown workspace key", relative: filepath.Join(".ox", "settings.json"), content: `{"modle":"test/model"}`, want: `unknown field "modle"`},
		{name: "wrong model type", relative: filepath.Join(".ox", "settings.json"), content: `{"model":1}`, want: "cannot unmarshal number"},
		{name: "blank model", relative: filepath.Join(".ox", "settings.json"), content: `{"model":"   "}`, want: `"model" must not be blank`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			child := start(t,
				withModelOverride(""),
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
				withModelOverride(""),
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
				return filepath.Join(path, "settings.json")
			},
		},
		{
			name: "config is a directory",
			setup: func(t *testing.T, child *process) string {
				path := filepath.Join(child.cwd, ".ox", "settings.json")
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			child := start(t, withModelOverride(""))
			path := test.setup(t, child)
			initialize(t, child)
			responseError := child.requestError("session/new", newSessionRequest(child.cwd))
			if responseError.Code != -32603 || !strings.Contains(responseError.Message, path) || !strings.Contains(responseError.Message, "read settings file") {
				t.Fatalf("configuration error = %#v, want read error naming %s", responseError, path)
			}
		})
	}
}

func TestNoUsableGlobalBaseNamesOnlyWorkspaceConfiguration(t *testing.T) {
	child := start(t,
		withModelOverride(""),
		withEnvironment("XDG_CONFIG_HOME", "relative"),
		withEnvironment("HOME", ""),
	)
	initialize(t, child)
	responseError := child.requestError("session/new", newSessionRequest(child.cwd))
	workspacePath := filepath.Join(child.cwd, ".ox", "settings.json")
	if responseError.Code != -32603 || !strings.Contains(responseError.Message, workspacePath) {
		t.Fatalf("configuration error = %#v, want workspace path", responseError)
	}
	if strings.Contains(responseError.Message, filepath.Join("relative", "ox")) {
		t.Errorf("configuration error = %q, want no relative global path", responseError.Message)
	}
}
