# Durable Delegated Queue

## Sources

- `docs/spec.md#isolation-memory-and-delegated-work` — queue operations, states,
  limits, retry, cancellation, ownership, and shared-policy behavior
- `eng/roadmap.md#durable-delegated-queue` — slice scope and completion gates
- `eng/architecture.md#context-and-durable-projections` and
  `eng/architecture.md#extension-boundaries` — log-owned attempts, effect
  uncertainty, request-scoped execution, and separation from todo state
- `internal/agent/{state,loop,subagent,tool}.go` and
  `internal/tools/{task,tools}.go` — current durable child context, tool
  lifecycle, nested ACP activity, invocation callback, and direct delegation
  seams
- `~/src/references/repos/personal/kappa/taikonaut/{task_queue,task_queue_tools}.py`
  — queue mutation and model-tool shapes to adapt without its sidecar store,
  three-state lifecycle, or implicit scheduler

## Goal

Replace immediate one-shot delegation with an explicit session-owned queue. The
parent can stage and inspect work, run one child at a time, cancel pending work,
and deliberately retry an unsuccessful attempt without hiding earlier effects.

## Implementation

- `internal/tools/task.go` and `internal/tools/tools.go` — replace the current
  direct `task` contract with strict parent-only `task_add`, `task_list`,
  `task_run`, `task_cancel`, and `task_retry` tools. A task description is the
  complete child prompt. Adding creates a pending first attempt; running names a
  pending task; cancelling accepts only pending work; retry accepts only failed,
  cancelled, or interrupted work and appends a pending attempt. Completed tasks
  cannot run or retry. Only `task_run` delegates and emits nested child
  activity. All queue tools are serialized and excluded from plan mode and child
  declarations.
- `internal/agent/state.go` — add validated queue mutation records and queue
  state to the session log and checkpoint projection. Retain at most 32 stable
  random task IDs in insertion order. Store each task's immutable description
  and ordered attempts with an attempt number, owning turn and tool-call IDs,
  state, and terminal result summary. Validate every transition against a
  matching open top-level queue call, preserve prior attempts, and reject
  duplicate IDs, skipped states, and mutation of terminal attempts atomically.
- `internal/agent/loop.go`, `subagent.go`, and `tool.go` — route queue callbacks
  through the existing commit path. Commit `running` before entering the child
  and commit `completed`, `failed`, or `cancelled` before returning the
  `task_run` result. A persistence failure poisons only that session and cannot
  publish a successful queue or child outcome. Keep pending tasks inert after
  the tool call or owning ACP request ends.
- On activation, convert every durably running attempt to `interrupted` before
  accepting more work. Never resume its child context or dispatch it
  automatically. An explicit retry creates a separate attempt, while replay and
  `task_list` keep the earlier attempt and nested effects visible.
- Replace the separate parent and child request counters with one durable
  per-turn request allowance consumed by both provider paths. Pass the frozen
  turn configuration, grants, read evidence, context admission, and cancellation
  through each queued child exactly as for current delegation. Queue-management
  and delegation callbacks remain absent from the child's frozen tool set.
- `internal/agent/prompt.go` — explain that queued tasks run only through
  `task_run`, retries require the user's explicit request, and unfinished
  pending work is never background work. Keep todo guidance separate from queue
  execution.

## Tests

- Tool and state tests cover strict schemas, stable ordering and IDs, the
  32-task cap, every valid state transition, invalid IDs/states without
  mutation, completed-task redispatch rejection, retry attempt history,
  checkpoint equivalence, restart interruption, and replay without execution.
- Integration tests prove two queued tasks run sequentially; parent and child
  share permissions, configuration, context limits, and one request allowance;
  queue tools are unavailable to children and in plan mode; fabricated child
  calls cannot mutate the queue; and todo changes do not schedule tasks.
- Cancellation tests stop the running child, durably cancel its attempt, leave
  other pending tasks paused, finish the owning prompt, and allow an unrelated
  session to complete.
- Fault-injection and real-process recovery tests fail persistence before child
  dispatch and after a child effect but before terminal queue recording. Prove
  no false success or automatic redispatch, restart reports the attempt as
  interrupted, and only an explicit retry starts a new attempt with the earlier
  effect still observable.

## Decisions

- Queue operations are separate tools so each schema describes one valid
  transition and only `task_run` needs delegation semantics.
- Retry appends an attempt under the same stable task ID. This keeps task
  identity useful while making uncertain prior effects impossible to erase or
  mistake for a fresh first execution.
