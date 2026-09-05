package agent

import (
	"errors"
	"fmt"
)

func (a *Agent) addQueuedTask(value *session, turnID, callID, description string) (QueuedTask, error) {
	id, err := randomID()
	if err != nil {
		return QueuedTask{}, err
	}
	task := delegatedTask{
		ID: id, Description: description,
		Attempts: []delegatedTaskAttempt{{Number: 1, State: taskPending}},
	}
	if err := a.commitTaskChange(value, turnID, callID, "add", task); err != nil {
		return QueuedTask{}, fmt.Errorf("persist queued task: %w", err)
	}
	return queuedTaskView(task), nil
}

func (a *Agent) queuedTasks(value *session) []QueuedTask {
	value.stateMu.Lock()
	defer value.stateMu.Unlock()
	result := make([]QueuedTask, len(value.state.tasks))
	for index := range value.state.tasks {
		result[index] = queuedTaskView(value.state.tasks[index])
	}
	return result
}

func (a *Agent) cancelQueuedTask(value *session, turnID, callID, id string) (QueuedTask, error) {
	task, err := currentQueuedTask(value, id)
	if err != nil {
		return QueuedTask{}, err
	}
	attempt := &task.Attempts[len(task.Attempts)-1]
	if attempt.State != taskPending {
		return QueuedTask{}, fmt.Errorf("task %q is %s; only pending tasks may be cancelled", id, attempt.State)
	}
	attempt.State = taskCancelled
	attempt.Result = "cancelled before dispatch"
	if err := a.commitTaskChange(value, turnID, callID, "cancel", task); err != nil {
		return QueuedTask{}, fmt.Errorf("persist task cancellation: %w", err)
	}
	return queuedTaskView(task), nil
}

func (a *Agent) retryQueuedTask(value *session, turnID, callID, id string) (QueuedTask, error) {
	task, err := currentQueuedTask(value, id)
	if err != nil {
		return QueuedTask{}, err
	}
	state := lastAttempt(task).State
	if state != taskFailed && state != taskCancelled && state != taskInterrupted {
		return QueuedTask{}, fmt.Errorf("task %q is %s; only failed, cancelled, or interrupted tasks may be retried", id, state)
	}
	task.Attempts = append(task.Attempts, delegatedTaskAttempt{
		Number: len(task.Attempts) + 1, State: taskPending,
	})
	if err := a.commitTaskChange(value, turnID, callID, "retry", task); err != nil {
		return QueuedTask{}, fmt.Errorf("persist task retry: %w", err)
	}
	return queuedTaskView(task), nil
}

func (a *Agent) beginQueuedTask(value *session, turnID, callID, id string) (delegatedTask, error) {
	task, err := currentQueuedTask(value, id)
	if err != nil {
		return delegatedTask{}, err
	}
	value.stateMu.Lock()
	for _, queued := range value.state.tasks {
		if queued.ID != id && lastAttempt(queued).State == taskRunning {
			value.stateMu.Unlock()
			return delegatedTask{}, errors.New("another queued task is already running")
		}
	}
	value.stateMu.Unlock()
	attempt := &task.Attempts[len(task.Attempts)-1]
	if attempt.State != taskPending {
		return delegatedTask{}, fmt.Errorf("task %q is %s; only pending tasks may run", id, attempt.State)
	}
	attempt.State = taskRunning
	attempt.TurnID = turnID
	attempt.CallID = callID
	if err := a.commitTaskChange(value, turnID, callID, "run", task); err != nil {
		return delegatedTask{}, fmt.Errorf("persist task dispatch: %w", err)
	}
	return task, nil
}

func (a *Agent) finishQueuedTask(
	value *session,
	turnID, callID, id, state, result string,
) error {
	task, err := currentQueuedTask(value, id)
	if err != nil {
		return err
	}
	attempt := &task.Attempts[len(task.Attempts)-1]
	if attempt.State != taskRunning || attempt.TurnID != turnID || attempt.CallID != callID {
		return fmt.Errorf("task %q has no matching running attempt", id)
	}
	attempt.State = state
	attempt.Result = result
	if err := a.commitTaskChange(value, turnID, callID, state, task); err != nil {
		return fmt.Errorf("persist task %s outcome: %w", state, err)
	}
	return nil
}

func (a *Agent) commitTaskChange(
	value *session,
	turnID, callID, operation string,
	task delegatedTask,
) error {
	value.stateMu.Lock()
	defer value.stateMu.Unlock()
	return a.commitLocked(value, recordTaskChanged, taskChanged{
		TurnID: turnID, CallID: callID, Operation: operation, Task: task,
	})
}

func currentQueuedTask(value *session, id string) (delegatedTask, error) {
	value.stateMu.Lock()
	defer value.stateMu.Unlock()
	index := delegatedTaskIndex(value.state.tasks, id)
	if index < 0 {
		return delegatedTask{}, fmt.Errorf("task %q does not exist", id)
	}
	return cloneDelegatedTask(value.state.tasks[index]), nil
}

func queuedTaskView(task delegatedTask) QueuedTask {
	attempts := make([]QueuedTaskAttempt, len(task.Attempts))
	for index, attempt := range task.Attempts {
		attempts[index] = QueuedTaskAttempt{
			Number: attempt.Number, State: attempt.State, Result: attempt.Result,
		}
	}
	return QueuedTask{
		ID: task.ID, Description: task.Description,
		State: lastAttempt(task).State, Attempts: attempts,
	}
}

func (a *Agent) interruptRunningTasks(value *session) error {
	value.stateMu.Lock()
	tasks := cloneDelegatedTasks(value.state.tasks)
	value.stateMu.Unlock()
	for _, task := range tasks {
		attempt := lastAttempt(task)
		if attempt.State != taskRunning {
			continue
		}
		if err := a.finishQueuedTask(
			value,
			attempt.TurnID,
			attempt.CallID,
			task.ID,
			taskInterrupted,
			"task interrupted; earlier effects may remain visible",
		); err != nil {
			return err
		}
	}
	return nil
}
