# Shared turn configuration

## Goal

`turnConfiguration` deep-clones the turn's frozen configuration on every read:
the tool declarations, the tool-kind and plan-tool maps, the MCP evidence, the
skill references, and the resolved settings. Record validation reads it once per
tool call in a loop to check a single tool's kind, and tool dispatch reads it
once per call to reach the skill catalog.

Nothing mutates what that read returns. The clone exists so a caller cannot
disturb durable state, but durable state is copy-on-write: a commit builds a
successor and replaces the struct rather than writing through the published one.

## Desired outcome

The turn's configuration is materialized once, when the turn freezes it, and
read without copying afterwards. A caller that needs one field looks that field
up rather than taking a whole configuration.

## Summary of approach

Return the frozen configuration as it is and name the invariant that makes
sharing safe. Give the two places that want a single field a direct lookup, so a
per-call loop reads a map entry instead of a configuration.

## Related code

- `internal/agent/state.go` - `durableState.turnConfiguration` and the record
  validation that reads a tool's kind.
- `internal/agent/agent.go` - `session.turnConfiguration`.
- `internal/agent/loop.go` and `internal/agent/compact.go` - the readers.
- `internal/agent/config_options.go` - `applySelections`, which clones the
  activation base before changing it and replaces rather than mutates.

## Current state

- Relevant existing behavior: `clone` already stopped copying append-only
  history for the same reason, and `commitLocked` still clones the configuration
  into each successor state.
- Existing patterns to follow: `modelRequest` clones the tool declarations it
  puts in a provider request, so the request owns its own copy.
- Constraints from the current implementation: every reader must keep treating
  the configuration as read-only.

## Test plan

- **Key behaviors to verify:** a configuration change still leaves a running
  turn's configuration alone, and a reader cannot reach durable state through
  what it was handed.
- **Test levels:** unit, in `internal/agent`; the existing overlap test already
  covers the frozen-turn guarantee.
- **Edge cases and failure modes:** plan mode, which replaces the tool list and
  the tool-kind map.
- **What not to test:** the clone helpers, which durable commits still use.

## Implementation plan

- Stop cloning in `turnConfiguration` and state the invariant.
- Add a tool-kind lookup and use it in record validation.
- Add a skills lookup and use it in tool dispatch.

## Documentation updates

- Todo list item "Stop cloning frozen turn configuration inside per-call loops
  (F18)".

## Impact assessment

- Code paths affected: every reader of the frozen turn configuration.
- Data, protocol, or schema impact: none.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the agent, integration, and end-to-end suites.
- Static checks: `make check-go`.
