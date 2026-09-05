package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/mcp"
	"github.com/kkestell/ox/internal/openrouter"
	"github.com/kkestell/ox/internal/workspace"
)

const mcpInlineBytes = 64 << 10

type failedToolResultError struct {
	content string
}

func (e failedToolResultError) Error() string { return "MCP server reported a tool error" }

func (a *Agent) activateMCP(
	ctx context.Context,
	root string,
	definitions []acp.MCPServer,
) (*mcp.Bundle, toolSet, toolSet, []mcpToolConfiguration, error) {
	bundle, err := mcp.Activate(ctx, root, definitions)
	if err != nil {
		return nil, toolSet{}, toolSet{}, nil, err
	}
	primary, subagent := a.negotiatedToolSets()
	descriptors := bundle.Tools()
	dynamic := make([]Tool, 0, len(descriptors))
	evidence := make([]mcpToolConfiguration, 0, len(descriptors))
	for _, descriptor := range descriptors {
		descriptor := descriptor
		dynamic = append(dynamic, Tool{
			Name:        descriptor.Name,
			Description: descriptor.Description,
			InputSchema: append(json.RawMessage(nil), descriptor.InputSchema...),
			Kind:        acp.ToolKindOther,
			Approval:    ApprovalAsk,
			Title: func(json.RawMessage) string {
				return mcpToolTitle(descriptor.ServerName, descriptor.ToolName, descriptor.Title)
			},
			Suggest: func(json.RawMessage) string { return descriptor.Identity },
			Covered: func(rules []string, _ json.RawMessage) bool {
				return slices.Contains(rules, descriptor.Identity)
			},
			Execute: func(callCtx context.Context, invocation Invocation) (string, error) {
				result, err := bundle.Call(callCtx, descriptor.Name, invocation.Arguments)
				if err != nil {
					return "", err
				}
				rendered, err := workspace.RenderText(
					invocation.SpillDir, "mcp", invocation.CallID,
					string(result.Content), mcpInlineBytes,
				)
				if err != nil {
					return "", err
				}
				if rendered.Spilled != "" && invocation.ReportSpill != nil {
					invocation.ReportSpill(rendered.Spilled)
				}
				if result.IsError {
					return "", failedToolResultError{content: rendered.Content}
				}
				return rendered.Content, nil
			},
		})
		evidence = append(evidence, mcpToolConfiguration{
			Name: descriptor.Name, ServerName: descriptor.ServerName,
			ToolName: descriptor.ToolName, Title: descriptor.Title,
			Identity: descriptor.Identity,
		})
	}
	primary, err = appendTools(primary, dynamic)
	if err != nil {
		_ = bundle.Close()
		return nil, toolSet{}, toolSet{}, nil, err
	}
	subagent, err = appendTools(subagent, dynamic)
	if err != nil {
		_ = bundle.Close()
		return nil, toolSet{}, toolSet{}, nil, err
	}
	return bundle, primary, subagent, evidence, nil
}

func appendTools(base toolSet, extra []Tool) (toolSet, error) {
	tools := make([]Tool, 0, len(base.tools)+len(extra))
	tools = append(tools, base.tools...)
	tools = append(tools, extra...)
	return newToolSet(tools)
}

func mcpToolTitle(serverName, toolName, title string) string {
	if title != "" {
		return fmt.Sprintf("%s / %s (%s)", serverName, toolName, title)
	}
	return serverName + " / " + toolName
}

func configuredMCPTool(configuration requestConfiguration, name string) (mcpToolConfiguration, bool) {
	for _, tool := range configuration.MCPTools {
		if tool.Name == name {
			return tool, true
		}
	}
	return mcpToolConfiguration{}, false
}

func (a *Agent) configuredToolTitle(
	configuration requestConfiguration,
	name string,
	arguments json.RawMessage,
) string {
	if tool, ok := configuredMCPTool(configuration, name); ok {
		return mcpToolTitle(tool.ServerName, tool.ToolName, tool.Title)
	}
	return a.toolTitle(name, arguments)
}

func validateRecoveredMCP(
	required requestConfiguration,
	available requestConfiguration,
	calls []openrouter.ToolCall,
) error {
	availableByName := make(map[string]mcpToolConfiguration, len(available.MCPTools))
	for _, tool := range available.MCPTools {
		availableByName[tool.Name] = tool
	}
	for _, call := range calls {
		expected, ok := configuredMCPTool(required, call.Function.Name)
		if !ok {
			continue
		}
		current, ok := availableByName[expected.Name]
		if !ok {
			return fmt.Errorf("session recovery requires MCP tool %q and fresh server credentials", expected.Name)
		}
		if current.Identity != expected.Identity || current.ServerName != expected.ServerName || current.ToolName != expected.ToolName {
			return fmt.Errorf("session recovery MCP tool %q changed since the interrupted turn", expected.Name)
		}
	}
	return nil
}

func toolResultContent(err error) (string, bool) {
	var result failedToolResultError
	if errors.As(err, &result) {
		return result.content, true
	}
	return "", false
}
