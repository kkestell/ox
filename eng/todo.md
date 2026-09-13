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
- [ ] Address findings in the
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
  - [ ] Simplify turn orchestration and lifecycle plumbing (F23)
  - [ ] Centralize process path resolution and XDG validation (F29)
  - [ ] Remove duplicate ACP activation validation (F30)
  - [ ] Deduplicate small safety and protocol rules (F32)
  - [ ] Make atomic file replacement crash-safe and cover its sync failures
        (F27, F43)
  - [ ] Reduce configuration copying and prevent settings-result aliasing (F33,
        F40)
  - [ ] Simplify durable-state transition code (F37)
  - [ ] Remove production test artifacts and test-only mutable seams (F38)
  - [ ] Standardize session-update values and remove trivial dead or misleading
        code (F35, F39)
  - [ ] Add focused configuration-option error coverage (F42)
  - [ ] Add focused MCP and trace error-path coverage (F44)
  - [ ] Replace wall-clock test heuristics with deterministic signals (F45)
- [ ] Language-server context
  - [x] Add the lazy LSP process adapter
  - [ ] Integrate language tools with session activation
