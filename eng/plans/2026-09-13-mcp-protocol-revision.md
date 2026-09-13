# MCP protocol revision negotiation

## Goal

Ox pins the MCP protocol revision to `2026-07-28` and refuses activation when a
server negotiates anything older. Real servers — including those built on the
current MCP SDKs — advertise older revisions, so Ox cannot connect to them.

## Desired outcome

A server that negotiates an older revision the embedded SDK speaks activates
successfully. Ox still speaks `2026-07-28` when the server supports it, and a
genuinely incompatible server still fails activation with the server named.

## Summary of approach

The embedded Go SDK (v1.7.0) already validates the negotiated revision against
its own supported set (`2026-07-28`, `2025-11-25`, `2025-06-18`, `2025-03-26`,
`2024-11-05`) and settles older servers on `2025-11-25` through its legacy
initialize fallback. Ox's pinned-equality check sits strictly inside that
boundary: it can never reject anything the SDK would have accepted, only accept
less. Remove the pinned `ProtocolVersion` constant and the refusal in
`connectServer`, and let the SDK's negotiation own the contract. The fixture
test that forced a downgrade becomes the regression test asserting activation
succeeds over `2025-11-25`.

## Related code

- `internal/mcp/mcp.go` — the `ProtocolVersion` constant and the
  revision-equality refusal at the end of `connectServer`.
- `internal/mcp/mcp_test.go` —
  `TestActivationRefusesAnUnsupportedProtocolRevision` rewrites the fixture
  server's advertised revision list to force the downgrade.
- `docs/spec.md` — the MCP tools section records the "rejects older revisions"
  product decision.
- `eng/architecture.md` — the extension-boundaries section records the
  revision-restriction decision.

## Current state

- Relevant existing behavior: `connectServer` closes the session and fails
  activation unless the negotiated revision equals `2026-07-28`; the SDK client
  never surfaces a result outside its supported set, so every real-world
  downgrade fails here.
- Existing patterns to follow: the fixture forces the downgrade by stripping
  `2026-07-28` from the advertised list, which drives the SDK into its legacy
  initialize fallback and settles on `2025-11-25`.
- Constraints from the current implementation: `docs/spec.md` owns the revision
  contract and `eng/architecture.md` owns the durable decision; both currently
  state the pinned behavior and must move with the code.

## Structural considerations

- **Hierarchy:** the SDK transport adapter remains the protocol boundary;
  removing the check removes a duplicate, weaker version of the SDK's own
  validation rather than relocating a responsibility.
- **Abstraction:** the revision contract follows the SDK Ox embeds, which is the
  component that actually implements each revision.
- **Encapsulation:** no new surface; a constant and a branch are deleted.
- **Testability:** the fixture's rewrite trick exercises the real negotiation
  path end to end through `Activate` without touching SDK internals.

## Test plan

- **Key behaviors to verify:** a server that settles on `2025-11-25` activates
  successfully and its tools are discoverable; an incompatible server still
  fails activation with the server name in the error (the SDK's refusal, wrapped
  by `connectServer`'s connect error).
- **Test levels:** unit tests in `internal/mcp`.
- **Edge cases and failure modes:** the modern handshake still negotiates
  `2026-07-28` unstripped, covered by the existing activation fixtures.
- **What not to test:** the negotiation mechanics themselves, which belong to
  the SDK.

## Implementation plan

- Remove `ProtocolVersion` from the constants and the revision-equality check
  and error from `connectServer`.
- Rewrite `TestActivationRefusesAnUnsupportedProtocolRevision` as
  `TestActivationAcceptsAnOlderProtocolRevision`: keep the fixture rewrite
  (hardcoding `"2026-07-28"` where the constant was), assert `Activate`
  succeeds, and assert the discovered tool is present.
- Update the fixture comment to describe the downgrade-forcing trick and the
  SDK's legacy fallback.
- Keep the SDK rejection path covered with an incompatible-revision fixture
  whose activation error names the configured server.

## Documentation updates

- `docs/spec.md`: replace "using protocol revision `2026-07-28` ... rejects
  older revisions" with Ox accepting the revisions the embedded SDK negotiates
  while still rejecting legacy HTTP+SSE transport.
- `eng/architecture.md`: state that the protocol revision follows the embedded
  SDK's negotiation; keep the capabilities restriction as-is.
- Todo list item for this work.

## Impact assessment

- Code paths affected: `internal/mcp` only.
- Data, protocol, or schema impact: sessions now complete over older MCP
  revisions instead of failing activation.
- Dependency or API impact: none.

## Validation

- Tests to write and run: the rewritten activation test, then the full MCP suite
  with `-count=1`.
- Static checks: `make check`.
- Manual verification: activate the `server-everything` MCP server from Zed and
  confirm its tools appear.
