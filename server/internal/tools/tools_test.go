package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	if want := []string{Shell, ShellProcess, ReadFile, Glob, Grep, ApplyPatch}; !reflect.DeepEqual(names, want) {
		t.Errorf("names = %v", names)
	}
	tools := toolbox(t)
	for _, name := range []string{"launch", "write_file"} {
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
		{ReadFile, object{"path": "src/main.rs"}, "Read src/main.rs"},
		{Glob, object{"pattern": "*.rs", "path": "."}, "Find files matching *.rs"},
		{Glob, object{"pattern": "*.rs", "path": "src"}, "Find files matching *.rs in src"},
		{Grep, object{"pattern": "fn main", "path": "src", "glob": "*.rs"}, "Search for fn main in src (files matching *.rs)"},
		{ApplyPatch, object{"patch": "*** Begin Patch\n*** Delete File: src/old.rs\n*** End Patch\n"}, "Apply patch to src/old.rs"},
		{ApplyPatch, object{"patch": "*** Begin Patch\n*** Delete File: a\n*** Delete File: b\n*** End Patch\n"}, "Apply patch to 2 files"},
		{ApplyPatch, object{"patch": "*** Begin Patch\n"}, "Apply patch"},
		{ReadFile, object{"path": strings.Repeat("a", 200)}, "Read " + strings.Repeat("a", 74) + "…"},
	} {
		if got := Title(call(test.name, test.arguments)); got != test.want {
			t.Errorf("%s %v: %q", test.name, test.arguments, got)
		}
	}
}

func TestReadToolsValidateArgumentsAndStayInsideTheWorkspace(t *testing.T) {
	tools := toolbox(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(tools.Workspace, "file"), "needle\n")
	writeFile(t, filepath.Join(outside, "file"), "needle\n")
	os.Symlink(outside, filepath.Join(tools.Workspace, "escape"))
	os.Symlink(filepath.Join(tools.Workspace, "file"), filepath.Join(tools.Workspace, "alias"))
	for _, name := range []string{ReadFile, Glob, Grep} {
		for _, arguments := range []any{"{", "{}", object{"path": 2, "pattern": 2}, object{"path": "file", "pattern": "*", "extra": true}} {
			if outcome := tools.call(name, arguments); outcome.Status != transcript.ToolFailed {
				t.Errorf("%s %v: %+v", name, arguments, outcome)
			}
		}
		for _, path := range []string{"../file", "/etc/passwd", "", "missing", "escape/file", "escape"} {
			arguments := object{"path": path, "pattern": "*"}
			if name == ReadFile {
				arguments = object{"path": path}
			}
			if outcome := tools.call(name, arguments); outcome.Status != transcript.ToolFailed {
				t.Errorf("%s %q: %+v", name, path, outcome)
			}
		}
	}
	for _, arguments := range []object{
		{"path": "file", "offset": 0}, {"path": "file", "limit": 0}, {"path": "file", "limit": 1001},
		{"path": "file", "offset": -1}, {"path": "."},
	} {
		if outcome := tools.call(ReadFile, arguments); outcome.Status != transcript.ToolFailed {
			t.Errorf("read %v: %+v", arguments, outcome)
		}
	}
	for name, arguments := range map[string]object{
		ReadFile: {"path": filepath.Join(tools.Workspace, "alias")},
		Glob:     {"path": tools.Workspace, "pattern": "*"},
		Grep:     {"path": "alias", "pattern": "needle"},
	} {
		if outcome := tools.call(name, arguments); outcome.Status != transcript.ToolCompleted {
			t.Errorf("%s %v: %+v", name, arguments, outcome)
		}
	}
	result := tools.call(ReadFile, object{"path": strings.Repeat("雪", outputLimit)})
	if result.Status != transcript.ToolFailed || len(result.Text) > outputLimit {
		t.Errorf("long path error is not bounded: %d bytes", len(result.Text))
	}
}

func readPage(t *testing.T, tools *Toolbox, offset, limit int) (string, []transcript.ToolContent) {
	t.Helper()
	outcome := tools.call(ReadFile, object{"path": "text", "offset": offset, "limit": limit})
	if outcome.Status != transcript.ToolCompleted {
		t.Fatalf("read %d: %s", offset, outcome.Text)
	}
	return outcome.Text, outcome.Content
}

func TestReadPagesLinesWithinTheByteBudget(t *testing.T) {
	tools := toolbox(t)
	path := filepath.Join(tools.Workspace, "text")
	writeFile(t, path, "one\r\n雪\nlast")
	if text, content := readPage(t, tools, 1, 2); text != "1: one\n2: 雪\nMore content remains. Continue with offset=3.\n" ||
		!reflect.DeepEqual(content, []transcript.ToolContent{{Text: "Lines 1–2"}}) {
		t.Errorf("first page: %q %v", text, content)
	}
	if text, content := readPage(t, tools, 3, 2); text != "3: last\n" || content[0].Text != "Lines 3–3 of 3" {
		t.Errorf("last page: %q %v", text, content)
	}
	if text, _ := readPage(t, tools, 4, 2); text != "Offset is past end of file." {
		t.Errorf("past the end: %q", text)
	}
	writeFile(t, path, "")
	if text, _ := readPage(t, tools, 1, 2); text != "File is empty." {
		t.Errorf("empty: %q", text)
	}

	long := strings.Repeat("x", bodyLimit-4)
	writeFile(t, path, long+"\nend\n")
	if text, _ := readPage(t, tools, 1, 200); !strings.HasPrefix(text, "1: "+long+"\n") || !strings.HasSuffix(text, "offset=2.\n") || len(text) > outputLimit {
		t.Errorf("a line that fits is kept whole")
	}
	writeFile(t, path, "small\n"+strings.Repeat("雪", 20_000)+"\nend")
	if text, _ := readPage(t, tools, 1, 200); !strings.HasSuffix(text, "offset=2.\n") {
		t.Errorf("an oversized line is deferred: %.80q", text)
	}
	preview, _ := readPage(t, tools, 2, 200)
	if !strings.Contains(preview, "Line truncated") || !strings.HasSuffix(preview, "offset=3.\n") ||
		strings.ContainsRune(preview, '�') || len(preview) > outputLimit {
		t.Errorf("an oversized line is previewed: %d bytes", len(preview))
	}
	if text, _ := readPage(t, tools, 3, 200); text != "3: end\n" {
		t.Errorf("after the long line: %q", text)
	}

	for _, bad := range [][]byte{{0}, {255}, {0xe2, 0x82}} {
		contents := append([]byte(strings.Repeat("x", bodyLimit*2)), bad...)
		writeFile(t, path, string(append(contents, "\nnext"...)))
		for _, offset := range []int{1, 2} {
			if outcome := tools.call(ReadFile, object{"path": "text", "offset": offset}); !strings.Contains(outcome.Text, "unsupported text file") {
				t.Errorf("%v at offset %d: %.80q", bad, offset, outcome.Text)
			}
		}
	}
}

func TestSearchesFilterIgnoredFilesAndHandleLiteralArguments(t *testing.T) {
	tools := toolbox(t)
	for path, text := range map[string]string{
		".ignore": "ignored\n", "src/a.rs": "Hello\nneedle\n", "src/b.txt": "needle\n", "ignored": "needle\n",
		".hidden": "needle\n", "- file": "-needle\n", "-": "dash\n",
	} {
		writeFile(t, filepath.Join(tools.Workspace, path), text)
	}
	for _, test := range []struct {
		name      string
		arguments object
		want      string
		summary   string
	}{
		{Glob, object{"pattern": "*.rs", "path": "src"}, "./src/a.rs\n", "1 files"},
		{Glob, object{"pattern": "src/**/*.rs"}, "./src/a.rs\n", "1 files"},
		{Glob, object{"pattern": "{a,c}.{rs,go}"}, "./src/a.rs\n", "1 files"},
		{Grep, object{"pattern": "(?i)hello", "glob": "*.rs"}, "./src/a.rs:1:Hello\n", "1 matches in 1 files"},
		{Grep, object{"pattern": "-needle", "path": "- file"}, "./- file:1:-needle\n", "1 matches in 1 files"},
		{Grep, object{"pattern": "dash", "path": "-"}, "./-:1:dash\n", "1 matches in 1 files"},
		{Grep, object{"pattern": "^needle$", "path": "src/a.rs"}, "./src/a.rs:2:needle\n", "1 matches in 1 files"},
		{Grep, object{"pattern": "needle", "path": ".hidden"}, "./.hidden:1:needle\n", "1 matches in 1 files"},
		{Grep, object{"pattern": "nothing-matches"}, "No matches found.", ""},
	} {
		outcome := tools.call(test.name, test.arguments)
		var summary string
		if len(outcome.Content) > 0 {
			summary = outcome.Content[0].Text
		}
		if outcome.Text != test.want || summary != test.summary {
			t.Errorf("%s %v: %q %q", test.name, test.arguments, outcome.Text, summary)
		}
	}
	all := tools.call(Grep, object{"pattern": "needle"})
	if strings.Contains(all.Text, "ignored") || strings.Contains(all.Text, ".hidden") || all.Content[0].Text != "3 matches in 3 files" {
		t.Errorf("grep everything: %q %v", all.Text, all.Content)
	}
	for _, pattern := range []string{"ignored", ".hidden", "**/*"} {
		if files := tools.call(Glob, object{"pattern": pattern}).Text; strings.Contains(files, "ignored") || strings.Contains(files, ".hidden") {
			t.Errorf("%s listed %q", pattern, files)
		}
	}
	for _, name := range []string{Glob, Grep} {
		if outcome := tools.call(name, object{"pattern": "["}); outcome.Status != transcript.ToolFailed {
			t.Errorf("%s accepted [", name)
		}
	}
}

func TestSearchesCapTheirOutputAndReportUnreadableFiles(t *testing.T) {
	tools := toolbox(t)
	for i := range 300 {
		writeFile(t, filepath.Join(tools.Workspace, fmt.Sprintf("%d-%s", i, strings.Repeat("x", 100))), strings.Repeat("雪", 100))
	}
	for name, pattern := range map[string]string{Glob: "*", Grep: "雪"} {
		outcome := tools.call(name, object{"pattern": pattern})
		if len(outcome.Text) > outputLimit || !strings.HasSuffix(outcome.Text, "Output truncated. Narrow the search path or pattern.") ||
			strings.ContainsRune(outcome.Text, '�') || !strings.HasSuffix(outcome.Content[0].Text, " files, truncated") {
			t.Errorf("%s: %d bytes, %v", name, len(outcome.Text), outcome.Content)
		}
	}

	tools = toolbox(t)
	writeFile(t, filepath.Join(tools.Workspace, "a.rs"), "needle\n")
	writeFile(t, filepath.Join(tools.Workspace, "c.rs"), "needle\n")
	os.Chmod(filepath.Join(tools.Workspace, "c.rs"), 0)
	locked := filepath.Join(tools.Workspace, "locked")
	writeFile(t, filepath.Join(locked, "b.rs"), "needle\n")
	os.Chmod(locked, 0)
	outcome := tools.call(Grep, object{"pattern": "needle"})
	os.Chmod(locked, 0o755)
	for _, want := range []string{"./a.rs:1:needle", "./c.rs: ", "Some paths could not be searched:", "Ripgrep could not enumerate some paths."} {
		if !strings.Contains(outcome.Text, want) {
			t.Errorf("missing %q in %q", want, outcome.Text)
		}
	}
}

func TestGlobsMatchLikeRipgrep(t *testing.T) {
	for _, test := range []struct {
		glob, path string
		want       bool
	}{
		{"*.rs", "./src/deep/a.rs", true},
		{"src/*.rs", "./src/a.rs", true},
		{"src/*.rs", "./src/deep/a.rs", false},
		{"src/**/*.rs", "./src/a.rs", true},
		{"src/**/*.rs", "./src/deep/a.rs", true},
		{"**/a.rs", "./a.rs", true},
		{"src/**", "./src/deep/a.rs", true},
		{"/src/*.rs", "./src/a.rs", true},
		{"a?.rs", "./ab.rs", true},
		{"[!a]b.rs", "./ab.rs", false},
		{"[]x]b.rs", "./]b.rs", true},
		{"*.{rs,go}", "./main.go", true},
		{"!*.rs", "./main.go", true},
		{"a.rs", "./src/xa.rs", false},
	} {
		matcher, err := compileGlob(test.glob)
		if err != nil || matcher.match(test.path) != test.want {
			t.Errorf("%s %s: %v", test.glob, test.path, err)
		}
	}
	for _, glob := range []string{"[", "{a,b", "{a,{b}}"} {
		if _, err := compileGlob(glob); err == nil {
			t.Errorf("%s compiled", glob)
		}
	}
}
