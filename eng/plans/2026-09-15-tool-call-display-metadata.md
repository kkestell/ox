# Structured tool-call display metadata

## Goal

Publish separate human-readable tool actions and bounded display arguments in
ACP tool-call metadata.

## Related code

- `docs/spec.md` — Owns ACP-visible tool-call presentation behavior.
- `internal/acp/types.go` — Defines Ox's namespaced ACP metadata keys.
- `internal/agent/{adapter,loop,mcp,state,tool}.go` — Publishes tool-call
  presentation for live calls, permissions, recovery, and replay.
- `internal/tools/titles.go` — Derives concise display actions and arguments
  from built-in invocation inputs.

## Decisions

- Ox sends `kkestell.ox/toolDisplayName` and, when meaningful,
  `kkestell.ox/toolDisplayArguments` in tool-call `_meta`. The former is the
  human action (such as `Read`); the latter is the existing bounded, single-line
  subject (such as `foo/bar.txt`). They are distinct from ACP's technical
  `name`.
- Keep ACP `title` as the interoperable fallback.
- One structured presentation derivation serves live updates, permission
  requests, recovered permissions, and replay. MCP tools contribute their
  discovered title as the action and no invented argument.

## Test plan

- Cover built-in structured action/argument derivation, including malformed or
  empty arguments and the current bound for display arguments.
- Assert live, permission, and replay ACP tool calls retain the matching title,
  raw technical name, and two presentation metadata values.

## Implementation plan

- Replace the combined tool-title contract with a structured presentation and
  attach it at every ACP tool-call publication boundary.
- Update the tool-call specification to describe the structured metadata
  boundary.
