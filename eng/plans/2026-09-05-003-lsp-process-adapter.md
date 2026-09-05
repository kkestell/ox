# LSP Process Adapter

## Sources

- `docs/spec.md#language-intelligence` — configured servers, lazy lifecycle,
  deadlines, synchronization, positions, diagnostics, and confined-result
  contract
- `eng/roadmap.md#language-server-context` — slice gates and exclusions
- `eng/architecture.md#extension-boundaries` and
  `eng/architecture.md#workspace-boundary` — focused adapter ownership,
  activation resources, selected filesystem authority, and confinement
- [Language Server Protocol 3.17](https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/)
  — framing, lifecycle, synchronization, position encodings, navigation,
  symbols, and diagnostic wire shapes
- `~/src/references/repos/personal/eta/internal/lsp/{client,manager,diagnostics,uri}.go`
  and adjacent tests — process, framing, routing, and formatting seams to port

## Goal

Add a focused, root-confined LSP boundary that lazily starts configured language
servers and implements the navigation, symbol, and diagnostic queries required
by the specification. This plan leaves the adapter unused by Ox sessions.

## Implementation

- `internal/lsp` — add immutable server definitions and an activation manager.
  Resolve each command through the inherited process `PATH`, run it in the
  session root, route file queries by normalized extension, and start a server
  only on its first query. Sort server names and reject overlapping extension
  ownership before use so routing does not depend on map order.
- Implement Content-Length JSON-RPC over stdio with correlated requests,
  notifications, bounded stderr draining, and responses to unsupported server
  requests instead of leaving them pending. Use a 30-second initialize deadline
  and a 10-second deadline for every query. On cancellation, send
  `$/cancelRequest`; after a crash, retain the failed state and do not restart
  that server during the activation.
- Initialize with the canonical root and workspace folder, advertise UTF-8,
  UTF-16, and UTF-32 position support, and honor the server's selected encoding
  (UTF-16 when omitted). Before every file query, load the authoritative UTF-8
  text through a caller-supplied reader and send a full-content `didOpen` or
  versioned `didChange`. Reject a server that cannot accept document
  synchronization.
- Implement definition, references, document symbols, workspace symbols, and
  explicit diagnostics. Model-facing inputs and results use one-based lines and
  Unicode-scalar columns; convert them to and from the negotiated LSP encoding
  against the synchronized content. Reject invalid input positions and malformed
  result ranges.
- Prefer `textDocument/diagnostic` when statically advertised. For a push-only
  server, wait only for `publishDiagnostics` tied to the synchronized document
  version. Report stale, unversioned, timed-out, unsupported, and empty-push
  observations distinctly; an empty push is not a clean or finished verdict.
- Convert file URIs to canonical workspace-relative paths. Omit non-file,
  missing, symlink-escaping, and otherwise out-of-root locations while returning
  an explicit omission count. Keep protocol types and absolute paths private to
  the adapter.
- Give the manager idempotent close behavior: request `shutdown`, notify `exit`,
  then terminate a server that does not exit within a bounded grace period.
- `eng/architecture.md` — add `internal/lsp` to package ownership and dependency
  direction without copying observable protocol behavior from the spec.

## Tests

- A helper-process server proves lazy `PATH`-resolved startup in the session
  root, initialization and query deadlines, request cancellation, concurrent
  response correlation, malformed framing/replies, crash-without-restart, and
  graceful or forced shutdown.
- Query fixtures prove full-content open/change versions, caller-supplied
  unsaved text, all three position encodings with non-ASCII content,
  definitions, references, both document-symbol forms, workspace symbols, and
  deterministic routing.
- Diagnostics cover pull results, matching-version push results, stale and
  unversioned pushes, empty pushes, timeout, cancellation, and unavailable
  diagnostics without reporting an unknown state as clean.
- URI fixtures cover percent encoding, relative output, symlink escapes,
  non-file URIs, malformed positions, and explicit omission counts.

## Sequence

This is the first of two plans for language-server context. The next plan adds
global configuration, model-facing tools, activation ownership, documentation,
and process-level coverage.

## Decisions

- Use the small handwritten 3.17-compatible protocol seam established by Eta;
  the required surface does not justify a new production dependency.
