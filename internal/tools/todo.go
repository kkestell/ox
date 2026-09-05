package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/agent"
)

const todoDescription = "Replace the complete ordered todo list. Pass every item on each call; partial updates are not supported. At most one item may be in_progress. Priority defaults to medium. Use an empty list to clear completed work."

const todoSchema = `{
  "type": "object",
  "properties": {
    "todos": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "content": {"type": "string"},
          "priority": {"type": "string", "enum": ["high", "medium", "low"]},
          "status": {"type": "string", "enum": ["pending", "in_progress", "completed"]}
        },
        "required": ["content", "status"],
        "additionalProperties": false
      }
    }
  },
  "required": ["todos"],
  "additionalProperties": false
}`

type todoArgs struct {
	Todos []todoItem `json:"todos"`
}

type todoItem struct {
	Content  string                `json:"content"`
	Priority acp.PlanEntryPriority `json:"priority,omitempty"`
	Status   acp.PlanEntryStatus   `json:"status"`
}

func executeTodo(_ context.Context, invocation agent.Invocation) (string, error) {
	var args todoArgs
	if err := decodeArgs(invocation.Arguments, &args); err != nil {
		return "", err
	}
	if args.Todos == nil {
		return "", errors.New("`todos` must be an array")
	}
	entries := make([]acp.PlanEntry, len(args.Todos))
	inProgress := 0
	for index, item := range args.Todos {
		priority := item.Priority
		if priority == "" {
			priority = acp.PlanEntryPriorityMedium
		}
		entry := acp.PlanEntry{
			Content: item.Content, Priority: priority, Status: item.Status,
		}
		if err := entry.Validate(); err != nil {
			return "", fmt.Errorf("todos item %d: %w", index+1, err)
		}
		if entry.Status == acp.PlanEntryStatusInProgress {
			inProgress++
		}
		entries[index] = entry
	}
	if inProgress > 1 {
		return "", fmt.Errorf("todos: at most one item may be in_progress (found %d)", inProgress)
	}
	if invocation.ReplaceTodo == nil {
		return "", errors.New("todo replacement is unavailable")
	}
	if err := invocation.ReplaceTodo(entries); err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "Todo list cleared", nil
	}
	var result strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&result, "[%s, %s] %s\n", entry.Status, entry.Priority, entry.Content)
	}
	return result.String(), nil
}
