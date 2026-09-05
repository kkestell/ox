package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/kkestell/ox/internal/acp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	ProtocolVersion = "2026-07-28"
	MaxTools        = 256
	MaxCatalogBytes = 256 << 10
	MaxCatalogPages = 256
	MaxResultBytes  = 1 << 20
	MaxWireBytes    = 2 << 20
)

var (
	connectTimeoutSetting = "30s"
	callTimeoutSetting    = "120s"
	connectTimeout        = mustDuration(connectTimeoutSetting)
	callTimeout           = mustDuration(callTimeoutSetting)
)

func mustDuration(value string) time.Duration {
	duration, err := time.ParseDuration(value)
	if err != nil {
		panic(err)
	}
	return duration
}

type Descriptor struct {
	Name        string          `json:"name"`
	ServerName  string          `json:"serverName"`
	ToolName    string          `json:"toolName"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Identity    string          `json:"identity"`
}

type Result struct {
	Content []byte
	IsError bool
}

type Bundle struct {
	servers []*server
	tools   []Descriptor
	byName  map[string]toolRef
}

type server struct {
	name      string
	session   *sdk.ClientSession
	transport identityTransport
	tools     map[string]discoveredTool
	secrets   []string
}

type identityTransport struct {
	Kind        string   `json:"kind"`
	Destination any      `json:"destination"`
	SecretNames []string `json:"secretNames,omitempty"`
}

type discoveredTool struct {
	descriptor Descriptor
}

type toolRef struct {
	server *server
	tool   discoveredTool
}

func Activate(ctx context.Context, root string, definitions []acp.MCPServer) (*Bundle, error) {
	if root == "" {
		return nil, errors.New("MCP session root is required")
	}
	if err := acp.ValidateMCPServers(definitions); err != nil {
		return nil, err
	}
	bundle := &Bundle{byName: make(map[string]toolRef)}
	catalogBytes := 2 // JSON array delimiters.
	for _, definition := range definitions {
		opened, err := connectServer(ctx, root, definition)
		if err != nil {
			_ = bundle.Close()
			return nil, err
		}
		bundle.servers = append(bundle.servers, opened)
		for _, tool := range opened.tools {
			if len(bundle.tools) == MaxTools {
				_ = bundle.Close()
				return nil, errors.New("MCP catalog exceeds 256 tools")
			}
			if _, exists := bundle.byName[tool.descriptor.Name]; exists {
				_ = bundle.Close()
				return nil, fmt.Errorf("MCP provider tool name %q is not unique", tool.descriptor.Name)
			}
			encoded, err := json.Marshal(tool.descriptor)
			if err != nil {
				_ = bundle.Close()
				return nil, errors.New("encode MCP catalog")
			}
			if len(bundle.tools) > 0 {
				catalogBytes++
			}
			catalogBytes += len(encoded)
			if catalogBytes > MaxCatalogBytes {
				_ = bundle.Close()
				return nil, errors.New("MCP catalog exceeds 256 KiB")
			}
			bundle.tools = append(bundle.tools, cloneDescriptor(tool.descriptor))
			bundle.byName[tool.descriptor.Name] = toolRef{server: opened, tool: tool}
		}
	}
	sort.Slice(bundle.tools, func(i, j int) bool { return bundle.tools[i].Name < bundle.tools[j].Name })
	return bundle, nil
}

func (b *Bundle) Tools() []Descriptor {
	result := make([]Descriptor, len(b.tools))
	for i, descriptor := range b.tools {
		result[i] = cloneDescriptor(descriptor)
	}
	return result
}

func (b *Bundle) Call(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	reference, ok := b.byName[name]
	if !ok {
		return Result{}, fmt.Errorf("unknown MCP tool %q", name)
	}
	var input map[string]any
	if len(arguments) == 0 {
		input = map[string]any{}
	} else if err := json.Unmarshal(arguments, &input); err != nil || input == nil {
		return Result{}, errors.New("MCP tool arguments must be a JSON object")
	}
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	fresh, err := discoverTools(callCtx, reference.server)
	if err != nil {
		return Result{}, reference.server.redact(fmt.Errorf("refresh MCP server %q tools: %w", reference.server.name, err))
	}
	current, ok := fresh[reference.tool.descriptor.ToolName]
	if !ok || current.descriptor.Identity != reference.tool.descriptor.Identity {
		return Result{}, fmt.Errorf("MCP tool %q changed since activation", name)
	}
	value, err := reference.server.session.CallTool(callCtx, &sdk.CallToolParams{Name: current.descriptor.ToolName, Arguments: input})
	if err != nil {
		return Result{}, reference.server.redact(fmt.Errorf("call MCP tool %q: %w", name, err))
	}
	if value.NeedsInput() {
		return Result{}, errors.New("MCP tool requires unsupported client input")
	}
	content, err := renderResult(value)
	if err != nil {
		return Result{}, reference.server.redact(err)
	}
	content = reference.server.redactContent(content)
	if len(content) > MaxResultBytes {
		return Result{}, errors.New("MCP tool result exceeds 1 MiB")
	}
	return Result{Content: content, IsError: value.IsError}, nil
}

func (b *Bundle) Close() error {
	var joined error
	for i := len(b.servers) - 1; i >= 0; i-- {
		joined = errors.Join(joined, b.servers[i].session.Close())
	}
	b.servers = nil
	return joined
}

func connectServer(parent context.Context, root string, definition acp.MCPServer) (*server, error) {
	ctx, cancel := context.WithTimeout(parent, connectTimeout)
	defer cancel()
	value := &server{}
	var transport sdk.Transport
	if definition.Stdio != nil {
		d := definition.Stdio
		value.name = d.Name
		value.transport = identityTransport{Kind: "stdio", Destination: struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		}{d.Command, append([]string(nil), d.Args...)}}
		for _, variable := range d.Env {
			value.transport.SecretNames = append(value.transport.SecretNames, variable.Name)
			value.secrets = append(value.secrets, variable.Value)
		}
		transport = &commandTransport{command: d.Command, args: d.Args, env: d.Env, dir: root}
	} else {
		d := definition.HTTP
		value.name = d.Name
		value.transport = identityTransport{Kind: "http", Destination: d.URL}
		for _, header := range d.Headers {
			value.transport.SecretNames = append(value.transport.SecretNames, http.CanonicalHeaderKey(header.Name))
			value.secrets = append(value.secrets, header.Value)
		}
		transport = newHTTPTransport(d.URL, d.Headers)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "ox", Version: "dev"}, &sdk.ClientOptions{
		Capabilities:   &sdk.ClientCapabilities{},
		MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true},
	})
	client.AddSendingMiddleware(disableToolCache)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, value.redact(fmt.Errorf("connect MCP server %q: %w", value.name, err))
	}
	value.session = session
	if result := session.InitializeResult(); result == nil || result.ProtocolVersion != ProtocolVersion {
		_ = session.Close()
		got := ""
		if result != nil {
			got = result.ProtocolVersion
		}
		return nil, fmt.Errorf("MCP server %q negotiated unsupported protocol revision %q", value.name, got)
	}
	value.tools, err = discoverTools(ctx, value)
	if err != nil {
		_ = session.Close()
		return nil, value.redact(fmt.Errorf("discover MCP server %q tools: %w", value.name, err))
	}
	return value, nil
}

func discoverTools(ctx context.Context, value *server) (map[string]discoveredTool, error) {
	result := make(map[string]discoveredTool)
	seenCursors := map[string]struct{}{}
	cursor := ""
	pageCount := 0
	catalogBytes := 2 // JSON array delimiters.
	for {
		pageCount++
		if pageCount > MaxCatalogPages {
			return nil, errors.New("MCP tools pagination exceeds 256 pages")
		}
		page, err := value.session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, tool := range page.Tools {
			if tool == nil || strings.TrimSpace(tool.Name) == "" {
				return nil, errors.New("MCP tool name is required")
			}
			if _, exists := result[tool.Name]; exists {
				return nil, fmt.Errorf("MCP server returned duplicate tool %q", tool.Name)
			}
			found, err := makeDiscovered(value, tool)
			if err != nil {
				return nil, fmt.Errorf("MCP tool %q: %w", tool.Name, err)
			}
			encoded, err := json.Marshal(found.descriptor)
			if err != nil {
				return nil, errors.New("encode MCP catalog")
			}
			if len(result) > 0 {
				catalogBytes++
			}
			catalogBytes += len(encoded)
			if catalogBytes > MaxCatalogBytes {
				return nil, errors.New("MCP catalog exceeds 256 KiB")
			}
			result[tool.Name] = found
			if len(result) > MaxTools {
				return nil, errors.New("MCP catalog exceeds 256 tools")
			}
		}
		if page.NextCursor == "" {
			break
		}
		if _, exists := seenCursors[page.NextCursor]; exists {
			return nil, errors.New("MCP tools pagination repeated a cursor")
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	return result, nil
}

func makeDiscovered(value *server, tool *sdk.Tool) (discoveredTool, error) {
	schema, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return discoveredTool{}, errors.New("input schema is not valid JSON")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(schema, &object); err != nil || object == nil {
		return discoveredTool{}, errors.New("input schema must be a JSON Schema object")
	}
	var parsed jsonschema.Schema
	if err := json.Unmarshal(schema, &parsed); err != nil {
		return discoveredTool{}, fmt.Errorf("invalid input schema: %w", err)
	}
	if _, err := parsed.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true}); err != nil {
		return discoveredTool{}, fmt.Errorf("invalid input schema: %w", err)
	}
	raw, err := json.Marshal(tool)
	if err != nil {
		return discoveredTool{}, fmt.Errorf("encode definition: %w", err)
	}
	if value.containsSecretJSON(raw) {
		return discoveredTool{}, errors.New("definition contains a configured secret")
	}
	identityData, err := json.Marshal(struct {
		Transport identityTransport `json:"transport"`
		Tool      json.RawMessage   `json:"tool"`
	}{value.transport, raw})
	if err != nil {
		return discoveredTool{}, err
	}
	digest := sha256.Sum256(identityData)
	title := tool.Title
	if title == "" && tool.Annotations != nil {
		title = tool.Annotations.Title
	}
	descriptor := Descriptor{
		Name: providerName(value.name, tool.Name), ServerName: value.name,
		ToolName: tool.Name, Title: title, Description: tool.Description,
		InputSchema: append(json.RawMessage(nil), schema...), Identity: hex.EncodeToString(digest[:]),
	}
	return discoveredTool{descriptor: descriptor}, nil
}

func providerName(serverName, toolName string) string {
	base := "mcp__" + safeComponent(serverName) + "__" + safeComponent(toolName)
	if len(base) <= 64 {
		return base
	}
	digest := sha256.Sum256([]byte(base))
	return base[:53] + "_" + hex.EncodeToString(digest[:5])
}

func safeComponent(value string) string {
	var result strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '_' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteByte('_')
		}
	}
	if result.Len() == 0 {
		return "_"
	}
	return result.String()
}

func renderResult(value *sdk.CallToolResult) ([]byte, error) {
	var parts [][]byte
	for _, content := range value.Content {
		text, ok := content.(*sdk.TextContent)
		if !ok {
			return nil, fmt.Errorf("MCP tool returned unsupported content %T", content)
		}
		parts = append(parts, []byte(text.Text))
	}
	if value.StructuredContent != nil {
		encoded, err := json.Marshal(value.StructuredContent)
		if err != nil {
			return nil, fmt.Errorf("encode MCP structured result: %w", err)
		}
		parts = append(parts, encoded)
	}
	return bytes.Join(parts, []byte("\n")), nil
}

func cloneDescriptor(value Descriptor) Descriptor {
	value.InputSchema = append(json.RawMessage(nil), value.InputSchema...)
	return value
}

func (s *server) redact(err error) error {
	return errors.New(string(s.redactContent([]byte(err.Error()))))
}

func (s *server) redactContent(content []byte) []byte {
	message := string(content)
	secrets := append([]string(nil), s.secrets...)
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
			if encoded, err := json.Marshal(secret); err == nil && len(encoded) >= 2 {
				message = strings.ReplaceAll(message, string(encoded[1:len(encoded)-1]), "[redacted]")
			}
		}
	}
	return []byte(message)
}

func (s *server) containsSecretJSON(data []byte) bool {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return true
	}
	return containsSecretValue(value, s.secrets)
}

func containsSecretValue(value any, secrets []string) bool {
	contains := func(text string) bool {
		for _, secret := range secrets {
			if secret != "" && strings.Contains(text, secret) {
				return true
			}
		}
		return false
	}
	switch typed := value.(type) {
	case string:
		return contains(typed)
	case []any:
		for _, item := range typed {
			if containsSecretValue(item, secrets) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if contains(key) || containsSecretValue(item, secrets) {
				return true
			}
		}
	}
	return false
}

func disableToolCache(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
		result, err := next(ctx, method, request)
		if page, ok := result.(*sdk.ListToolsResult); ok {
			page.TTLMs = 0
		}
		return result, err
	}
}
