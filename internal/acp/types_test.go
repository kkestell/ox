package acp_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestInitializeRequestRoundTrip(t *testing.T) {
	literal := []byte(`{
		"protocolVersion": 1,
		"clientCapabilities": {
			"auth": {"terminal": true},
			"fs": {
				"readTextFile": true,
				"writeTextFile": true
			},
			"terminal": true
		},
		"clientInfo": {
			"name": "my-client",
			"title": "My Client",
			"version": "1.0.0"
		}
	}`)

	var request acp.InitializeRequest
	if err := json.Unmarshal(literal, &request); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, encoded, literal)
}

func TestInitializeRequestPreservesOmittedCapabilities(t *testing.T) {
	var request acp.InitializeRequest
	if err := json.Unmarshal([]byte(`{"protocolVersion":1}`), &request); err != nil {
		t.Fatal(err)
	}
	if request.ClientCapabilities != nil {
		t.Fatalf("clientCapabilities = %#v, want nil", request.ClientCapabilities)
	}
}

func TestInitializeResponseShape(t *testing.T) {
	response := acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{
			Auth: &acp.AgentAuthCapabilities{Logout: &acp.LogoutCapabilities{}},
			PromptCapabilities: acp.PromptCapabilities{
				Image:           true,
				Audio:           true,
				EmbeddedContext: true,
			},
		},
		AgentInfo: acp.Implementation{Name: "ox", Version: "0.0.1"},
		AuthMethods: []acp.AuthMethod{
			{
				ID:          "openrouter",
				Name:        "OpenRouter credential",
				Description: "Use an OpenRouter API key already available to Ox.",
			},
			{
				ID:          "openrouter-terminal",
				Type:        "terminal",
				Name:        "Log in to OpenRouter",
				Description: "Enter and store an OpenRouter API key in a terminal.",
				Args:        []string{"login"},
			},
		},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, encoded, []byte(`{
		"protocolVersion": 1,
		"agentCapabilities": {
			"loadSession": false,
			"auth": {"logout": {}},
			"promptCapabilities": {
				"image": true,
				"audio": true,
				"embeddedContext": true
			}
		},
		"agentInfo": {
			"name": "ox",
			"version": "0.0.1"
		},
		"authMethods": [
			{
				"id": "openrouter",
				"name": "OpenRouter credential",
				"description": "Use an OpenRouter API key already available to Ox."
			},
			{
				"id": "openrouter-terminal",
				"type": "terminal",
				"name": "Log in to OpenRouter",
				"description": "Enter and store an OpenRouter API key in a terminal.",
				"args": ["login"]
			}
		]
	}`))
}

func TestContentBlockMarshalsEachVariantsRequiredFields(t *testing.T) {
	for _, test := range []struct {
		name  string
		block acp.ContentBlock
		want  string
	}{
		{
			name:  "text",
			block: acp.ContentBlock{Type: "text", Text: "hello"},
			want:  `{"type":"text","text":"hello"}`,
		},
		{
			name:  "empty text",
			block: acp.ContentBlock{Type: "text"},
			want:  `{"type":"text","text":""}`,
		},
		{
			name:  "resource link",
			block: acp.ContentBlock{Type: "resource_link", Name: "main.go", URI: "file:///main.go"},
			want:  `{"type":"resource_link","name":"main.go","uri":"file:///main.go"}`,
		},
		{
			name: "image",
			block: acp.ContentBlock{
				Type: "image", MIMEType: "image/png", Data: "cGljdHVyZQ==",
			},
			want: `{"type":"image","mimeType":"image/png","data":"cGljdHVyZQ=="}`,
		},
		{
			name: "audio",
			block: acp.ContentBlock{
				Type: "audio", MIMEType: "audio/wav", Data: "c291bmQ=",
			},
			want: `{"type":"audio","mimeType":"audio/wav","data":"c291bmQ="}`,
		},
		{
			name: "embedded text resource",
			block: acp.ContentBlock{
				Type: "resource",
				Resource: &acp.EmbeddedResource{
					URI: "file:///main.go", MIMEType: "text/plain", Text: stringPointer("package main"),
				},
			},
			want: `{"type":"resource","resource":{"uri":"file:///main.go","mimeType":"text/plain","text":"package main"}}`,
		},
		{
			name: "embedded blob resource",
			block: acp.ContentBlock{
				Type: "resource",
				Resource: &acp.EmbeddedResource{
					URI: "file:///sound.wav", MIMEType: "audio/wav", Blob: stringPointer("c291bmQ="),
				},
			},
			want: `{"type":"resource","resource":{"uri":"file:///sound.wav","mimeType":"audio/wav","blob":"c291bmQ="}}`,
		},
		{
			name:  "missing embedded resource",
			block: acp.ContentBlock{Type: "resource"},
			want:  `{"type":"resource","resource":null}`,
		},
		{
			name:  "unsupported",
			block: acp.ContentBlock{Type: "future"},
			want:  `{"type":"future"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.block)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, encoded, []byte(test.want))
		})
	}
}

func TestSessionNotificationShape(t *testing.T) {
	notification := acp.SessionNotification{
		SessionID: "session",
		Update: acp.ContentChunk{
			SessionUpdate: acp.SessionUpdateAgentMessageChunk,
			Content:       acp.ContentBlock{Type: "text", Text: "hello"},
			MessageID:     "message",
		},
	}
	encoded, err := json.Marshal(notification)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, encoded, []byte(`{
		"sessionId": "session",
		"update": {
			"sessionUpdate": "agent_message_chunk",
			"content": {"type": "text", "text": "hello"},
			"messageId": "message"
		}
	}`))
}

func TestPromptRequestRoundTrip(t *testing.T) {
	literal := []byte(`{
		"sessionId": "session",
		"prompt": [
			{"type": "text", "text": "hello"},
			{"type": "image", "mimeType": "image/png", "data": "aW1hZ2U="},
			{"type": "audio", "mimeType": "audio/wav", "data": "YXVkaW8="},
			{"type": "resource_link", "name": "main.go", "uri": "file:///main.go"},
			{"type": "resource", "resource": {
				"uri": "file:///context.txt", "mimeType": "text/plain", "text": "context"
			}}
		]
	}`)

	var request acp.PromptRequest
	if err := json.Unmarshal(literal, &request); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, encoded, literal)
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func stringPointer(value string) *string {
	return &value
}
