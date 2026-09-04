# Ox Implementation Roadmap

The order in which Ox is built. This file keeps a brief summary of the most
recently completed milestone, then the scope and gates for work that remains. It
records scope and gates, not design. `eng/architecture.md` owns the design.

Milestones are ordered and build on each other. Slices within a milestone may be
planned and implemented independently when their contracts do not overlap.

Every milestone passes the project gates before it is complete:

```sh
make check
```

Every milestone that changes ACP-visible behavior also passes the automated
browser-client smoke test and checks the changed behavior against the pinned ACP
v1 schema:

```sh
make test-client
```

The gates listed under each slice are the behavior that slice must prove on top
of that. Behavior the pinned browser client cannot exercise is proved through
the process harness in `internal/e2e` against the schema.

`.gitmodules` pins the browser client, the browser suite's lockfile pins the
stdio bridge, and the prior-art map below pins the schema and the browser
client's source. Move them together when an interoperability pass adopts newer
versions.

## Porting policy

Ox should reuse proven code from the reference projects instead of rediscovering
its behavior. Port the smallest coherent seam, including its invariants and
tests. Do not copy a framework, UI, storage format, or protocol merely because
it surrounds useful code.

Every implementation plan must name the exact source files, behavior, and tests
being ported. It must also record deliberate omissions or ACP adaptations and
the browser-client acceptance case when the client boundary is involved.

Prefer the personal repositories. Use third-party repositories for the ACP
contract, ACP client behavior, or a capability with no strong personal
implementation.

## Prior-art map

Paths in this section are relative to `~/src/references/repos`.

### ACP and client behavior

Use the canonical schema as the protocol oracle and the pinned browser client as
the interoperability oracle:

- `third-party/protocol/agent-client-protocol` at `8e3eb8f2` for the canonical
  v1 schema, the drafts it marks unstable, and the RFDs behind them;
- `third-party/protocol/acp-go-sdk` for typed APIs, cancellation, notification
  ordering, and cross-language JSON test cases; and
- the ACP UI web release checked out as the `internal/e2e/browser/acp-ui`
  submodule, built from `formulahendry/acp-ui` at `e6e36d05`, for what a real
  client sends, renders, and tolerates.

### Later tools

Eta has the widest useful remaining tool set. Draw later capabilities from
`personal/eta/internal/agent/tools`, `internal/lsp`, `internal/permissions`,
`internal/skills`, and `internal/agents`. Its LSP navigation, todo, web, skills,
and agent-instruction behavior are the preferred starting points. Use
`personal/eta/internal/agent/tool.go` and `loop.go` for per-path locking and
concurrent tool execution where the current scheduler needs to grow.

Use `personal/mu/src/agent/mcp.ts` for starting namespaced MCP servers and
exposing their tools. Use Alpha's steering persistence in
`personal/alpha/runtime/internal/agent/loop.go` for queued mid-turn input once
its ACP entry point is settled.

### Durable sessions

Gamma has the strongest checkpoint and recovery semantics. Use
`personal/gamma/internal/agent/engine.go`, `state.go`, `store.go`, and their
invariant tests for checkpoint parity and for reissuing pending approvals with
stable identities.

Use `personal/beta/src/session.rs`, `agent.rs`, and `acp.rs` as a second ACP
implementation for lossless model-history persistence, replay, permission
memory, cancellation, and concurrent stdio test cases. Use the clean reference
snapshot, not Beta's current working tree.

### Context and diagnostics

Use `personal/beta/src/compaction.rs`, `personal/eta/internal/agent/compact.go`,
and `personal/delta/cmd/fleur/compaction.go` for model-context compaction that
keeps tool calls paired. Use `personal/beta/src/trace.rs` for a sanitized
machine-readable trace and `personal/beta/src/session.rs` for changed-file
accounting.

### Supporting capabilities

- Use `personal/theta/internal/tui/preview.go` for permission-preview data and
  `internal/session/snapshot.go` for atomic snapshot tests. The ACP client owns
  the UI.
- Use `personal/iota/crates/adapter-git`, `crates/bin-web/src/lifecycle`, and
  `crates/app/src/tools/hashlines.rs` for later worktree isolation,
  merge-or-abandon lifecycle, and anchored edits.
- Use `personal/delta/cmd/fleur/session.go` and `events.go` for transcript
  pagination, reconnect, and interrupted-tool recovery cases when they become
  relevant to ACP replay.
- Use `personal/kappa/taikonaut/memory_manager.py` and `task_queue.py` for
  semantic workspace memory with retention and for a durable subtask queue.
- Use `personal/eta/harbor` and `personal/beta/evals/src/coral_harbor` for the
  Harbor evaluation adapter.

### Deliberate non-ports

- Do not port Alpha's VS Code extension, Eta or Theta's terminal UI, or Delta
  and Iota's web UI. The ACP client owns interactive presentation.
- Do not port Alpha's `_amber/session/steer` extension method. Steering enters
  through an ACP mechanism settled before it is planned.
- Do not port Gamma's custom Coral protocol. Translate its state invariants to
  ACP.
- Do not run a private terminal implementation when the ACP client capability is
  available and sufficient.
- Do not introduce Nu's provider framework or a multi-provider abstraction
  before Ox has a second provider.

## Most recently completed: durable session completion

Ox now loads durable sessions from checkpoints, preserves each activation's
negotiated executor choices, and recovers turns that were waiting for
permission.

## Milestone: context and diagnostics

Ox reports what a session has changed and consumed, keeps long sessions within
the model's context window, and emits a trace that tools can read without
exposing secrets or workspace content.

### Model-context compaction

**Build**

- When a session's context approaches the model's window, replace older model
  history with a summary the model produced, and record the compaction durably.
- Keep the uncompacted log authoritative. Compaction changes what the model sees
  on the next turn, not what `session/load` replays.

**Gates**

- Compaction never separates a tool call from its result, never drops a pending
  tool result, and never runs during an open turn.
- `session/load` replays the full transcript after compaction, and the next turn
  sends the compacted history.
- A session restarted after compaction continues from the recorded compaction
  rather than recomputing it, and `usage_update` reports the compacted size.
- The system prompt and tool declarations are never summarized away.

### Sanitized trace

**Build**

- Emit a machine-readable trace of turns, provider requests, tool calls,
  permissions, and stop reasons. It never goes to standard output.

**Gates**

- The trace contains no credential, prompt text, model output, file content, or
  shell output. Identifiers, kinds, timings, sizes, and outcomes are enough to
  reconstruct a turn's shape.
- Standard output still carries only ACP messages while tracing is on.
- Every trace line is one JSON object that names its session and, where one
  applies, its tool-call identity.

## Milestone: agent capabilities

Ox gains the working-context tools of the reference harnesses: todo state,
workspace instructions, skills, session modes and configuration options, a
process configuration file, MCP servers, steering, language-server tools, and
web tools. Each slice enters through standard ACP or a model-facing tool, not a
client-specific channel.

### Todo tool

**Build**

- Port Eta's todo tool, keep its state in the durable record, and publish it as
  the ACP `plan` update.

**Gates**

- Each todo change emits one `plan` update whose entries match the tool's state,
  and `session/load` replays the latest plan.
- Todo state survives model-context compaction and restart.
- Invalid todo arguments fail the tool call with a message the model can act on.

### Workspace instructions

**Build**

- Load `AGENTS.md` from the session root into the frozen request configuration
  at activation.

**Gates**

- The instructions reach the model on every turn, a missing file adds nothing,
  and a file outside the root or through a symlink is never read.
- Changing the file between activations appends one configuration change, and
  the configuration stays frozen during a turn.
- An unreadable file is reported with its path rather than silently skipped.

### Skills

**Build**

- Discover Agent Skills, list their names and descriptions in the prompt, and
  load a skill's body only when the model asks for it.

**Gates**

- Only skill metadata enters the prompt until a skill is loaded, and loading
  reads confined paths and records the content as a tool result.
- A malformed skill is reported by path and does not hide the others.
- The listed skills are part of the frozen configuration and change only between
  activations.

### Session modes and configuration options

**Build**

- Settle first: which modes Ox offers and which configuration options besides
  the model it exposes.
- Return modes and configuration options from `session/new`, `session/load`, and
  `session/resume`, implement `session/set_mode` and
  `session/set_config_option`, and expose model selection as a configuration
  option.

**Gates**

- A model change is validated against the model catalog before it is accepted,
  recorded as a configuration change, applied from the next turn, and announced
  with `config_option_update`.
- The running turn keeps its frozen configuration.
- `current_mode_update` and `config_option_update` are replayed on
  `session/load` so a reconnecting client shows the current state.
- An unknown mode or option identifier is a useful JSON-RPC error.

### Process configuration file

**Build**

- Move process settings to the global configuration file with explicit CLI flags
  for overrides, keep credentials in ACP authentication and the keyring, and
  replace test-only environment switches with explicit test entry points or CLI
  flags.
- Remove `OX_MODEL`, `OX_LOG_LEVEL`, `OX_OPENROUTER_BASE_URL`,
  `OX_KEYRING_DISABLED`, `OPENROUTER_API_KEY`, `OX_LIVE_TESTS`, and
  `OX_LIVE_MODEL`.

**Gates**

- The shipped binary reads no `OX_` or `OPENROUTER_` environment variable, and a
  workspace still cannot redirect provider requests or change process logging.
- The end-to-end and browser harnesses drive the binary through the explicit
  entry points and flags, and the live checks take the credential from `.env`
  through a test entry point rather than through the shipped binary's
  environment.
- `docs/settings.md`, `docs/zed.md`, and `eng/architecture.md` describe the file
  and flags and name no environment variable.

### MCP servers

**Build**

- Accept the `mcpServers` a client passes to `session/new`, `session/load`, and
  `session/resume`, advertise `mcpCapabilities` only for transports Ox
  implements, and expose each server's tools to the model.

**Gates**

- MCP tools go through the same permission and durable-record path as local
  tools and replay with the same ACP tool-call updates.
- A server that fails to start or list tools fails the session request with an
  error naming the server, and a server that fails mid-session fails its tool
  calls rather than the turn.
- The server list is part of the frozen request configuration, and a transport
  Ox does not advertise is rejected at the boundary.

### Steering and queued input

**Build**

- Settle first: how mid-turn input enters over ACP while each session admits one
  prompt turn at a time.
- Persist queued input before it is acknowledged, deliver it at the next model
  boundary, and keep the order the client saw.

**Gates**

- Input queued during a turn is durable before the client learns it was
  accepted, appears in the model history at the boundary where it was applied,
  and replays in that position.
- Cancelling the turn records what happened to acknowledged input so the next
  turn neither loses nor duplicates it.
- A turn with no queued input behaves exactly as it does today.

### Language-server tools

**Build**

- Port Eta's definition, reference, symbol, and diagnostics tools only where
  they add context the ACP client does not already attach.

**Gates**

- Every tool resolves and confines its paths through the workspace boundary and
  reports results relative to the session root.
- A missing or crashed language server fails the tool call with a useful message
  and never blocks the turn or the process.

### Web search and fetch

**Build**

- Settle first: the trust, citation, and content-size contracts for fetched
  content.
- Add web search and fetch tools under those contracts.

**Gates**

- Fetched content is bounded and spills like other large tool output, is labeled
  as untrusted in the model's context, and carries its source URL in the tool
  result.

## Milestone: differentiators

Ox adds the capabilities that set it apart from the reference agents: isolated
worktrees, one well-evidenced edit primitive, workspace memory, task
decomposition, and an evaluation harness.

### Worktree isolation

**Build**

- Port Iota's one-worktree-per-session isolation and its explicit merge,
  abandon, and rollback lifecycle.

**Gates**

- A session's tools operate in its worktree, and two sessions on one repository
  never see each other's changes.
- Merge, abandon, and rollback are explicit operations proved against a real Git
  repository through the process boundary, and each refuses with a useful error
  when it would discard changes the user has not seen.

### One edit primitive

**Build**

- Evaluate Iota's hashline-anchored edits against the current exact-edit
  contract on the same edit tasks.

**Gates**

- The evaluation records success rate, retries, and failure modes for both
  primitives, and Ox keeps one model-facing edit primitive unless the evidence
  shows both are needed.

### Workspace memory

**Build**

- Add persistent semantic workspace memory with bounded retention, following
  Kappa's typed facts, supersession, and retrieval into turns.

**Gates**

- Retention bounds are enforced, and expired or superseded facts never reach the
  model.
- Retrieval into a turn is visible in the durable record, so replay shows what
  the model saw.

### Task decomposition

**Build**

- Add automatic task decomposition backed by a durable subtask queue, following
  Kappa's task queue and Ox's existing subagent delegation.

**Gates**

- The queue survives restart, each subtask runs to completion at most once, and
  the parent turn's ACP updates nest subtask activity as they do for subagents
  today.
- Cancellation stops running subtasks and leaves the queue replayable.

### Evaluation harness

**Build**

- Add a Harbor evaluation adapter and a small regression task suite.

**Gates**

- The adapter drives the shipped binary over ACP with no test-only hooks.
- The suite runs on demand, records a result per task, and does not run under
  `make check`.

## ACP method coverage

Requests and notifications Ox accepts from an ACP client:

- [x] `initialize`
- [x] `authenticate`
- [x] `logout`
- [x] `session/new`
- [x] `session/load`
- [x] `session/resume`
- [x] `session/list`
- [x] `session/close`
- [x] `session/delete`
- [x] `session/prompt`
- [x] `session/cancel`
- [ ] `session/set_mode`
- [ ] `session/set_config_option`
- [x] `$/cancel_request`

Requests Ox sends to an ACP client:

- [x] `session/request_permission`
- [x] `fs/read_text_file`
- [x] `fs/write_text_file`
- [x] `terminal/create`
- [x] `terminal/output`
- [x] `terminal/wait_for_exit`
- [x] `terminal/kill`
- [x] `terminal/release`
- [ ] `elicitation/create`

Notifications Ox sends to an ACP client:

- [x] `session/update`
- [ ] `elicitation/complete`

Elicitation has no milestone yet. `session/fork` is an unstable draft in the
pinned schema and is not tracked until the schema stabilizes it. Terminal
authentication methods are also a draft there, and Ox advertises one only when
the client asks for it.
