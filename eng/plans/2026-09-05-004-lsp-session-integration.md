# Language Tools Session Integration

## Sources

- `docs/spec.md#language-intelligence`, `docs/spec.md#session-configuration`,
  and `docs/spec.md#isolation-memory-and-delegated-work` — configuration
  authority, tool behavior, plan-mode access, filesystem selection, and
  host-privilege boundary
- `eng/roadmap.md#language-server-context` — slice scope and completion gates
- `eng/architecture.md#session-and-turn-state`,
  `eng/architecture.md#configuration-and-credentials`, and
  `eng/architecture.md#extension-boundaries` — process inputs, per-activation
  resources, tool wiring, and cleanup
- `internal/settings/settings.go`, `internal/agent/{agent,tool,loop}.go`,
  `internal/tools`, and `internal/e2e` — current configuration, activation,
  selected filesystem, built-in tool, and real-process seams
- `~/src/references/repos/personal/eta/internal/agent/tools/lsp_*.go` — model
  contracts and deterministic rendering patterns to adapt
- `eng/plans/2026-09-05-003-lsp-process-adapter.md` — required adapter starting
  point

## Goal

Expose the LSP adapter through global process configuration and five read-only
tools shared by parent and child turns. Each session activation owns its lazy
server processes, uses client-backed unsaved content when selected, and releases
all processes on close or process exit.

## Implementation

- `internal/settings` — add `process.language_servers`, a map from stable server
  name to `command`, optional `args`, and nonempty `extensions`. Validate names,
  commands, extensions, and duplicate extension ownership at startup; normalize
  extensions without leading dots in the resolved process value. Keep the object
  global-only through the existing workspace decoder boundary.
- `cmd/ox` and `internal/agent` — pass the resolved definitions as immutable
  process input. Create one lazy `lsp.Manager` for each new, loaded, or resumed
  activation without starting a command. Store it on the active session, make
  failed activation cleanup atomic, and close it only after active work and
  configuration setters finish. Process shutdown and session close share the
  same cleanup path.
- Extend tool invocation with the activation's language manager and the turn's
  frozen client-filesystem callbacks. A query resolves its input through the
  existing workspace boundary, reads through `fs/read_text_file` when that
  executor was selected and otherwise through confined local handles, validates
  UTF-8, then supplies those exact bytes to LSP synchronization. Do not require
  prior read evidence for these nonmutating queries.
- `internal/tools` — add `lsp_definition`, `lsp_references`,
  `lsp_document_symbols`, `lsp_workspace_symbols`, and `lsp_diagnostics` with
  strict schemas. File-position tools accept workspace-relative `path`,
  one-based `line`, and one-based Unicode-scalar `column`; references also
  accepts `include_declaration`. Render deterministic bounded results with
  relative paths, symbol kinds, observed document versions, diagnostic
  severity/source/code, omission counts, and explicit unavailable or unknown
  states.
- Register all five as approval-free read/search tools available to parent and
  child in both code and plan modes. They never mutate files, format, rename,
  apply code actions, run automatically after edits, install a server, or expose
  an IDE side channel. No configured server produces a clear tool result rather
  than starting an implicit fallback.
- `docs/settings.md` — document the shipped global JSON shape, validation, lazy
  startup, deadlines, and the fact that configured servers run with host
  privileges in the session root. Keep target behavior in `docs/spec.md`.

## Tests

- Settings tests prove accepted maps, normalization, startup errors, duplicate
  extension rejection, and workspace exclusion. Command lookup uses a temporary
  `PATH`; tests never require an installed language server.
- Tool tests prove strict arguments, confinement, UTF-8 handling, one-based
  positions, deterministic truncation, omission reporting, no read-evidence or
  permission gate, and parent/child plus plan-mode registration.
- Real-binary tests use a configurable helper LSP and fake provider to prove
  lazy startup, each tool's model-visible request/result, client unsaved-file
  synchronization, cancellation with an unrelated responsive session, crashed
  and timed-out servers, stale diagnostic handling, and session/process
  shutdown. Assert no mutation, install, formatting, or automatic diagnostic
  request occurs.

## Sequence

This is the second of two plans. It requires the complete adapter from the first
plan and completes the language-server context slice.

## Decisions

- LSP tools remain part of the stable built-in catalog even when no server is
  configured; availability is decided at invocation so replay and model tool
  identities do not depend on a live child process.
