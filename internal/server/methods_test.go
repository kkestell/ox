package server

import (
	"testing"
)

func TestConfigureValidation(t *testing.T) {
	s := New()
	for _, params := range []string{
		`{}`,
		`{"agent_id":"missing"}`,
		`{"agent_id":"missing","model":" "}`,
		`{"agent_id":"missing","effort":"minimal"}`,
		`{"agent_id":"missing","permission_mode":""}`,
		`{"agent_id":"missing","model":42}`,
		`{"agent_id":"missing","effort":{}}`,
		`{"agent_id":"missing","permission_mode":false}`,
	} {
		if response := s.configureAgent(nil, []byte(params)); response.Error == nil || response.Error.Code != -32602 {
			t.Errorf("invalid configuration %s: %+v", params, response.Error)
		}
	}
	request := `{"jsonrpc":"2.0","id":1,"method":"agent/configure","params":{"agent_id":"missing","model":"haiku"}}`
	if response := s.handle(nil, []byte(request)); response.Error == nil || response.Error.Code != -32001 {
		t.Fatalf("configuration did not reach agent lookup: %+v", response.Error)
	}
}
