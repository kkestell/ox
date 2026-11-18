package protocol

import (
	"encoding/json"
	"testing"

	"github.com/kkestell/ox/internal/agents"
)

func TestConfigureValidation(t *testing.T) {
	registry := agents.NewRegistry()
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
		_, err := configureAgent(registry, json.RawMessage(params))
		if err == nil || err.Code != -32602 {
			t.Errorf("invalid configuration %s: %+v", params, err)
		}
	}
	c := &client{}
	_, err := c.dispatch(registry, "agent/configure", json.RawMessage(`{"agent_id":"missing","model":"haiku"}`))
	if err == nil || err.Code != -32001 {
		t.Fatalf("configuration did not reach agent lookup: %+v", err)
	}
}
