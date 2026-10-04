package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ox/internal/transcript"
)

func TestGlobsUseRipgrepSyntax(t *testing.T) {
	for _, test := range []struct {
		glob, path string
		want       bool
	}{
		{"*.rs", "src/deep/a.rs", true},
		{"src/*.rs", "src/a.rs", true},
		{"src/*.rs", "src/deep/a.rs", false},
		{"src/**/*.rs", "src/a.rs", true},
		{"src/**/*.rs", "src/deep/a.rs", true},
		{"**/a.rs", "a.rs", true},
		{"src/**", "src/deep/a.rs", true},
		{"/src/*.rs", "src/a.rs", true},
		{"a?.rs", "ab.rs", true},
		{"[!a]b.rs", "ab.rs", false},
		{"[]x]b.rs", "]b.rs", true},
		{"*.{rs,go}", "main.go", true},
		{"!*.rs", "main.go", true},
		{"a.rs", "src/xa.rs", false},
		{"雪*.rs", "src/雪.rs", true},
		{"café/*.go", "café/main.go", true},
		{`\雪.rs`, "雪.rs", true},
	} {
		t.Run(test.glob+"/"+test.path, func(t *testing.T) {
			tools := toolbox(t)
			writeFile(t, filepath.Join(tools.Workspace, test.path), "text")
			outcome := tools.call(Glob, object{"pattern": test.glob})
			found := outcome.Text == "./"+test.path+"\n"
			if outcome.Status != transcript.ToolCompleted || found != test.want {
				t.Fatalf("%+v", outcome)
			}
		})
	}
	for _, pattern := range []string{"[", "{a,b"} {
		tools := toolbox(t)
		if outcome := tools.call(Glob, object{"pattern": pattern}); outcome.Status != transcript.ToolFailed {
			t.Errorf("%s: %+v", pattern, outcome)
		}
	}
}

func TestGrepHandlesUnicodeAndInvalidUTF8(t *testing.T) {
	tools := toolbox(t)
	writeFile(t, filepath.Join(tools.Workspace, "text"), "needle 雪\nneedle \xff\nneedle")
	outcome := tools.call(Grep, object{"pattern": "needle"})
	if outcome.Status != transcript.ToolCompleted || outcome.Content[0].Text != "3 matches in 1 files" ||
		outcome.Text != "./text:1:needle 雪\n./text:2:needle �\n./text:3:needle\n" {
		t.Fatalf("%+v", outcome)
	}
}

func TestSearchesAcceptPathsOutsideTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "file")
	writeFile(t, path, "needle\n")
	for _, scope := range []string{"..", parent} {
		for _, test := range []struct{ name, pattern string }{{Glob, "file"}, {Grep, "needle"}} {
			args, _ := json.Marshal(object{"path": scope, "pattern": test.pattern})
			text, _, err := search(context.Background(), root, test.name, string(args))
			if err != nil || !strings.Contains(text, "file") {
				t.Fatalf("%s %s: %q, %v", test.name, scope, text, err)
			}
		}
	}
}

func TestSearchRejectsInvalidPatternsInEmptyWorkspacesAndCancellation(t *testing.T) {
	tools := toolbox(t)
	for _, name := range []string{Glob, Grep} {
		if outcome := tools.call(name, object{"pattern": "["}); outcome.Status != transcript.ToolFailed {
			t.Fatalf("%s: %+v", name, outcome)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := search(ctx, tools.Workspace, name, `{"pattern":"*"}`)
		if err != context.Canceled {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
