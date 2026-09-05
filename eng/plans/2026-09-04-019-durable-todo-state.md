# Durable Todo State

## Sources

- `docs/spec.md#todo-and-questions` and `docs/spec.md#context-continuity` —
  replacement semantics, validation, ACP projection, durability, and model
  context requirements
- `eng/roadmap.md#todo-state` — slice scope and completion gates
- `eng/architecture.md#context-and-durable-projections` — authoritative log,
  checkpoint, replay, and transient-context ownership
- `~/src/references/repos/personal/eta/internal/agent/tools/{todo,todo_test}.go`
  — focused tool schema, validation, and readable result shape to port
- `internal/agent/{state,loop,adapter,tool}.go` and `internal/tools/tools.go` —
  durable commit, provider projection, ACP event, and tool-registration seams
- `~/src/references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json#/$defs/Plan`
  — pinned ACP plan update and entry wire shapes

## Goal

Add a parent-owned todo tool whose validated full-list replacements are durable
before they become visible. Project the current list to both the ACP client and
the parent's subsequent model requests without turning it into executable queue
state.

## Implementation

- `internal/acp` — add the pinned `plan` session-update type and priority/status
  enums, with exact wire-shape tests for populated and empty replacement lists.
- `internal/tools` — add `todo` to the built-in catalog. Port Eta's full-list
  schema and validation, add optional `priority` with the `medium` default, and
  return a concise representation of the accepted list. Mark the tool as
  serialized, approval-free, available in both code and plan modes, and
  parent-only.
- `internal/agent/tool.go` and tool-set construction — represent parent-only and
  plan-mode availability explicitly. Exclude `todo` from child declarations, and
  make the dispatch checks use the same frozen declarations so a hidden child
  call cannot mutate the parent's list.
- `internal/agent/state.go` — add a validated todo-replacement record and
  current ordered list to durable state. Bind replacements to an open, started
  top-level `todo` call; reject invalid content, priorities, statuses, and
  multiple in-progress entries without changing live state. Include the list in
  cloning and checkpoint projection, bump the checkpoint version, and
  reconstruct it identically on restart.
- `internal/agent/loop.go` and `internal/agent/adapter.go` — give the top-level
  todo invocation a replacement callback that commits the complete list before
  emitting exactly one ACP `plan` update, including an empty list when clearing.
  Add the current list as explicit bounded transient context to every subsequent
  parent provider request. Keep that projection outside transcript replay and
  compaction summaries, while adjusting admission/compaction bookkeeping so the
  current list is always retained.
- `internal/agent/state.go` replay — reproduce each durably recorded plan update
  in record order on `session/load`; normal tool history continues to carry the
  accepted tool result, while checkpoints preserve the current list used for
  future requests.

## Tests

- Tool tests cover missing and blank content, priority defaulting and invalid
  values, invalid status, multiple in-progress entries, ordered output, and
  clearing.
- State tests prove invalid replacement atomicity, record-to-call validation,
  cloning, checkpoint equivalence, restart recovery, and replay of populated and
  empty ACP updates.
- Agent and integration tests prove persistence precedes notification, a commit
  failure emits no plan update or successful tool result, every accepted
  replacement emits one matching update, and later parent requests see the
  current list after replacement, clearing, checkpoint recovery, and compaction.
- Integration and real-binary tests prove `todo` is usable in code and plan
  modes, absent from child declarations, rejected if a child fabricates the
  call, and unable to change the parent's durable list.

## Decisions

- Reuse ACP plan entries as the durable todo value: the product vocabulary and
  wire vocabulary are identical, and the agent remains the sole owner of
  persistence and projection.
- Treat todo context as parent-only transient state. It is rebuilt from the
  durable list for each request rather than appended to conversation history, so
  stale lists cannot survive replacement or compaction.
