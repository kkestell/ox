package acp_test

import (
	"encoding/json"
	"strings"
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
		wantErr string
	}{
		{
			name: "empty text",
			request: acp.PromptRequest{
				SessionID: "session", Prompt: []acp.ContentBlock{{Type: "text"}},
			},
		},
		{
			name: "resource link",
			request: acp.PromptRequest{
				SessionID: "session",
				Prompt: []acp.ContentBlock{
					{Type: "resource_link", Name: "main.go", URI: "file:///main.go"},
				},
			},
		},
		{
			name: "image",
			request: acp.PromptRequest{SessionID: "session", Prompt: []acp.ContentBlock{
				{Type: "image", MIMEType: "image/png", Data: "cGljdHVyZQ=="},
			}},
		},
		{
			name: "audio",
			request: acp.PromptRequest{SessionID: "session", Prompt: []acp.ContentBlock{
				{Type: "audio", MIMEType: "audio/wav", Data: "c291bmQ="},
			}},
		},
		{
			name: "empty embedded text",
			request: acp.PromptRequest{SessionID: "session", Prompt: []acp.ContentBlock{
				{Type: "resource", Resource: &acp.EmbeddedResource{
					URI: "file:///empty.txt", Text: stringPointer(""),
				}},
			}},
		},
		{
			name: "embedded blob without mime type",
			request: acp.PromptRequest{SessionID: "session", Prompt: []acp.ContentBlock{
				{Type: "resource", Resource: &acp.EmbeddedResource{
					URI: "file:///blob", Blob: stringPointer("YmxvYg=="),
				}},
			}},
		},
		{
			name:    "missing session",
			request: acp.PromptRequest{Prompt: []acp.ContentBlock{{Type: "text"}}},
			wantErr: "sessionId is required",
		},
		{
			name:    "empty prompt",
			request: acp.PromptRequest{SessionID: "session"},
			wantErr: "at least one",
		},
		{
			name:    "image without mime type",
			request: promptWith(acp.ContentBlock{Type: "image", Data: "cGljdHVyZQ=="}),
			wantErr: "block 1 image requires mimeType",
		},
		{
			name:    "image with malformed mime type",
			request: promptWith(acp.ContentBlock{Type: "image", MIMEType: "png", Data: "cGljdHVyZQ=="}),
			wantErr: "mimeType \"png\" must contain /",
		},
		{
			name:    "image without data",
			request: promptWith(acp.ContentBlock{Type: "image", MIMEType: "image/png"}),
			wantErr: "image requires data",
		},
		{
			name:    "image with invalid base64",
			request: promptWith(acp.ContentBlock{Type: "image", MIMEType: "image/png", Data: "not base64"}),
			wantErr: "image data must be standard base64",
		},
		{
			name:    "audio without mime type",
			request: promptWith(acp.ContentBlock{Type: "audio", Data: "c291bmQ="}),
			wantErr: "audio requires mimeType",
		},
		{
			name:    "audio without data",
			request: promptWith(acp.ContentBlock{Type: "audio", MIMEType: "audio/wav"}),
			wantErr: "audio requires data",
		},
		{
			name:    "audio with invalid base64",
			request: promptWith(acp.ContentBlock{Type: "audio", MIMEType: "audio/wav", Data: "%%%"}),
			wantErr: "audio data must be standard base64",
		},
		{
			name:    "resource link without uri",
			request: promptWith(acp.ContentBlock{Type: "resource_link", Name: "main.go"}),
			wantErr: "resource link requires uri",
		},
		{
			name:    "resource link without name",
			request: promptWith(acp.ContentBlock{Type: "resource_link", URI: "file:///main.go"}),
			wantErr: "resource link requires name",
		},
		{
			name:    "resource without object",
			request: promptWith(acp.ContentBlock{Type: "resource"}),
			wantErr: "resource is required",
		},
		{
			name: "resource without uri",
			request: promptWith(acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{
				Text: stringPointer("text"),
			}}),
			wantErr: "resource requires uri",
		},
		{
			name: "resource without payload",
			request: promptWith(acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{
				URI: "file:///empty",
			}}),
			wantErr: "exactly one of text or blob",
		},
		{
			name: "resource with text and blob",
			request: promptWith(acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{
				URI: "file:///both", Text: stringPointer("text"), Blob: stringPointer("YmxvYg=="),
			}}),
			wantErr: "exactly one of text or blob",
		},
		{
			name: "resource with invalid blob base64",
			request: promptWith(acp.ContentBlock{Type: "resource", Resource: &acp.EmbeddedResource{
				URI: "file:///blob", Blob: stringPointer("not base64"),
			}}),
			wantErr: "resource blob must be standard base64",
		},
		{
			name: "unsupported block after a supported one",
			request: acp.PromptRequest{
				SessionID: "session",
				Prompt:    []acp.ContentBlock{{Type: "text"}, {Type: "future"}},
			},
			wantErr: `block 2 has unsupported type "future"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func promptWith(block acp.ContentBlock) acp.PromptRequest {
	return acp.PromptRequest{SessionID: "session", Prompt: []acp.ContentBlock{block}}
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
