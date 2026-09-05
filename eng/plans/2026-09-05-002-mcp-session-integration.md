# MCP Session Integration

## Sources

- `docs/spec.md#mcp-tools`, `docs/spec.md#session-lifecycle-and-recovery`, and
  `docs/spec.md#session-configuration` — session ownership, permission,
  recovery, replay, cancellation, and plan-mode behavior
- `eng/roadmap.md#mcp-activation-and-dispatch` — slice scope and completion
  gates
- `eng/architecture.md#session-and-turn-state`,
  `eng/architecture.md#context-and-durable-projections`, and
  `eng/architecture.md#extension-boundaries` — activation resources, immutable
  turns, dispatch evidence, and cleanup boundaries
- `internal/agent/{agent,tool,loop,state,subagent,config_options}.go`,
  `internal/workspace/spill.go`, and `internal/e2e` — current activation,
  dynamic dispatch, durable unknown-outcome, plan-mode, output, and process-test
  seams
- `eng/plans/2026-09-05-001-mcp-transport-and-catalog.md` — required adapter and
  catalog starting point

## Goal

Expose client-supplied MCP tools to parent and child turns under Ox's existing
permission, durability, mode, output, and cancellation rules. Make each MCP
bundle an activation resource and preserve enough nonsecret evidence to recover
safely without persisting credentials.

## Implementation

- `internal/agent/agent.go` — validate typed MCP inputs for new, load, and
  resume, then activate the complete bundle before publishing the session. Merge
  its descriptors with built-ins into per-session parent and child tool sets;
  fail collisions atomically. Advertise only Streamable HTTP through ACP
  `mcpCapabilities` because stdio is baseline and SSE remains unsupported.
- Give `session` ownership of its MCP bundle and dynamic tool sets. Close them
  after active turns and configuration setters finish on `session/close`, on
  failed activation, and when the Ox process exits. Loading with an empty MCP
  list remains valid and can replay recorded results without reconnecting an old
  server.
- `internal/agent/{tool,loop,subagent}.go` — adapt each descriptor to an
  approval-required, non-parallel MCP tool available to parent and child code
  turns. Keep every MCP tool out of plan mode regardless of server annotations.
  Use the exact server/tool identity in ACP titles and details, serialize calls
  with other effectful session work, and invoke the adapter only after the
  existing durable dispatch record is synced.
- Scope allow-always grants with the descriptor identity digest. Immediately
  before `tools/call`, refresh and compare the selected definition; a mismatch
  returns a tool error without dispatch or widening the old grant. Treat
  transport failure as a possibly executed failed call and never retry it.
- `internal/agent/state.go` — add the immutable MCP identity evidence needed by
  active and open-turn configurations, with validation and clone/equality
  support. Never store headers, environment values, or another recoverable
  credential representation. Pending permission recovery requires matching
  server destination and tool definition plus freshly supplied live connections;
  a changed definition/destination or unavailable credential fails activation
  before dispatch, while credential rotation alone remains compatible.
  Already-started calls use the existing unknown-outcome closure and are never
  repeated.
- Generalize the workspace spill helper only as needed to render MCP text/JSON
  with a 64-KiB inline limit and a faithful session-owned spill. Record the
  rendered result before model continuation and report MCP `isError` as a failed
  tool result without discarding the server's bounded text.
- Update session-specific title, kind, replay, configuration filtering, and
  context-admission lookups so dynamic tools use the frozen turn catalog rather
  than the agent's static registry. Server annotations remain display hints only
  and cannot affect permission, parallelism, or plan-mode policy.
- `cmd/ox` — close all activation resources after the ACP server stops.

## Tests

- Agent and process tests use local stdio and HTTP MCP fixtures plus the fake
  model to prove activation, atomic cleanup, model-visible names/schemas,
  permission projection, identity-scoped grants, serialized calls, parent/child
  access, plan-mode exclusion, deadlines, tool errors, inline/spilled output,
  and session/process shutdown.
- Recovery tests cover a pending same-definition call, changed schema,
  destination change, credential rotation, missing credentials, a started call
  with unknown outcome, cancellation during each transport, and an unrelated
  responsive session. Replay recorded MCP results with no server definition and
  assert no discovery or call occurs.
- Inspect session logs, checkpoints, traces, ACP errors, tool metadata, and
  provider requests for header/environment secrets. Schema-based process tests
  cover all accepted/rejected ACP server variants and exact HTTP capability
  advertisement. Run the required ACP client gate after the ordinary repository
  checks.

## Sequence

This is the second of two plans. It requires the transport/catalog adapter from
the first plan and completes the roadmap's MCP activation and dispatch slice.

## Decisions

- Dynamic MCP tools belong to the session activation, not the process-wide
  built-in registry. Persist only their nonsecret identity evidence; live
  connections and secret-bearing inputs are recreated from each activation
  request.
