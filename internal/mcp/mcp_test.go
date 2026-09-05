package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHTTPActivationDiscoveryAndCall(t *testing.T) {
	for _, jsonResponse := range []bool{true, false} {
		t.Run(map[bool]string{true: "json", false: "sse"}[jsonResponse], func(t *testing.T) {
			server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, &sdk.ServerOptions{PageSize: 1})
			server.AddTool(&sdk.Tool{
				Name: "say hi", Title: "Say hi", Description: "greets",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{
					Content:           []sdk.Content{&sdk.TextContent{Text: "hello"}},
					StructuredContent: map[string]any{"ok": true},
				}, nil
			})
			server.AddTool(&sdk.Tool{Name: "other", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return &sdk.CallToolResult{}, nil
			})
			var missingHeader, missingMetadata bool
			handler := sdk.NewStreamableHTTPHandler(func(request *http.Request) *sdk.Server {
				if request.Header.Get("Authorization") != "secret-token" {
					missingHeader = true
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				request.Body = io.NopCloser(strings.NewReader(string(body)))
				for _, key := range []string{
					"io.modelcontextprotocol/protocolVersion",
					"io.modelcontextprotocol/clientInfo",
					"io.modelcontextprotocol/clientCapabilities",
				} {
					if !strings.Contains(string(body), key) {
						missingMetadata = true
					}
				}
				return server
			}, &sdk.StreamableHTTPOptions{JSONResponse: jsonResponse, Stateless: true})
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()

			bundle, err := Activate(context.Background(), t.TempDir(), []acp.MCPServer{{HTTP: &acp.MCPHTTPServer{
				Type: "http", Name: "demo server", URL: httpServer.URL,
				Headers: []acp.HTTPHeader{{Name: "Authorization", Value: "secret-token"}},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			defer bundle.Close()
			if missingHeader {
				t.Fatal("configured header was absent")
			}
			if missingMetadata {
				t.Fatal("required per-request metadata was absent")
			}
			tools := bundle.Tools()
			if len(tools) != 2 || tools[0].ToolName != "other" || tools[1].Name != "mcp__demo_server__say_hi" || tools[1].ToolName != "say hi" {
				t.Fatalf("tools = %#v", tools)
			}
			encoded, _ := json.Marshal(tools)
			if strings.Contains(string(encoded), "secret-token") {
				t.Fatalf("descriptor leaks secret: %s", encoded)
			}
			result, err := bundle.Call(context.Background(), tools[1].Name, json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Content) != "hello\n{\"ok\":true}" || result.IsError {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestCallRejectsChangedDefinition(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	add := func(description string) {
		server.AddTool(&sdk.Tool{Name: "tool", Description: description, InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "called"}}}, nil
		})
	}
	add("first")
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true}))
	defer httpServer.Close()
	bundle, err := Activate(context.Background(), t.TempDir(), []acp.MCPServer{{HTTP: &acp.MCPHTTPServer{Type: "http", Name: "server", URL: httpServer.URL, Headers: []acp.HTTPHeader{}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	add("changed")
	if _, err := bundle.Call(context.Background(), bundle.Tools()[0].Name, nil); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("call error = %v", err)
	}
}

func TestCallResultPolicies(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "tool-error", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "try again"}}, IsError: true}, nil
	})
	server.AddTool(&sdk.Tool{Name: "too-large", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: strings.Repeat("x", MaxResultBytes+1)}}}, nil
	})
	sdk.AddTool(server, &sdk.Tool{Name: "needs-input"}, func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{InputRequests: sdk.InputRequestMap{"answer": &sdk.ElicitParams{}}}, nil, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true}))
	defer httpServer.Close()
	bundle, err := Activate(context.Background(), t.TempDir(), []acp.MCPServer{{HTTP: &acp.MCPHTTPServer{Type: "http", Name: "server", URL: httpServer.URL, Headers: []acp.HTTPHeader{}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	byTool := map[string]string{}
	for _, descriptor := range bundle.Tools() {
		byTool[descriptor.ToolName] = descriptor.Name
	}
	result, err := bundle.Call(context.Background(), byTool["tool-error"], nil)
	if err != nil || !result.IsError || string(result.Content) != "try again" {
		t.Fatalf("tool error result = %#v, %v", result, err)
	}
	if _, err := bundle.Call(context.Background(), byTool["too-large"], nil); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversized result error = %v", err)
	}
	if _, err := bundle.Call(context.Background(), byTool["needs-input"], nil); err == nil || !strings.Contains(err.Error(), "unsupported client input") {
		t.Fatalf("input-required error = %v", err)
	}
}

func TestStdioActivationUsesRootAndEnvironment(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bundle, err := Activate(context.Background(), root, []acp.MCPServer{{Stdio: &acp.MCPStdioServer{
		Name: "stdio", Command: executable,
		Args: []string{"-test.run=^TestMCPStdioHelper$"},
		Env:  []acp.EnvVariable{{Name: "GO_WANT_MCP_HELPER", Value: "mcp-helper-enabled"}, {Name: "MCP_FIXTURE_VALUE", Value: "configured-secret"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	tools := bundle.Tools()
	result, err := bundle.Call(context.Background(), tools[0].Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := canonical + "|[redacted]"
	if string(result.Content) != want {
		t.Fatalf("result = %q, want %q", result.Content, want)
	}
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "mcp-helper-enabled" {
		return
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "stdio-fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "where", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: cwd + "|" + os.Getenv("MCP_FIXTURE_VALUE")}}}, nil
	})
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		t.Fatal(err)
	}
}

func TestCrossOriginRedirectStripsConfiguredHeaders(t *testing.T) {
	var received string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		received = request.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	origin, _ := url.Parse(source.URL)
	client := &http.Client{Transport: &headerTransport{
		base: http.DefaultTransport, origin: origin,
		headers: http.Header{"Authorization": []string{"secret"}},
	}}
	response, err := client.Get(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if received != "" {
		t.Fatalf("redirect leaked configured header %q", received)
	}
}

func TestRedirectTargetsRejectPlaintextRemoteDestinations(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/mcp",
		"ftp://example.com/mcp",
		"https://user:secret@example.com/mcp",
	} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRedirectTarget(target); err == nil {
			t.Fatalf("accepted redirect target %q", raw)
		}
	}
	for _, raw := range []string{"https://example.com/mcp", "http://127.0.0.1/mcp", "http://service.localhost/mcp"} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateRedirectTarget(target); err != nil {
			t.Fatalf("redirect target %q: %v", raw, err)
		}
	}
}

func TestDescriptorCloneNameAndResultBounds(t *testing.T) {
	name := providerName(strings.Repeat("server", 20), strings.Repeat("tool", 20))
	if len(name) != 64 || name != providerName(strings.Repeat("server", 20), strings.Repeat("tool", 20)) {
		t.Fatalf("provider name = %q", name)
	}
	descriptor := Descriptor{InputSchema: json.RawMessage(`{"type":"object"}`)}
	bundle := &Bundle{tools: []Descriptor{descriptor}}
	got := bundle.Tools()
	got[0].InputSchema[0] = '['
	if bundle.tools[0].InputSchema[0] != '{' {
		t.Fatal("Tools returned mutable schema storage")
	}
	if _, err := renderResult(&sdk.CallToolResult{Content: []sdk.Content{&sdk.ImageContent{}}}); err == nil {
		t.Fatal("accepted image content")
	}
	content, err := renderResult(&sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "failed"}}, IsError: true})
	if err != nil || string(content) != "failed" {
		t.Fatalf("render = %q, %v", content, err)
	}
}

func TestProviderNameIsASCIIAndByteBounded(t *testing.T) {
	name := providerName(strings.Repeat("é", 80), strings.Repeat("١", 80))
	if len(name) > 64 {
		t.Fatalf("provider name is %d bytes: %q", len(name), name)
	}
	for _, value := range []byte(name) {
		if !(value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' || value == '_' || value == '-') {
			t.Fatalf("provider name contains non-ASCII-safe byte %#x: %q", value, name)
		}
	}
}

func TestSecretsAreRejectedFromDefinitionsAndRedactedFromResults(t *testing.T) {
	const secret = "configured-secret-value"
	for _, leak := range []struct {
		name string
		tool *sdk.Tool
	}{
		{name: "name", tool: &sdk.Tool{Name: "tool-" + secret, InputSchema: json.RawMessage(`{"type":"object"}`)}},
		{name: "description", tool: &sdk.Tool{Name: "tool", Description: secret, InputSchema: json.RawMessage(`{"type":"object"}`)}},
		{name: "schema-key", tool: &sdk.Tool{Name: "tool", InputSchema: json.RawMessage(`{"type":"object","properties":{"configured-secret-value":{"type":"string"}}}`)}},
	} {
		t.Run(leak.name, func(t *testing.T) {
			value := &server{name: "server", secrets: []string{secret}, transport: identityTransport{Kind: "http", Destination: "https://example.com"}}
			if _, err := makeDiscovered(value, leak.tool); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("definition error = %v", err)
			}
		})
	}

	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "tool", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "result " + secret}}}, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true}))
	defer httpServer.Close()
	bundle, err := Activate(context.Background(), t.TempDir(), []acp.MCPServer{{HTTP: &acp.MCPHTTPServer{
		Type: "http", Name: "server", URL: httpServer.URL,
		Headers: []acp.HTTPHeader{{Name: "Authorization", Value: secret}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer bundle.Close()
	result, err := bundle.Call(context.Background(), bundle.Tools()[0].Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Content), secret) || string(result.Content) != "result [redacted]" {
		t.Fatalf("result = %q", result.Content)
	}
}

func TestSecretRedactionCoversJSONEscaping(t *testing.T) {
	value := &server{secrets: []string{"token<value"}}
	got := string(value.redactContent([]byte(`{"token":"token\u003cvalue"}`)))
	if strings.Contains(got, `token\u003cvalue`) || got != `{"token":"[redacted]"}` {
		t.Fatalf("redacted content = %q", got)
	}
}

func TestToolDefinitionValidation(t *testing.T) {
	value := &server{name: "server", transport: identityTransport{Kind: "http", Destination: "https://example.com"}}
	for _, schema := range []any{nil, []any{}, json.RawMessage(`{"type":7}`)} {
		if _, err := makeDiscovered(value, &sdk.Tool{Name: "tool", InputSchema: schema}); err == nil {
			t.Fatalf("accepted invalid schema %#v", schema)
		}
	}
	if providerName("a b", "tool") != providerName("a_b", "tool") {
		t.Fatal("fixture does not create an escaped-name collision")
	}
}

func TestIdentityExcludesCredentialValues(t *testing.T) {
	tool := &sdk.Tool{Name: "tool", InputSchema: json.RawMessage(`{"type":"object"}`)}
	one := &server{
		name: "server", secrets: []string{"first"},
		transport: identityTransport{Kind: "http", Destination: "https://example.com", SecretNames: []string{"Authorization"}},
	}
	two := &server{
		name: "server", secrets: []string{"second"},
		transport: identityTransport{Kind: "http", Destination: "https://example.com", SecretNames: []string{"Authorization"}},
	}
	left, err := makeDiscovered(one, tool)
	if err != nil {
		t.Fatal(err)
	}
	right, err := makeDiscovered(two, tool)
	if err != nil {
		t.Fatal(err)
	}
	if left.descriptor.Identity != right.descriptor.Identity {
		t.Fatal("credential rotation changed tool identity")
	}
	if strings.Contains(left.descriptor.Identity, "first") {
		t.Fatal("identity leaked credential")
	}
}

func TestWireReadersRejectOversizedFrames(t *testing.T) {
	reader := &frameReader{reader: strings.NewReader(strings.Repeat("x", MaxWireBytes+1))}
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("accepted oversized NDJSON frame")
	}
	body := &httpBodyReader{reader: io.NopCloser(strings.NewReader(strings.Repeat("x", MaxWireBytes+1)))}
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("accepted oversized JSON response")
	}
}

func TestDiscoveryRejectsExcessivePagesAndBytes(t *testing.T) {
	for _, test := range []struct {
		name string
		list func(*sdk.ListToolsParams) *sdk.ListToolsResult
		want string
	}{
		{
			name: "pages",
			list: func(params *sdk.ListToolsParams) *sdk.ListToolsResult {
				page := 0
				if params.Cursor != "" {
					page, _ = strconv.Atoi(params.Cursor)
				}
				return &sdk.ListToolsResult{Tools: []*sdk.Tool{}, NextCursor: strconv.Itoa(page + 1)}
			},
			want: "256 pages",
		},
		{
			name: "bytes",
			list: func(*sdk.ListToolsParams) *sdk.ListToolsResult {
				return &sdk.ListToolsResult{Tools: []*sdk.Tool{{
					Name: "tool", Description: strings.Repeat("x", MaxCatalogBytes),
					InputSchema: json.RawMessage(`{"type":"object"}`),
				}}}
			},
			want: "256 KiB",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, &sdk.ServerOptions{
				Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
			})
			server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
				return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
					if method == "tools/list" {
						params, _ := request.GetParams().(*sdk.ListToolsParams)
						return test.list(params), nil
					}
					return next(ctx, method, request)
				}
			})
			httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true}))
			defer httpServer.Close()
			_, err := Activate(context.Background(), t.TempDir(), []acp.MCPServer{{HTTP: &acp.MCPHTTPServer{
				Type: "http", Name: "server", URL: httpServer.URL, Headers: []acp.HTTPHeader{},
			}}})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("activation error = %v", err)
			}
		})
	}
}
