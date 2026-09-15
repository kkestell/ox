# Structured tool-call display metadata

## Goal

Make a tool activity title render its action and argument separately: for
example, `Read` followed by code-styled `foo/bar.txt`, rather than one truncated
sentence containing both.

## Related code

- `docs/spec.md` — Owns ACP-visible tool-call presentation behavior.
- `eng/client-architecture.md` — Owns the ACP-to-browser transcript projection.
- `internal/acp/types.go` — Defines Ox's namespaced ACP metadata keys.
- `internal/agent/{adapter,loop,mcp,state,tool}.go` — Publishes tool-call
  presentation for live calls, permissions, recovery, and replay.
- `internal/tools/titles.go` — Derives concise display actions and arguments
  from built-in invocation inputs.
- `client/src/{session-controller,protocol}.ts` — Admits only the two explicit
  metadata values into browser-safe tool state.
- `client/src/components/transcript.tsx` — Renders a tool action with its
  code-styled argument.

## Decisions

- Ox sends `kkestell.ox/toolDisplayName` and, when meaningful,
  `kkestell.ox/toolDisplayArguments` in tool-call `_meta`. The former is the
  human action (such as `Read`); the latter is the existing bounded, single-line
  subject (such as `foo/bar.txt`). They are distinct from ACP's technical
  `name`.
- Keep ACP `title` as the action-only interoperable fallback. The first-party
  client renders the structured metadata when both values are well-formed and
  falls back to `title` for unknown tools or other ACP agents.
- One structured presentation derivation serves live updates, permission
  requests, recovered permissions, and replay. MCP tools contribute their
  discovered title as the action and no invented argument.

## Test plan

- Cover built-in structured action/argument derivation, including malformed or
  empty arguments and the current bound for display arguments.
- Assert live, permission, and replay ACP tool calls retain the matching title,
  raw technical name, and two presentation metadata values.
- Verify the client reducer ignores arbitrary metadata, projects the two
  namespaced strings, and the transcript renders the argument in a `code`
  element without parsing `title` or raw tool names.

## Implementation plan

- Replace the combined tool-title contract with a structured presentation and
  attach it at every ACP tool-call publication boundary.
- Project the two metadata values through the host's browser-safe transcript
  schema and render a compact action-plus-code-argument label.
- Update the tool-call specification and client architecture to describe the
  structured metadata boundary.
