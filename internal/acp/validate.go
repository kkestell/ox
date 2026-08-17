package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

func (r InitializeRequest) Validate() error {
	if r.ProtocolVersion <= 0 {
		return errors.New("protocolVersion must be positive")
	}
	return nil
}

func (n CancelRequestNotification) Validate() error {
	id := bytes.TrimSpace(n.RequestID)
	if len(id) == 0 {
		return errors.New("requestId is required")
	}
	if !json.Valid(id) {
		return errors.New("requestId must be valid JSON")
	}

	switch id[0] {
	case '"':
		var value string
		if err := json.Unmarshal(id, &value); err != nil {
			return errors.New("requestId must be a string, integer, or null")
		}
		return nil
	case 'n':
		if bytes.Equal(id, []byte("null")) {
			return nil
		}
	default:
		if _, err := strconv.ParseInt(string(id), 10, 64); err == nil {
			return nil
		}
	}

	return errors.New("requestId must be a string, integer, or null")
}
