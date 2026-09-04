package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInitializeResponseOmitsUnsetOptionalFields(t *testing.T) {
	data, err := json.Marshal(InitializeResponse{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}

	got := string(data)
	if got != `{"protocolVersion":1}` {
		t.Fatalf("encoded response = %s", got)
	}
	if strings.Contains(got, "null") {
		t.Fatalf("encoded response contains null: %s", got)
	}
}

func TestInitializeMetadataRoundTrip(t *testing.T) {
	input := []byte(`{"protocolVersion":1,"_meta":{"ox.example":{"enabled":true}}}`)

	var request InitializeRequest
	if err := json.Unmarshal(input, &request); err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	meta, ok := got["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("_meta missing from %s", data)
	}
	ox, ok := meta["ox.example"].(map[string]any)
	if !ok || ox["enabled"] != true {
		t.Fatalf("_meta changed during round trip: %#v", meta)
	}
}

func TestSessionPayloadsUsePinnedWireShapes(t *testing.T) {
	request := NewSessionRequest{
		CWD:        "/workspace",
		MCPServers: []json.RawMessage{},
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"cwd":"/workspace","mcpServers":[]}` {
		t.Fatalf("new session request = %s", data)
	}

	prompt := PromptRequest{
		SessionID: "session-1",
		Prompt: []ContentBlock{
			{Type: "text", Text: ""},
			{Type: "resource_link", Name: "guide", URI: "file:///guide.md"},
		},
	}
	data, err = json.Marshal(prompt)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"sessionId":"session-1","prompt":[{"type":"text","text":""},{"type":"resource_link","name":"guide","uri":"file:///guide.md"}]}`
	if string(data) != want {
		t.Fatalf("prompt request = %s", data)
	}

	cancel := CancelNotification{SessionID: "session-1"}
	data, err = json.Marshal(cancel)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"sessionId":"session-1"}` {
		t.Fatalf("cancel notification = %s", data)
	}
}

func TestFilesystemPayloadsUsePinnedWireShapes(t *testing.T) {
	line, limit := 10, 50
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name: "read request",
			value: ReadTextFileRequest{
				SessionID: "sess_abc123def456",
				Path:      "/home/user/project/src/main.py",
				Line:      &line,
				Limit:     &limit,
			},
			want: `{"sessionId":"sess_abc123def456","path":"/home/user/project/src/main.py","line":10,"limit":50}`,
		},
		{
			name: "read request without paging",
			value: ReadTextFileRequest{
				SessionID: "session-1",
				Path:      "/workspace/empty.txt",
			},
			want: `{"sessionId":"session-1","path":"/workspace/empty.txt"}`,
		},
		{
			name:  "empty read response",
			value: ReadTextFileResponse{},
			want:  `{"content":""}`,
		},
		{
			name: "empty write request",
			value: WriteTextFileRequest{
				SessionID: "session-1",
				Path:      "/workspace/empty.txt",
				Content:   "",
			},
			want: `{"sessionId":"session-1","path":"/workspace/empty.txt","content":""}`,
		},
		{
			name:  "empty write response",
			value: WriteTextFileResponse{},
			want:  `{}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != test.want {
				t.Fatalf("payload = %s, want %s", data, test.want)
			}
		})
	}
}

func TestFilesystemRequestsValidateOutboundFields(t *testing.T) {
	one := 1
	for _, test := range []struct {
		name    string
		request interface{ Validate() error }
		valid   bool
	}{
		{
			name: "read",
			request: ReadTextFileRequest{
				SessionID: "session-1",
				Path:      "/workspace/file.txt",
				Line:      &one,
				Limit:     &one,
			},
			valid: true,
		},
		{name: "read missing session", request: ReadTextFileRequest{Path: "/workspace/file.txt"}},
		{name: "read relative path", request: ReadTextFileRequest{SessionID: "session-1", Path: "file.txt"}},
		{
			name:    "write empty file",
			request: WriteTextFileRequest{SessionID: "session-1", Path: "/workspace/file.txt"},
			valid:   true,
		},
		{name: "write missing path", request: WriteTextFileRequest{SessionID: "session-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if test.valid && err != nil {
				t.Fatal(err)
			}
			if !test.valid && err == nil {
				t.Fatal("request validated unexpectedly")
			}
		})
	}
}

func TestSessionUpdatesCarryExactDiscriminators(t *testing.T) {
	updates := []struct {
		value any
		want  string
	}{
		{
			value: UserMessageChunk{
				SessionUpdate: "user_message_chunk",
				Content:       ContentBlock{Type: "text", Text: "follow up"},
				MessageID:     "message-1",
			},
			want: `{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"follow up"},"messageId":"message-1"}`,
		},
		{
			value: AgentMessageChunk{
				SessionUpdate: "agent_message_chunk",
				Content:       ContentBlock{Type: "text", Text: "hello"},
				MessageID:     "message-1",
			},
			want: `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"},"messageId":"message-1"}`,
		},
		{
			value: ToolCall{
				SessionUpdate: "tool_call",
				ToolCallID:    "call-1",
				Title:         "lookup",
				Status:        ToolCallStatusPending,
			},
			want: `{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"lookup","status":"pending"}`,
		},
		{
			value: ToolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "call-1",
				Status:        ToolCallStatusCompleted,
			},
			want: `{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed"}`,
		},
		{
			value: UsageUpdate{
				SessionUpdate: "usage_update",
				Used:          12,
				Size:          100,
				Cost:          &Cost{Amount: 0.25, Currency: "USD"},
			},
			want: `{"sessionUpdate":"usage_update","used":12,"size":100,"cost":{"amount":0.25,"currency":"USD"}}`,
		},
	}

	for _, test := range updates {
		data, err := json.Marshal(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != test.want {
			t.Errorf("update = %s, want %s", data, test.want)
		}
	}
}
