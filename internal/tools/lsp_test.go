package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/lsp"
)

// fakeLanguages records what a tool asked for and replays a canned answer, so
// these tests cover argument handling and rendering without a server process.
type fakeLanguages struct {
	path        string
	position    lsp.Position
	declaration bool
	query       string
	text        string
	readErr     error

	locations lsp.Locations
	symbols   lsp.Symbols
	report    lsp.DiagnosticReport
	err       error
}

func (f *fakeLanguages) record(ctx context.Context, path string, reader lsp.TextReader) {
	f.path = path
	if reader == nil {
		return
	}
	f.text, f.readErr = reader(ctx, path)
}

func (f *fakeLanguages) Definition(
	ctx context.Context, path string, position lsp.Position, reader lsp.TextReader,
) (lsp.Locations, error) {
	f.record(ctx, path, reader)
	f.position = position
	return f.locations, f.err
}

func (f *fakeLanguages) References(
	ctx context.Context, path string, position lsp.Position, declaration bool, reader lsp.TextReader,
) (lsp.Locations, error) {
	f.record(ctx, path, reader)
	f.position, f.declaration = position, declaration
	return f.locations, f.err
}

func (f *fakeLanguages) DocumentSymbols(
	ctx context.Context, path string, reader lsp.TextReader,
) (lsp.Symbols, error) {
	f.record(ctx, path, reader)
	return f.symbols, f.err
}

func (f *fakeLanguages) WorkspaceSymbols(
	ctx context.Context, query string, reader lsp.TextReader,
) (lsp.Symbols, error) {
	f.query = query
	return f.symbols, f.err
}

func (f *fakeLanguages) Diagnostics(
	ctx context.Context, path string, reader lsp.TextReader,
) (lsp.DiagnosticReport, error) {
	f.record(ctx, path, reader)
	return f.report, f.err
}

func languageInvocation(t *testing.T, arguments string, fake *fakeLanguages) agent.Invocation {
	t.Helper()
	invocation := testInvocation(t, arguments)
	invocation.Languages = fake
	return invocation
}

func position(line, column int) lsp.Position { return lsp.Position{Line: line, Column: column} }

func span(startLine, startColumn, endLine, endColumn int) lsp.Range {
	return lsp.Range{Start: position(startLine, startColumn), End: position(endLine, endColumn)}
}

func TestLanguageToolsWithoutConfiguredServers(t *testing.T) {
	for _, name := range []string{
		"lsp_definition", "lsp_references", "lsp_document_symbols",
		"lsp_workspace_symbols", "lsp_diagnostics",
	} {
		t.Run(name, func(t *testing.T) {
			invocation := testInvocation(t, `{"path":"main.go","line":1,"column":1,"query":"x"}`)
			_, err := invoke(t, toolNamed(t, name), invocation)
			if err == nil || !strings.Contains(err.Error(), "no language servers are configured") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLanguageToolArgumentsAndDefaults(t *testing.T) {
	t.Run("references default include_declaration", func(t *testing.T) {
		fake := &fakeLanguages{}
		invocation := languageInvocation(t, `{"path":"main.go","line":3,"column":7}`, fake)
		if _, err := invoke(t, toolNamed(t, "lsp_references"), invocation); err != nil {
			t.Fatal(err)
		}
		if fake.path != "main.go" || fake.position != position(3, 7) || !fake.declaration {
			t.Fatalf("query = %#v", fake)
		}
	})

	t.Run("references honors include_declaration", func(t *testing.T) {
		fake := &fakeLanguages{}
		invocation := languageInvocation(
			t, `{"path":"main.go","line":3,"column":7,"include_declaration":false}`, fake,
		)
		if _, err := invoke(t, toolNamed(t, "lsp_references"), invocation); err != nil {
			t.Fatal(err)
		}
		if fake.declaration {
			t.Fatal("include_declaration=false was ignored")
		}
	})

	t.Run("definition rejects the references option", func(t *testing.T) {
		invocation := languageInvocation(
			t, `{"path":"main.go","line":1,"column":1,"include_declaration":true}`, &fakeLanguages{},
		)
		if _, err := invoke(t, toolNamed(t, "lsp_definition"), invocation); err == nil {
			t.Fatal("lsp_definition accepted include_declaration")
		}
	})

	t.Run("workspace symbols pass the query through", func(t *testing.T) {
		fake := &fakeLanguages{}
		invocation := languageInvocation(t, `{"query":"Manager"}`, fake)
		if _, err := invoke(t, toolNamed(t, "lsp_workspace_symbols"), invocation); err != nil {
			t.Fatal(err)
		}
		if fake.query != "Manager" {
			t.Fatalf("query = %q", fake.query)
		}
	})

	for name, arguments := range map[string]string{
		"missing line":        `{"path":"main.go","column":1}`,
		"missing column":      `{"path":"main.go","line":1}`,
		"missing path":        `{"line":1,"column":1}`,
		"zero line":           `{"path":"main.go","line":0,"column":1}`,
		"negative column":     `{"path":"main.go","line":1,"column":-2}`,
		"unknown property":    `{"path":"main.go","line":1,"column":1,"extra":true}`,
		"wrong include type":  `{"path":"main.go","line":1,"column":1,"include_declaration":"yes"}`,
		"not an object":       `[]`,
		"trailing characters": `{"path":"main.go","line":1,"column":1} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			invocation := languageInvocation(t, arguments, &fakeLanguages{})
			if _, err := invoke(t, toolNamed(t, "lsp_references"), invocation); err == nil {
				t.Fatal("invalid arguments were accepted")
			}
		})
	}
}

// The adapter reads whole files through the activation's selected filesystem,
// so a client-backed executor decides what the server sees.
func TestLanguageReaderUsesTheSelectedFileSystem(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		fake := &fakeLanguages{}
		invocation := languageInvocation(t, `{"path":"main.go"}`, fake)
		writeToolFile(t, invocation.Root, "main.go", "on disk\n")
		if _, err := invoke(t, toolNamed(t, "lsp_document_symbols"), invocation); err != nil {
			t.Fatal(err)
		}
		if fake.text != "on disk\n" || fake.readErr != nil {
			t.Fatalf("text = %q, err = %v", fake.text, fake.readErr)
		}
	})

	t.Run("client", func(t *testing.T) {
		fake := &fakeLanguages{}
		invocation := languageInvocation(t, `{"path":"main.go"}`, fake)
		writeToolFile(t, invocation.Root, "main.go", "on disk\n")
		invocation.FileSystem.ReadTextFile = func(
			context.Context, string, *int, *int,
		) (string, error) {
			return "unsaved\n", nil
		}
		if _, err := invoke(t, toolNamed(t, "lsp_document_symbols"), invocation); err != nil {
			t.Fatal(err)
		}
		if fake.text != "unsaved\n" || fake.readErr != nil {
			t.Fatalf("text = %q, err = %v", fake.text, fake.readErr)
		}
	})

	t.Run("client text stays within the local size bound", func(t *testing.T) {
		fake := &fakeLanguages{}
		invocation := languageInvocation(t, `{"path":"main.go"}`, fake)
		writeToolFile(t, invocation.Root, "main.go", "on disk\n")
		invocation.FileSystem.ReadTextFile = func(
			context.Context, string, *int, *int,
		) (string, error) {
			return strings.Repeat("x", 9<<20), nil
		}
		if _, err := invoke(t, toolNamed(t, "lsp_document_symbols"), invocation); err != nil {
			t.Fatal(err)
		}
		if fake.readErr == nil || !strings.Contains(fake.readErr.Error(), "byte limit") {
			t.Fatalf("read error = %v", fake.readErr)
		}
	})
}

func TestLanguageToolsReportServerFailures(t *testing.T) {
	fake := &fakeLanguages{err: errors.New("language server exited")}
	invocation := languageInvocation(t, `{"path":"main.go","line":1,"column":1}`, fake)
	_, err := invoke(t, toolNamed(t, "lsp_definition"), invocation)
	if err == nil || !strings.Contains(err.Error(), "language server exited") {
		t.Fatalf("error = %v", err)
	}
}

func TestLanguageRendering(t *testing.T) {
	t.Run("locations", func(t *testing.T) {
		fake := &fakeLanguages{locations: lsp.Locations{
			Items: []lsp.Location{
				{Path: "a.go", Range: span(1, 2, 1, 5)},
				{Path: "b.go", Range: span(9, 1, 9, 4)},
			},
			Omitted: 2,
		}}
		invocation := languageInvocation(t, `{"path":"main.go","line":1,"column":1}`, fake)
		got, err := invoke(t, toolNamed(t, "lsp_definition"), invocation)
		if err != nil {
			t.Fatal(err)
		}
		want := "a.go:1:2\nb.go:9:1\n[2 results omitted: outside the workspace or unreadable]"
		if got != want {
			t.Fatalf("rendered = %q", got)
		}
	})

	t.Run("no locations", func(t *testing.T) {
		fake := &fakeLanguages{}
		invocation := languageInvocation(t, `{"path":"main.go","line":1,"column":1}`, fake)
		got, err := invoke(t, toolNamed(t, "lsp_definition"), invocation)
		if err != nil {
			t.Fatal(err)
		}
		if got != "no definition found" {
			t.Fatalf("rendered = %q", got)
		}
	})

	t.Run("symbols name their kind and container", func(t *testing.T) {
		fake := &fakeLanguages{symbols: lsp.Symbols{Items: []lsp.Symbol{
			{Name: "Manager", Kind: 23, Path: "lsp.go", Range: span(10, 1, 40, 2)},
			{Name: "Close", Kind: 6, Container: "Manager", Path: "lsp.go", Range: span(50, 1, 55, 2)},
			{Name: "Odd", Kind: 99, Path: "lsp.go", Range: span(60, 1, 60, 9)},
		}}}
		invocation := languageInvocation(t, `{"path":"lsp.go"}`, fake)
		got, err := invoke(t, toolNamed(t, "lsp_document_symbols"), invocation)
		if err != nil {
			t.Fatal(err)
		}
		want := "struct Manager lsp.go:10:1-40:2\n" +
			"method Manager.Close lsp.go:50:1-55:2\n" +
			"kind(99) Odd lsp.go:60:1-60:9"
		if got != want {
			t.Fatalf("rendered = %q", got)
		}
	})

	t.Run("complete diagnostics", func(t *testing.T) {
		fake := &fakeLanguages{report: lsp.DiagnosticReport{
			Version: 3, Complete: true, Items: []lsp.Diagnostic{
				{Path: "a.go", Range: span(1, 1, 1, 2), Severity: 1, Message: "broken", Source: "vet", Code: "E1"},
				{Path: "a.go", Range: span(2, 4, 2, 9), Severity: 2, Message: "shadowed"},
				{Path: "b.go", Range: span(7, 1, 7, 2), Severity: 9, Message: "unknown", Code: "X"},
			},
		}}
		invocation := languageInvocation(t, `{"path":"a.go"}`, fake)
		got, err := invoke(t, toolNamed(t, "lsp_diagnostics"), invocation)
		if err != nil {
			t.Fatal(err)
		}
		want := "version 3, analysis complete\n" +
			"error a.go:1:1 broken [vet E1]\n" +
			"warning a.go:2:4 shadowed\n" +
			"severity(9) b.go:7:1 unknown [X]"
		if got != want {
			t.Fatalf("rendered = %q", got)
		}
	})

	t.Run("incomplete diagnostics say so", func(t *testing.T) {
		fake := &fakeLanguages{report: lsp.DiagnosticReport{Version: 4}}
		invocation := languageInvocation(t, `{"path":"a.go"}`, fake)
		got, err := invoke(t, toolNamed(t, "lsp_diagnostics"), invocation)
		if err != nil {
			t.Fatal(err)
		}
		want := "no diagnostics (version 4, published diagnostics only; analysis may still be running)"
		if got != want {
			t.Fatalf("rendered = %q", got)
		}
	})
}

// A result too large to inline goes through the ordinary spill path, so the
// model pages through a file instead of receiving an unusable wall of text.
func TestLanguageResultsSpillWhenOversized(t *testing.T) {
	items := make([]lsp.Location, 0, 4000)
	for index := range cap(items) {
		items = append(items, lsp.Location{
			Path: strings.Repeat("d", 40) + "/file.go", Range: span(index+1, 1, index+1, 2),
		})
	}
	fake := &fakeLanguages{locations: lsp.Locations{Items: items}}
	invocation := languageInvocation(t, `{"path":"main.go","line":1,"column":1}`, fake)
	var spilled string
	invocation.ReportSpill = func(path string) { spilled = path }
	got, err := invoke(t, toolNamed(t, "lsp_references"), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if spilled == "" || len(got) > lspInlineBytes {
		t.Fatalf("spill = %q, rendered %d bytes", spilled, len(got))
	}
}
