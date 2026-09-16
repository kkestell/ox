# Server-owned tool titles

## Goal

Ox must send a complete, human-readable ACP tool-call title for every known
built-in or MCP invocation in live updates, replay, and permission requests.

## Related code

- `docs/spec.md` — Owns the observable ACP-visible behavior.
- `internal/agent/{tool,loop,adapter,state,mcp}.go` — Defines tool presentation
  data and publishes it for live calls, recovered permissions, and replay.
- `internal/tools/{tools,titles}.go` — Registers built-in tools and derives
  their bounded invocation titles.

## Decisions

- Use ACP's standard `title` field as the sole display-ready tool-call label.
  Keep `name` as the exact provider-facing identifier and do not add a
  namespaced metadata field that duplicates the standard title.
- Ox owns title wording at tool registration. Built-in titles include the action
  as well as the bounded argument-derived subject; in particular, shell calls
  publish `Run <command>` instead of requiring a client prefix. MCP calls use
  the discovered human-readable title when available and otherwise an honest
  server/tool fallback without exposing the generated `mcp__...` provider name
  as presentation data.
- Unknown provider-requested tools may retain their raw name as the ACP
  fallback: they have no registered presentation contract.
- Remove the unused legacy `Tool.Label` fallback so one server-side title path
  owns live calls, permission requests, recovery, and replay.

## Test plan

- Add exact built-in title cases, including shell commands, malformed inputs,
  empty values, multiline values, and the existing length bound.
- Extend agent tests to assert identical titles and raw names on live ACP tool
  calls, permission requests, recovered permission requests, and replayed MCP
  calls with and without a discovered title.

## Implementation plan

- Make each built-in and MCP registration produce a complete display-ready
  title, simplify agent title lookup to that single contract, and preserve the
  same derivation through frozen configuration replay and permission recovery.

## Documentation updates

- `docs/spec.md` — State that Ox supplies display-ready ACP tool-call titles.
