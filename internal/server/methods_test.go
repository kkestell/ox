package server

import (
	"path/filepath"
	"testing"
)

func TestSettingsValidation(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "state.json"), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []string{
		`{}`,
		`{"session_id":"missing"}`,
		`{"session_id":"missing","model":"haiku","effort":"low"}`,
		`{"session_id":"missing","model":" ","effort":"low","permission_mode":"plan"}`,
		`{"session_id":"missing","model":"haiku","effort":"","permission_mode":"plan"}`,
		`{"session_id":"missing","model":"haiku","effort":"minimal","permission_mode":"plan"}`,
		`{"session_id":"missing","model":"haiku","effort":"low","permission_mode":""}`,
		`{"session_id":"missing","model":42,"effort":"low","permission_mode":"plan"}`,
	} {
		if response := s.configureSession(nil, []byte(params)); response.Error == nil || response.Error.Code != -32602 {
			t.Errorf("invalid configuration %s: %+v", params, response.Error)
		}
	}
	for _, params := range []string{
		`{"workspace_id":"missing"}`,
		`{"workspace_id":"missing","model":"haiku","effort":"","permission_mode":"plan"}`,
	} {
		if response := s.createSession(nil, []byte(params)); response.Error == nil || response.Error.Code != -32602 {
			t.Errorf("invalid creation %s: %+v", params, response.Error)
		}
	}
	request := `{"jsonrpc":"2.0","id":1,"method":"session/configure","params":{"session_id":"missing","model":"haiku","effort":"low","permission_mode":"plan"}}`
	if response := s.handle(nil, []byte(request)); response.Error == nil || response.Error.Code != -32001 {
		t.Fatalf("configuration did not reach session lookup: %+v", response.Error)
	}
}
