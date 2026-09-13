# TODO

- [x] ACP-first foundation
  - [x] Build the stdio server, streaming prompt turns, prompt content support,
        configuration, credentials, and authentication
  - [x] Add end-to-end process coverage and concurrent-session safety
  - [x] Document local ACP-client setup
  - [x] Confine workspace access and delegate supported client filesystem and
        terminal operations
  - [x] Freeze each activation's negotiated executor capabilities and verify
        local and client-backed executor conformance
- [x] Durable sessions and working context
  - [x] Add checkpoints, replay, permission and interrupted-effect recovery,
        changed-file reporting, compaction, request admission, and diagnostic
        trace
  - [x] Add process and session configuration, todo state, root instructions,
        workspace skills, form questions, and MCP transport, catalog, and
        activation
- [x] External context and evaluation
  - [x] Add bounded public web fetch, MCP-supplied search, explicit workspace
        memory, and the ACP evaluation baseline
  - [x] Retain exact editing after evaluation
  - [x] Verify client-owned worktrees
- [x] Simplify the agent runtime
  - [x] Remove delegated tasks and child-agent runtime
- [x] Address findings in the
      [consolidated codebase review](reviews/2026-09-12-consolidated-codebase-review.md)
  - [x] Synchronize live-turn session-state access and cover concurrent
        configuration changes (F01, F36)
  - [x] Harden reusable shell-rule derivation (F02)
  - [x] Bound model-chosen file reads and `read_file` output (F03, F26)
  - [x] Bound checkpoint growth and keep aged sessions reloadable (F04)
  - [x] Remove quadratic durable-state cloning from record commits (F05)
  - [x] Consolidate permission approval and move tests onto its production path
        (F06)
  - [x] Make multimodal admission match advertised ACP capabilities (F07)
  - [x] Enforce coherent edit evidence, replacement, and text-format semantics
        (F08, F09)
  - [x] Keep read evidence and write verification on a coherent filesystem
        executor (F10)
  - [x] Make streamed tool-output retention bounded and incremental (F11, F17)
  - [x] Use consistent token units for context occupancy and compaction (F12)
  - [x] Correct provider stream retry detection and classification (F13, F25)
  - [x] Repair model-catalog caching and refresh ownership (F14)
  - [x] Make `session/list` bounded, resilient, and cursor-tested (F15)
  - [x] Eliminate redundant provider-request encoding (F16)
  - [x] Stop cloning frozen turn configuration inside per-call loops (F18)
  - [x] Preserve partial grep results and reduce workspace-walk overhead (F19,
        F34, F41)
  - [x] Reconcile the parked LSP adapter and harden it before integration (F20,
        F28)
  - [x] Centralize confined regular-file reads at the workspace boundary (F21)
  - [x] Remove delegated-task remnants and repair affected docs and tests (F22,
        F24, F31)
  - [x] Simplify turn orchestration and lifecycle plumbing (F23)
  - [x] Centralize process path resolution and XDG validation (F29)
  - [x] Remove duplicate ACP activation validation (F30)
  - [x] Deduplicate small safety and protocol rules (F32)
  - [x] Make atomic file replacement crash-safe and cover its sync failures
        (F27, F43)
  - [x] Reduce configuration copying and prevent settings-result aliasing (F33,
        F40)
  - [x] Simplify durable-state transition code (F37)
  - [x] Remove production test artifacts and test-only mutable seams (F38)
  - [x] Standardize session-update values and remove trivial dead or misleading
        code (F35, F39)
  - [x] Add focused configuration-option error coverage (F42)
  - [x] Add focused MCP and trace error-path coverage (F44)
  - [x] Replace wall-clock test heuristics with deterministic signals (F45)
- [x] Concurrent subagents
  - [x] Add turn-scoped concurrent children with lifecycle control and
        bidirectional messaging
  - [x] Remove model-request and tool-loop iteration ceilings from primary and
        child agents
- [x] Language-server context
  - [x] Add the lazy LSP process adapter
  - [x] Integrate language tools with session activation
- [x] Accept MCP servers that negotiate older protocol revisions
- [x] Add auto-approval mode with one parent/child permission policy, durable
      selection, shipped-process coverage, and documentation
- [x] Address remaining findings in the
      [repository rough-edges review](reviews/2026-09-13-003-repository-rough-edges-review.md)
      and
      [full-codebase testing review](reviews/2026-09-13-002-full-codebase-testing-review.md)
  - [x] Harden turn finalization and cancellation history for malformed or
        partial provider tool calls
  - [x] Reconcile OpenRouter and MCP catalog freshness with bounded refresh cost
  - [x] Stabilize the required gate and complete shipped-process and evaluation
        coverage
  - [x] Settle the platform contract and repair documentation and line-ending
        portability edges
- [x] Address findings in the
      [completion review](reviews/2026-09-13-004-completion-review.md)
  - [x] Keep an MCP listing refresh cancellable and confine activation's
        ownership of the listing fields
  - [x] Serve and bound a held model catalog when a refresh fails
  - [x] Fail an end-to-end test whose repeating model response is never used
- [x] Configure and switch complete per-model request profiles
- [x] Publish versioned release binaries
- [ ] Address findings in the
      [session tool-friction review](reviews/2026-09-13-006-session-tool-friction-review.md)
  - [ ] Remove session read evidence from file mutations and shell execution
        (F01)
  - [ ] Preserve validator exit status without model-added output-truncation
        pipelines (F02)
  - [ ] Add open-stdin process-harness support and prove `ox --version` exits
        without reading input (F03)
