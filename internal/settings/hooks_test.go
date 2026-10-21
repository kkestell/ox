package settings_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ox/internal/openroutertest"
	"ox/internal/settings"
)

func TestHookSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if hooks, err := settings.LoadHooks(path); err != nil || len(hooks.PostToolUse) != 0 {
		t.Fatalf("missing file: %+v, %v", hooks, err)
	}
	write(t, path, `{"hooks":{"PostToolUse":[
		{"matcher":"*","hooks":[{"type":"command","command":"first","timeout":10}]},
		{"hooks":[{"type":"command","command":"second"}]},
		{"matcher":"shell|apply_patch","hooks":[{"type":"command","command":"third","timeout":0.5}]}
	]}}`)
	hooks, err := settings.LoadHooks(path)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, hook := range hooks.PostToolUse {
		got = append(got, hook.Command+" "+hook.Timeout.String())
	}
	if strings.Join(got, ", ") != "first 10s, second 1m0s, third 500ms" {
		t.Errorf("hooks = %v", got)
	}
	matches := func(hook settings.Hook, tools ...string) string {
		var matched []string
		for _, tool := range tools {
			if hook.Matches(tool) {
				matched = append(matched, tool)
			}
		}
		return strings.Join(matched, " ")
	}
	all := []string{"shell", "shell_process", "apply_patch", "mcp__project__read"}
	for i, want := range []string{strings.Join(all, " "), strings.Join(all, " "), "shell apply_patch"} {
		if got := matches(hooks.PostToolUse[i], all...); got != want {
			t.Errorf("hook %d matches %q, want %q", i, got, want)
		}
	}
	if !(settings.Hook{Timeout: time.Second}).Matches("anything") {
		t.Error("a hook without a matcher does not match every tool")
	}
}

func TestInvalidHookSettings(t *testing.T) {
	for _, test := range []struct{ hooks, want string }{
		{`null`, "hooks must be an object"},
		{`[]`, "hooks must be an object"},
		{`{"PreToolUse":[]}`, "hook event PreToolUse is not supported"},
		{`{"PostToolUse":null}`, "must be a list of matcher groups"},
		{`{"PostToolUse":[null]}`, "matcher group: must be an object"},
		{`{"PostToolUse":[{"matcher":"*"}]}`, "hooks must be a nonempty list"},
		{`{"PostToolUse":[{"matcher":"*","hooks":[],"extra":1}]}`, "unknown field"},
		{`{"PostToolUse":[{"matcher":"(","hooks":[{"type":"command","command":"x"}]}]}`, `matcher "(" is not a valid regular expression`},
		{`{"PostToolUse":[{"hooks":[{"command":"x"}]}]}`, "type is required"},
		{`{"PostToolUse":[{"hooks":[{"type":"prompt","command":"x"}]}]}`, `hook type "prompt" is not supported`},
		{`{"PostToolUse":[{"hooks":[{"type":"command"}]}]}`, "command must be nonempty"},
		{`{"PostToolUse":[{"hooks":[{"type":"command","command":" "}]}]}`, "command must be nonempty"},
		{`{"PostToolUse":[{"hooks":[{"type":"command","command":"x","timeout":0}]}]}`, "timeout must be"},
		{`{"PostToolUse":[{"hooks":[{"type":"command","command":"x","timeout":"10"}]}]}`, "cannot unmarshal"},
		{`{"PostToolUse":[{"hooks":[{"type":"command","command":"x","async":true}]}]}`, "unknown field"},
	} {
		path := filepath.Join(t.TempDir(), "settings.json")
		write(t, path, `{"hooks":`+test.hooks+`}`)
		_, err := settings.LoadHooks(path)
		if err == nil || !strings.HasPrefix(err.Error(), path+": ") || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: %v", test.hooks, err)
		}
	}
}

func TestHooksCanBeSetOnlyInTheGlobalFile(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".ox/settings.json")
	write(t, path, `{"hooks":{}}`)
	global := settings.Settings{Model: model}
	_, err := global.ForWorkspace(workspace, openroutertest.ParsedCatalog())
	if err == nil || err.Error() != path+": hooks can be set only in the global settings file" {
		t.Errorf("error = %v", err)
	}
}
