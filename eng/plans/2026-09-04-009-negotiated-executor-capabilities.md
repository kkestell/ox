# Freeze Negotiated Executor Capabilities

## Sources

- `eng/roadmap.md#negotiated-capabilities-in-the-request-configuration` — owns
  capability recording, activation-change, replay, executor-identity, and
  turn-freezing gates.
- `eng/architecture.md#session-and-turn-state` and
  `eng/architecture.md#workspace-boundary` — define activation-frozen request
  configuration and capability-selected local or client execution.
- `internal/agent/agent.go` and `internal/agent/state.go` — current connection
  capability state, activation configuration changes, prompt executor
  construction, durable configuration, checkpoints, and replay.
- `internal/e2e/executor_conformance_test.go` and
  `internal/e2e/browser/lifecycle.spec.ts` — existing local/delegated durability
  comparison and the pinned browser's local-executor acceptance case.
- `~/src/references/repos/third-party/protocol/agent-client-protocol@8e3eb8f2:agent-client-protocol-schema/src/v1/client.rs`
  — authoritative filesystem sub-capabilities and all-or-nothing terminal
  capability negotiated at initialization. No personal reference persists this
  ACP executor-selection seam; extend Ox's existing request-configuration
  records instead of porting a separate session format.

## Goal

Record the filesystem and terminal capabilities that select a session
activation's executors, and use that durable snapshot for every turn in the
activation.

## Implementation

- `internal/agent/state.go` — add an explicit executor-capability projection to
  `requestConfiguration`, limited to filesystem read, filesystem write, and
  terminal. Include it in cloning, equality, checkpoint projection, and
  validation so each `session_created` or `request_configuration_changed` record
  identifies the executor available to later turns until the next configuration
  record. Keep configuration records out of ACP replay.
- `internal/agent/agent.go` — snapshot the negotiated connection capabilities
  while resolving configuration for `session/new`, `session/load`, and
  `session/resume`. Build prompt filesystem and terminal callbacks from the
  active session's frozen snapshot rather than rereading mutable agent-wide
  capability fields. A capability change on reactivation must flow through the
  existing single configuration-change commit before the session becomes active.
- `internal/e2e/executor_conformance_test.go` — assert that local and delegated
  logs record different executor-capability snapshots, then compare the
  remaining durable behavior and complete replay exactly as before. Remove the
  temporary expectation that capability selection is absent from durable
  records.
- `eng/roadmap.md` — remove the completed negotiated-capability slice while
  leaving pending permission recovery as the remaining durable-session work.

## Tests

- `internal/agent/state_test.go` and `internal/agent/agent_test.go` — prove
  capability snapshots survive fold, clone, configuration changes, and
  checkpoints; affect configuration equality; and select each filesystem method
  and terminal independently from the frozen session value.
- `internal/e2e` — create a session under local capabilities, close and restart
  Ox with delegated capabilities, load the session, and assert exactly one
  configuration change precedes the next turn. Prove that turn delegates its
  operations, a same-capability reactivation adds no second change, and replay
  before and after the capability change is identical.
- Keep the pinned browser's existing read/edit/shell workflow as the acceptance
  case: its advertised false filesystem flags and absent terminal capability
  must be recorded as the local executor and must still issue no delegated
  callback.

## Decisions

- Persist only capabilities that choose tool executors. Authentication and
  future ACP client capabilities remain connection state until their behavior
  needs an activation-frozen record.
