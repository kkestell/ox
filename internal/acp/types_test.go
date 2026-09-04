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

func TestTerminalPayloadsUsePinnedWireShapes(t *testing.T) {
	cwd := "/workspace"
	limit := 10 * 1024 * 1024
	exitCode := 7
	signal := "SIGTERM"
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name: "create request",
			value: CreateTerminalRequest{
				SessionID:       "session-1",
				Command:         "/bin/sh",
				Args:            []string{"-c", "printf hello"},
				Env:             []EnvVariable{{Name: "TERM", Value: "dumb"}},
				CWD:             &cwd,
				OutputByteLimit: &limit,
			},
			want: `{"sessionId":"session-1","command":"/bin/sh","args":["-c","printf hello"],"env":[{"name":"TERM","value":"dumb"}],"cwd":"/workspace","outputByteLimit":10485760}`,
		},
		{
			name:  "create request without optional fields",
			value: CreateTerminalRequest{SessionID: "session-1", Command: "true"},
			want:  `{"sessionId":"session-1","command":"true"}`,
		},
		{
			name:  "create response",
			value: CreateTerminalResponse{TerminalID: "terminal-1"},
			want:  `{"terminalId":"terminal-1"}`,
		},
		{
			name:  "output request",
			value: TerminalOutputRequest{SessionID: "session-1", TerminalID: "terminal-1"},
			want:  `{"sessionId":"session-1","terminalId":"terminal-1"}`,
		},
		{
			name:  "empty output",
			value: TerminalOutputResponse{},
			want:  `{"output":"","truncated":false}`,
		},
		{
			name: "completed output",
			value: TerminalOutputResponse{
				Output:     "failed\n",
				Truncated:  true,
				ExitStatus: &TerminalExitStatus{ExitCode: &exitCode},
			},
			want: `{"output":"failed\n","truncated":true,"exitStatus":{"exitCode":7}}`,
		},
		{
			name:  "wait request",
			value: WaitForTerminalExitRequest{SessionID: "session-1", TerminalID: "terminal-1"},
			want:  `{"sessionId":"session-1","terminalId":"terminal-1"}`,
		},
		{
			name:  "signalled wait response",
			value: WaitForTerminalExitResponse{Signal: &signal},
			want:  `{"signal":"SIGTERM"}`,
		},
		{
			name:  "kill request",
			value: KillTerminalRequest{SessionID: "session-1", TerminalID: "terminal-1"},
			want:  `{"sessionId":"session-1","terminalId":"terminal-1"}`,
		},
		{name: "empty kill response", value: KillTerminalResponse{}, want: `{}`},
		{
			name:  "release request",
			value: ReleaseTerminalRequest{SessionID: "session-1", TerminalID: "terminal-1"},
			want:  `{"sessionId":"session-1","terminalId":"terminal-1"}`,
		},
		{name: "empty release response", value: ReleaseTerminalResponse{}, want: `{}`},
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

	var nullable TerminalOutputResponse
	if err := json.Unmarshal(
		[]byte(`{"output":"","truncated":false,"exitStatus":{"exitCode":null,"signal":null}}`),
		&nullable,
	); err != nil {
		t.Fatal(err)
	}
	if nullable.ExitStatus == nil || nullable.ExitStatus.ExitCode != nil ||
		nullable.ExitStatus.Signal != nil {
		t.Fatalf("nullable exit status = %#v", nullable.ExitStatus)
	}
}

func TestTerminalRequestsValidateOutboundFields(t *testing.T) {
	cwd := "/workspace"
	relative := "workspace"
	positive, zero := 1, 0
	tests := []struct {
		name    string
		request interface{ Validate() error }
		valid   bool
	}{
		{
			name: "create",
			request: CreateTerminalRequest{
				SessionID:       "session-1",
				Command:         "/bin/sh",
				CWD:             &cwd,
				OutputByteLimit: &positive,
			},
			valid: true,
		},
		{name: "create missing session", request: CreateTerminalRequest{Command: "true"}},
		{name: "create missing command", request: CreateTerminalRequest{SessionID: "session-1"}},
		{
			name:    "create relative cwd",
			request: CreateTerminalRequest{SessionID: "session-1", Command: "true", CWD: &relative},
		},
		{
			name: "create zero output limit",
			request: CreateTerminalRequest{
				SessionID: "session-1", Command: "true", OutputByteLimit: &zero,
			},
		},
		{
			name:    "create response",
			request: CreateTerminalResponse{TerminalID: "terminal-1"},
			valid:   true,
		},
		{name: "create response missing terminal", request: CreateTerminalResponse{}},
		{
			name:    "output",
			request: TerminalOutputRequest{SessionID: "session-1", TerminalID: "terminal-1"},
			valid:   true,
		},
		{
			name:    "wait missing terminal",
			request: WaitForTerminalExitRequest{SessionID: "session-1"},
		},
		{
			name:    "kill missing session",
			request: KillTerminalRequest{TerminalID: "terminal-1"},
		},
		{
			name:    "release",
			request: ReleaseTerminalRequest{SessionID: "session-1", TerminalID: "terminal-1"},
			valid:   true,
		},
	}

	for _, test := range tests {
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
				Locations:     []ToolCallLocation{{Path: "/workspace/a.go"}},
			},
			want: `{"sessionUpdate":"tool_call","toolCallId":"call-1","title":"lookup","status":"pending","locations":[{"path":"/workspace/a.go"}]}`,
		},
		{
			value: ToolCallUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    "call-1",
				Status:        ToolCallStatusCompleted,
				Locations:     []ToolCallLocation{{Path: "/workspace/a.go"}},
			},
			want: `{"sessionUpdate":"tool_call_update","toolCallId":"call-1","status":"completed","locations":[{"path":"/workspace/a.go"}]}`,
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
