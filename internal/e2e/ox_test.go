package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestInitialize(t *testing.T) {
	child := start(t)
	result := child.request("initialize", acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion,
		ClientCapabilities: &acp.ClientCapabilities{
			FS: &acp.FileSystemCapabilities{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Terminal: true,
		},
		ClientInfo: &acp.Implementation{Name: "test-client", Version: "1.0.0"},
	})

	var response acp.InitializeResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if response.ProtocolVersion != acp.ProtocolVersion {
		t.Errorf("protocolVersion = %d, want %d", response.ProtocolVersion, acp.ProtocolVersion)
	}
	if response.AgentCapabilities.LoadSession {
		t.Error("loadSession = true, want false")
	}
	wantPromptCapabilities := acp.PromptCapabilities{
		Image:           true,
		Audio:           true,
		EmbeddedContext: true,
	}
	if response.AgentCapabilities.PromptCapabilities != wantPromptCapabilities {
		t.Errorf("promptCapabilities = %#v, want %#v",
			response.AgentCapabilities.PromptCapabilities, wantPromptCapabilities)
	}
	if response.AgentInfo != (acp.Implementation{Name: "ox", Version: "0.0.1"}) {
		t.Errorf("agentInfo = %#v", response.AgentInfo)
	}
	if response.AuthMethods == nil || len(response.AuthMethods) != 0 {
		t.Errorf("authMethods = %#v, want empty array", response.AuthMethods)
	}
}

func TestInitializeNegotiatesSupportedVersion(t *testing.T) {
	child := start(t)
	result := child.request("initialize", acp.InitializeRequest{ProtocolVersion: 999})

	var response acp.InitializeResponse
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if response.ProtocolVersion != acp.ProtocolVersion {
		t.Fatalf("protocolVersion = %d, want %d", response.ProtocolVersion, acp.ProtocolVersion)
	}
}

func TestInitializeRejectsNonPositiveProtocolVersion(t *testing.T) {
	for _, test := range []struct {
		name   string
		params any
	}{
		{name: "missing", params: map[string]any{}},
		{name: "zero", params: acp.InitializeRequest{ProtocolVersion: 0}},
		{name: "negative", params: acp.InitializeRequest{ProtocolVersion: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			child := start(t)
			responseError := child.requestError("initialize", test.params)
			if responseError.Code != -32602 {
				t.Fatalf("error code = %d, want -32602", responseError.Code)
			}
		})
	}
}

func TestUnknownMethod(t *testing.T) {
	child := start(t)
	responseError := child.requestError("unknown", map[string]any{})
	if responseError.Code != -32601 {
		t.Fatalf("error code = %d, want -32601", responseError.Code)
	}
}

func TestMalformedJSONReturnsParseErrorAndRecovers(t *testing.T) {
	child := start(t)
	child.send(`{not json`)
	response := child.readUntil("malformed JSON error", func(candidate message) bool {
		return candidate.Error != nil && candidate.Error.Code == -32700
	})
	if string(response.ID) != "null" {
		t.Errorf("parse error id = %s, want null", response.ID)
	}

	result := child.request("initialize", acp.InitializeRequest{ProtocolVersion: 1})
	if len(result) == 0 {
		t.Fatal("initialize returned an empty result after malformed JSON")
	}
}

func TestUnknownCancellationDoesNotRespondAndServerRecovers(t *testing.T) {
	for _, requestID := range []json.RawMessage{json.RawMessage(`"missing"`), json.RawMessage(`999`), json.RawMessage(`null`)} {
		t.Run(string(requestID), func(t *testing.T) {
			child := start(t)
			child.notify("$/cancel_request", struct {
				RequestID json.RawMessage `json:"requestId"`
			}{RequestID: requestID})
			result := child.request("initialize", acp.InitializeRequest{ProtocolVersion: 1})
			if len(result) == 0 {
				t.Fatal("server did not answer after cancellation notification")
			}
		})
	}
}

func TestDebugLoggingStaysOffStdout(t *testing.T) {
	child := start(t)
	_ = child.request("initialize", acp.InitializeRequest{ProtocolVersion: 1})
	child.notify("$/cancel_request", struct {
		RequestID json.RawMessage `json:"requestId"`
	}{RequestID: json.RawMessage(`"missing"`)})
	// The second round trip proves the notification was handled before shutdown.
	_ = child.request("initialize", acp.InitializeRequest{ProtocolVersion: 1})
	child.stop()

	stderr := child.stderr.String()
	for _, want := range []string{
		`level=INFO msg="ox starting"`,
		`level=INFO msg="initializing client"`,
		`level=DEBUG msg="cancelling request"`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want %s", stderr, want)
		}
	}
}

func TestStdinEOFExitsCleanly(t *testing.T) {
	for _, level := range []string{"", "unrecognized"} {
		name := level
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			child := start(t, withEnvironment("OX_LOG_LEVEL", level))
			child.stop()
			if !strings.Contains(child.stderr.String(), `level=INFO msg="ox starting"`) {
				t.Errorf("stderr = %q, want startup log", child.stderr.String())
			}
		})
	}
}
