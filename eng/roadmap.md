# Ox Implementation Roadmap

The order in which Ox is built. This file keeps a brief summary of the most
recently completed milestone, then the scope and gates for work that remains. It
records scope and gates, not design. `eng/architecture.md` owns the design.

Milestones are ordered and build on each other. Slices within a milestone may be
planned and implemented independently when their contracts do not overlap.

Every milestone passes the complete local quality suite before it is complete:

```sh
make check
```

Changes to ACP-visible behavior must also pass the automated browser-client
smoke test.

## Porting policy

Ox should reuse proven code from the reference projects instead of rediscovering
its behavior. Port the smallest coherent seam, including its invariants and
tests. Do not copy a framework, UI, storage format, or protocol merely because
it surrounds useful code.

Every implementation plan must name the exact source files, behavior, and tests
being ported. It must also record deliberate omissions or ACP adaptations and
the Zed-visible acceptance case when the client boundary is involved.

Prefer the personal repositories. Use third-party repositories for the ACP
contract, Zed behavior, or a capability with no strong personal implementation.

## Prior-art map

Paths in this section are relative to `~/src/references/repos`.

### ACP and Zed behavior

Use the checked-in Zed implementation as the client oracle:

- `third-party/protocol/zed-acp/crates/agent_servers/src/acp.rs` for advertised
  client capabilities, permission cancellation, delegated filesystem calls, and
  terminal lifecycle handling;
- `third-party/protocol/zed-acp/crates/acp_thread/src/acp_thread.rs` and
  `terminal.rs` for session-update rendering and terminal behavior;
- `third-party/protocol/agent-client-protocol` for the canonical schema; and
- `third-party/protocol/acp-go-sdk` for typed APIs, cancellation, notification
  ordering, and cross-language JSON test cases.

### Later tools

Eta has the widest useful remaining tool set. Draw later capabilities from
`personal/eta/internal/agent/tools`, `internal/lsp`, `internal/permissions`,
`internal/skills`, and `internal/agents`. Its LSP navigation, todo, web, skills,
and agent-instruction behavior are the preferred starting points. Use
`personal/eta/internal/agent/tool.go` and `loop.go` for per-path locking and
concurrent tool execution where the current scheduler needs to grow.

### Durable sessions

Gamma has the strongest checkpoint and recovery semantics. Use
`personal/gamma/internal/agent/engine.go`, `state.go`, `store.go`, and their
invariant tests for monotonic event cursors, checkpoint parity, and reissuing
pending approvals and elicitation with stable identities.

Use `personal/beta/src/session.rs`, `agent.rs`, and `acp.rs` as a second ACP
implementation for lossless model-history persistence, replay, permission
memory, cancellation, and concurrent stdio test cases. Use the clean reference
snapshot, not Beta's current working tree.

### Supporting capabilities

- Use `personal/theta/internal/tui/preview.go` for permission-preview data and
  `internal/session/snapshot.go` for atomic snapshot tests. Zed owns the UI.
- Use `personal/iota/crates/adapter-git`, `crates/bin-web/src/lifecycle`, and
  `crates/app/src/tools/hashlines.rs` for later worktree isolation,
  merge-or-abandon lifecycle, and anchored edits.
- Use `personal/delta/cmd/fleur/session.go` and `events.go` for transcript
  pagination, reconnect, and interrupted-tool recovery cases when they become
  relevant to ACP replay.

### Deliberate non-ports

- Do not port Alpha's VS Code extension, Eta or Theta's terminal UI, or Delta
  and Iota's web UI. Zed owns interactive presentation.
- Do not port Gamma's custom Coral protocol. Translate its state invariants to
  ACP.
- Do not run a private terminal implementation when the ACP client capability is
  available and sufficient.
- Do not introduce Nu's provider framework or a multi-provider abstraction
  before Ox has a second provider.

## Most recently completed: production runtime core

Ox now has a durable ACP session runtime with safe local coding tools,
permissions, subagents, provider resilience, usage accounting, lifecycle replay,
and process-level recovery and cancellation coverage.

## Current milestone: client interoperability baseline

- [x] Document the development setup for launching Ox as a Zed ACP agent.
- [x] Add an automated browser-client smoke test that drives an independently
      maintained ACP UI against Ox through an upstream stdio bridge, covering
      initialization, authentication, session creation, prompt streaming,
      cancellation, and process shutdown. Pins: ACP UI artifact `4482f93a` from
      source `e6e36d05`, `@rebornix/stdio-to-ws@0.2.0`, and ACP v1 schema
      `8e3eb8f2`.
- [ ] Record and test the capabilities the browser client advertises, including
      behavior when an optional capability is absent.
- [ ] Verify that every supported prompt content block and session update
      renders correctly in the browser client.
- [ ] Verify that protocol errors reach the browser client as useful errors and
      that logs never contaminate stdout.
- [ ] Keep the pinned browser client, stdio bridge, checked-in Zed ACP reference
      snapshot, and ACP schema version recorded with each interoperability pass.

## Milestone: client-delegated tools

- [ ] Use client-delegated `fs/read_text_file` and `fs/write_text_file` when Zed
      advertises those capabilities.
- [ ] Use the ACP terminal lifecycle for shell commands: `terminal/create`,
      `terminal/output`, `terminal/wait_for_exit`, `terminal/kill`, and
      `terminal/release`. Cancellation must terminate the command tree and
      release the terminal exactly once.
- [ ] Define explicit behavior for clients without delegated filesystem or
      terminal capabilities.
- [ ] Pass the automated browser-client smoke test with read, edit, and shell
      workflows.

## Milestone: durable session completion

- [ ] Assign monotonic event sequence numbers and replay from a stable cursor.
- [ ] Implement `session/fork`.
- [ ] Persist the relevant negotiated client capabilities with the request
      configuration.
- [ ] Add checkpoints and compaction that preserve tool-call pairing, event
      sequence continuity, pending approvals, and exact replay semantics.
- [ ] Reissue pending permission or elicitation requests after restart with
      stable identities and generations.
- [ ] Test pending-permission recovery and checkpoint/load parity through the
      real stdio process boundary.

## Milestone: context and diagnostics

- [ ] Track changed files alongside usage, cost, and context capacity.
- [ ] Compact context without breaking tool-call pairing, replay, or the durable
      record; keep the uncompacted log authoritative.
- [ ] Emit a sanitized machine-readable trace.

## Milestone: agent capabilities

- [ ] Port Eta's todo tool with state included in replay and compaction.
- [ ] Load workspace instructions from `AGENTS.md`.
- [ ] Discover and load Agent Skills on demand.
- [ ] Implement `session/set_mode` and `session/set_config_option`.
- [ ] Add MCP servers without weakening the ACP client boundary.
- [ ] Support mid-turn steering and queued follow-up input.
- [ ] Port Eta's LSP definition, reference, symbol, and diagnostics tools only
      where they complement rather than duplicate Zed client context.
- [ ] Add web search and fetch only after their trust, citation, and
      content-size contracts are explicit.

## Milestone: differentiators

- [ ] Port Iota's one-worktree-per-session isolation and explicit merge,
      abandon, and rollback lifecycle.
- [ ] Evaluate Iota's hashline-anchored edits against Alpha's exact-edit
      contract; keep only one model-facing edit primitive unless evidence shows
      both are needed.
- [ ] Add persistent semantic workspace memory with bounded retention.
- [ ] Add automatic task decomposition backed by a durable subtask queue.
- [ ] Add a Harbor evaluation adapter and a small regression task suite.

## ACP method coverage

Requests and notifications Ox accepts from an ACP client:

- [x] `initialize`
- [x] `session/new`
- [x] `session/load`
- [x] `session/prompt`
- [x] `session/cancel`
- [x] `authenticate`
- [x] `logout`
- [x] `session/list`
- [x] `session/delete`
- [x] `session/close`
- [ ] `session/fork`
- [x] `session/resume`
- [ ] `session/set_mode`
- [ ] `session/set_config_option`
- [x] `$/cancel_request`

Requests Ox sends to an ACP client:

- [x] `session/request_permission`
- [ ] `fs/read_text_file`
- [ ] `fs/write_text_file`
- [ ] `terminal/create`
- [ ] `terminal/output`
- [ ] `terminal/wait_for_exit`
- [ ] `terminal/kill`
- [ ] `terminal/release`

Notifications Ox sends to an ACP client:

- [x] `session/update`
