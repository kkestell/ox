# Browser MCP activation

## Goal

Let a browser user activate Ox sessions with HTTP or stdio MCP servers without
leaking credentials into host snapshots, durable browser state, diagnostics, or
URLs.

## Desired outcome

The unstyled browser client offers native forms for HTTP and stdio MCP server
definitions. Submitting a new, load, or resume activation discovers the selected
servers' tools. A subsequent prompt can call those tools, and connection or tool
failures are shown as activation or transcript outcomes without exposing a
secret.

## Summary of approach

Add bounded browser-protocol definitions for the two Ox-supported MCP
transports. The React activation form owns its drafts and sends complete
definitions only with the requested activation command; it immediately clears
the submitted draft. The host forwards the values directly through the workspace
supervisor to the matching ACP request and retains no definition or secret. The
same transient input is required by new, load, and resume routes.

## Related code

- `client/src/protocol.ts` — bounded browser command validation.
- `client/src/host.ts` — browser-command routing.
- `client/src/workspace-supervisor.ts` — ACP session activation requests.
- `client/src/browser.tsx` — semantic activation forms and secret clearing.
- `client/src/{protocol,workspace-supervisor}.test.ts` — stable protocol and
  host-to-ACP routing coverage.
- `client/e2e/smoke.spec.ts` — real Ox and browser MCP scenarios.
- `docs/spec.md#mcp-tools` — the existing product contract for supported MCP
  transports and secret lifetime.

## Current state

- Session activation currently always passes an empty `mcpServers` list.
- The browser protocol and snapshots are validated with Zod, and snapshots are
  the only host-to-browser state channel.
- Ox already validates activation definitions, discovers tools before success,
  redacts secrets at its MCP boundary, and supports HTTP and stdio transports.

## Structural considerations

- **Hierarchy:** React supplies an activation request; the host and workspace
  supervisor remain the only ACP client and process owners.
- **Abstraction:** Browser-safe command values describe only the two product
  transports. Conversion to ACP values happens at the supervisor boundary.
- **Modularization:** Keep form draft mechanics in the React surface and keep no
  secret-bearing activation store.
- **Encapsulation:** Do not place definitions in snapshots, diagnostics, URLs,
  session summaries, or durable browser storage.
- **Testability:** Validate command shapes in pure tests, inspect ACP requests
  with the supervisor fixture, and exercise discovery and calls through the real
  browser-to-Ox path.

## Test plan

- Reject malformed, oversized, and unsupported browser MCP definitions while
  accepting empty HTTP headers and stdio args and environment lists.
- Assert new, load, and resume forward the exact definition set and do not
  retain it in supervisor state.
- Use local HTTP and stdio MCP fixtures through a real Ox process to prove
  discovery, successful calls, server-reported tool failures, failed activation,
  and reactivation with a fresh definition.
- Assert browser-visible snapshots and page text never contain configured HTTP
  header or stdio environment secret values.

## Implementation plan

- Add bounded HTTP and stdio MCP definition schemas and attach them to the three
  activation commands.
- Convert validated definitions to official ACP SDK request values and thread
  them through all supervisor activation routes.
- Add the semantic server editor, HTTP header and stdio argument/environment
  fields, and activation buttons that submit the current draft then clear it.
- Extend unit fixtures and the Playwright harness with local MCP servers and
  activation/call/failure/reactivation coverage.
- Mark the MCP activation task complete in `eng/todo.md`.

## Documentation updates

- Mark the completed browser-client MCP activation task in `eng/todo.md`.

## Impact assessment

- Code paths affected: browser WebSocket commands and all session activation
  calls.
- Data, protocol, or schema impact: new secret-bearing browser-to-host command
  fields; no snapshot or durable-state change.
- Dependency or API impact: no new dependency and no Ox ACP extension.

## Validation

- Run the client type check, pure Bun tests, and browser end-to-end suite.
- Run focused Go MCP tests and the repository's relevant static/documentation
  checks if client gate integration changes them.
