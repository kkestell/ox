# Session Configuration Options

## Sources

- `docs/spec.md#session-configuration` — option values, persistence, turn
  isolation, mode policy, and failure behavior
- `eng/roadmap.md#session-configuration-options` — slice scope and completion
  gates
- `eng/architecture.md#configuration-and-credentials` and
  `eng/architecture.md#session-and-turn-state` — activation, durable selection,
  and immutable-turn ownership
- `~/src/references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json`
  and `~/src/references/repos/third-party/protocol/acp-go-sdk/schema/meta.json`
  — pinned wire shapes and method name
- `internal/agent/{agent,state,loop,subagent}.go` — activation, persistence,
  request construction, dispatch, replay, and recovery seams
- `internal/settings/{settings,resolve}.go` and `internal/openrouter/catalog.go`
  — file defaults and model capability metadata

## Goal

Expose ACP `mode`, `model`, and `reasoning` select options, persist explicit
selections, and apply them only to subsequent turns. Keep each parent turn,
child request, and recovered permission wait on its original effective
configuration.

## Implementation

- `internal/acp` — add the pinned select-option request, response,
  response-list, category, and `config_option_update` session-update types with
  boundary validation. Extend new, load, and resume responses with
  `configOptions`, and register only `session/set_config_option`; do not add
  legacy `modes`, `session/set_mode`, or model extensions.
- `internal/openrouter` and `internal/settings` — expose a cloned deterministic
  model catalog to the agent and retain the catalog metadata needed to reject a
  selected model that cannot support configured sampling/output fields, the
  effective tool set, or modalities already retained in conversation history.
  Keep reasoning validation catalog-driven rather than hardcoding model IDs.
- `internal/agent/state.go` and `internal/agent/store.go` — persist explicit
  session selections separately from activation defaults and add an ordered
  record for accepted option changes. Include selections and the active turn's
  effective request configuration in checkpoint/recovery state so a setter may
  be recorded during a turn without changing that turn or a recovered approval.
  Fold option records into ACP replay as complete `config_option_update`
  snapshots.
- `internal/agent/agent.go` — retain the activation's freshly resolved file
  inputs and catalog beside durable selections. Build complete options in the
  stable order `mode`, `model`, `reasoning`: `code`/`plan`; catalog models
  sorted by ID with catalog names where available; and `default` plus the
  selected model's supported efforts, omitting reasoning when it is not
  selectable. A configured explicit effort supplies the initial current value.
- `internal/agent/agent.go` — implement a per-session serialized setter that
  builds and validates the entire candidate state before one durable commit.
  Unknown option IDs/values and boolean requests fail atomically. A model change
  clears any reasoning override to `default`; a current-value selection writes
  and emits nothing. After persistence, emit one complete `config_option_update`
  and return the same option list.
- `internal/agent/agent.go` and `internal/agent/loop.go` — return complete
  current options from new, load, and resume. On activation, reread files, apply
  saved explicit selections over those defaults, and fail with the responsible
  option when the combination is incompatible. Snapshot the effective
  configuration when accepting a prompt and use it for all primary requests,
  compaction, dispatch, delegation, and recovery until that turn ends.
- `internal/agent/loop.go` and `internal/agent/subagent.go` — derive the code
  and plan tool declarations from the frozen turn configuration. Plan mode
  currently admits only nondelegating read/search tools. Check membership again
  at parent and child dispatch so hidden effectful calls and reusable grants
  cannot bypass the mode.

## Tests

- ACP wire tests match the pinned schema for select options, setter requests,
  complete responses, and `config_option_update`, including missing fields,
  boolean payloads, and unknown values.
- Agent/state tests cover initial configured reasoning, reasoning omission,
  deterministic model ordering, atomic invalid updates, model-triggered reset,
  same-value no-op, concurrent setter order, commit/notification failure,
  replay, checkpoint, close/load/resume persistence, file-default rereads, and
  incompatible saved selections.
- Integration tests hold a primary request and a permission wait while changing
  options, then prove the current parent, child, tool policy, and recovery keep
  their frozen configuration while the next turn uses the new state.
- Real-binary tests assert complete options on new/load/resume and setter
  responses, ordered replay updates, model compatibility failures for explicit
  temperature/max-token/tool/modality requirements, and dispatch rejection of
  effectful or delegated calls in plan mode despite an existing grant.

## Decisions

- Treat `default` as an explicit durable reasoning override only when the client
  selects it; otherwise a reactivated session may inherit a changed file-based
  effort. Mode defaults to `code`, while model and reasoning overrides remain
  optional so file defaults continue to be reread as specified.
- Use catalog model IDs as protocol values and stable display-name fallbacks;
  never persist the catalog itself in the session log.
