package server

import (
	"fmt"
	"maps"
	"reflect"

	protocol "github.com/coder/acp-go-sdk"
	"ox/internal/settings"
)

func acpMCPServers(definitions []protocol.McpServer) (map[string]settings.MCPServer, error) {
	servers := map[string]settings.MCPServer{}
	for _, definition := range definitions {
		if definition.Stdio == nil || definition.Http != nil || definition.Sse != nil || definition.Acp != nil {
			return nil, fmt.Errorf("ACP MCP servers support only stdio transport")
		}
		stdio := definition.Stdio
		if _, exists := servers[stdio.Name]; exists {
			return nil, fmt.Errorf("duplicate ACP MCP server %q", stdio.Name)
		}
		server := settings.MCPServer{Command: stdio.Command, Args: append([]string{}, stdio.Args...), Env: map[string]string{}}
		for _, env := range stdio.Env {
			if _, exists := server.Env[env.Name]; exists {
				return nil, fmt.Errorf("ACP MCP server %q: duplicate environment name %q", stdio.Name, env.Name)
			}
			server.Env[env.Name] = env.Value
		}
		if err := settings.ValidateMCPServer("ACP", stdio.Name, server); err != nil {
			return nil, err
		}
		servers[stdio.Name] = server
	}
	return servers, nil
}

func sameMCPServers(a, b map[string]settings.MCPServer) bool { return reflect.DeepEqual(a, b) }

func (s *Server) mcpServers(workspace string, acp map[string]settings.MCPServer) (map[string]settings.MCPServer, error) {
	configured, err := settings.LoadMCPServers(s.home, workspace)
	if err != nil {
		return nil, err
	}
	maps.Copy(configured, acp)
	return configured, nil
}
