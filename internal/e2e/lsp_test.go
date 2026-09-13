package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
)

// fixtureLanguageServer points ox at this test binary running as a language
// server for .go files, which is the only server a session needs to answer a
// query.
func fixtureLanguageServer(t *testing.T) map[string]any {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"command":    executable,
		"args":       []string{"-test.run=^TestE2ELanguageServerHelper$"},
		"extensions": []string{".GO"},
	}
}

func languageServerConfig(t *testing.T, marker string, servers map[string]any) startOption {
	t.Helper()
	config, err := json.Marshal(map[string]any{
		"process": map[string]any{"language_servers": servers},
	})
	if err != nil {
		t.Fatal(err)
	}
	return combine(
		withGlobalConfig(string(config)),
		withEnvironment(helperVariable, "enabled"),
		withEnvironment("OX_LSP_SHUTDOWN_MARKER", marker),
	)
}

func combine(options ...startOption) startOption {
	return func(config *startConfig) {
		for _, option := range options {
			option(config)
		}
	}
}

func TestLanguageQueriesThroughShippedBinary(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "language-server-closed")
	model := startModel(t,
		toolResponse("call-symbols", "lsp_document_symbols", `{"path":"main.go"}`),
		sse(evText("outlined"), evFinishReason("stop")),
		toolResponse("call-definition", "lsp_definition", `{"path":"main.go","line":1,"column":6}`),
		sse(evText("located"), evFinishReason("stop")),
		toolResponse("call-references", "lsp_references", `{"path":"main.go","line":3,"column":6}`),
		sse(evText("referenced"), evFinishReason("stop")),
		toolResponse("call-fanned-out", "lsp_workspace_symbols", `{"query":"main"}`),
		sse(evText("searched"), evFinishReason("stop")),
		toolResponse("call-diagnostics", "lsp_diagnostics", `{"path":"main.go"}`),
		sse(evText("checked"), evFinishReason("stop")),
		toolResponse("call-unowned", "lsp_definition", `{"path":"notes.txt","line":1,"column":1}`),
		sse(evText("unsupported"), evFinishReason("stop")),
		toolResponse("call-uninstalled", "lsp_definition", `{"path":"script.py","line":1,"column":1}`),
		sse(evText("unavailable"), evFinishReason("stop")),
	)
	child := start(t,
		withModel(model),
		languageServerConfig(t, marker, map[string]any{
			"fixture": fixtureLanguageServer(t),
			"uninstalled": map[string]any{
				"command":    "ox-language-server-that-is-not-installed",
				"extensions": []string{"py"},
			},
		}),
		withFile("main.go", "package main\n\nfunc main() {}\n"),
		withFile("notes.txt", "plain text\n"),
		withFile("script.py", "x = 1\n"),
	)
	initialize(t, child)
	session := newSession(t, child, child.cwd)

	prompt(t, child, session, "outline the file")
	_ = updates(t, child, session)
	prompt(t, child, session, "find the definition")
	_ = updates(t, child, session)
	prompt(t, child, session, "find the references")
	_ = updates(t, child, session)
	prompt(t, child, session, "search every configured server")
	_ = updates(t, child, session)
	prompt(t, child, session, "check diagnostics")
	_ = updates(t, child, session)
	prompt(t, child, session, "query an unowned file")
	_ = updates(t, child, session)
	prompt(t, child, session, "query a file whose server is not installed")
	_ = updates(t, child, session)

	requests := model.requests()
	if len(requests) != 14 {
		t.Fatalf("model requests = %d, want 14", len(requests))
	}
	for _, name := range []string{
		"lsp_document_symbols", "lsp_workspace_symbols", "lsp_definition",
		"lsp_references", "lsp_diagnostics",
	} {
		if !requestHasTool(requests[0], name) {
			t.Fatalf("language tool %q was not offered to the model", name)
		}
	}
	for index, want := range map[int]string{
		1: "function main main.go:3:1-3:15",
		3: "main.go:1:6",
		// The location outside the workspace is dropped, so one confined
		// reference is reported and the omission is noted.
		5: "main.go:3:6",
		// A workspace-wide query has no file to choose a server by, so it
		// reaches every configured one and fails when any cannot start.
		7: `start language server "uninstalled"`,
		// The version counts this session's synchronizations of main.go: the
		// outline opened it, and the definition, references, and diagnostics
		// queries each resent it. The failed workspace query never reached it.
		9:  "version 4, analysis complete\nerror main.go:1:1 fixture diagnostic [fixture E1]",
		11: `no language server is configured for extension "txt"`,
		// Ox never installs a server, so a command it cannot run is an ordinary
		// tool failure rather than a retry or a download.
		13: `start language server "uninstalled"`,
	} {
		if !requestContainsText(requests[index], want) {
			t.Fatalf("request %d did not carry %q: %s", index, want, requestText(requests[index]))
		}
	}

	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	awaitMarker(t, marker)
	child.stop()
}

// TestWorkspaceSymbolsThroughShippedBinary covers the one language tool a
// path-scoped query cannot reach. It needs a workspace whose every configured
// server can start, because a workspace-wide query reaches all of them.
func TestWorkspaceSymbolsThroughShippedBinary(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "language-server-closed")
	model := startModel(t,
		toolResponse("call-workspace-symbols", "lsp_workspace_symbols", `{"query":"main"}`),
		sse(evText("searched"), evFinishReason("stop")),
	)
	child := start(t,
		withModel(model),
		languageServerConfig(t, marker, map[string]any{"fixture": fixtureLanguageServer(t)}),
		withFile("main.go", "package main\n\nfunc main() {}\n"),
	)
	initialize(t, child)
	session := newSession(t, child, child.cwd)

	prompt(t, child, session, "search workspace symbols")
	_ = updates(t, child, session)

	requests := model.requests()
	if len(requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(requests))
	}
	if !requestContainsText(requests[1], "function pkg.main main.go:3:1-3:15") {
		t.Fatalf("workspace symbols did not reach the model: %s", requestText(requests[1]))
	}

	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	awaitMarker(t, marker)
	child.stop()
}

func requestText(request modelRequest) string {
	var out strings.Builder
	for _, message := range request.Messages {
		for _, content := range message.Content {
			out.WriteString(content.Text)
			out.WriteString("\n")
		}
	}
	return out.String()
}

func awaitMarker(t *testing.T, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("language server did not shut down with the session")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestE2ELanguageServerHelper is the language server ox drives in
// TestLanguageQueriesThroughShippedBinary. It answers the few requests those
// queries make and records its own shutdown.
func TestE2ELanguageServerHelper(t *testing.T) {
	if os.Getenv(helperVariable) != "enabled" ||
		!slices.Contains(os.Args, "-test.run=^TestE2ELanguageServerHelper$") {
		return
	}
	runLanguageServerHelper(os.Stdin, os.Stdout)
	if marker := os.Getenv("OX_LSP_SHUTDOWN_MARKER"); marker != "" {
		if err := os.WriteFile(marker, []byte("closed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func runLanguageServerHelper(input io.Reader, output io.Writer) {
	reader := bufio.NewReader(input)
	write := func(value any) {
		body, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(output, "Content-Length: %d\r\n\r\n%s", len(body), body)
	}
	var documentURI, rootURI string
	for {
		raw, err := readLanguageServerMessage(reader)
		if err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return
		}
		switch request.Method {
		case "initialize":
			var params struct {
				RootURI string `json:"rootUri"`
			}
			_ = json.Unmarshal(request.Params, &params)
			rootURI = params.RootURI
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
				"capabilities": map[string]any{
					"positionEncoding": "utf-16",
					"textDocumentSync": 1,
					"diagnosticProvider": map[string]any{
						"interFileDependencies": false, "workspaceDiagnostics": false,
					},
				},
			}})
		case "textDocument/didOpen":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(request.Params, &params)
			documentURI = params.TextDocument.URI
		case "textDocument/documentSymbol":
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": []any{
				map[string]any{
					"name": "main", "kind": 12,
					"range":          languageServerRange(2, 0, 2, 14),
					"selectionRange": languageServerRange(2, 5, 2, 9),
				},
			}})
		case "textDocument/definition":
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
				"uri": documentURI, "range": languageServerRange(0, 5, 0, 9),
			}})
		case "textDocument/references":
			// The second location is outside the workspace, so the confined
			// result must drop it rather than report a path a tool cannot read.
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": []any{
				map[string]any{"uri": documentURI, "range": languageServerRange(2, 5, 2, 9)},
				map[string]any{
					"uri":   "file:///outside.go",
					"range": languageServerRange(0, 0, 0, 1),
				},
			}})
		case "workspace/symbol":
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": []any{
				map[string]any{
					"name": "main", "kind": 12, "containerName": "pkg",
					"location": map[string]any{
						"uri":   rootURI + "/main.go",
						"range": languageServerRange(2, 0, 2, 14),
					},
				},
			}})
		case "textDocument/diagnostic":
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{
				"kind": "full", "items": []any{map[string]any{
					"range": languageServerRange(0, 0, 0, 4), "severity": 1,
					"message": "fixture diagnostic", "source": "fixture", "code": "E1",
				}},
			}})
		case "shutdown":
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": nil})
		case "exit":
			return
		}
	}
}

func languageServerRange(startLine, startCharacter, endLine, endCharacter int) map[string]any {
	return map[string]any{
		"start": map[string]any{"line": startLine, "character": startCharacter},
		"end":   map[string]any{"line": endLine, "character": endCharacter},
	}
}

func readLanguageServerMessage(reader *bufio.Reader) ([]byte, error) {
	length := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if found && strings.EqualFold(strings.TrimSpace(name), "content-length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if length <= 0 {
		return nil, io.ErrUnexpectedEOF
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}
