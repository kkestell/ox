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

Changes to ACP-visible behavior must also pass the Zed smoke-test checklist.

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

### Tool core

Alpha has the strongest safety-critical tool core. Port from:

- `personal/alpha/runtime/internal/tools` for read, write, exact edit, glob,
  grep, shell, and bounded tool-result behavior;
- `personal/alpha/runtime/internal/workspace` for `os.Root` confinement,
  ignore-aware traversal, streaming output, and spill files; and
- `personal/alpha/runtime/internal/agent/reads.go`, `tool.go`, and `approval.go`
  for read evidence, execution, permissions, and ACP lifecycle projection.

Keep Alpha's read-before-mutation rule, atomic writes, file-mode and text-format
preservation, symlink safety, bounded output, and whole-process-group shell
cancellation. Adapt filesystem and terminal execution to ACP client calls when
Zed advertises those capabilities.

Eta has the widest useful tool set. Draw later capabilities from
`personal/eta/internal/agent/tools`, `internal/lsp`, `internal/permissions`,
`internal/skills`, and `internal/agents`. Its LSP navigation, todo, web, skills,
and subagent behavior are the preferred starting points. Use
`personal/eta/internal/agent/tool.go` and `loop.go` for per-path locking and
concurrent tool execution. Alpha remains the authority for the safety-critical
core.

### Durable sessions

Gamma has the strongest durable-state semantics. Port the state machine and
failure invariants from `personal/gamma/internal/agent/engine.go`, `state.go`,
and `store.go`. Adapt the invariant tests in `engine_test.go` and
`internal/checker` to ACP. Append and sync records before publishing events or
swapping live state. Recover a torn final record, preserve monotonic event
sequence numbers, checkpoint without dangling tool calls, and reissue pending
approvals and elicitation with stable identities.

Alpha has the best production ACP session implementation. Use
`personal/alpha/runtime/internal/agent/store.go`, `state.go`, and `lock.go` for
owner-only append logs, file locking, size limits, repair, replay, activation,
and ACP session lifecycle methods. Gamma is the semantic authority; Alpha is the
implementation donor.

Use `personal/beta/src/session.rs`, `agent.rs`, and `acp.rs` as a second ACP
implementation for lossless model-history persistence, replay, permission
memory, cancellation, and concurrent stdio test cases. Use the clean reference
snapshot, not Beta's current working tree.

### Supporting capabilities

- Use `personal/alpha/runtime/internal/openrouter` for streaming, catalog
  caching, retry, reasoning-detail, and usage behavior. Compare the provider
  boundary with `personal/nu/crates/ur-core` and `ur-openai-compat`; port
  behavior, not the Rust abstraction hierarchy.
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

## Most recently completed: protocol and prompt foundation

Ox now has its Go binary, ACP stdio server and validation boundary, black-box
test harness, streaming prompt loop, complete prompt-content handling,
concurrent in-memory sessions, layered model configuration, OpenRouter
credential management, authentication, and confined session workspaces.

## Current milestone: Zed interoperability baseline

- [ ] Document the development setup for launching Ox as a Zed ACP agent.
- [ ] Add a repeatable Zed smoke-test checklist covering initialization,
      authentication, session creation, prompt streaming, cancellation, and
      clean shutdown.
- [ ] Record and test the capabilities Zed advertises, including behavior when
      an optional capability is absent.
- [ ] Verify that every supported prompt content block and session update
      renders correctly in Zed.
- [ ] Verify that protocol errors reach Zed as useful JSON-RPC errors and that
      logs never contaminate stdout.
- [ ] Keep the checked-in Zed ACP reference snapshot and ACP schema version
      recorded with each interoperability pass.

## Milestone: ACP-native tools and permissions

- [ ] Port Alpha's model-facing read, write, exact-edit, glob, grep, and shell
      contracts behind ACP filesystem and terminal adapters.
- [ ] Require session-scoped read evidence before mutation and serialize
      conflicting mutations without serializing independent tool calls.
- [ ] Preserve file mode, BOM, line endings, and trailing-newline behavior; make
      writes atomic and edit failures diagnostic.
- [ ] Define ignore, hidden-file, pagination, truncation, spill, and maximum
      result-size behavior for every discovery or output-producing tool.
- [ ] Project every tool call and state transition through ACP session updates.
- [ ] Implement `session/request_permission`, including allow-once, reusable
      grants, cancellation, mutation previews, and denial feedback to the model.
- [ ] Use client-delegated `fs/read_text_file` and `fs/write_text_file` when Zed
      advertises those capabilities.
- [ ] Use the ACP terminal lifecycle for shell commands: `terminal/create`,
      `terminal/output`, `terminal/wait_for_exit`, `terminal/kill`, and
      `terminal/release`. Cancellation must terminate the command tree and
      release the terminal exactly once.
- [ ] Define explicit behavior for clients without delegated filesystem or
      terminal capabilities.
- [ ] Run independent model-requested tools concurrently while preserving model
      result order and preventing same-file races.
- [ ] Extend the black-box harness with a scripted ACP client that asserts
      request ordering, permission decisions, read-before-write enforcement,
      tool updates, output bounds, and cancellation.
- [ ] Pass the Zed smoke-test checklist with read, edit, and shell workflows.

## Milestone: durable session lifecycle

- [ ] Port Gamma's append-before-publish transaction boundary and Alpha's
      owner-only, locked, versioned append log with size limits, sync, and
      torn-tail repair.
- [ ] Persist exact model messages and ACP-visible events so `session/load`
      reproduces both model history and a lossless client transcript.
- [ ] Assign monotonic event sequence numbers and replay from a stable cursor.
- [ ] Implement `session/list`, `session/delete`, `session/close`,
      `session/fork`, and `session/resume` where supported by ACP.
- [ ] Add single-writer session activation locking, idempotent close and delete,
      and explicit recovery for interrupted turns and incomplete tool calls.
- [ ] Freeze the model request prefix, tool declarations, model, and relevant
      client capabilities when a session is created.
- [ ] Add checkpoints and compaction that preserve tool-call pairing, event
      sequence continuity, pending approvals, and exact replay semantics.
- [ ] Reissue pending permission or elicitation requests after restart with
      stable identities and generations.
- [ ] Test restart, replay, resume, and concurrent activation through the real
      stdio process boundary, including append failure, torn tails, interrupted
      tools, pending permissions, and checkpoint/load parity.

## Milestone: provider robustness and accounting

- [ ] Port Alpha's bounded provider retry behavior. Never retry after the first
      response delta has been published.
- [ ] Port Alpha's cached OpenRouter model catalog and validation behavior.
- [ ] Round-trip raw reasoning details needed for provider cache continuity.
- [ ] Track usage, cost, context capacity, and changed files.
- [ ] Compact context without breaking tool-call pairing, replay, or the durable
      record; keep the uncompacted log authoritative.
- [ ] Emit a sanitized machine-readable trace.

## Milestone: agent capabilities

- [ ] Port Eta's delegated subagent semantics with nested ACP activity reporting
      and durable child ownership.
- [ ] Port Eta's todo tool with state included in replay and compaction.
- [ ] Load workspace instructions from `AGENTS.md`.
- [ ] Discover and load Agent Skills on demand.
- [ ] Implement `session/set_mode` and `session/set_config_option`.
- [ ] Add MCP servers without weakening the ACP client boundary.
- [ ] Generate session titles.
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
- [ ] `session/load`
- [x] `session/prompt`
- [x] `session/cancel`
- [x] `authenticate`
- [x] `logout`
- [ ] `session/list`
- [ ] `session/delete`
- [ ] `session/close`
- [ ] `session/fork`
- [ ] `session/resume`
- [ ] `session/set_mode`
- [ ] `session/set_config_option`
- [x] `$/cancel_request`

Requests Ox sends to an ACP client:

- [ ] `session/request_permission`
- [ ] `fs/read_text_file`
- [ ] `fs/write_text_file`
- [ ] `terminal/create`
- [ ] `terminal/output`
- [ ] `terminal/wait_for_exit`
- [ ] `terminal/kill`
- [ ] `terminal/release`

Notifications Ox sends to an ACP client:

- [x] `session/update`
