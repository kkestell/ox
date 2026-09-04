package acp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

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
		if json.Unmarshal(id, &value) == nil {
			return nil
		}
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

func (r InitializeRequest) Validate() error {
	if r.ProtocolVersion <= 0 {
		return errors.New("protocolVersion must be positive")
	}
	return nil
}

func (r ReadTextFileRequest) Validate() error {
	if r.SessionID == "" {
		return errors.New("sessionId is required")
	}
	if r.Path == "" {
		return errors.New("path is required")
	}
	if !filepath.IsAbs(r.Path) {
		return errors.New("path must be absolute")
	}
	if r.Line != nil && *r.Line < 1 {
		return errors.New("line must be positive")
	}
	if r.Limit != nil && *r.Limit < 1 {
		return errors.New("limit must be positive")
	}
	return nil
}

func (r WriteTextFileRequest) Validate() error {
	if r.SessionID == "" {
		return errors.New("sessionId is required")
	}
	if r.Path == "" {
		return errors.New("path is required")
	}
	if !filepath.IsAbs(r.Path) {
		return errors.New("path must be absolute")
	}
	return nil
}

func (r CreateTerminalRequest) Validate() error {
	if r.SessionID == "" {
		return errors.New("sessionId is required")
	}
	if r.Command == "" {
		return errors.New("command is required")
	}
	if r.CWD != nil && !filepath.IsAbs(*r.CWD) {
		return errors.New("cwd must be absolute")
	}
	if r.OutputByteLimit != nil && *r.OutputByteLimit < 1 {
		return errors.New("outputByteLimit must be positive")
	}
	return nil
}

func (r CreateTerminalResponse) Validate() error {
	if r.TerminalID == "" {
		return errors.New("terminalId is required")
	}
	return nil
}

func (r TerminalOutputRequest) Validate() error {
	return validateTerminalRequest(r.SessionID, r.TerminalID)
}

func (r WaitForTerminalExitRequest) Validate() error {
	return validateTerminalRequest(r.SessionID, r.TerminalID)
}

func (r KillTerminalRequest) Validate() error {
	return validateTerminalRequest(r.SessionID, r.TerminalID)
}

func (r ReleaseTerminalRequest) Validate() error {
	return validateTerminalRequest(r.SessionID, r.TerminalID)
}

func validateTerminalRequest(sessionID, terminalID string) error {
	if sessionID == "" {
		return errors.New("sessionId is required")
	}
	if terminalID == "" {
		return errors.New("terminalId is required")
	}
	return nil
}

func (r AuthenticateRequest) Validate() error {
	if r.MethodID == "" {
		return errors.New("methodId is required")
	}
	return nil
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
	for index, block := range r.Prompt {
		position := index + 1
		switch block.Type {
		case "text":
		case "resource_link":
			if block.Name == "" {
				return fmt.Errorf("prompt content block %d resource link requires name", position)
			}
			if block.URI == "" {
				return fmt.Errorf("prompt content block %d resource link requires uri", position)
			}
		case "image", "audio":
			if block.MIMEType == "" {
				return fmt.Errorf("prompt content block %d %s requires mimeType", position, block.Type)
			}
			if !strings.Contains(block.MIMEType, "/") {
				return fmt.Errorf(
					"prompt content block %d %s mimeType %q must contain /",
					position, block.Type, block.MIMEType,
				)
			}
			if block.Data == "" {
				return fmt.Errorf("prompt content block %d %s requires data", position, block.Type)
			}
			if _, err := base64.StdEncoding.DecodeString(block.Data); err != nil {
				return fmt.Errorf(
					"prompt content block %d %s data must be standard base64: %v",
					position, block.Type, err,
				)
			}
		case "resource":
			if block.Resource == nil {
				return fmt.Errorf("prompt content block %d resource is required", position)
			}
			resource := block.Resource
			if resource.URI == "" {
				return fmt.Errorf("prompt content block %d resource requires uri", position)
			}
			if (resource.Text == nil) == (resource.Blob == nil) {
				return fmt.Errorf(
					"prompt content block %d resource requires exactly one of text or blob",
					position,
				)
			}
			if resource.Blob != nil {
				if _, err := base64.StdEncoding.DecodeString(*resource.Blob); err != nil {
					return fmt.Errorf(
						"prompt content block %d resource blob must be standard base64: %v",
						position, err,
					)
				}
			}
		default:
			return fmt.Errorf(
				"prompt content block %d has unsupported type %q", position, block.Type,
			)
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
