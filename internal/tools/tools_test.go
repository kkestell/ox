package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ox/internal/shellproc"
	"ox/internal/transcript"
)

func toolbox(t *testing.T) *Toolbox {
	t.Helper()
	processes := &shellproc.Processes{}
	t.Cleanup(processes.Shutdown)
	return &Toolbox{Workspace: t.TempDir(), Processes: processes}
}

func call(name string, arguments any) transcript.ToolCall {
	text, ok := arguments.(string)
	if !ok {
		data, _ := json.Marshal(arguments)
		text = string(data)
	}
	return transcript.ToolCall{CallID: "call-1", Name: name, Arguments: text}
}

func (t *Toolbox) call(name string, arguments any) transcript.ToolOutcome {
	return t.Execute(context.Background(), call(name, arguments))
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
		t.Fatal(err)
	}
}

type object = map[string]any

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func entries(t *testing.T, dir string) int {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestSchemasRejectExtraPropertiesAndUnknownToolsFail(t *testing.T) {
	var names []string
	for _, schema := range Schemas() {
		var decoded struct {
			Function struct {
				Name       string `json:"name"`
				Parameters struct {
					AdditionalProperties bool `json:"additionalProperties"`
				} `json:"parameters"`
			} `json:"function"`
		}
		if err := json.Unmarshal(schema, &decoded); err != nil || decoded.Function.Parameters.AdditionalProperties {
			t.Errorf("%s: %v", schema, err)
		}
		names = append(names, decoded.Function.Name)
	}
	if want := []string{Shell, ShellProcess, WebFetch}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v", names)
	}
	tools := toolbox(t)
	for _, name := range []string{"launch", "write_file", "read_file", "glob", "grep", "apply_patch"} {
		if outcome := tools.call(name, "{}"); !reflect.DeepEqual(outcome, transcript.Failed("Unknown tool: "+name)) || Title(call(name, "{}")) != name {
			t.Errorf("%s: %+v", name, outcome)
		}
	}
}

func TestTitlesDescribeTheCallAndFallBackToTheToolName(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments any
		want      string
	}{
		{Shell, object{"command": "cargo test"}, "cargo test"},
		{Shell, object{"command": "\n  cargo build\ncargo test\n"}, "cargo build …"},
		{Shell, object{"command": "  \n"}, "Run shell command"},
		{Shell, "not json", "Run shell command"},
		{Shell, object{"command": "npm run dev", "background": true}, "Background: npm run dev"},
		{ShellProcess, object{"action": "list"}, "List shell processes"},
		{ShellProcess, object{"action": "write", "process_id": "p-1", "text": "y\n"}, "Write to shell process p-1"},
		{ShellProcess, object{"action": "stop"}, "Use shell process"},
		{WebFetch, object{"url": "https://example.com"}, "Fetch https://example.com"},
		{WebFetch, "not json", "Fetch web page"},
		{Shell, object{"command": strings.Repeat("a", 200)}, strings.Repeat("a", 79) + "…"},
	} {
		if got := Title(call(test.name, test.arguments)); got != test.want {
			t.Errorf("%s %v: %q", test.name, test.arguments, got)
		}
	}
}
