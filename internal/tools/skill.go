package tools

import (
	"context"
	"errors"

	"github.com/kkestell/ox/internal/agent"
)

const skillDescription = "Load the validated Markdown instructions for one workspace skill by its exact catalog name. Referenced files must be read separately with file tools."

const skillSchema = `{
  "type": "object",
  "properties": {
    "name": {"type": "string", "description": "Exact skill name from the workspace skill catalog."}
  },
  "required": ["name"],
  "additionalProperties": false
}`

type skillArguments struct {
	Name *string `json:"name"`
}

func executeSkill(_ context.Context, invocation agent.Invocation) (string, error) {
	var arguments skillArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	name, err := requireString("name", arguments.Name)
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("`name` must not be empty")
	}
	if invocation.LoadSkill == nil {
		return "", errors.New("workspace skill loading is unavailable")
	}
	return invocation.LoadSkill(name)
}
