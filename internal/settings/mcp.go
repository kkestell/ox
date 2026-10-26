package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// MCPServer describes one local stdio server. It is never saved in a session.
type MCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// ValidateMCPServer validates both settings and ACP definitions before startup.
func ValidateMCPServer(source, name string, server MCPServer) error {
	fail := func(message string) error { return fmt.Errorf("%s: MCP server %q: %s", source, name, message) }
	if strings.TrimSpace(name) == "" {
		return fail("name is empty")
	}
	if strings.TrimSpace(server.Command) == "" || strings.ContainsRune(server.Command, 0) {
		return fail("command must be nonempty and contain no NUL")
	}
	for _, arg := range server.Args {
		if strings.ContainsRune(arg, 0) {
			return fail("argument contains NUL")
		}
	}
	for _, key := range slices.Sorted(maps.Keys(server.Env)) {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return fail("invalid environment name " + key)
		}
		if strings.ContainsRune(server.Env[key], 0) {
			return fail("environment value for " + key + " contains NUL")
		}
	}
	return nil
}

// LoadMCPServers reads configuration when a session becomes active. A present
// workspace map replaces the global map, including when it is empty. The
// launch settings' servers are added, replacing any with the same name.
func LoadMCPServers(home, workspace string) (map[string]MCPServer, error) {
	servers := map[string]MCPServer{}
	for _, path := range []string{GlobalPath(home), filepath.Join(workspace, ".ox/settings.json"), LaunchVariable} {
		text, err := readSource(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &fields); err != nil || fields == nil {
			return nil, invalid(path, "settings must be a JSON object")
		}
		raw, present := fields["mcpServers"]
		if !present {
			continue
		}
		var definitions map[string]json.RawMessage
		if err := json.Unmarshal(raw, &definitions); err != nil || definitions == nil {
			return nil, invalid(path, "mcpServers must be an object")
		}
		if path != LaunchVariable {
			servers = map[string]MCPServer{}
		}
		for _, name := range slices.Sorted(maps.Keys(definitions)) {
			raw := definitions[name]
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
				return nil, invalid(path, "MCP server %q: definition must be an object", name)
			}
			for key, value := range fields {
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return nil, invalid(path, "MCP server %q: %s cannot be null", name, key)
				}
			}
			var server MCPServer
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&server); err != nil {
				return nil, invalid(path, "MCP server %q: %v", name, err)
			}
			// encoding/json otherwise accepts null elements as empty strings.
			var args []json.RawMessage
			if err := json.Unmarshal(fields["args"], &args); fields["args"] != nil && err != nil {
				return nil, invalid(path, "MCP server %q: invalid args", name)
			}
			var env map[string]json.RawMessage
			_ = json.Unmarshal(fields["env"], &env)
			for _, value := range append(args, slices.Collect(maps.Values(env))...) {
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
					return nil, invalid(path, "MCP server %q: arguments and environment values must be strings", name)
				}
			}
			if err := ValidateMCPServer(path, name, server); err != nil {
				return nil, err
			}
			servers[name] = server
		}
	}
	return servers, nil
}
