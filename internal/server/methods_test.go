package server

import (
	"path/filepath"
	"testing"
)

func TestConfigureValidation(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "state.json"), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []string{
		`{}`,
		`{"session_id":"missing"}`,
		`{"session_id":"missing","model":" "}`,
		`{"session_id":"missing","effort":"minimal"}`,
		`{"session_id":"missing","permission_mode":""}`,
		`{"session_id":"missing","model":42}`,
		`{"session_id":"missing","effort":{}}`,
		`{"session_id":"missing","permission_mode":false}`,
	} {
		if response := s.configureSession(nil, []byte(params)); response.Error == nil || response.Error.Code != -32602 {
			t.Errorf("invalid configuration %s: %+v", params, response.Error)
		}
	}
	request := `{"jsonrpc":"2.0","id":1,"method":"session/configure","params":{"session_id":"missing","model":"haiku"}}`
	if response := s.handle(nil, []byte(request)); response.Error == nil || response.Error.Code != -32001 {
		t.Fatalf("configuration did not reach session lookup: %+v", response.Error)
	}
}
