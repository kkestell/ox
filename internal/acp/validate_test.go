package acp_test

import (
	"encoding/json"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestInitializeRequestValidate(t *testing.T) {
	for _, test := range []struct {
		name    string
		version int
		valid   bool
	}{
		{name: "supported", version: acp.ProtocolVersion, valid: true},
		{name: "unsupported positive", version: 999, valid: true},
		{name: "missing", valid: false},
		{name: "negative", version: -1, valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := (acp.InitializeRequest{ProtocolVersion: test.version}).Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestCancelRequestNotificationValidate(t *testing.T) {
	for _, test := range []struct {
		name  string
		id    string
		valid bool
	}{
		{name: "string", id: `"abc"`, valid: true},
		{name: "empty string", id: `""`, valid: true},
		{name: "integer", id: `2`, valid: true},
		{name: "negative integer", id: `-2`, valid: true},
		{name: "null", id: `null`, valid: true},
		{name: "missing", valid: false},
		{name: "fraction", id: `2.5`, valid: false},
		{name: "boolean", id: `true`, valid: false},
		{name: "object", id: `{}`, valid: false},
		{name: "array", id: `[]`, valid: false},
		{name: "invalid JSON", id: `{`, valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			notification := acp.CancelRequestNotification{RequestID: json.RawMessage(test.id)}
			err := notification.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}
