# Workspace Memory

## Sources

- `docs/spec.md#isolation-memory-and-delegated-work` — fact types, ownership,
  retention, bounds, retrieval order, permission, replay, and session-deletion
  behavior
- `eng/roadmap.md#workspace-memory` — slice scope and completion gates
- `eng/architecture.md#session-and-turn-state` and
  `eng/architecture.md#extension-boundaries` — shared workspace ownership,
  private atomic storage, locking, and durable tool-result boundaries
- `internal/agent/{agent,store,tool,loop}.go` and
  `internal/tools/{tools,todo}.go` — data-directory, deletion, invocation
  callback, registration, validation, permission, and durable-result patterns
- `~/src/references/repos/personal/kappa/taikonaut/memory_manager.py` — typed
  fact, expiry, supersession, and source-session concepts to adapt without its
  automatic extraction, SQLite, Chroma, embeddings, or access-based TTL renewal

## Goal

Add explicit, permission-aware workspace memory whose facts can be written,
searched, superseded, and deleted across sessions while preserving deterministic
replay and strict workspace ownership.

## Implementation

- `internal/agent/memory.go` and platform lock helpers — add one private memory
  store under the Ox data directory, keyed by a digest of the canonical
  workspace root and recording that root in the versioned document. Serialize
  operations in-process and hold a separate OS lock file across each
  read/modify/write. Persist owner-only JSON by synced temporary-file rename and
  directory sync; reject malformed, mismatched, oversized, or unsupported store
  data without treating it as empty.
- Store only active facts. Each fact has a stable random ID, `preference`,
  `decision`, or `finding` type, source session, UTC creation and fixed expiry
  timestamps, content, and an optional superseded ID. Drop expired facts while
  holding the write lock; supersession atomically removes the named active fact
  and inserts its replacement. Enforce 2 KiB UTF-8 content and 256 active facts
  before committing, with an injected clock for deterministic tests.
- `internal/tools/memory.go`, `internal/tools/tools.go`, and
  `internal/agent/tool.go` — register strict `memory_search`, `memory_write`,
  and `memory_delete` tools and route their validated operations through
  invocation callbacks owned by the agent. Search is approval-free,
  parallel-safe, and available in plan mode. Write and delete use the ordinary
  approval path, are serialized with effectful calls, and are excluded from plan
  mode. Parent and child agents may use the same workspace-scoped store.
- Search all whitespace-separated terms as case-insensitive content substrings;
  an empty query lists active facts. Sort newest first and then by ID, return at
  most ten facts within a complete 8 KiB rendered result, and expose IDs and
  metadata needed for explicit deletion or supersession. Reads never update a
  fact or extend expiry.
- `internal/agent/agent.go` and `cmd/ox/main.go` — derive the production memory
  directory from the same XDG data root as sessions, initialize it without a
  user-facing setting, attach callbacks by canonical session root and source
  session, and remove that session's facts before deleting its inactive session
  log. Keep cleanup idempotent so an interrupted delete can safely repeat.
- `internal/agent/prompt.go` — identify retrieved memory as workspace data that
  cannot override the current request or instructions, and tell models that
  storing, superseding, and deleting facts is explicit rather than automatic.

## Tests

- Store tests inject time and cover fixed expiry, no renewal on read, all-term
  matching, empty search, deterministic ordering, ten-result and 8 KiB caps,
  UTF-8 byte limits, capacity, supersession, deletion, corrupt/version/root
  rejection, restart, and concurrent instances updating one workspace without
  lost writes.
- Tool and integration tests prove strict schemas, permission decisions, plan
  mode, parent/child access, durable replay of retrieved content after the live
  fact changes, cross-session visibility at one canonical root, separate
  worktree ownership, and session deletion removing only its sourced facts.
- Fault tests fail atomic replacement and session-log deletion to prove rejected
  mutations leave the previous store intact and repeated source-session cleanup
  is safe.

## Decisions

- `memory_write` accepts optional `supersedes`; this makes replacement one
  permissioned atomic operation instead of exposing a second mutation step.
- The store retains active facts only. The replacement fact preserves the
  superseded identity, while expired, deleted, and replaced content cannot grow
  a second unbounded history outside the session logs that recorded tool use.
