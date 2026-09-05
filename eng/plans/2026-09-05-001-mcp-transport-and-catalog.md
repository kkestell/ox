# MCP Transport and Catalog

## Sources

- `docs/spec.md#mcp-tools` — transport, protocol, discovery, catalog, call,
  output, secret, and unsupported-feature contract
- `eng/roadmap.md#mcp-activation-and-dispatch` — slice gates
- `eng/architecture.md#extension-boundaries` and
  `eng/architecture.md#configuration-and-credentials` — adapter ownership,
  selected SDK, lifecycle, and nonsecret identity boundary
- `~/src/references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json#/$defs/McpCapabilities`
  and `#/$defs/McpServer` — pinned ACP capability and server-definition wire
  shapes
- `github.com/modelcontextprotocol/go-sdk/mcp/{client,cmd,streamable,protocol,event}.go`
  at `v1.7.0` — selected MCP client, transports, revision metadata, pagination,
  cancellation, and result types
- `~/src/references/repos/personal/mu/src/agent/mcp.ts` and
  `~/src/references/repos/third-party/coding-agents/goose/crates/goose/src/agents/mcp_client.rs`
  — namespacing and connection-cleanup patterns to adapt without their plugin
  frameworks

## Goal

Add a focused MCP boundary that can validate client-supplied definitions,
atomically connect and discover stdio and Streamable HTTP servers, and execute
bounded tool calls. This plan does not expose those tools to Ox sessions yet.

## Implementation

- `go.mod` and `go.sum` — add the architecture-selected stable
  `github.com/modelcontextprotocol/go-sdk` `v1.7.0` dependency.
- `internal/acp` — replace raw MCP server entries with the pinned v1 typed union
  and add `mcpCapabilities` to agent capabilities. Preserve the required empty
  arrays, headers, environments, and metadata on the wire. Reject ambiguous
  variants, legacy SSE, ACP transport, missing names, relative stdio commands,
  malformed headers/environments, URL credentials, and non-HTTP(S) URLs; allow
  plaintext HTTP only for loopback/localhost endpoints.
- `internal/mcp` — add the focused adapter named by the architecture. Convert
  validated ACP inputs into private connection definitions so secret header and
  environment values never enter catalog descriptors. Start each definition in
  the session root with a 30-second connect/discovery deadline and close every
  connection already opened if any definition or discovery page fails.
- Use an SDK client with no roots, sampling, elicitation, list-change
  subscriptions, or multi-round-trip handling. Disable HTTP reconnection and the
  standalone SSE listener. Require the negotiated revision to be exactly
  `2026-07-28`; do not accept the SDK's legacy initialization fallback.
- Wrap stdio and HTTP response readers before SDK JSON allocation so one NDJSON
  message, JSON response, or SSE event has a fixed wire bound. The stdio wrapper
  must own graceful close, termination, and kill of the child process. Inject
  configured HTTP headers on every request and remove them before any
  cross-origin redirect.
- Page `tools/list` deterministically, validate each input schema as a JSON
  Schema object, and enforce the combined 256-tool and 256-KiB catalog bounds.
  Generate provider-safe names from `mcp__<server>__<tool>` with deterministic
  escaping/truncation and collision rejection while retaining the exact server
  and tool names for display.
- Return immutable descriptors containing model schema and a nonsecret identity
  digest over the transport destination and complete discovered definition.
  Exclude only header/environment values so credential rotation preserves the
  identity. Before a call, bypass SDK list caching, refresh the selected
  definition, and reject a changed or missing tool without dispatch.
- Execute `tools/call` once with a 120-second deadline and no retry. Disable the
  SDK's input-request retry path and return a clear unsupported-input result.
  Accept only text and structured JSON, reject other content kinds, preserve
  `isError`, and reject accepted payloads above 1 MiB before returning them to
  the caller.
- `eng/architecture.md` — add `internal/mcp` to package ownership and dependency
  direction without duplicating the protocol behavior owned by the spec.

## Tests

- Package fixtures prove both transports, exact revision negotiation,
  pagination, deterministic names, duplicate names, invalid schemas, catalog
  limits, partial-start cleanup, deadlines, shutdown, and command working
  directory/environment behavior.
- Raw wire fixtures prove required per-request metadata, configured-header
  parity, JSON and SSE HTTP responses, stdio and HTTP cancellation, no retry
  after connection loss, cross-origin header removal, and bounded allocation for
  oversized NDJSON, JSON, and SSE frames.
- Calls cover text, structured JSON, MCP tool errors, unsupported content,
  input-required responses, missing/changed definitions, and the 1-MiB boundary.
  Assert secrets are absent from descriptors, identity digests, and every
  returned error.

## Sequence

This is the first of two plans for MCP activation and dispatch. It leaves a
tested adapter unused by production session activation; the next plan wires it
into agent-owned lifecycle, policy, durability, and ACP-visible behavior.

## Decisions

- Use the SDK for protocol semantics but keep Ox-owned transport wrappers for
  pre-allocation bounds and process cleanup. A middleware forces tool-list TTLs
  to zero so the required pre-dispatch refresh cannot return cached schema.
