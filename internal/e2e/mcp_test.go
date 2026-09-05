package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpHTTPServer(name, endpoint, secret string) acp.MCPServer {
	headers := []acp.HTTPHeader{}
	if secret != "" {
		headers = append(headers, acp.HTTPHeader{Name: "Authorization", Value: secret})
	}
	return acp.MCPServer{HTTP: &acp.MCPHTTPServer{
		Type: "http", Name: name, URL: endpoint, Headers: headers,
	}}
}

func newMCPSession(t *testing.T, child *process, servers ...acp.MCPServer) string {
	t.Helper()
	created := child.request("session/new", acp.NewSessionRequest{
		CWD: child.cwd, MCPServers: servers,
	})
	var session acp.NewSessionResponse
	if err := json.Unmarshal(created, &session); err != nil {
		t.Fatal(err)
	}
	if session.SessionID == "" {
		t.Fatal("MCP session has no ID")
	}
	return session.SessionID
}

func TestHTTPMCPActivationThroughShippedBinary(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name: "lookup", Description: "look up a value",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "found"},
		}}, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true, PropagateRequestCancellation: true},
	))
	defer httpServer.Close()

	child := start(t)
	initialized := child.request("initialize", acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion,
	})
	var initialize acp.InitializeResponse
	if err := json.Unmarshal(initialized, &initialize); err != nil {
		t.Fatal(err)
	}
	if initialize.AgentCapabilities == nil ||
		initialize.AgentCapabilities.MCPCapabilities == nil ||
		!initialize.AgentCapabilities.MCPCapabilities.HTTP ||
		initialize.AgentCapabilities.MCPCapabilities.SSE {
		t.Fatalf("MCP capabilities = %#v", initialize.AgentCapabilities)
	}
	created := child.request("session/new", acp.NewSessionRequest{
		CWD: child.cwd,
		MCPServers: []acp.MCPServer{{HTTP: &acp.MCPHTTPServer{
			Type: "http", Name: "search", URL: httpServer.URL,
			Headers: []acp.HTTPHeader{},
		}}},
	})
	var session acp.NewSessionResponse
	if err := json.Unmarshal(created, &session); err != nil {
		t.Fatal(err)
	}
	if session.SessionID == "" {
		t.Fatal("MCP session has no ID")
	}
	_ = child.request("session/close", acp.CloseSessionRequest{SessionID: session.SessionID})
}

func TestMCPDispatchPolicySecretsAndShutdownThroughShippedBinary(t *testing.T) {
	const (
		secret   = "mcp-secret-sentinel"
		toolName = "mcp__fixture_server__lookup"
	)
	var calls atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name: "lookup", Description: "look up a value",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
	}, func(_ context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		calls.Add(1)
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "found " + secret},
		}}, nil
	})
	sdkHandler := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true},
	)
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != secret {
			t.Errorf("MCP Authorization = %q", request.Header.Get("Authorization"))
		}
		sdkHandler.ServeHTTP(writer, request)
	}))
	defer httpServer.Close()

	model := startModel(t,
		toolResponse("call-mcp", toolName, `{}`),
		sse(evText("done"), evFinishReason("stop")),
		sse(evText("plan"), evFinishReason("stop")),
	)
	child := start(t, withModel(model), withArguments("--trace", "trace.jsonl"))
	initialize(t, child)
	session := newMCPSession(t, child, mcpHTTPServer("fixture server", httpServer.URL, secret))
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("use MCP"),
	})
	permissionMessage := child.serverRequest()
	permission := permissionRequest(t, permissionMessage, "call-mcp")
	if permission.ToolCall.Name != toolName || permission.ToolCall.Title != "fixture server / lookup" {
		t.Fatalf("MCP permission = %#v", permission.ToolCall)
	}
	child.respond(permissionMessage, acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Outcome: "selected", OptionID: "allow_once",
	}})
	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	if calls.Load() != 1 {
		t.Fatalf("MCP calls = %d", calls.Load())
	}

	requests := model.requests()
	encodedRequests, err := json.Marshal(requests)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedRequests), secret) ||
		!requestContainsText(requests[1], "found [redacted]") {
		t.Fatalf("provider requests contain unsafe MCP result: %s", encodedRequests)
	}
	if !requestHasTool(requests[0], toolName) {
		t.Fatal("parent model did not receive MCP tool")
	}

	child.request(acp.MethodSessionSetConfigOption, acp.SetSessionConfigOptionRequest{
		SessionID: session, ConfigID: "mode", Value: "plan",
	})
	prompt(t, child, session, "plan with annotations")
	_ = updates(t, child, session)
	requests = model.requests()
	if requestHasTool(requests[2], toolName) {
		t.Fatal("read-only MCP annotation exposed tool in plan mode")
	}

	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	child.stop()
	for _, path := range []string{
		filepath.Join(child.cwd, "trace.jsonl"),
		filepath.Join(child.cwd, "data", "ox", "sessions", session+".jsonl"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("%s contains MCP secret", path)
		}
	}
	for _, received := range child.received {
		if strings.Contains(string(received.raw), secret) {
			t.Fatal("ACP output contains MCP secret")
		}
	}
}

func TestMCPSecretInDefinitionIsRejectedWithoutExposure(t *testing.T) {
	const secret = "definition-secret-sentinel"
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{
		Name: "tool", Description: "reflected " + secret,
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{}, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true},
	))
	defer httpServer.Close()
	child := start(t)
	initialize(t, child)
	err := child.requestError("session/new", acp.NewSessionRequest{
		CWD:        child.cwd,
		MCPServers: []acp.MCPServer{mcpHTTPServer("fixture", httpServer.URL, secret)},
	})
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), secret) || !strings.Contains(err.Message, "configured secret") {
		t.Fatalf("activation error = %s", encoded)
	}
}

func requestHasTool(request modelRequest, name string) bool {
	for _, tool := range request.Tools {
		if strings.Contains(string(tool), `"name":"`+name+`"`) {
			return true
		}
	}
	return false
}

func TestMCPChildDispatchThroughShippedBinary(t *testing.T) {
	const toolName = "mcp__fixture__lookup"
	var calls atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		calls.Add(1)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "child result"}}}, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true},
	))
	defer httpServer.Close()
	model := startModel(t,
		toolResponse("call-task", "task", `{"description":"MCP child","prompt":"use the MCP lookup tool"}`),
		toolResponse("call-child-mcp", toolName, `{}`),
		sse(evText("child done"), evFinishReason("stop")),
		sse(evText("parent done"), evFinishReason("stop")),
	)
	child := start(t, withModel(model))
	initialize(t, child)
	session := newMCPSession(t, child, mcpHTTPServer("fixture", httpServer.URL, ""))
	turn := child.begin("session/prompt", acp.PromptRequest{SessionID: session, Prompt: textPrompt("delegate MCP")})
	permission := child.serverRequest()
	var requested acp.RequestPermissionRequest
	if err := json.Unmarshal(permission.Params, &requested); err != nil {
		t.Fatal(err)
	}
	if permission.Method != acp.MethodSessionRequestPermission || requested.ToolCall.ToolCallID == "" || requested.ToolCall.Name != toolName {
		t.Fatalf("child MCP permission = %#v", requested.ToolCall)
	}
	child.respond(permission, acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Outcome: "selected", OptionID: "allow_once",
	}})
	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, session)
	if calls.Load() != 1 {
		t.Fatalf("MCP calls = %d", calls.Load())
	}
	requests := model.requests()
	if len(requests) != 4 || !requestHasTool(requests[0], toolName) || !requestHasTool(requests[1], toolName) ||
		!requestContainsText(requests[2], "child result") {
		t.Fatalf("parent/child MCP requests = %#v", requests)
	}
}

func TestMCPPendingRecoveryAndReplayThroughShippedBinary(t *testing.T) {
	const toolName = "mcp__fixture__effect"
	var calls atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "effect", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		calls.Add(1)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "executed once"}}}, nil
	})
	httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true, PropagateRequestCancellation: true},
	))
	defer httpServer.Close()
	dataDir := t.TempDir()
	model := startModel(t,
		toolResponse("call-effect", toolName, `{}`),
		sse(evText("done"), evFinishReason("stop")),
	)
	options := []startOption{withModel(model), withEnvironment("XDG_DATA_HOME", dataDir)}
	first := start(t, options...)
	initialize(t, first)
	cwd := first.cwd
	definition := mcpHTTPServer("fixture", httpServer.URL, "")
	session := newMCPSession(t, first, definition)
	_ = first.begin("session/prompt", acp.PromptRequest{SessionID: session, Prompt: textPrompt("recover MCP")})
	original := permissionRequest(t, first.serverRequest(), "call-effect")
	first.kill()

	second := start(t, options...)
	initialize(t, second)
	load := second.begin("session/load", acp.LoadSessionRequest{
		SessionID: session, CWD: cwd, MCPServers: []acp.MCPServer{definition},
	})
	reissuedMessage := second.serverRequest()
	reissued := permissionRequest(t, reissuedMessage, "call-effect")
	if reissued.ToolCall.Name != original.ToolCall.Name || reissued.ToolCall.Title != original.ToolCall.Title {
		t.Fatalf("reissued MCP permission changed: %#v / %#v", original, reissued)
	}
	second.respond(reissuedMessage, acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Outcome: "selected", OptionID: "allow_once",
	}})
	_ = second.result(second.await(load))
	_ = updates(t, second, session)
	if calls.Load() != 1 {
		t.Fatalf("recovered MCP calls = %d", calls.Load())
	}
	second.request("session/close", acp.CloseSessionRequest{SessionID: session})
	second.stop()

	third := start(t, options...)
	initialize(t, third)
	replayStart := len(third.received)
	loadSession(t, third, session, cwd)
	replay := receivedSessionUpdates(t, third, replayStart, session)
	_ = updates(t, third, session)
	if calls.Load() != 1 {
		t.Fatalf("replay contacted MCP server: calls = %d", calls.Load())
	}
	var completed bool
	for _, notification := range replay {
		if notification.Update.ToolCallID == "call-effect" && notification.Update.Status == acp.ToolCallStatusCompleted {
			completed = true
		}
	}
	if !completed {
		t.Fatalf("replay omitted completed MCP result: %#v", replay)
	}
}

func TestMCPStdioShutdownThroughShippedBinary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "closed")
	child := start(t)
	initialize(t, child)
	session := newMCPSession(t, child, acp.MCPServer{Stdio: &acp.MCPStdioServer{
		Name: "stdio", Command: executable,
		Args: []string{"-test.run=^TestMCPStdioShutdownHelper$"},
		Env: []acp.EnvVariable{
			{Name: "GO_WANT_E2E_MCP_SHUTDOWN_HELPER", Value: "enabled"},
			{Name: "MCP_SHUTDOWN_MARKER", Value: marker},
		},
	}})
	child.request("session/close", acp.CloseSessionRequest{SessionID: session})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("stdio MCP server did not shut down")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMCPStdioShutdownHelper(t *testing.T) {
	if os.Getenv("GO_WANT_E2E_MCP_SHUTDOWN_HELPER") != "enabled" {
		return
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "stdio-fixture", Version: "1"}, &sdk.ServerOptions{
		Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
	})
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("MCP_SHUTDOWN_MARKER"), []byte("closed"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMCPDeadlinesThroughShippedBinary(t *testing.T) {
	t.Run("activation", func(t *testing.T) {
		server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, &sdk.ServerOptions{
			Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		})
		server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
				if method == "tools/list" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return next(ctx, method, request)
			}
		})
		httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(
			func(*http.Request) *sdk.Server { return server },
			&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true, PropagateRequestCancellation: true},
		))
		defer httpServer.Close()
		child := start(t)
		initialize(t, child)
		started := time.Now()
		err := child.requestError("session/new", acp.NewSessionRequest{
			CWD: child.cwd, MCPServers: []acp.MCPServer{mcpHTTPServer("fixture", httpServer.URL, "")},
		})
		if elapsed := time.Since(started); elapsed < time.Second || elapsed > 5*time.Second ||
			!strings.Contains(err.Message, "deadline exceeded") {
			t.Fatalf("activation deadline after %v: %#v", elapsed, err)
		}
	})

	t.Run("call", func(t *testing.T) {
		const toolName = "mcp__fixture__wait"
		var calls atomic.Int32
		server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
		server.AddTool(&sdk.Tool{Name: "wait", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			calls.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(
			func(*http.Request) *sdk.Server { return server },
			&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true, PropagateRequestCancellation: true},
		))
		defer httpServer.Close()
		model := startModel(t,
			toolResponse("call-wait", toolName, `{}`),
			sse(evText("deadline handled"), evFinishReason("stop")),
		)
		child := start(t, withModel(model))
		initialize(t, child)
		session := newMCPSession(t, child, mcpHTTPServer("fixture", httpServer.URL, ""))
		started := time.Now()
		turn := child.begin("session/prompt", acp.PromptRequest{SessionID: session, Prompt: textPrompt("deadline MCP")})
		allowPermission(t, child, "call-wait", true)
		if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("stop reason = %q", response.StopReason)
		}
		_ = updates(t, child, session)
		if elapsed := time.Since(started); elapsed < time.Second || elapsed > 5*time.Second || calls.Load() != 1 {
			t.Fatalf("call deadline after %v with %d calls", elapsed, calls.Load())
		}
		requests := model.requests()
		if len(requests) != 2 || !requestContainsText(requests[1], "deadline exceeded") {
			t.Fatalf("deadline result request = %#v", requests)
		}
	})
}

func TestMCPCancellationDoesNotRetryThroughShippedBinary(t *testing.T) {
	const toolName = "mcp__fixture__wait"
	started := make(chan struct{})
	release := make(chan struct{})
	cancelled := make(chan struct{})
	var calls atomic.Int32
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdk.Tool{Name: "wait", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			close(cancelled)
			return nil, ctx.Err()
		case <-release:
			return nil, errors.New("fixture released")
		}
	})
	sdkHandler := sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true, PropagateRequestCancellation: true},
	)
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if strings.Contains(string(data), `"method":"notifications/cancelled"`) {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		request.Body = io.NopCloser(strings.NewReader(string(data)))
		sdkHandler.ServeHTTP(writer, request)
	}))
	defer httpServer.Close()
	model := startModel(t, toolResponse("call-wait", toolName, `{}`))
	child := start(t, withModel(model))
	initialize(t, child)
	session := newMCPSession(t, child, mcpHTTPServer("fixture", httpServer.URL, ""))
	turn := child.begin("session/prompt", acp.PromptRequest{SessionID: session, Prompt: textPrompt("wait in MCP")})
	allowPermission(t, child, "call-wait", true)
	select {
	case <-started:
	case <-time.After(readTimeout):
		t.Fatal("MCP call did not start")
	}
	child.notify("session/cancel", acp.CancelNotification{SessionID: session})
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Error("HTTP MCP request context was not cancelled")
		close(release)
	}
	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	if calls.Load() != 1 {
		t.Fatalf("cancelled MCP calls = %d", calls.Load())
	}
	_ = updates(t, child, session)
}

func TestMCPActivationFailureCleansUpAndCatalogBoundsThroughShippedBinary(t *testing.T) {
	t.Run("atomic cleanup", func(t *testing.T) {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(t.TempDir(), "closed")
		first := acp.MCPServer{Stdio: &acp.MCPStdioServer{
			Name: "first", Command: executable,
			Args: []string{"-test.run=^TestMCPStdioShutdownHelper$"},
			Env: []acp.EnvVariable{
				{Name: "GO_WANT_E2E_MCP_SHUTDOWN_HELPER", Value: "enabled"},
				{Name: "MCP_SHUTDOWN_MARKER", Value: marker},
			},
		}}
		broken := sdk.NewServer(&sdk.Implementation{Name: "broken", Version: "1"}, &sdk.ServerOptions{
			Capabilities: &sdk.ServerCapabilities{Tools: &sdk.ToolCapabilities{}},
		})
		broken.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
				if method == "tools/list" {
					return &sdk.ListToolsResult{Tools: []*sdk.Tool{{Name: "bad", InputSchema: json.RawMessage(`{"type":7}`)}}}, nil
				}
				return next(ctx, method, request)
			}
		})
		brokenHTTP := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return broken }, &sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true}))
		defer brokenHTTP.Close()
		child := start(t)
		initialize(t, child)
		_ = child.requestError("session/new", acp.NewSessionRequest{CWD: child.cwd, MCPServers: []acp.MCPServer{
			first, mcpHTTPServer("broken", brokenHTTP.URL, ""),
		}})
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(marker); err == nil {
				break
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatal("failed activation did not close earlier MCP server")
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	for _, test := range []struct {
		name     string
		pageSize int
		fill     func(*sdk.Server)
		want     string
	}{
		{
			name: "name collision",
			fill: func(server *sdk.Server) {
				for _, name := range []string{"a b", "a_b"} {
					server.AddTool(&sdk.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
						return &sdk.CallToolResult{}, nil
					})
				}
			},
			want: "not unique",
		},
		{
			name: "tool count", pageSize: 257,
			fill: func(server *sdk.Server) {
				for index := range 257 {
					server.AddTool(&sdk.Tool{Name: "tool-" + strconv.Itoa(index), InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
						return &sdk.CallToolResult{}, nil
					})
				}
			},
			want: "256 tools",
		},
		{
			name: "catalog bytes",
			fill: func(server *sdk.Server) {
				server.AddTool(&sdk.Tool{Name: "large", Description: strings.Repeat("x", 257<<10), InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
					return &sdk.CallToolResult{}, nil
				})
			},
			want: "256 KiB",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pageSize := test.pageSize
			if pageSize == 0 {
				pageSize = 1
			}
			server := sdk.NewServer(&sdk.Implementation{Name: "fixture", Version: "1"}, &sdk.ServerOptions{PageSize: pageSize})
			test.fill(server)
			httpServer := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true}))
			defer httpServer.Close()
			child := start(t)
			initialize(t, child)
			err := child.requestError("session/new", acp.NewSessionRequest{CWD: child.cwd, MCPServers: []acp.MCPServer{mcpHTTPServer("fixture", httpServer.URL, "")}})
			if !strings.Contains(err.Message, test.want) {
				t.Fatalf("activation error = %#v", err)
			}
		})
	}
}
