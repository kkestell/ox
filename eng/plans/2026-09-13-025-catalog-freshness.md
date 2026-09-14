# Catalog freshness with bounded refresh cost

## Goal

Make both catalogs Ox depends on age correctly: refresh the OpenRouter model
catalog a long-running process holds in memory, and stop paying for an MCP
server's whole catalog on every tool call.

## Desired outcome

A process running for days sees current context windows and supported
parameters. A burst of MCP calls against one server costs one tool listing
rather than one per call, and a redefined tool is still rejected rather than run
under a grant taken for its old definition.

## Summary of approach

Give the in-memory OpenRouter catalog an expiry derived from the age of what was
loaded, so the memory copy expires with the catalog itself rather than living
for the process. A catalog already past its maximum age expires on a short retry
interval instead, so an unreachable provider costs one fetch per interval rather
than one per request.

Give each MCP server a bounded-age listing that a dispatch validates against.
MCP exposes no way to read one tool, so the alternative to a window is relisting
the whole catalog per call. The model-facing tool set and system prompt stay
frozen at activation, so this window changes only how promptly a redefinition is
noticed, never what a request carries.

## Related code

- `internal/openrouter/catalog.go` - Owns the freshness rule, the single-flight
  load, and the stale-on-error fallback.
- `internal/openrouter/client.go` - Holds the in-memory catalog.
- `internal/mcp/mcp.go` - Connects servers, lists their tools, and validates the
  selected definition before dispatch.
- `internal/agent/mcp.go` - Snapshots descriptors into the session's tool set at
  activation, which is what keeps the request prefix stable.

## Current state

- `Client.Catalog` returns the first successfully loaded catalog forever. The
  24-hour rule reaches only the disk cache, so the architecture's promise that a
  stale catalog is refetched on the spot is not kept in memory.
- `Bundle.Call` paginates `tools/list` for the whole server before every call.
  This is O(server catalog) per call, spends part of the same 120-second call
  deadline, and lets an unrelated catalog page failure stop a stable tool.
- Activation snapshots each descriptor's name, description, and schema into the
  session tool set and frozen request configuration. Nothing re-reads the bundle
  afterwards, so a server changing at runtime already cannot move the request
  prefix.

## Structural considerations

- **Hierarchy:** Both freshness rules stay inside the adapter that owns the
  catalog. The agent keeps its frozen activation snapshot.
- **Abstraction:** The OpenRouter expiry is one pure function over an age. The
  MCP window is one method on the server that already owns its listing.
- **Encapsulation:** The MCP listing stays private to the server; the bundle
  keeps asking for the current tools rather than reaching for a cache.
- **Testability:** The expiry rule is a pure function. The MCP window is visible
  as the number of `tools/list` requests a burst of calls produces.

## Test plan

- **Key behaviors to verify:** The expiry lifetime for a fresh fetch, a partly
  aged cache, a nearly expired cache, and a stale fallback; a client whose
  memory copy expired reloads; a burst of MCP calls produces one listing; a
  redefinition is rejected once the listing ages out; an MCP catalog change
  cannot move the tools or system prompt a later provider request carries.
- **Test levels:** Focused package tests for both freshness rules, plus an
  end-to-end test for prefix stability, which is only observable in the bytes Ox
  sends the provider.
- **Edge cases and failure modes:** A stale fallback must not refetch per
  request; a redefinition inside the window is deliberately not yet visible.
- **What not to test:** The disk cache freshness rule and the stale fallback
  itself, which are unchanged and already covered.

## Implementation plan

- Expire the in-memory OpenRouter catalog from the age of what was loaded.
- Validate MCP dispatch against a bounded-age listing per server.
- Add the tests above, including the prefix-stability regression test.
- Update the specification and architecture.

## Documentation updates

- `docs/spec.md` states what a dispatch validates against and that a mid-session
  catalog change cannot move a later request's prefix.
- `eng/architecture.md` states that in-memory catalog freshness is bounded, that
  a stale fallback bounds its retry, and why MCP validates against a window.

## Impact assessment

- Code paths affected: OpenRouter catalog loading, MCP dispatch validation.
- Data, protocol, or schema impact: None. No new record, field, or method.
- Dependency or API impact: None.

## Validation

- Tests to write and run: The tests above, then `make check`.
- Static checks: The checks included by `make check`.
