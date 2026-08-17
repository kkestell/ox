# 2026-08-16-02. ACP stdio server core

## Goal

Ox speaks ACP v1 over stdin and stdout but has no protocol boundary. Establish
the wire transport, the `initialize` handshake, input validation for untrusted
client JSON, and `$/cancel_request` handling, so later methods attach to a
boundary that already enforces the protocol's rules.

## Desired outcome

An ACP client launches `ox` as a subprocess, sends `initialize`, and receives a
well-formed response advertising protocol version 1. Malformed requests produce
correct JSON-RPC errors rather than crashes. `$/cancel_request` cancels the
identified in-flight request's context. stdout carries only ACP messages.

## Summary of approach

ACP types are hand-written in `internal/acp`, covering only what this plan uses.
`github.com/creachadair/jrpc2` supplies the JSON-RPC machinery — framing,
dispatch, per-request contexts, and error encoding — over
`channel.Line(os.Stdin, os.Stdout)`, which matches ACP's newline-delimited
framing exactly.

`internal/agent` holds the handlers. An `Agent` value exposes `Methods()`
returning a `handler.Map`, which `main` hands to `jrpc2.NewServer`. This plan
registers `initialize` and `$/cancel_request`; later methods are added to the
same map.

Validation lives at the decode boundary in `internal/acp`. Each request type
gets a `Validate` method returning a human-readable error, which handlers
convert to JSON-RPC `-32602`. Past that boundary, impossible states panic rather
than being papered over.

The negotiated client capabilities are the only state `initialize` retains,
because the whole purpose of the handshake is to constrain what Ox may later ask
the client to do.

## Related code

- `references/repos/personal/alpha/runtime/internal/acp/types.go` - Hand-written
  ACP v1 types over `jrpc2`, ~400 lines for the subset it needs. The model for
  this package, including `ProtocolVersion = 1` and capability structs as
  empty-struct pointers so presence encodes support.
- `references/repos/personal/alpha/runtime/internal/agent/agent.go:143-193` -
  `Methods()` and the `Initialize` handler, including the version-negotiation
  branch.
- `references/repos/personal/alpha/runtime/integration/runtime_test.go:42` -
  `TestInitializeAndStdoutPurity`, which runs the real binary at debug level and
  unmarshals every stdout line. The pattern that turns the stdout rule into an
  enforced contract.
- `references/repos/personal/gamma/protocol/validate.go` - A hand-written Go
  validation layer with per-request `Validate` functions and a test file beside
  it.
- `references/repos/third-party/protocol/agent-client-protocol/schema/v1/schema.json` -
  The authoritative v1 schema. `docs/protocol/v1/initialization.mdx`,
  `transports.mdx`, and `cancellation.mdx` are the prose contracts.

## Current state

- Relevant existing behavior: `cmd/ox/main.go` builds a logger and blocks on
  stdin. No protocol handling exists.
- Existing patterns to follow: the logger is constructed in `main` and passed
  explicitly.
- Constraints from the current implementation: stdout is reserved for the
  transport; logging already goes to stderr.

## Structural considerations

- **Hierarchy:** `internal/agent` depends on `internal/acp`, never the reverse.
  `internal/acp` knows the wire format and nothing about handling.
- **Abstraction:** `internal/acp` owns the protocol vocabulary; `internal/agent`
  owns behavior. Validation belongs to the types being validated.
- **Modularization:** Two packages, each with a real distinct job. Mixing wire
  types with handler logic would produce a package that changes for two
  unrelated reasons.
- **Encapsulation:** Handlers receive decoded, validated Go values and never see
  raw JSON, with one deliberate exception: the cancel notification's `requestId`
  stays `json.RawMessage` for the reason given below.
- **Testability:** Validation is pure functions over values, testable without a
  transport. The handshake is testable end to end by driving the real binary
  over a pipe.

## Test plan

- **Key behaviors to verify:** a valid `initialize` returns protocol version 1
  with the advertised capabilities; a client requesting an unsupported version
  still receives 1; missing or non-positive `protocolVersion` yields `-32602`;
  an unknown method yields `-32601`; malformed JSON yields `-32700`; stdout
  contains only valid JSON.
- **Test levels:** unit tests in `internal/acp` for validation and for
  round-tripping the initialize types against literal JSON copied from the
  schema docs; one integration test driving the built binary over stdin/stdout.
- **Edge cases and failure modes:** omitted `clientCapabilities` must mean
  unsupported, not zero-valued-and-ignored; `requestId` arriving as a JSON
  string, a number, and null; `$/cancel_request` naming an id that is not in
  flight, which is a no-op and must not error.
- **What not to test:** `jrpc2`'s own framing, dispatch, and error encoding.

## Implementation plan

- Write `internal/acp/types.go`: `ProtocolVersion = 1`; `InitializeRequest`
  (`protocolVersion` int, optional `clientCapabilities`, optional `clientInfo`);
  `InitializeResponse` (`protocolVersion`, `agentCapabilities`, `agentInfo`,
  `authMethods`); `ClientCapabilities` with `fs.readTextFile`,
  `fs.writeTextFile`, and `terminal`; `AgentCapabilities` with `loadSession` and
  `promptCapabilities`; `Implementation` with name, title, version;
  `CancelRequestNotification` holding `requestId` as `json.RawMessage`.
- Add the ACP cancelled error code `-32800` as a constant. `jrpc2` maps
  `context.Canceled` to its own `-32097`, so handlers that observe cancellation
  must return the ACP code explicitly.
- Write `internal/acp/validate.go` with `Validate` methods returning
  human-readable errors, and `validate_test.go` covering the cases above.
- Write `internal/agent/agent.go`: an `Agent` value carrying name, version,
  logger, and the negotiated client capabilities; `Methods()` returning the
  `handler.Map`; `Initialize` responding with version 1, `loadSession: false`,
  empty `promptCapabilities` (text and resource links are the baseline every
  agent must support), `agentInfo`, and an empty `authMethods` list, then
  storing the client capabilities.
- Register `$/cancel_request` as a notification handler that passes the raw
  `requestId` text to `jrpc2.Server.CancelRequest`. Pass the `json.RawMessage`
  through as a string without unmarshalling and re-marshalling it: `jrpc2` keys
  in-flight requests by the raw JSON text of the id, so a numeric id is `2`
  while a string id is `"abc"` including its quotes. Normalizing the value would
  fail to match any request. `jrpc2` reserves only the `rpc.` method prefix, so
  `$/cancel_request` registers like any other method.
- Wire `main` to build the `Agent`, call `jrpc2.NewServer(agent.Methods(), ...)`
  with `AllowPush` enabled for the client-bound notifications that follow, and
  start it on `channel.Line(os.Stdin, os.Stdout)`.
- Add the integration test: run the built binary, send `initialize`, assert the
  response, and unmarshal every stdout line to prove purity at debug log level.

## Documentation updates

- Mark `initialize` and `$/cancel_request` as accepted in the ACP method
  coverage list.
- Roadmap item completed: "ACP stdio server core".

## Impact assessment

- Code paths affected: new `internal/acp` and `internal/agent` packages;
  `cmd/ox/main.go` replaces its stdin block with the server.
- Data, protocol, or schema impact: establishes the ACP v1 wire contract.
  Advertised capabilities are deliberately minimal and grow as features land,
  which is a non-breaking protocol change by design.
- Dependency or API impact: uses the already-added
  `github.com/creachadair/jrpc2`. No new dependency.

## Validation

- Tests to write and run: `go test -race ./...`.
- Static checks: `gofmt`, `go vet ./...`, `staticcheck ./...`, `dprint check`.
- Manual verification: pipe a literal `initialize` request from
  `docs/protocol/v1/initialization.mdx` into `ox` and confirm the response
  matches the documented shape.
