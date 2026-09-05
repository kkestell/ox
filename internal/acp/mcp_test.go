package acp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPServerWireShapes(t *testing.T) {
	for _, test := range []struct {
		name string
		wire string
	}{
		{"stdio", `{"name":"local","command":"/bin/tool","args":[],"env":[],"_meta":{"x":true}}`},
		{"http", `{"type":"http","name":"remote","url":"https://example.com/mcp","headers":[],"_meta":{"x":true}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var server MCPServer
			if err := json.Unmarshal([]byte(test.wire), &server); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(server)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.wire {
				t.Fatalf("round trip = %s", encoded)
			}
		})
	}
}

func TestMCPServerRejectsInvalidDefinitions(t *testing.T) {
	for _, wire := range []string{
		`{"type":"sse","name":"old","url":"https://example.com","headers":[]}`,
		`{"type":"acp","name":"nested"}`,
		`{"type":"http","name":"mixed","url":"https://example.com","headers":[],"command":"/bin/tool"}`,
		`{"type":"http","name":"remote","url":"http://example.com","headers":[]}`,
		`{"type":"http","name":"remote","url":"https://user:secret@example.com","headers":[]}`,
		`{"type":"http","name":"remote","url":"https://example.com","headers":[{"name":"Bad Header","value":"x"}]}`,
		`{"name":"local","command":"tool","args":[],"env":[]}`,
		`{"name":"local","command":"/bin/tool","args":[],"env":[{"name":"BAD=NAME","value":"x"}]}`,
	} {
		var server MCPServer
		if err := json.Unmarshal([]byte(wire), &server); err == nil {
			t.Fatalf("accepted invalid definition %s", wire)
		}
	}
}

func TestMCPServerRequiredArraysAndLocalHTTP(t *testing.T) {
	for _, wire := range []string{
		`{"name":"local","command":"/bin/tool","env":[]}`,
		`{"name":"local","command":"/bin/tool","args":[]}`,
		`{"type":"http","name":"local","url":"http://localhost/mcp"}`,
	} {
		var server MCPServer
		if err := json.Unmarshal([]byte(wire), &server); err == nil || !strings.Contains(err.Error(), "required") {
			t.Fatalf("error for %s = %v", wire, err)
		}
	}
	var server MCPServer
	if err := json.Unmarshal([]byte(`{"type":"http","name":"local","url":"http://127.0.0.1/mcp","headers":[]}`), &server); err != nil {
		t.Fatal(err)
	}
}

func TestMCPCapabilitiesWireShape(t *testing.T) {
	data, err := json.Marshal(AgentCapabilities{MCPCapabilities: &MCPCapabilities{HTTP: true}})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"mcpCapabilities":{"http":true},"loadSession":false}` {
		t.Fatalf("capabilities = %s", data)
	}
}
