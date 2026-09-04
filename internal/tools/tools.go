package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/agent"
)

func All() []agent.Tool {
	return []agent.Tool{
		{
			Name:         "task",
			Description:  taskDescription,
			InputSchema:  json.RawMessage(taskSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			Delegates:    true,
			Label:        taskLabel,
			Execute:      executeTask,
		},
		{
			Name:         "read_file",
			Description:  readDescription,
			InputSchema:  json.RawMessage(readSchema),
			Kind:         acp.ToolKindRead,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			Execute:      executeRead,
		},
		{
			Name:         "glob",
			Description:  globDescription,
			InputSchema:  json.RawMessage(globSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			Execute:      executeGlob,
		},
		{
			Name:         "grep",
			Description:  grepDescription,
			InputSchema:  json.RawMessage(grepSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			Execute:      executeGrep,
		},
		{
			Name:         "write_file",
			Description:  writeDescription,
			InputSchema:  json.RawMessage(writeSchema),
			Kind:         acp.ToolKindEdit,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Execute:      executeWrite,
		},
		{
			Name:         "edit_file",
			Description:  editDescription,
			InputSchema:  json.RawMessage(editSchema),
			Kind:         acp.ToolKindEdit,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Execute:      executeEdit,
		},
		{
			Name:         "shell",
			Description:  shellDescription,
			InputSchema:  json.RawMessage(shellSchema),
			Kind:         acp.ToolKindExecute,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Suggest:      shellSuggestion,
			Covered:      shellCovered,
			Execute:      executeShell,
		},
	}
}

func decodeArgs(arguments json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New(trimJSONError(err.Error()))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("tool arguments must be a single JSON object")
	}
	return nil
}

func trimJSONError(message string) string {
	const prefix = "json: "
	if len(message) >= len(prefix) && message[:len(prefix)] == prefix {
		return message[len(prefix):]
	}
	return message
}

func requireString(name string, value *string) (string, error) {
	if value == nil {
		return "", errors.New("`" + name + "` must be a string")
	}
	return *value, nil
}
