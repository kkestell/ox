package acp_test

import (
	"encoding/json"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestInitializeRequestValidate(t *testing.T) {
	for _, test := range []struct {
		name    string
		version int
		valid   bool
	}{
		{name: "supported", version: acp.ProtocolVersion, valid: true},
		{name: "unsupported positive", version: 999, valid: true},
		{name: "missing", valid: false},
		{name: "negative", version: -1, valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (acp.InitializeRequest{ProtocolVersion: test.version}).Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestNewSessionRequestValidate(t *testing.T) {
	empty := []json.RawMessage{}
	for _, test := range []struct {
		name    string
		request acp.NewSessionRequest
		valid   bool
	}{
		{
			name:    "absolute cwd",
			request: acp.NewSessionRequest{CWD: "/workspace", MCPServers: empty},
			valid:   true,
		},
		{
			name:    "empty additional directories",
			request: acp.NewSessionRequest{CWD: "/workspace", MCPServers: empty, AdditionalDirectories: []string{}},
			valid:   true,
		},
		{
			name:    "missing cwd",
			request: acp.NewSessionRequest{MCPServers: empty},
		},
		{
			name:    "relative cwd",
			request: acp.NewSessionRequest{CWD: "workspace", MCPServers: empty},
		},
		{
			name:    "missing mcpServers",
			request: acp.NewSessionRequest{CWD: "/workspace"},
		},
		{
			name: "mcp server requested",
			request: acp.NewSessionRequest{
				CWD:        "/workspace",
				MCPServers: []json.RawMessage{json.RawMessage(`{"name":"files"}`)},
			},
		},
		{
			name: "additional directory requested",
			request: acp.NewSessionRequest{
				CWD:                   "/workspace",
				MCPServers:            empty,
				AdditionalDirectories: []string{"/other"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestPromptRequestValidate(t *testing.T) {
	for _, test := range []struct {
		name    string
		request acp.PromptRequest
		valid   bool
	}{
		{
			name: "text",
			request: acp.PromptRequest{
				SessionID: "session",
				Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
			},
			valid: true,
		},
		{
			name: "resource link",
			request: acp.PromptRequest{
				SessionID: "session",
				Prompt: []acp.ContentBlock{
					{Type: "resource_link", Name: "main.go", URI: "file:///main.go"},
				},
			},
			valid: true,
		},
		{
			name:    "missing session",
			request: acp.PromptRequest{Prompt: []acp.ContentBlock{{Type: "text"}}},
		},
		{
			name:    "empty prompt",
			request: acp.PromptRequest{SessionID: "session"},
		},
		{
			name: "unsupported content",
			request: acp.PromptRequest{
				SessionID: "session",
				Prompt:    []acp.ContentBlock{{Type: "image"}},
			},
		},
		{
			name: "resource link without uri",
			request: acp.PromptRequest{
				SessionID: "session",
				Prompt:    []acp.ContentBlock{{Type: "resource_link", Name: "main.go"}},
			},
		},
		{
			name: "resource link without name",
			request: acp.PromptRequest{
				SessionID: "session",
				Prompt:    []acp.ContentBlock{{Type: "resource_link", URI: "file:///main.go"}},
			},
		},
		{
			name: "unsupported block after a supported one",
			request: acp.PromptRequest{
				SessionID: "session",
				Prompt:    []acp.ContentBlock{{Type: "text"}, {Type: "audio"}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestCancelNotificationValidate(t *testing.T) {
	if err := (acp.CancelNotification{SessionID: "session"}).Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := (acp.CancelNotification{}).Validate(); err == nil {
		t.Fatal("Validate() accepted a missing sessionId")
	}
}

func TestCancelRequestNotificationValidate(t *testing.T) {
	for _, test := range []struct {
		name  string
		id    string
		valid bool
	}{
		{name: "string", id: `"abc"`, valid: true},
		{name: "empty string", id: `""`, valid: true},
		{name: "integer", id: `2`, valid: true},
		{name: "negative integer", id: `-2`, valid: true},
		{name: "null", id: `null`, valid: true},
		{name: "missing", valid: false},
		{name: "fraction", id: `2.5`, valid: false},
		{name: "boolean", id: `true`, valid: false},
		{name: "object", id: `{}`, valid: false},
		{name: "array", id: `[]`, valid: false},
		{name: "invalid JSON", id: `{`, valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			notification := acp.CancelRequestNotification{RequestID: json.RawMessage(test.id)}
			err := notification.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}
