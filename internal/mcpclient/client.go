// Package mcpclient owns local MCP connections for one active session.
package mcpclient

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"ox/internal/settings"
)

// Clients is used by one operation at a time. Close follows the last operation.
type Clients struct {
	workspace    string
	definitions  map[string]settings.MCPServer
	sessions     map[string]*mcp.ClientSession
	ready        bool
	closed       bool
	setupTimeout time.Duration
	callTimeout  time.Duration
}

func New(workspace string, definitions map[string]settings.MCPServer) *Clients {
	copy := make(map[string]settings.MCPServer, len(definitions))
	for name, definition := range definitions {
		definition.Args = slices.Clone(definition.Args)
		definition.Env = maps.Clone(definition.Env)
		copy[name] = definition
	}
	return &Clients{workspace: workspace, definitions: copy, setupTimeout: 30 * time.Second, callTimeout: 120 * time.Second}
}

// Tool is an immutable turn's model definition and permission metadata.
type Tool struct {
	Name, Server, OriginalName string
	Description                string
	InputSchema                json.RawMessage
	ReadOnly                   bool
}

type target struct {
	tool    Tool
	session *mcp.ClientSession
}

// Catalog fixes discovery and dispatch for a single turn.
type Catalog struct {
	targets map[string]target
	timeout time.Duration
}

func (c *Catalog) Tools() []Tool {
	if c == nil {
		return nil
	}
	var tools []Tool
	for _, name := range slices.Sorted(maps.Keys(c.targets)) {
		tool := c.targets[name].tool
		tool.InputSchema = slices.Clone(tool.InputSchema)
		tools = append(tools, tool)
	}
	return tools
}

func (c *Catalog) Lookup(name string) (Tool, bool) {
	if c == nil {
		return Tool{}, false
	}
	target, ok := c.targets[name]
	tool := target.tool
	tool.InputSchema = slices.Clone(tool.InputSchema)
	return tool, ok
}

// Prepare connects lazily and refreshes every server's complete tool list.
func (c *Clients) Prepare(ctx context.Context) (_ *Catalog, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.closed {
		return nil, errors.New("MCP clients are closed; close and reload the session")
	}
	initial := !c.ready
	if initial {
		c.sessions = map[string]*mcp.ClientSession{}
		defer func() {
			if err != nil {
				c.closeConnections()
			}
		}()
	}
	catalog := &Catalog{targets: map[string]target{}, timeout: c.callTimeout}
	for _, name := range slices.Sorted(maps.Keys(c.definitions)) {
		if err := settings.ValidateMCPServer("configuration", name, c.definitions[name]); err != nil {
			return nil, err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.definitions)) {
		err := c.discover(ctx, name, initial, catalog)
		if err != nil {
			return nil, fmt.Errorf("MCP server %q: %w", name, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.ready = true
	return catalog, nil
}

func (c *Clients) discover(parent context.Context, name string, initial bool, catalog *Catalog) error {
	ctx, cancel := context.WithTimeout(parent, c.setupTimeout)
	defer cancel()
	session := c.sessions[name]
	if initial {
		client := mcp.NewClient(&mcp.Implementation{Name: "ox", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
		var err error
		// Use the tools-only protocol without the newer TTL discovery cache: every
		// turn must observe fresh permission annotations.
		session, err = client.Connect(ctx, resultTransport{&groupTransport{cmd: command(c.workspace, c.definitions[name])}}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
		if err != nil {
			return err
		}
		c.sessions[name] = session
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		var result struct {
			Tools []*struct {
				Name        string
				Description string
				InputSchema json.RawMessage
				Annotations *mcp.ToolAnnotations
			}
			NextCursor string
		}
		err := receiveResult(ctx, func(ctx context.Context) error {
			_, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
			return err
		}, &result)
		if err != nil {
			return connectionError(err)
		}
		for _, tool := range result.Tools {
			if tool == nil || tool.Name == "" {
				return errors.New("discovered a tool with no name")
			}
			schema := tool.InputSchema
			var object map[string]json.RawMessage
			if json.Unmarshal(schema, &object) != nil || object == nil {
				return fmt.Errorf("tool %q has no input schema object", tool.Name)
			}
			modelName := modelName(name, tool.Name)
			if _, exists := catalog.targets[modelName]; exists {
				return fmt.Errorf("tool name collision: %s", modelName)
			}
			catalog.targets[modelName] = target{Tool{Name: modelName, Server: name, OriginalName: tool.Name, Description: tool.Description, InputSchema: schema, ReadOnly: tool.Annotations != nil && tool.Annotations.ReadOnlyHint}, session}
		}
		if result.NextCursor == "" {
			return nil
		}
		if seen[result.NextCursor] {
			return errors.New("tool discovery repeated a pagination cursor")
		}
		cursor = result.NextCursor
		seen[cursor] = true
	}
}

func modelName(server, tool string) string {
	original := "mcp__" + server + "__" + tool
	safe := func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}
	name := strings.Map(safe, original)
	// Separators inside either component also need encoding to avoid ambiguous pairs.
	if name != original || len(name) > 64 || strings.Contains(server, "__") || strings.Contains(tool, "__") {
		pair, _ := json.Marshal([]string{server, tool})
		hash := sha256.Sum256(pair)
		name = name[:min(len(name), 39)] + fmt.Sprintf("_%x", hash[:12])
	}
	return name
}

// Result keeps SDK types inside this package. Content contains protocol blocks
// for the text adapter; dispatch preserves the original argument JSON.
type Result struct {
	Content    []json.RawMessage `json:"content"`
	Structured json.RawMessage   `json:"structuredContent"`
	IsError    bool              `json:"isError"`
}

func (c *Catalog) Call(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	if c == nil {
		return Result{}, errors.New("unknown MCP tool")
	}
	target, ok := c.targets[name]
	if !ok {
		return Result{}, errors.New("unknown MCP tool: " + name)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var result Result
	err := receiveResult(ctx, func(ctx context.Context) error {
		_, err := target.session.CallTool(ctx, &mcp.CallToolParams{Name: target.tool.OriginalName, Arguments: arguments})
		return err
	}, &result)
	if err != nil {
		return Result{}, fmt.Errorf("MCP server %q tool %q: %w", target.tool.Server, target.tool.OriginalName, connectionError(err))
	}
	return result, nil
}

func connectionError(err error) error {
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return fmt.Errorf("disconnected; close and reload the session: %w", err)
	}
	return err
}

func (c *Clients) closeConnections() {
	var wait sync.WaitGroup
	for _, session := range c.sessions {
		wait.Go(func() { _ = session.Close() })
	}
	wait.Wait()
	c.sessions = nil
}

func (c *Clients) Close() {
	if c == nil || c.closed {
		return
	}
	c.closed = true
	c.closeConnections()
}
