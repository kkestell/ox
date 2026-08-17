package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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

func (r NewSessionRequest) Validate() error {
	if r.CWD == "" {
		return errors.New("cwd is required")
	}
	if !filepath.IsAbs(r.CWD) {
		return errors.New("cwd must be an absolute path")
	}
	if r.MCPServers == nil {
		return errors.New("mcpServers is required")
	}
	if len(r.MCPServers) != 0 {
		return errors.New("MCP servers are not supported")
	}
	if len(r.AdditionalDirectories) != 0 {
		return errors.New("additional directories are not supported")
	}
	return nil
}

func (r PromptRequest) Validate() error {
	if r.SessionID == "" {
		return errors.New("sessionId is required")
	}
	if len(r.Prompt) == 0 {
		return errors.New("prompt requires at least one content block")
	}
	for _, block := range r.Prompt {
		switch block.Type {
		case "text":
		case "resource_link":
			if block.Name == "" || block.URI == "" {
				return errors.New("resource link content requires name and uri")
			}
		default:
			return fmt.Errorf("unsupported prompt content type %q", block.Type)
		}
	}
	return nil
}

func (n CancelNotification) Validate() error {
	if n.SessionID == "" {
		return errors.New("sessionId is required")
	}
	return nil
}
