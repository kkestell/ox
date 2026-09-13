# Integrate language-server tools with sessions

## Goal

`internal/lsp` implements the required lazy, confined language-server boundary,
but no package imports it. Ox cannot configure that boundary, give an activation
ownership of it, or expose its queries to the model.

## Desired outcome

Global process settings configure language servers, every active session owns a
lazy manager, and the stable built-in tool catalog exposes definition,
reference, document-symbol, workspace-symbol, and diagnostic queries in code and
plan modes. Queries use the activation's selected filesystem, return bounded
deterministic output, and release every started server on activation close or
process exit.

## Summary of approach

Resolve and validate immutable language-server definitions with the other
process settings, then pass them through `cmd/ox` into the agent. Construct one
`lsp.Manager` for each activation without starting a server and store it beside
the activation's MCP resources. The built-in tools pass the manager an
authoritative whole-file reader built from the existing per-turn filesystem
callbacks. Tool registration remains independent of configuration so model tool
identities do not depend on whether a server is installed or running.

## Related code

- `docs/spec.md#language-intelligence`, `docs/spec.md#session-configuration`,
  and `docs/spec.md#isolation-and-memory` - observable configuration, query,
  filesystem-authority, lifecycle, and host-privilege requirements.
- `eng/architecture.md#session-and-turn-state`,
  `eng/architecture.md#configuration-and-credentials`, and
  `eng/architecture.md#extension-boundaries` - process inputs, activation-owned
  resources, dependency direction, and cleanup.
- `internal/settings/settings.go` and `cmd/ox/main.go` - global process settings
  and the one-time process wiring point.
- `internal/agent/{agent,tool,loop}.go` and `internal/tools` - activation
  lifecycle, per-turn filesystem callbacks, dispatch metadata, and the stable
  built-in catalog.
- `internal/lsp` - the complete lazy manager and model-facing query values this
  work exposes.
- `internal/e2e` - real-process configuration, ACP callback, provider, and
  lifecycle seams.
- `eng/plans/2026-09-05-003-lsp-process-adapter.md` and
  `eng/plans/2026-09-13-lsp-adapter-reconciliation.md` - the implemented and
  subsequently hardened adapter boundary.
- `~/src/references/repos/personal/eta/internal/agent/tools/lsp_*.go` and
  `~/src/references/repos/personal/eta/internal/lsp/diagnostics.go` - prior
  model contracts and deterministic renderers to adapt without retaining Eta's
  local-disk or read-evidence assumptions.

## Current state

- The adapter already owns command lookup, lazy startup, deadlines,
  cancellation, failed-server state, position conversion, document versions,
  result confinement, omission counts, and shutdown. Integration must not
  duplicate those protocol rules.
- Process settings are loaded once by `cmd/ox`; model settings are separately
  reloaded for each activation. `Invocation` already carries the turn's selected
  client-filesystem callbacks.
- A session currently owns and closes only its MCP activation resource after
  active work and configuration setters finish.
- Delegated tasks and child-agent tools have been removed. There is one tool
  catalog and one invocation path to integrate.
- Local whole-file reads are capped by `workspace.MaxFileBytes`; client-backed
  reads must retain the same bound before their text reaches the adapter.

## Structural considerations

- `internal/settings` owns the JSON vocabulary and startup validation,
  `internal/lsp` continues to own runtime definitions and protocol behavior, and
  `internal/agent` translates and wires the two. Do not add a general
  activation-resource registry.
- Language-server definitions are process inputs. Clone their maps and slices at
  resolution and agent construction so later mutation cannot change an
  activation.
- The session owns the concrete manager for cleanup; tool invocation receives
  only the query access it needs plus the already-selected filesystem callback.
- Language queries mutate server-side document synchronization state, so
  serialize them within a session while preserving concurrency between sessions.
- The tool catalog is stable even with no configured server. An unavailable,
  missing, crashed, or timed-out server becomes a clear model-visible tool
  failure and never triggers installation, restart, or another fallback.

## Test plan

- **Key behaviors to verify:** accepted global maps and normalized extensions;
  startup rejection of blank names or commands, empty or invalid extensions, and
  overlapping normalized extension ownership; workspace exclusion; immutable
  resolved values; strict tool arguments and reference defaults;
  selected-filesystem whole-file reads with the local size and UTF-8 rules;
  one-based positions; deterministic rendering, diagnostic completeness and
  version wording, omission counts, and bounded spills; approval-free code- and
  plan-mode registration; lazy startup; and complete session/process cleanup.
- **Test levels:** settings and renderer unit tests, focused agent lifecycle and
  invocation tests, and real-binary tests with the existing fake provider plus a
  helper-process language server.
- **Edge cases and failure modes:** no configured servers, no server for a file
  extension, a missing executable on first use, an unknown symbol kind or
  diagnostic severity, an incomplete push-diagnostic observation,
  client-supplied unsaved text, cancellation of one blocked language query while
  another session remains responsive, activation failure after manager
  construction, and errors from both LSP and MCP cleanup.
- **What not to test:** installed third-party servers or a live model. Keep LSP
  framing, encoding, confinement, crash, timeout, stale-diagnostic, and forced
  shutdown permutations in `internal/lsp`; process coverage needs only to prove
  their failures and results cross the integrated boundary correctly.

## Implementation plan

- Add `process.language_servers` to `internal/settings` as a map from stable
  server name to `command`, optional `args`, and nonempty `extensions`. Validate
  and normalize definitions in `ResolveProcess`, including duplicate ownership
  after case-folding and leading-dot removal, while preserving lazy executable
  lookup and rejecting the field in workspace settings.
- Pass cloned resolved definitions from `cmd/ox` into `internal/agent`. Create a
  manager for every new, loaded, or resumed activation; include it in failed
  activation cleanup; and close it after active work and configuration changes,
  joining its error with MCP cleanup. Process shutdown continues to use the
  session close path.
- Extend tool invocation with the activation's language queries. Build the
  adapter reader from the existing turn filesystem selection: resolve inside the
  workspace, call `fs/read_text_file` for a client-backed executor or the
  confined local whole-file read otherwise, enforce `workspace.MaxFileBytes`,
  and let the adapter validate UTF-8. These read-only queries neither record nor
  require mutation read evidence.
- Add strict `lsp_definition`, `lsp_references`, `lsp_document_symbols`,
  `lsp_workspace_symbols`, and `lsp_diagnostics` tools. File-position tools take
  workspace-relative `path` and one-based Unicode-scalar `line` and `column`;
  references defaults `include_declaration` to true. Classify navigation and
  symbol tools as searches and diagnostics as a read; make all five
  approval-free, plan-mode available, and session-serialized.
- Sort and render adapter values with workspace-relative paths, one-based
  ranges, stable names for known symbol kinds and diagnostic severities,
  explicit numeric fallbacks for unknown kinds, diagnostic source/code when
  present, the observed diagnostic version and completeness, and adapter
  omission counts. Send oversized text through the ordinary bounded spill
  renderer and report its spill path.
- Update `docs/settings.md` with the global JSON shape, normalization, lazy
  executable lookup and startup, deadlines, lifecycle, and host-privilege
  boundary. Update `eng/architecture.md` to remove the parked-adapter wording,
  mark the LSP dependency edges implemented, and include language queries in
  tool ownership. `docs/spec.md` already owns the target behavior and needs no
  duplicated implementation detail.

## Impact assessment

- **Code paths affected:** process configuration, executable wiring, session
  activation and close, tool invocation, and built-in tool rendering.
- **Data, protocol, or schema impact:** one additive global settings field and
  five new model-facing tool schemas; no ACP or durable-session format change.
- **Dependency or API impact:** `agent` and `tools` begin importing the existing
  `internal/lsp` package; no production dependency is added. Configured server
  processes retain host privileges in the session root.

## Validation

- Run the focused settings, tools, agent, LSP, and end-to-end tests while
  iterating.
- Run `make check` before marking language-server context complete in
  `eng/todo.md`.
