# Local MCP server support

## Goal

Let Ox discover and call tools from local MCP subprocesses configured in Ox
settings or supplied by an ACP client. Support terminal sessions, editor
sessions, and headless runs. In Ask mode, trust tools explicitly marked
read-only and request permission for every other MCP tool call.

## Related code

- `internal/settings/settings.go` reads settings and preserves unrelated fields
  when saving model selections. `Settings` represents model, effort, and mode.
- `internal/server/server.go` owns active sessions, accepts ACP session
  requests, starts turns, and shuts down session processes. It currently ignores
  `McpServers` in new and load requests.
- `internal/agent/agent.go` builds model requests with a fixed tool list and
  executes calls through `tools.Toolbox` and the existing approval flow.
- `internal/tools/tools.go` provides built-in schemas, execution, permission
  classification, titles, and a 16 KiB result limit.
- `internal/server/convert.go` converts tool events and permission requests to
  ACP. The existing terminal renderer accepts arbitrary tool names and text.
- `cmd/ox-server/main.go` owns the separate headless session lifecycle.
- `internal/shellproc/shellproc.go` demonstrates process group cleanup and
  bounded output capture; its command runner consumes stdout, so MCP cannot use
  it directly for protocol traffic.
- The [official Go SDK](https://github.com/modelcontextprotocol/go-sdk) provides
  MCP clients, subprocess transport, tool discovery, and tool calls. Use its
  protocol implementation rather than adding another JSON-RPC implementation.

## Decisions

### Configuration

Add `mcpServers` to global and workspace settings as a map from server names to
objects containing `command`, optional `args`, and optional `env` string maps.
Keep this separate from the terminal client's existing `servers` setting.

```json
{
  "mcpServers": {
    "project": {
      "command": "project-mcp",
      "args": ["--stdio"],
      "env": { "PROJECT_PROFILE": "development" }
    }
  }
}
```

An absent workspace key inherits the global map. A present workspace map
replaces the entire global map, consistent with existing workspace overrides; an
empty map disables settings-defined servers. Reject null, unknown server fields,
empty names or commands, malformed arguments, and invalid environment names or
values with errors naming the source and server.

Read MCP settings when a session first becomes active, separately from model
selections. Keep `settings.Settings` unchanged so saved model selections and
later configuration changes cannot overwrite MCP configuration. Headless runs
read the same settings once per run.

Convert ACP stdio definitions into the same `settings.MCPServer` type. Merge
them over the effective settings map by server name, replacing the complete
definition on a match. Reject duplicate ACP names and unsupported transports.
The pinned ACP SDK treats stdio as mandatory; leave HTTP, SSE, and ACP transport
capabilities false rather than inventing a stdio capability field.

Active sessions keep their original MCP configuration. Repeated loads reuse
connections and reject changed ACP definitions with an instruction to close and
reload the session. Closing and loading again reads current settings and uses
the supplied ACP definitions. Do not persist commands or environment values in
the session database.

### Ownership and discovery

Introduce `internal/mcpclient.Clients`, owned by one active session or headless
run. Construct it without starting processes. On the first prompt, connect and
discover tools before sending a model request. Reuse those connections across
turns. Before each later turn, list tools again and build an immutable
`mcpclient.Catalog` for that turn, including permissions and dispatch targets.
Follow every discovery page and sort definitions for deterministic requests.

Run commands directly with argument arrays, starting in the session workspace.
Inherit the environment except `OPENROUTER_API_KEY`, matching shell tools, then
apply explicit configured environment values. Keep subprocess stdout confined to
MCP and send stderr to Ox's stderr. Configuration authorizes starting the
server; Ask mode applies to its tool calls.

Use the SDK subprocess transport with bounded shutdown and a process group for
each server. Keep process ownership in `mcpclient`; close protocol connections,
terminate remaining group members, and reap the child once. Follow the existing
two-second termination grace before forced cleanup. A finished prompt must not
cancel the context that owns a healthy server process.

Bound each server's initialization and complete tool listing to 30 seconds and
each tool call to 120 seconds, also honoring prompt cancellation. A startup or
discovery failure names the server and fails that prompt before a model request.
Close connections opened by a failed initial setup. A later prompt may retry
setup, but do not retry tool calls or automatically restart a disconnected
server. Report disconnection and require closing and reloading the session.

Close and delete sessions only after their current operation has finished; then
shut down their MCP processes alongside background shell processes. Connection
shutdown and headless exit clean up MCP processes too. Keep startup and
discovery inside the prompt goroutine so ACP cancellation and shutdown remain
responsive.

### Tool behavior

Expose each discovered tool as a normal model function with its description and
input schema. Generate deterministic names prefixed with `mcp__` and including
server and tool names. Restrict model names to letters, digits, underscores, and
hyphens and at most 64 characters; when encoding or shortening is needed,
include a stable hash of the original pair. Keep an explicit mapping to the
original names and reject any collision. Never dispatch by splitting a
model-supplied name or overwrite a built-in tool.

Keep the existing title fallback to the tool's name, which also works when
replaying saved transcripts without a live MCP connection. Permission requests
show the original server name, original tool name, and complete arguments.

Only `annotations.readOnlyHint: true` skips permission in Ask mode. False or
missing annotations require approval; other annotations do not waive it. Auto
mode skips approval. This deliberately trusts the configured server's claim: the
[MCP tool specification](https://github.com/modelcontextprotocol/modelcontextprotocol/blob/main/docs/specification/2025-11-25/server/tools.mdx)
defines annotations as hints. Denied calls never reach the server and receive an
MCP-specific denial result through the normal transcript flow.

Accept only JSON object arguments and preserve their numeric values when
forwarding them. Map MCP `isError` and protocol failures to failed tool results.
Cancellation produces a cancelled result that does not claim remote effects were
undone. Forward cancellation through the SDK; do not replay the call.

Use existing text results: preserve text blocks, embedded text resources, and
resource-link metadata, and include structured content as JSON without
duplicating an identical text block. Represent unsupported image, audio, or
binary blocks with explicit omission notices. Bound the complete result,
including failures and notices, to the existing 16 KiB limit at a UTF-8
boundary. Preserve the server's success or failure status when content is
omitted. Do not fetch resource links automatically.

This change exposes MCP tools only. It does not add standalone resource or
prompt discovery, sampling, elicitation, remote transports, or binary tool
results. Advertise only implemented client capabilities. No transcript or
database format change is needed.

## Implementation plan

1. Add the official MCP Go SDK dependency in `go.mod` and `go.sum`, pinning a
   stable version compatible with the current Go version.
2. Create `internal/settings/mcp.go` with `MCPServer`, validation, and
   `LoadMCPServers(home, workspace)`. Keep presence detection separate from an
   empty map. Add `internal/settings/mcp_test.go` for decoding, overrides,
   missing files, validation, and preservation by `settings.Save`. Update the
   package comment in `internal/settings/settings.go` to distinguish when MCP
   settings are read.
3. Create `internal/mcpclient/client.go` for `Clients`, connection ownership,
   discovery, `Catalog`, and dispatch. Create `internal/mcpclient/process.go`
   for command construction and bounded process cleanup. Keep protocol details
   inside this package and expose only the metadata and call results required by
   the tool adapter. Add `internal/mcpclient/client_test.go`.
4. Create `internal/mcptest/mcptest.go`, a small shared stdio server fixture
   launched through test helper subprocesses. Give tests controllable tool
   definitions, results, cancellation, startup failures, and process markers; do
   not depend on installed Node or Python servers or network access.
5. Extend `internal/tools/tools.go` with a per-turn MCP catalog and schemas
   combining built-ins with that catalog. Add `PermissionMCP` with original
   server and tool names. Create `internal/tools/mcp.go` for argument handling,
   dispatch, and bounded result conversion, and `internal/tools/mcp_test.go` for
   observable adapter behavior.
6. Modify `internal/agent/agent.go` to accept the prepared catalog in `Input`,
   use the toolbox's schemas, and return the correct MCP denial text. Extend
   `internal/agent/agent_test.go` to cover model-visible schemas, permission
   decisions, execution order, saved results, and cancellation.
7. Create `internal/server/mcp.go` to normalize and merge ACP definitions.
   Modify `internal/server/server.go` to retain `Clients` and original ACP
   configuration in `activeSession`, prepare catalogs inside prompt goroutines,
   and clean up on close, successful deletion, and shutdown. Preserve operation
   reservation, prompt response accounting, and existing error mapping when
   moving turn startup into the goroutine. Cancellation during preparation must
   respond as cancelled without starting a model request.
8. Modify `internal/server/convert.go` to render MCP permission details and
   remove shell-specific wording from generic permission errors in
   `internal/server/server.go`. Extend `internal/server/server_test.go` for
   configuration precedence, approval content, session reuse, transcript replay,
   and lifecycle cleanup.
9. Modify `cmd/ox-server/main.go` to load MCP configuration, prepare the
   catalog, pass it to the headless turn, and shut down processes on every exit.
   Extend `cmd/ox-server/main_test.go` with an end-to-end headless MCP call and
   signal cancellation during startup and execution.

## Test plan

- Missing settings produce only built-in tools. Workspace replacement, an empty
  map, ACP name overrides, and configuration saves have the specified behavior.
  Invalid definitions fail before any process starts.
- Real stdio initialization and paginated discovery reach the model with
  descriptions and schemas intact. Identical tool names on different servers,
  unusual names, long names, and built-in names dispatch correctly. Changed tool
  definitions and read-only annotations apply on the next turn.
- Ask mode bypasses approval only for explicit read-only tools. Other calls
  support approval, denial, and cancellation. Auto mode never asks. Denied and
  cancelled-before-start calls produce no server-side invocation.
- Text, structured results, resource links, embedded text, unsupported content,
  empty results, `isError`, invalid arguments, malformed replies, UTF-8
  truncation, and protocol failures produce bounded, accurate outcomes.
- Subprocesses see the workspace, arguments, and expected environment. Protocol
  output never reaches ACP stdout or the headless final answer.
- Connection reuse survives successive prompts and repeated loads. Sessions are
  isolated. Closed sessions reconnect when loaded; changed active ACP
  definitions are rejected. Replay does not need the original server running.
- Startup timeout, discovery failure, process exit, and cancellation return
  promptly. Close, successful delete, connection loss, and headless exit reap
  processes and remaining group members, including a child that ignores
  termination. A failed delete leaves the active session usable.
- SDK cancellation reaches a cooperative server; an uncooperative server cannot
  hold the turn open. No failed or cancelled call is automatically repeated. A
  cancelled preparation sends no model request.

## Documentation updates

- Update `README.md` with `mcpServers` configuration, precedence, activation and
  reload behavior, process environment, supported results, timeouts, and Ask
  mode's trust in read-only annotations. Explain that these settings belong to
  `ox-server`; the terminal client does not forward them to other ACP agents.
- Add an empty `mcpServers` map to `examples/settings.json` so the installed
  example does not launch an external program by default.
