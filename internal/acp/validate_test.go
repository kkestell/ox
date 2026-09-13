package acp

import (
	"strings"
	"testing"
)

// TestActivationRequestsValidateAtTheBoundary covers the rules every activation
// shares, and the one place session/load and session/resume differ: a load
// resupplies the client's MCP servers, a resume does not reconnect them.
func TestActivationRequestsValidateAtTheBoundary(t *testing.T) {
	valid := []MCPServer{{HTTP: &MCPHTTPServer{
		Type: "http", Name: "fixture", URL: "https://example.com/mcp",
		Headers: []HTTPHeader{},
	}}}
	broken := []MCPServer{{}}

	for _, test := range []struct {
		name    string
		request interface{ Validate() error }
		want    string
	}{
		{
			name:    "new session rejects a relative workspace",
			request: NewSessionRequest{CWD: "workspace", MCPServers: valid},
			want:    "absolute",
		},
		{
			name:    "load rejects a missing session",
			request: LoadSessionRequest{CWD: "/workspace", MCPServers: valid},
			want:    "sessionId is required",
		},
		{
			name:    "load rejects a relative workspace",
			request: LoadSessionRequest{SessionID: "s", CWD: "workspace", MCPServers: valid},
			want:    "absolute",
		},
		{
			name: "load rejects unsupported additional directories",
			request: LoadSessionRequest{
				SessionID: "s", CWD: "/workspace", MCPServers: valid,
				AdditionalDirectories: []string{"/other"},
			},
			want: "additional directories",
		},
		{
			name:    "load requires resupplied servers",
			request: LoadSessionRequest{SessionID: "s", CWD: "/workspace"},
			want:    "mcpServers is required",
		},
		{
			name:    "load rejects a malformed server",
			request: LoadSessionRequest{SessionID: "s", CWD: "/workspace", MCPServers: broken},
			want:    "mcpServers item 1",
		},
		{
			name:    "resume rejects a missing session",
			request: ResumeSessionRequest{CWD: "/workspace"},
			want:    "sessionId is required",
		},
		{
			name:    "resume rejects a relative workspace",
			request: ResumeSessionRequest{SessionID: "s", CWD: "workspace"},
			want:    "absolute",
		},
		{
			name:    "resume rejects a malformed server",
			request: ResumeSessionRequest{SessionID: "s", CWD: "/workspace", MCPServers: broken},
			want:    "mcpServers item 1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate = %v, want it to mention %q", err, test.want)
			}
		})
	}

	for _, test := range []struct {
		name    string
		request interface{ Validate() error }
	}{
		{
			name:    "load with resupplied servers",
			request: LoadSessionRequest{SessionID: "s", CWD: "/workspace", MCPServers: valid},
		},
		{
			name:    "resume without servers",
			request: ResumeSessionRequest{SessionID: "s", CWD: "/workspace"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.request.Validate(); err != nil {
				t.Fatalf("Validate = %v", err)
			}
		})
	}
}
