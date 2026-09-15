# Server-owned tool titles

## Goal

Remove browser-side tool-name parsing. Ox must send a complete, human-readable
ACP tool-call title for every known built-in or MCP invocation, and the browser
must render that server-owned title unchanged in live transcripts, replay, and
permission prompts.

## Related code

- `docs/spec.md` — Owns the observable ACP and first-party client behavior.
- `eng/client-architecture.md` — Owns the ACP-to-browser projection boundary.
- `internal/agent/{tool,loop,adapter,state,mcp}.go` — Defines tool presentation
  data and publishes it for live calls, recovered permissions, and replay.
- `internal/tools/{tools,titles}.go` — Registers built-in tools and derives their
  bounded invocation titles.
- `client/src/{session-controller,protocol}.ts` — Projects ACP titles into
  browser-safe transcript and permission state.
- `client/src/components/{tool-label,transcript,pending-interactions}.tsx` —
  Contains the current client-side parsing and its two consumers.

## Decisions

- Use ACP's standard `title` field as the sole display-ready tool-call label.
  Keep `name` as the exact provider-facing identifier and do not add a
  namespaced metadata field that duplicates the standard title.
- Ox owns title wording at tool registration. Built-in titles include the
  action as well as the bounded argument-derived subject; in particular, shell
  calls publish `Run <command>` instead of requiring a client prefix. MCP calls
  use the discovered human-readable title when available and otherwise an
  honest server/tool fallback without exposing the generated `mcp__...`
  provider name as presentation data.
- Unknown provider-requested tools may retain their raw name as the ACP fallback:
  they have no registered presentation contract. Client fixtures must not rely
  on parsing an invalid tool call into a friendly label.
- Remove the unused legacy `Tool.Label` fallback so one server-side title path
  owns live calls, permission requests, recovery, and replay.

## Test plan

- Add exact built-in title cases, including shell commands, malformed inputs,
  empty values, multiline values, and the existing length bound.
- Extend agent tests to assert identical titles and raw names on live ACP tool
  calls, permission requests, recovered permission requests, and replayed MCP
  calls with and without a discovered title.
- Extend client reducer/component tests to prove transcript and permission UI
  render the received title verbatim and do not derive text from `shell` or
  `mcp__...` names.
- Update the responsive Playwright fixture to exercise a valid server-titled
  tool call while retaining the long raw name as disclosed technical data.

## Implementation plan

- Make each built-in and MCP registration produce a complete display-ready
  title, simplify agent title lookup to that single contract, and preserve the
  same derivation through frozen configuration replay and permission recovery.
- Delete `client/src/components/tool-label.ts`; have transcript and pending
  permission components use the projected ACP title directly while keeping the
  raw name available only for technical details.
- Adjust the browser projection and focused fixtures only as needed to carry and
  verify the server-owned title without copying arbitrary ACP metadata into
  browser snapshots.

## Documentation updates

- `docs/spec.md` — State that Ox supplies display-ready ACP tool-call titles and
  that the first-party client does not derive presentation text from raw tool
  identifiers.
- `eng/client-architecture.md` — Record the title/name projection boundary.
