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
			Name:         "question",
			Description:  questionDescription,
			InputSchema:  json.RawMessage(questionSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalNone,
			ParallelSafe: false,
			PlanMode:     true,
			RequiresForm: true,
			Execute:      executeQuestion,
		},
		{
			Name:         "skill",
			Description:  skillDescription,
			InputSchema:  json.RawMessage(skillSchema),
			Kind:         acp.ToolKindRead,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Execute:      executeSkill,
		},
		{
			Name:         "todo",
			Description:  todoDescription,
			InputSchema:  json.RawMessage(todoSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalNone,
			ParallelSafe: false,
			ParentOnly:   true,
			PlanMode:     true,
			Execute:      executeTodo,
		},
		{
			Name: "task_add", Description: taskAddDescription,
			InputSchema: json.RawMessage(taskAddSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParentOnly: true, Execute: executeTaskAdd,
		},
		{
			Name: "task_list", Description: taskListDescription,
			InputSchema: json.RawMessage(emptyObjectSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParentOnly: true, Execute: executeTaskList,
		},
		{
			Name: "task_run", Description: taskRunDescription,
			InputSchema: json.RawMessage(taskIDSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, Delegates: true, ParentOnly: true,
			Label: taskRunLabel, Execute: executeTaskRun,
		},
		{
			Name: "task_cancel", Description: taskCancelDescription,
			InputSchema: json.RawMessage(taskIDSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParentOnly: true, Execute: executeTaskCancel,
		},
		{
			Name: "task_retry", Description: taskRetryDescription,
			InputSchema: json.RawMessage(taskIDSchema), Kind: acp.ToolKindOther,
			Approval: agent.ApprovalNone, ParentOnly: true, Execute: executeTaskRetry,
		},
		{
			Name:         "read_file",
			Description:  readDescription,
			InputSchema:  json.RawMessage(readSchema),
			Kind:         acp.ToolKindRead,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Execute:      executeRead,
		},
		{
			Name:         "glob",
			Description:  globDescription,
			InputSchema:  json.RawMessage(globSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Execute:      executeGlob,
		},
		{
			Name:         "grep",
			Description:  grepDescription,
			InputSchema:  json.RawMessage(grepSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Execute:      executeGrep,
		},
		{
			Name:         "web_fetch",
			Description:  webFetchDescription,
			InputSchema:  json.RawMessage(webFetchSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: true,
			PlanMode:     true,
			Title:        webFetchTitle,
			Execute:      executeWebFetch,
		},
		{
			Name:         "memory_search",
			Description:  memorySearchDescription,
			InputSchema:  json.RawMessage(memorySearchSchema),
			Kind:         acp.ToolKindSearch,
			Approval:     agent.ApprovalNone,
			ParallelSafe: true,
			PlanMode:     true,
			Execute:      executeMemorySearch,
		},
		{
			Name:         "memory_write",
			Description:  memoryWriteDescription,
			InputSchema:  json.RawMessage(memoryWriteSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Title:        memoryWriteTitle,
			Execute:      executeMemoryWrite,
		},
		{
			Name:         "memory_delete",
			Description:  memoryDeleteDescription,
			InputSchema:  json.RawMessage(memoryDeleteSchema),
			Kind:         acp.ToolKindOther,
			Approval:     agent.ApprovalAsk,
			ParallelSafe: false,
			Title:        memoryDeleteTitle,
			Execute:      executeMemoryDelete,
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
