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
		MCPServers: []MCPServer{},
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

func TestSessionConfigPayloadsUsePinnedWireShapes(t *testing.T) {
	options := []SessionConfigOption{{
		Type:         SessionConfigOptionTypeSelect,
		ID:           "mode",
		Name:         "Mode",
		Description:  "Choose how Ox works.",
		Category:     SessionConfigOptionCategoryMode,
		CurrentValue: "code",
		Options: []SessionConfigSelectOption{
			{Value: "code", Name: "Code"},
			{Value: "plan", Name: "Plan", Description: "Read-only planning."},
		},
	}}

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name:  "new session response",
			value: NewSessionResponse{SessionID: "session-1", ConfigOptions: options},
			want:  `{"sessionId":"session-1","configOptions":[{"type":"select","id":"mode","name":"Mode","description":"Choose how Ox works.","category":"mode","currentValue":"code","options":[{"value":"code","name":"Code"},{"value":"plan","name":"Plan","description":"Read-only planning."}]}]}`,
		},
		{
			name:  "load session response",
			value: LoadSessionResponse{ConfigOptions: options},
			want:  `{"configOptions":[{"type":"select","id":"mode","name":"Mode","description":"Choose how Ox works.","category":"mode","currentValue":"code","options":[{"value":"code","name":"Code"},{"value":"plan","name":"Plan","description":"Read-only planning."}]}]}`,
		},
		{
			name:  "resume session response",
			value: ResumeSessionResponse{ConfigOptions: options},
			want:  `{"configOptions":[{"type":"select","id":"mode","name":"Mode","description":"Choose how Ox works.","category":"mode","currentValue":"code","options":[{"value":"code","name":"Code"},{"value":"plan","name":"Plan","description":"Read-only planning."}]}]}`,
		},
		{
			name: "setter request",
			value: SetSessionConfigOptionRequest{
				SessionID: "session-1",
				ConfigID:  "mode",
				Value:     "plan",
			},
			want: `{"sessionId":"session-1","configId":"mode","value":"plan"}`,
		},
		{
			name:  "setter response",
			value: SetSessionConfigOptionResponse{ConfigOptions: options},
			want:  `{"configOptions":[{"type":"select","id":"mode","name":"Mode","description":"Choose how Ox works.","category":"mode","currentValue":"code","options":[{"value":"code","name":"Code"},{"value":"plan","name":"Plan","description":"Read-only planning."}]}]}`,
		},
		{
			name: "config option update",
			value: ConfigOptionUpdate{
				SessionUpdate: SessionUpdateConfigOptionUpdate,
				ConfigOptions: options,
			},
			want: `{"sessionUpdate":"config_option_update","configOptions":[{"type":"select","id":"mode","name":"Mode","description":"Choose how Ox works.","category":"mode","currentValue":"code","options":[{"value":"code","name":"Code"},{"value":"plan","name":"Plan","description":"Read-only planning."}]}]}`,
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

func TestPlanPayloadsUsePinnedWireShapes(t *testing.T) {
	tests := []struct {
		name    string
		entries []PlanEntry
		want    string
	}{
		{
			name: "populated",
			entries: []PlanEntry{{
				Content: "Implement it", Priority: PlanEntryPriorityHigh,
				Status: PlanEntryStatusInProgress,
			}},
			want: `{"sessionUpdate":"plan","entries":[{"content":"Implement it","priority":"high","status":"in_progress"}]}`,
		},
		{name: "empty", entries: []PlanEntry{}, want: `{"sessionUpdate":"plan","entries":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := Plan{SessionUpdate: SessionUpdatePlan, Entries: test.entries}
			if err := value.Validate(); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != test.want {
				t.Fatalf("payload = %s, want %s", data, test.want)
			}
		})
	}
}

func TestElicitationCapabilitiesUseExplicitFormPresence(t *testing.T) {
	tests := []struct {
		name string
		json string
		form bool
	}{
		{name: "omitted", json: `{}`},
		{name: "null", json: `{"elicitation":null}`},
		{name: "empty", json: `{"elicitation":{}}`},
		{name: "null form", json: `{"elicitation":{"form":null}}`},
		{name: "form", json: `{"elicitation":{"form":{}}}`, form: true},
		{name: "url only", json: `{"elicitation":{"url":{}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var capabilities ClientCapabilities
			if err := json.Unmarshal([]byte(test.json), &capabilities); err != nil {
				t.Fatal(err)
			}
			got := capabilities.Elicitation != nil && capabilities.Elicitation.Form != nil
			if got != test.form {
				t.Fatalf("form support = %v, want %v", got, test.form)
			}
		})
	}
}

func TestElicitationPayloadsUsePinnedWireShapes(t *testing.T) {
	minimum := uint32(1)
	defaultValue := "balanced"
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name: "free text request",
			value: CreateElicitationRequest{
				SessionID: "session-1", ToolCallID: "call-1", Mode: ElicitationModeForm,
				Message: "What should I name it?",
				RequestedSchema: ElicitationSchema{
					Type: "object",
					Properties: map[string]ElicitationStringProperty{
						"answer": {Type: "string", Title: "Answer", MinLength: &minimum},
					},
					Required: []string{"answer"},
				},
			},
			want: `{"sessionId":"session-1","toolCallId":"call-1","mode":"form","message":"What should I name it?","requestedSchema":{"type":"object","properties":{"answer":{"type":"string","title":"Answer","minLength":1}},"required":["answer"]}}`,
		},
		{
			name: "choice request",
			value: CreateElicitationRequest{
				SessionID: "session-1", ToolCallID: "call-1", Mode: ElicitationModeForm,
				Message: "Choose a strategy.",
				RequestedSchema: ElicitationSchema{
					Type: "object",
					Properties: map[string]ElicitationStringProperty{
						"answer": {
							Type: "string", Title: "Answer", Default: &defaultValue,
							OneOf: []ElicitationEnumOption{
								{Const: "safe", Title: "safe", Description: "Small changes."},
								{Const: "balanced", Title: "balanced"},
							},
						},
					},
					Required: []string{"answer"},
				},
			},
			want: `{"sessionId":"session-1","toolCallId":"call-1","mode":"form","message":"Choose a strategy.","requestedSchema":{"type":"object","properties":{"answer":{"type":"string","title":"Answer","default":"balanced","oneOf":[{"const":"safe","title":"safe","description":"Small changes."},{"const":"balanced","title":"balanced"}]}},"required":["answer"]}}`,
		},
		{name: "accept response", value: CreateElicitationResponse{
			Action:  ElicitationActionAccept,
			Content: map[string]json.RawMessage{"answer": json.RawMessage(`"safe"`)},
		}, want: `{"action":"accept","content":{"answer":"safe"}}`},
		{name: "decline response", value: CreateElicitationResponse{Action: ElicitationActionDecline}, want: `{"action":"decline"}`},
		{name: "cancel response", value: CreateElicitationResponse{Action: ElicitationActionCancel}, want: `{"action":"cancel"}`},
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

func TestElicitationResponseValidation(t *testing.T) {
	defaultValue := "safe"
	request := CreateElicitationRequest{
		SessionID: "session-1", ToolCallID: "call-1", Mode: ElicitationModeForm,
		Message: "Choose.",
		RequestedSchema: ElicitationSchema{
			Type: "object",
			Properties: map[string]ElicitationStringProperty{"answer": {
				Type: "string", Default: &defaultValue,
				OneOf: []ElicitationEnumOption{{Const: "safe", Title: "Safe"}},
			}},
			Required: []string{"answer"},
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	valid := CreateElicitationResponse{
		Action:  ElicitationActionAccept,
		Content: map[string]json.RawMessage{"answer": json.RawMessage(`"safe"`)},
	}
	if answer, err := valid.Validate(request); err != nil || answer != "safe" {
		t.Fatalf("answer = %q, error = %v", answer, err)
	}
	for _, response := range []CreateElicitationResponse{
		{Action: ElicitationActionAccept},
		{Action: ElicitationActionAccept, Content: map[string]json.RawMessage{"answer": json.RawMessage(`1`)}},
		{Action: ElicitationActionAccept, Content: map[string]json.RawMessage{"answer": json.RawMessage(`""`)}},
		{Action: ElicitationActionAccept, Content: map[string]json.RawMessage{"answer": json.RawMessage(`"other"`)}},
		{Action: ElicitationActionAccept, Content: map[string]json.RawMessage{
			"answer": json.RawMessage(`"safe"`), "extra": json.RawMessage(`true`),
		}},
		{Action: "future"},
	} {
		if _, err := response.Validate(request); err == nil {
			t.Fatalf("response %#v validated unexpectedly", response)
		}
	}
	for _, action := range []ElicitationAction{ElicitationActionDecline, ElicitationActionCancel} {
		if _, err := (CreateElicitationResponse{Action: action}).Validate(request); err != nil {
			t.Fatalf("%s response error = %v", action, err)
		}
	}
}

func TestSetSessionConfigOptionRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		valid   bool
		wantErr string
	}{
		{
			name:  "select value",
			input: `{"sessionId":"session-1","configId":"mode","value":"plan"}`,
			valid: true,
		},
		{
			name:  "unknown discriminator with string value",
			input: `{"sessionId":"session-1","configId":"mode","type":"future","value":"plan"}`,
			valid: true,
		},
		{
			name:    "missing session",
			input:   `{"configId":"mode","value":"plan"}`,
			wantErr: "sessionId is required",
		},
		{
			name:    "missing config",
			input:   `{"sessionId":"session-1","value":"plan"}`,
			wantErr: "configId is required",
		},
		{
			name:    "missing value",
			input:   `{"sessionId":"session-1","configId":"mode"}`,
			wantErr: "value is required",
		},
		{
			name:    "boolean discriminator",
			input:   `{"sessionId":"session-1","configId":"mode","type":"boolean","value":"plan"}`,
			wantErr: "boolean session configuration options are not supported",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var request SetSessionConfigOptionRequest
			if err := json.Unmarshal([]byte(test.input), &request); err != nil {
				t.Fatal(err)
			}
			err := request.Validate()
			if test.valid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || err.Error() != test.wantErr {
				t.Fatalf("validation error = %v, want %q", err, test.wantErr)
			}
		})
	}

	var request SetSessionConfigOptionRequest
	err := json.Unmarshal(
		[]byte(`{"sessionId":"session-1","configId":"mode","type":"boolean","value":false}`),
		&request,
	)
	if err == nil || !strings.Contains(err.Error(), "cannot unmarshal bool") {
		t.Fatalf("boolean value error = %v", err)
	}
}

func TestSessionConfigOptionValidation(t *testing.T) {
	valid := SessionConfigOption{
		Type:         SessionConfigOptionTypeSelect,
		ID:           "mode",
		Name:         "Mode",
		CurrentValue: "code",
		Options:      []SessionConfigSelectOption{{Value: "code", Name: "Code"}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}

	unknown := valid
	unknown.CurrentValue = "unknown"
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "is not in options") {
		t.Fatalf("unknown current value error = %v", err)
	}

	if err := (SetSessionConfigOptionResponse{}).Validate(); err == nil {
		t.Fatal("response without configOptions validated unexpectedly")
	}
	if err := (ConfigOptionUpdate{
		SessionUpdate: SessionUpdateConfigOptionUpdate,
	}).Validate(); err == nil {
		t.Fatal("update without configOptions validated unexpectedly")
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
