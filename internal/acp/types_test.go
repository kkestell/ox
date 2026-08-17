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
			PromptCapabilities: acp.PromptCapabilities{},
		},
		AgentInfo:   acp.Implementation{Name: "ox", Version: "0.0.1"},
		AuthMethods: []json.RawMessage{},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, encoded, []byte(`{
		"protocolVersion": 1,
		"agentCapabilities": {
			"loadSession": false,
			"promptCapabilities": {}
		},
		"agentInfo": {
			"name": "ox",
			"version": "0.0.1"
		},
		"authMethods": []
	}`))
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
