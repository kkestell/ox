package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kkestell/ox/internal/agent"
)

const taskAddDescription = "Add one complete standalone subtask to the durable queue. Adding does not run it; use task_run with the returned ID."
const taskListDescription = "Inspect the durable delegated-task queue, including every attempt and terminal result."
const taskRunDescription = "Run the pending attempt for one queued task. Only one queued child runs at a time, and pending tasks never run in the background."
const taskCancelDescription = "Cancel a pending queued task. A running child is cancelled by cancelling the session turn."
const taskRetryDescription = "Append a pending attempt for a failed, cancelled, or interrupted task. Use only when the user explicitly requested a retry."

const taskAddSchema = `{
	"type": "object",
	"properties": {
		"description": {
			"type": "string",
			"description": "The complete standalone prompt for the child agent."
		}
	},
	"required": ["description"],
	"additionalProperties": false
}`

const taskIDSchema = `{
	"type": "object",
	"properties": {
		"task_id": {"type": "string"}
	},
	"required": ["task_id"],
	"additionalProperties": false
}`

const emptyObjectSchema = `{
	"type": "object",
	"properties": {},
	"additionalProperties": false
}`

type taskAddArguments struct {
	Description *string `json:"description"`
}

type taskIDArguments struct {
	TaskID *string `json:"task_id"`
}

func taskRunLabel(arguments json.RawMessage) string {
	var input taskIDArguments
	if decodeArgs(arguments, &input) != nil || input.TaskID == nil {
		return ""
	}
	return *input.TaskID
}

func executeTaskAdd(_ context.Context, invocation agent.Invocation) (string, error) {
	var arguments taskAddArguments
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	description, err := requireNonemptyString("description", arguments.Description)
	if err != nil {
		return "", err
	}
	if invocation.AddTask == nil {
		return "", errors.New("task queue is unavailable")
	}
	task, err := invocation.AddTask(description)
	if err != nil {
		return "", err
	}
	return renderTask(task)
}

func executeTaskList(_ context.Context, invocation agent.Invocation) (string, error) {
	var arguments struct{}
	if err := decodeArgs(invocation.Arguments, &arguments); err != nil {
		return "", err
	}
	if invocation.ListTasks == nil {
		return "", errors.New("task queue is unavailable")
	}
	tasks := invocation.ListTasks()
	data, err := json.Marshal(struct {
		Tasks []agent.QueuedTask `json:"tasks"`
	}{Tasks: tasks})
	if err != nil {
		return "", fmt.Errorf("render task queue: %w", err)
	}
	return string(data), nil
}

func executeTaskRun(ctx context.Context, invocation agent.Invocation) (string, error) {
	taskID, err := decodeTaskID(invocation.Arguments)
	if err != nil {
		return "", err
	}
	if invocation.RunTask == nil {
		return "", errors.New("task queue is unavailable")
	}
	return invocation.RunTask(ctx, taskID)
}

func executeTaskCancel(_ context.Context, invocation agent.Invocation) (string, error) {
	taskID, err := decodeTaskID(invocation.Arguments)
	if err != nil {
		return "", err
	}
	if invocation.CancelTask == nil {
		return "", errors.New("task queue is unavailable")
	}
	task, err := invocation.CancelTask(taskID)
	if err != nil {
		return "", err
	}
	return renderTask(task)
}

func executeTaskRetry(_ context.Context, invocation agent.Invocation) (string, error) {
	taskID, err := decodeTaskID(invocation.Arguments)
	if err != nil {
		return "", err
	}
	if invocation.RetryTask == nil {
		return "", errors.New("task queue is unavailable")
	}
	task, err := invocation.RetryTask(taskID)
	if err != nil {
		return "", err
	}
	return renderTask(task)
}

func decodeTaskID(arguments json.RawMessage) (string, error) {
	var input taskIDArguments
	if err := decodeArgs(arguments, &input); err != nil {
		return "", err
	}
	return requireNonemptyString("task_id", input.TaskID)
}

func requireNonemptyString(name string, value *string) (string, error) {
	result, err := requireString(name, value)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(result) == "" {
		return "", errors.New("`" + name + "` must not be blank")
	}
	return result, nil
}

func renderTask(task agent.QueuedTask) (string, error) {
	data, err := json.Marshal(task)
	if err != nil {
		return "", fmt.Errorf("render queued task: %w", err)
	}
	return string(data), nil
}
