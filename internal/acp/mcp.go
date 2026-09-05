package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

type HTTPHeader struct {
	Name  string   `json:"name"`
	Value string   `json:"value"`
	Meta  Metadata `json:"_meta,omitempty"`
}

type MCPServer struct {
	HTTP  *MCPHTTPServer
	Stdio *MCPStdioServer
}

type MCPHTTPServer struct {
	Type    string       `json:"type"`
	Name    string       `json:"name"`
	URL     string       `json:"url"`
	Headers []HTTPHeader `json:"headers"`
	Meta    Metadata     `json:"_meta,omitempty"`
}

type MCPStdioServer struct {
	Name    string        `json:"name"`
	Command string        `json:"command"`
	Args    []string      `json:"args"`
	Env     []EnvVariable `json:"env"`
	Meta    Metadata      `json:"_meta,omitempty"`
}

func (s MCPServer) MarshalJSON() ([]byte, error) {
	if (s.HTTP == nil) == (s.Stdio == nil) {
		return nil, errors.New("MCP server must contain exactly one transport")
	}
	if s.HTTP != nil {
		return json.Marshal(s.HTTP)
	}
	return json.Marshal(s.Stdio)
}

func (s *MCPServer) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if rawType, ok := fields["type"]; ok {
		var transport string
		if err := json.Unmarshal(rawType, &transport); err != nil {
			return errors.New("MCP server type must be a string")
		}
		if transport != "http" {
			return fmt.Errorf("MCP server transport %q is not supported", transport)
		}
		if _, ok := fields["command"]; ok {
			return errors.New("MCP HTTP server must not contain stdio fields")
		}
		var value MCPHTTPServer
		if err := decodeStrict(data, &value); err != nil {
			return err
		}
		*s = MCPServer{HTTP: &value}
		return s.Validate()
	}
	if _, ok := fields["url"]; ok {
		return errors.New("MCP HTTP server type is required")
	}
	var value MCPStdioServer
	if err := decodeStrict(data, &value); err != nil {
		return err
	}
	*s = MCPServer{Stdio: &value}
	return s.Validate()
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func (s MCPServer) Validate() error {
	if (s.HTTP == nil) == (s.Stdio == nil) {
		return errors.New("MCP server must contain exactly one transport")
	}
	if s.HTTP != nil {
		return s.HTTP.Validate()
	}
	return s.Stdio.Validate()
}

func (s MCPHTTPServer) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("MCP server name is required")
	}
	if s.Type != "http" {
		return errors.New("MCP HTTP server type must be http")
	}
	if s.Headers == nil {
		return errors.New("MCP HTTP server headers are required")
	}
	parsed, err := url.Parse(s.URL)
	if err != nil || parsed.Host == "" {
		return errors.New("MCP HTTP server URL must be absolute")
	}
	if parsed.User != nil {
		return errors.New("MCP HTTP server URL must not contain credentials")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return errors.New("MCP HTTP server URL must use http or https")
	}
	if parsed.Scheme == "http" && !isLocalHost(parsed.Hostname()) {
		return errors.New("plaintext MCP HTTP is allowed only for loopback or localhost")
	}
	seen := map[string]struct{}{}
	for i, header := range s.Headers {
		if header.Name == "" || !validHeaderName(header.Name) || !validHeaderValue(header.Value) {
			return fmt.Errorf("MCP HTTP header %d is malformed", i+1)
		}
		key := http.CanonicalHeaderKey(header.Name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("MCP HTTP header %q is duplicated", header.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (s MCPStdioServer) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("MCP server name is required")
	}
	if s.Command == "" || !filepath.IsAbs(s.Command) {
		return errors.New("MCP stdio command must be absolute")
	}
	if s.Args == nil {
		return errors.New("MCP stdio args are required")
	}
	if s.Env == nil {
		return errors.New("MCP stdio env is required")
	}
	seen := map[string]struct{}{}
	for i, variable := range s.Env {
		if variable.Name == "" || strings.Contains(variable.Name, "=") || strings.IndexByte(variable.Name, 0) >= 0 || strings.IndexByte(variable.Value, 0) >= 0 {
			return fmt.Errorf("MCP stdio environment variable %d is malformed", i+1)
		}
		if _, ok := seen[variable.Name]; ok {
			return fmt.Errorf("MCP stdio environment variable %q is duplicated", variable.Name)
		}
		seen[variable.Name] = struct{}{}
	}
	return nil
}

func ValidateMCPServers(servers []MCPServer) error {
	seen := map[string]struct{}{}
	for i, server := range servers {
		if err := server.Validate(); err != nil {
			return fmt.Errorf("mcpServers item %d: %w", i+1, err)
		}
		name := ""
		if server.HTTP != nil {
			name = server.HTTP.Name
		} else if server.Stdio != nil {
			name = server.Stdio.Name
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("mcpServers item %d duplicates server name %q", i+1, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func isLocalHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validHeaderName(name string) bool {
	for i := range len(name) {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for i := range len(value) {
		if value[i] == '\t' {
			continue
		}
		if value[i] < ' ' || value[i] == 0x7f {
			return false
		}
	}
	return true
}
