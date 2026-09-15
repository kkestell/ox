package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/agent"
)

const subagentStartDescription = "Start a named subagent in the background on a complete standalone task. The child runs concurrently with you and other children, cannot start children, and sees only its task plus messages you send later."
const subagentSendDescription = "Send a follow-up message to a running subagent. The message is added before its next model request; a child already finishing may reject it."
const subagentStopDescription = "Request cancellation of a running subagent. The result reports stopping until the child has actually exited."
const subagentListDescription = "Inspect every subagent in this turn, including its reports and terminal result."
const subagentWaitDescription = "Wait without polling until any selected subagent reports a message or reaches a terminal state, then return current state and consume those reports."
const subagentReportDescription = "Send an interim result or coordination message to the primary agent. Your final answer is delivered automatically."

const subagentStartSchema = `{
	"type": "object",
	"properties": {
		"name": {"type": "string", "description": "A short unique label for this child."},
		"task": {"type": "string", "description": "The complete standalone task."}
	},
	"required": ["name", "task"],
	"additionalProperties": false
}`

const subagentMessageSchema = `{
	"type": "object",
	"properties": {
		"id": {"type": "string", "description": "The subagent ID returned by subagent_start."},
		"message": {"type": "string"}
	},
	"required": ["id", "message"],
	"additionalProperties": false
}`

const subagentIDSchema = `{
	"type": "object",
	"properties": {
		"id": {"type": "string", "description": "The subagent ID returned by subagent_start."}
	},
	"required": ["id"],
	"additionalProperties": false
}`

const subagentListSchema = `{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`

const subagentWaitSchema = `{
	"type": "object",
	"properties": {
		"ids": {
			"type": "array",
			"items": {"type": "string"},
			"minItems": 1,
			"uniqueItems": true,
			"description": "Subagent IDs to wait for. Omit to wait for any child."
		}
	},
	"additionalProperties": false
}`

const subagentReportSchema = `{
	"type": "object",
	"properties": {
		"message": {"type": "string", "description": "A concise useful update for the primary agent."}
	},
	"required": ["message"],
	"additionalProperties": false
}`

type subagentStartArguments struct {
	Name *string `json:"name"`
	Task *string `json:"task"`
}

type subagentMessageArguments struct {
	ID      *string `json:"id"`
	Message *string `json:"message"`
}

type subagentIDArguments struct {
	ID *string `json:"id"`
}

type subagentWaitArguments struct {
	IDs []string `json:"ids"`
}

type subagentReportArguments struct {
	Message *string `json:"message"`
}

func subagentTools() []agent.Tool {
	return []agent.Tool{
		{
			Name: "subagent_start", Description: subagentStartDescription,
			InputSchema: json.RawMessage(subagentStartSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParallelSafe: false, PlanMode: true,
			Scope: agent.ToolScopePrimary, Presentation: subagentStartPresentation, Execute: executeSubagentStart,
		},
		{
			Name: "subagent_send", Description: subagentSendDescription,
			InputSchema: json.RawMessage(subagentMessageSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParallelSafe: false, PlanMode: true,
			Scope: agent.ToolScopePrimary, Presentation: subagentSendPresentation, Execute: executeSubagentSend,
		},
		{
			Name: "subagent_stop", Description: subagentStopDescription,
			InputSchema: json.RawMessage(subagentIDSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParallelSafe: false, PlanMode: true,
			Scope: agent.ToolScopePrimary, Presentation: subagentStopPresentation, Execute: executeSubagentStop,
		},
		{
			Name: "subagent_list", Description: subagentListDescription,
			InputSchema: json.RawMessage(subagentListSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParallelSafe: true, PlanMode: true,
			Scope: agent.ToolScopePrimary, Presentation: subagentListPresentation, Execute: executeSubagentList,
		},
		{
			Name: "subagent_wait", Description: subagentWaitDescription,
			InputSchema: json.RawMessage(subagentWaitSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParallelSafe: true, PlanMode: true,
			Scope: agent.ToolScopePrimary, Presentation: subagentWaitPresentation, Execute: executeSubagentWait,
		},
		{
			Name: "subagent_report", Description: subagentReportDescription,
			InputSchema: json.RawMessage(subagentReportSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParallelSafe: true, PlanMode: true,
			Scope: agent.ToolScopeSubagent, Presentation: subagentReportPresentation, Execute: executeSubagentReport,
		},
	}
}

func executeSubagentStart(_ context.Context, invocation agent.Invocation) (string, error) {
	var input subagentStartArguments
	if err := decodeArgs(invocation.Arguments, &input); err != nil {
		return "", err
	}
	name, err := requiredTrimmed("name", input.Name)
	if err != nil {
		return "", err
	}
	task, err := requiredTrimmed("task", input.Task)
	if err != nil {
		return "", err
	}
	if invocation.StartSubagent == nil {
		return "", errors.New("subagent coordination is unavailable")
	}
	child, err := invocation.StartSubagent(name, task)
	if err != nil {
		return "", err
	}
	return renderSubagents(child)
}

func executeSubagentSend(_ context.Context, invocation agent.Invocation) (string, error) {
	var input subagentMessageArguments
	if err := decodeArgs(invocation.Arguments, &input); err != nil {
		return "", err
	}
	id, err := requiredTrimmed("id", input.ID)
	if err != nil {
		return "", err
	}
	message, err := requiredTrimmed("message", input.Message)
	if err != nil {
		return "", err
	}
	if invocation.SendSubagent == nil {
		return "", errors.New("subagent coordination is unavailable")
	}
	child, err := invocation.SendSubagent(id, message)
	if err != nil {
		return "", err
	}
	return renderSubagents(child)
}

func executeSubagentStop(_ context.Context, invocation agent.Invocation) (string, error) {
	id, err := decodeSubagentID(invocation.Arguments)
	if err != nil {
		return "", err
	}
	if invocation.StopSubagent == nil {
		return "", errors.New("subagent coordination is unavailable")
	}
	child, err := invocation.StopSubagent(id)
	if err != nil {
		return "", err
	}
	return renderSubagents(child)
}

func executeSubagentList(_ context.Context, invocation agent.Invocation) (string, error) {
	var input struct{}
	if err := decodeArgs(invocation.Arguments, &input); err != nil {
		return "", err
	}
	if invocation.ListSubagents == nil {
		return "", errors.New("subagent coordination is unavailable")
	}
	return renderSubagents(invocation.ListSubagents()...)
}

func executeSubagentWait(ctx context.Context, invocation agent.Invocation) (string, error) {
	var input subagentWaitArguments
	if err := decodeArgs(invocation.Arguments, &input); err != nil {
		return "", err
	}
	for index, id := range input.IDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return "", fmt.Errorf("`ids[%d]` must not be blank", index)
		}
		input.IDs[index] = trimmed
	}
	seen := make(map[string]struct{}, len(input.IDs))
	for _, id := range input.IDs {
		if _, exists := seen[id]; exists {
			return "", fmt.Errorf("duplicate subagent id %q", id)
		}
		seen[id] = struct{}{}
	}
	if invocation.WaitSubagents == nil {
		return "", errors.New("subagent coordination is unavailable")
	}
	children, err := invocation.WaitSubagents(ctx, input.IDs)
	if err != nil {
		return "", err
	}
	return renderSubagents(children...)
}

func executeSubagentReport(_ context.Context, invocation agent.Invocation) (string, error) {
	var input subagentReportArguments
	if err := decodeArgs(invocation.Arguments, &input); err != nil {
		return "", err
	}
	message, err := requiredTrimmed("message", input.Message)
	if err != nil {
		return "", err
	}
	if invocation.ReportToParent == nil {
		return "", errors.New("subagent reporting is unavailable")
	}
	if err := invocation.ReportToParent(message); err != nil {
		return "", err
	}
	return "message delivered to the primary agent", nil
}

func decodeSubagentID(arguments json.RawMessage) (string, error) {
	var input subagentIDArguments
	if err := decodeArgs(arguments, &input); err != nil {
		return "", err
	}
	return requiredTrimmed("id", input.ID)
}

func requiredTrimmed(name string, value *string) (string, error) {
	result, err := requireString(name, value)
	if err != nil {
		return "", err
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return "", errors.New("`" + name + "` must not be blank")
	}
	return result, nil
}

func renderSubagents(children ...agent.SubagentSnapshot) (string, error) {
	data, err := json.Marshal(struct {
		Subagents []agent.SubagentSnapshot `json:"subagents"`
	}{Subagents: children})
	if err != nil {
		return "", fmt.Errorf("render subagents: %w", err)
	}
	return string(data), nil
}
