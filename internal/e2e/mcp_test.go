package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kkestell/ox/internal/acp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

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
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true},
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
