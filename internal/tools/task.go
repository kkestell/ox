package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kkestell/ox/internal/agent"
)

const taskDescription = "Delegate a complete, standalone subtask to a subagent with the same " +
	"tools except task. The subagent cannot see this conversation and only its final answer " +
	"comes back, so prompt must contain all necessary context. Several task calls in one " +
	"message run concurrently; do not duplicate their work, and partition file work so no " +
	"two subagents touch the same file. Description is a short label shown to the user."

const taskSchema = `{
	"type": "object",
	"properties": {
		"description": {
			"type": "string",
			"description": "A short label for the delegated work."
		},
		"prompt": {
			"type": "string",
			"description": "The complete standalone task for the subagent."
		}
	},
	"required": ["description", "prompt"],
	"additionalProperties": false
}`

type taskArguments struct {
	Description *string `json:"description"`
	Prompt      *string `json:"prompt"`
}

func taskLabel(arguments json.RawMessage) string {
	var input taskArguments
	if decodeArgs(arguments, &input) != nil || input.Description == nil {
		return ""
	}
	return *input.Description
}

func executeTask(ctx context.Context, invocation agent.Invocation) (string, error) {
	var arguments taskArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	if _, err := requireString("description", arguments.Description); err != nil {
		return "", err
	}
	prompt, err := requireString("prompt", arguments.Prompt)
	if err != nil {
		return "", err
	}
	if invocation.Delegate == nil {
		return "", errors.New("delegation is unavailable")
	}
	return invocation.Delegate(ctx, prompt)
}
