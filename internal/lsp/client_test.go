package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLSPHelper(t *testing.T) {
	index := slices.Index(os.Args, "--lsp-helper")
	if index < 0 {
		return
	}
	scenario := "normal"
	if index+1 < len(os.Args) {
		scenario = os.Args[index+1]
	}
	runLSPHelper(os.Stdin, os.Stdout, scenario)
	os.Exit(0)
}

func runLSPHelper(input io.Reader, output io.Writer, scenario string) {
	reader := bufio.NewReader(input)
	var rootURI string
	var text string
	var version int
	var writeMu sync.Mutex
	write := func(value any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		body, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(output, "Content-Length: %d\r\n\r\n", len(body))
		_, _ = output.Write(body)
	}
	writeBroken := func() {
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = fmt.Fprint(output, "Content-Length: 1\r\n\r\n{")
	}
	for {
		raw, err := readMessage(reader)
		if err != nil {
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(raw, &message)
		switch message.Method {
		case "initialize":
			if scenario == "initialize-timeout" {
				continue
			}
			var params struct {
				RootURI string `json:"rootUri"`
			}
			_ = json.Unmarshal(message.Params, &params)
			rootURI = params.RootURI
			if scenario == "cwd" {
				cwd, _ := os.Getwd()
				_ = os.WriteFile(filepath.Join(mustURIPath(rootURI), "cwd"), []byte(cwd), 0o644)
			}
			encoding := "utf-16"
			if strings.HasPrefix(scenario, "encoding-") {
				encoding = strings.TrimPrefix(scenario, "encoding-")
			}
			if scenario == "bad-encoding" {
				encoding = "utf-7"
			}
			syncKind := 1
			if scenario == "no-sync" {
				syncKind = 0
			}
			capabilities := map[string]any{"positionEncoding": encoding, "textDocumentSync": syncKind}
			if scenario == "pull-diagnostics" {
				capabilities["diagnosticProvider"] = map[string]any{"interFileDependencies": false, "workspaceDiagnostics": false}
			}
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"capabilities": capabilities}})
		case "initialized":
			if scenario == "crash" {
				return
			}
		case "textDocument/didOpen":
			var params struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
					Text    string `json:"text"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(message.Params, &params)
			text, version = params.TextDocument.Text, params.TextDocument.Version
			recordSync(rootURI, scenario, version, text)
			publishForScenario(write, scenario, params.TextDocument.URI, version)
		case "textDocument/didChange":
			var params struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			}
			_ = json.Unmarshal(message.Params, &params)
			text, version = params.ContentChanges[0].Text, params.TextDocument.Version
			recordSync(rootURI, scenario, version, text)
			publishForScenario(write, scenario, params.TextDocument.URI, version)
		case "textDocument/definition", "textDocument/references":
			if scenario == "query-timeout" || scenario == "cancel" {
				continue
			}
			if scenario == "malformed-frame" {
				writeBroken()
				continue
			}
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Position wirePosition `json:"position"`
			}
			_ = json.Unmarshal(message.Params, &params)
			position := params.Position
			end := wirePosition{Line: position.Line, Character: position.Character + 1}
			if scenario == "malformed-range" {
				position.Character = 999
				end.Character = 1000
			}
			location := map[string]any{"uri": params.TextDocument.URI, "range": map[string]any{"start": position, "end": end}}
			result := any(location)
			if message.Method == "textDocument/references" {
				result = []any{location, map[string]any{"uri": "file:///outside.go", "range": map[string]any{"start": wirePosition{}, "end": wirePosition{Character: 1}}}}
			}
			response := map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result}
			if scenario == "concurrent" {
				delay := time.Duration(4-position.Character) * 10 * time.Millisecond
				go func() { time.Sleep(delay); write(response) }()
				continue
			}
			write(response)
		case "textDocument/documentSymbol":
			if scenario == "flat-document-symbol" {
				var params struct {
					TextDocument struct {
						URI string `json:"uri"`
					} `json:"textDocument"`
				}
				_ = json.Unmarshal(message.Params, &params)
				write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": []any{
					map[string]any{"name": "Flat", "kind": 12, "containerName": "pkg", "location": map[string]any{"uri": params.TextDocument.URI, "range": rangeValue(0, 0, 0, 1)}},
				}})
				continue
			}
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": []any{
				map[string]any{"name": "Outer", "kind": 5, "range": rangeValue(0, 0, 0, 3), "selectionRange": rangeValue(0, 0, 0, 3), "children": []any{
					map[string]any{"name": "Method", "kind": 6, "range": rangeValue(0, 1, 0, 2), "selectionRange": rangeValue(0, 1, 0, 2)},
				}},
			}})
		case "workspace/symbol":
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": []any{
				map[string]any{"name": "Found", "kind": 12, "containerName": "pkg", "location": map[string]any{"uri": rootURI + "/main.go", "range": rangeValue(0, 0, 0, 1)}},
			}})
		case "textDocument/diagnostic":
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{"kind": "full", "items": []any{
				map[string]any{"range": rangeValue(0, 0, 0, 1), "severity": 1, "message": "broken", "source": "fixture", "code": "E1"},
			}}})
		case "$/cancelRequest":
			_ = os.WriteFile(filepath.Join(mustURIPath(rootURI), "cancelled"), []byte("yes"), 0o644)
		case "shutdown":
			if scenario == "shutdown-hang" {
				continue
			}
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": nil})
		case "exit":
			if scenario == "shutdown-hang" {
				continue
			}
			return
		}
		_ = text
	}
}

func recordSync(rootURI, scenario string, version int, text string) {
	if scenario != "sync" {
		return
	}
	file, err := os.OpenFile(filepath.Join(mustURIPath(rootURI), "sync.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_ = json.NewEncoder(file).Encode(map[string]any{"version": version, "text": text})
}

func publishForScenario(write func(any), scenario, uri string, version int) {
	if !strings.HasPrefix(scenario, "push-") {
		return
	}
	publication := func(value *int, items []any) {
		params := map[string]any{"uri": uri, "diagnostics": items}
		if value != nil {
			params["version"] = *value
		}
		write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": params})
	}
	switch scenario {
	case "push-matching":
		publication(&version, []any{map[string]any{"range": rangeValue(0, 0, 0, 1), "severity": 2, "message": "warning"}})
	case "push-empty":
		publication(&version, []any{})
	case "push-stale":
		stale := version - 1
		publication(&stale, []any{map[string]any{"range": rangeValue(0, 0, 0, 1), "message": "stale"}})
	case "push-unversioned":
		publication(nil, []any{map[string]any{"range": rangeValue(0, 0, 0, 1), "message": "unknown"}})
	}
}

func rangeValue(startLine, startCharacter, endLine, endCharacter int) map[string]any {
	return map[string]any{"start": wirePosition{Line: startLine, Character: startCharacter}, "end": wirePosition{Line: endLine, Character: endCharacter}}
}

func mustURIPath(uri string) string {
	path, _ := uriToPath(uri)
	return path
}

func helperDefinition(t *testing.T, scenario string) Definition {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Definition{Name: "fixture", Command: executable, Args: []string{"-test.run=^TestLSPHelper$", "--", "--lsp-helper", scenario}, Extensions: []string{"go"}}
}

func fixtureManager(t *testing.T, scenario string) (*Manager, string, TextReader) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("abcd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := New(root, []Definition{helperDefinition(t, scenario)})
	if err != nil {
		t.Fatal(err)
	}
	manager.initializeTimeout = 500 * time.Millisecond
	manager.queryTimeout = 250 * time.Millisecond
	manager.shutdownTimeout = 250 * time.Millisecond
	reader := func(_ context.Context, path string) (string, error) {
		raw, err := os.ReadFile(path)
		return string(raw), err
	}
	return manager, path, reader
}

func TestManagerRoundTripAndLazyLifecycle(t *testing.T) {
	manager, path, reader := fixtureManager(t, "normal")
	if manager.servers[0].client != nil {
		t.Fatal("New started a language server eagerly")
	}
	locations, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 2}, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations.Items) != 1 || locations.Items[0].Path != "main.go" || locations.Items[0].Range.Start.Column != 2 {
		t.Fatalf("definition = %#v", locations)
	}
	references, err := manager.References(context.Background(), path, Position{Line: 1, Column: 1}, true, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(references.Items) != 1 || references.Omitted != 1 {
		t.Fatalf("references = %#v", references)
	}
	symbols, err := manager.DocumentSymbols(context.Background(), path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols.Items) != 2 || symbols.Items[1].Container != "Outer" {
		t.Fatalf("document symbols = %#v", symbols)
	}
	workspaceSymbols, err := manager.WorkspaceSymbols(context.Background(), "Found", reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaceSymbols.Items) != 1 || workspaceSymbols.Items[0].Path != "main.go" {
		t.Fatalf("workspace symbols = %#v", workspaceSymbols)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestAuthoritativeTextAndPositionEncodings(t *testing.T) {
	for _, scenario := range []string{"encoding-utf-8", "encoding-utf-16", "encoding-utf-32"} {
		t.Run(scenario, func(t *testing.T) {
			manager, path, _ := fixtureManager(t, scenario)
			defer manager.Close()
			reader := func(context.Context, string) (string, error) { return "a😀z\n", nil }
			locations, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 3}, reader)
			if err != nil {
				t.Fatal(err)
			}
			if locations.Items[0].Range.Start.Column != 3 || locations.Items[0].Range.End.Column != 4 {
				t.Fatalf("location = %#v", locations.Items[0])
			}
		})
	}
}

func TestDocumentSynchronizationUsesAuthoritativeReader(t *testing.T) {
	manager, path, _ := fixtureManager(t, "sync")
	defer manager.Close()
	text := "first\n"
	reader := func(context.Context, string) (string, error) { return text, nil }
	if _, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader); err != nil {
		t.Fatal(err)
	}
	text = "second\n"
	if _, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "sync.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"text":"first\n"`) ||
		!strings.Contains(lines[0], `"version":1`) ||
		!strings.Contains(lines[1], `"text":"second\n"`) ||
		!strings.Contains(lines[1], `"version":2`) {
		t.Fatalf("sync records = %s", raw)
	}
}

func TestFlatDocumentSymbols(t *testing.T) {
	manager, path, reader := fixtureManager(t, "flat-document-symbol")
	defer manager.Close()
	symbols, err := manager.DocumentSymbols(context.Background(), path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols.Items) != 1 || symbols.Items[0].Name != "Flat" || symbols.Items[0].Container != "pkg" {
		t.Fatalf("symbols = %#v", symbols)
	}
}

func TestDiagnosticsStates(t *testing.T) {
	for _, test := range []struct {
		scenario string
		complete bool
		items    int
		wantErr  bool
	}{
		{"pull-diagnostics", true, 1, false},
		{"push-matching", false, 1, false},
		{"push-empty", false, 0, false},
		{"push-stale", false, 0, true},
		{"push-unversioned", false, 0, true},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			manager, path, reader := fixtureManager(t, test.scenario)
			defer manager.Close()
			report, err := manager.Diagnostics(context.Background(), path, reader)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v", err)
			}
			if test.wantErr {
				if !strings.Contains(err.Error(), "diagnostics unavailable") {
					t.Fatalf("unexpected error = %v", err)
				}
				if test.scenario == "push-stale" && !strings.Contains(err.Error(), "stale") {
					t.Fatalf("stale error = %v", err)
				}
				if test.scenario == "push-unversioned" && !strings.Contains(err.Error(), "unversioned") {
					t.Fatalf("unversioned error = %v", err)
				}
				return
			}
			if report.Complete != test.complete || len(report.Items) != test.items || report.Version != 1 {
				t.Fatalf("report = %#v", report)
			}
		})
	}
	t.Run("diagnostic cancellation", func(t *testing.T) {
		manager, path, reader := fixtureManager(t, "normal")
		defer manager.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		_, err := manager.Diagnostics(ctx, path, reader)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestTimeoutCancellationCrashAndMalformedReply(t *testing.T) {
	t.Run("initialize timeout", func(t *testing.T) {
		manager, path, reader := fixtureManager(t, "initialize-timeout")
		defer manager.Close()
		manager.initializeTimeout = 30 * time.Millisecond
		_, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
	})
	for _, scenario := range []string{"bad-encoding", "no-sync"} {
		t.Run(scenario, func(t *testing.T) {
			manager, path, reader := fixtureManager(t, scenario)
			defer manager.Close()
			_, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader)
			if err == nil {
				t.Fatal("invalid initialization was accepted")
			}
		})
	}
	t.Run("query cancellation", func(t *testing.T) {
		manager, path, reader := fixtureManager(t, "cancel")
		defer manager.Close()
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(30 * time.Millisecond); cancel() }()
		_, err := manager.Definition(ctx, path, Position{Line: 1, Column: 1}, reader)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
		deadline := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), "cancelled")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("server did not observe $/cancelRequest")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	t.Run("query timeout", func(t *testing.T) {
		manager, path, reader := fixtureManager(t, "query-timeout")
		defer manager.Close()
		manager.queryTimeout = 30 * time.Millisecond
		_, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("crash is sticky", func(t *testing.T) {
		manager, path, reader := fixtureManager(t, "crash")
		defer manager.Close()
		_, first := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader)
		_, second := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader)
		if first == nil || second == nil || manager.servers[0].client == nil {
			t.Fatalf("errors = %v, %v", first, second)
		}
	})
	t.Run("malformed range", func(t *testing.T) {
		manager, path, reader := fixtureManager(t, "malformed-range")
		defer manager.Close()
		_, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader)
		if err == nil || !strings.Contains(err.Error(), "outside line") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("malformed frame", func(t *testing.T) {
		manager, path, reader := fixtureManager(t, "malformed-frame")
		defer manager.Close()
		_, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader)
		if err == nil || !strings.Contains(err.Error(), "invalid LSP JSON") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestConcurrentResponseCorrelation(t *testing.T) {
	manager, path, reader := fixtureManager(t, "concurrent")
	defer manager.Close()
	var wait sync.WaitGroup
	errors := make(chan error, 4)
	for column := 1; column <= 4; column++ {
		column := column
		wait.Add(1)
		go func() {
			defer wait.Done()
			locations, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: column}, reader)
			if err != nil {
				errors <- err
				return
			}
			if got := locations.Items[0].Range.Start.Column; got != column {
				errors <- fmt.Errorf("column %d result = %d", column, got)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestPATHWorkingDirectoryAndForcedShutdown(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(executable)+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("abcd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	definition := Definition{Name: "fixture", Command: filepath.Base(executable), Args: []string{"-test.run=^TestLSPHelper$", "--", "--lsp-helper", "cwd"}, Extensions: []string{"go"}}
	manager, err := New(root, []Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	manager.queryTimeout = 250 * time.Millisecond
	reader := func(_ context.Context, path string) (string, error) {
		raw, err := os.ReadFile(path)
		return string(raw), err
	}
	if _, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader); err != nil {
		t.Fatal(err)
	}
	seenCWD, err := os.ReadFile(filepath.Join(root, "cwd"))
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, _ := filepath.EvalSymlinks(root)
	if string(seenCWD) != canonicalRoot {
		t.Fatalf("server cwd = %q, want %q", seenCWD, canonicalRoot)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	manager, path, reader = fixtureManager(t, "shutdown-hang")
	manager.shutdownTimeout = 30 * time.Millisecond
	if _, err := manager.Definition(context.Background(), path, Position{Line: 1, Column: 1}, reader); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := manager.Close(); err == nil || time.Since(started) > time.Second {
		t.Fatalf("forced close error/time = %v, %v", err, time.Since(started))
	}
}
