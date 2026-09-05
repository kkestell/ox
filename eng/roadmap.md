# Ox Implementation Roadmap

`docs/spec.md` owns target behavior, `eng/architecture.md` owns durable design,
and this file owns build order and evidence of completion. User guides describe
shipped behavior. Milestones and their slices are ordered by position. All
remaining slices have concrete gates so implementation does not need another
product decision; a separate bounded plan still precedes each slice.

Apply the checks required by `AGENTS.md`. ACP-visible changes additionally pass
`make test-client` and schema-based process tests for behavior the browser
cannot exercise. Review completeness and simplicity once per finished milestone.
Do not mark a gate passed from a reference implementation or this specification
alone.

The browser submodule pins ACP UI at `e6e36d05`; its lockfile pins the stdio
bridge. The protocol oracle is `third-party/protocol/agent-client-protocol` at
`8e3eb8f2`, specifically `schema/v1/schema.json` and `meta.json`. Unstable and
v2 schemas are separate. Adopt new protocol/client revisions together in an
explicit interoperability change; documentation on the web is context, not
permission to silently change the pinned wire contract.

## Prior art and adoption decisions

Paths below are relative to `~/src/references/repos`. The feature inventory is
`~/src/references/index.md`. Plans name exact source files and useful tests,
what is ported, and the deliberate Ox adaptation. Port coherent behavior and its
tests without importing the surrounding framework or UI.

| Capability                   | Preferred source                                                                                                                 | Ox decision                                                                                                                                                    |
| ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Durable context and recovery | `personal/gamma/internal/agent/{engine,state,store}.go`; `personal/beta/src/{session,agent,acp}.rs`                              | Keep the existing JSONL/checkpoint model; prove recovery before adding new durable state.                                                                      |
| Compaction                   | `personal/beta/src/compaction.rs`; `personal/eta/internal/agent/compact.go`; `personal/delta/cmd/fleur/compaction.go`            | Preserve paired tool groups and transcript replay; extend Ox's idle-only compaction to long turns and children.                                                |
| Trace                        | `personal/beta/src/trace.rs`                                                                                                     | Port scoped correlation, excluding Beta's raw content fields. The existing trace plan defines the adaptation.                                                  |
| Todo and instructions        | `personal/eta/internal/agent/tools/{todo,todo_test}.go`; `personal/eta/internal/agent/{prompt,prompt_test}.go`                   | Eta's todo returns text but does not own durable todo state. Add that state in Ox rather than assuming a direct port supplies it. Keep root-only instructions. |
| Skills                       | `personal/eta/internal/skills/{skills,skills_test}.go`                                                                           | Port metadata validation and deterministic discovery. Use confined workspace skills, explicit loading, and change detection; omit Eta's user-root access.      |
| MCP                          | `personal/mu/src/agent/mcp.ts`                                                                                                   | Port namespacing and lifecycle. Replace its silent partial startup with atomic activation; do not persist secrets or import LangGraph.                         |
| LSP                          | `personal/eta/internal/lsp/{client,manager,diagnostics,uri}.go` and adjacent tests; `personal/eta/internal/agent/tools/lsp_*.go` | Port navigation and version-aware diagnostics. Add deadlines, confined result paths, and client-file synchronization.                                          |
| Web fetch                    | `personal/eta/internal/agent/tools/{web_fetch,web_fetch_test}.go`                                                                | Port extraction and spills; add redirect/address validation and explicit oversize failures. Search comes from MCP, not Eta's embedded Brave credential path.   |
| Questions                    | `personal/kappa/taikonaut/ask_user_tools.py`; canonical ACP form schema                                                          | Adapt one question to negotiated form elicitation. Keep permission and clarification distinct.                                                                 |
| Edit comparison              | `personal/iota/crates/app/src/tools/hashlines.rs`; current Ox edit tests                                                         | Measure anchors against exact edits before changing the production tool.                                                                                       |
| Isolation                    | `personal/iota/crates/adapter-git/src/lib.rs` and `personal/iota/crates/bin-web/src/lifecycle`                                   | Reuse dirty-tree and conflict scenarios as tests. The client owns worktree creation and teardown; do not port forced removal into the agent.                   |
| Memory                       | `personal/kappa/taikonaut/memory_manager.py`                                                                                     | Keep typed facts, expiry, supersession, and provenance. Begin with explicit bounded text retrieval; omit SQLite/Chroma and automatic extraction.               |
| Task queue                   | `personal/kappa/taikonaut/{task_queue,task_queue_tools}.py`; current Ox delegation                                               | Port explicit state transitions; add interrupted attempts. Atomic queue-file replacement alone cannot guarantee exactly-once side effects.                     |
| Evaluations                  | `personal/eta/harbor`; `personal/beta/evals/src/coral_harbor`                                                                    | Build an ACP adapter and task baseline before choosing experimental features.                                                                                  |

Use third-party protocol implementations when the personal projects do not own
the contract: `third-party/protocol/acp-go-sdk` for JSON/cancellation parity and
`third-party/protocol/zed-acp` for additional real-client semantics. MCP
transport and cancellation use the official `2026-07-28` protocol and Go SDK
selected in `eng/architecture.md`. Where Mu lacks lifecycle behavior, also
consult `third-party/coding-agents/goose/crates/goose/src/agents/mcp_client.rs`.
Do not import a provider framework, TUI, web host, or editor extension.

The review also considered Alpha's steering persistence and Eta's per-path
scheduler. Neither is a required port: v1 has no stable steering method, and
Ox's parallel reads plus serialized effectful calls remain the simpler default.
Worktree lifecycle, semantic retrieval, and anchored edits are not evidence of
quality merely because a reference has them.

## Most recently completed: durable session completion

Ox now loads durable sessions from checkpoints, preserves each activation's
negotiated executor choices, and recovers turns that were waiting for
permission.

## Milestone: context and diagnostics

Complete the remaining live diagnostic surface. Changed-file accounting and
idle-turn compaction already exist; the long-turn gaps are scoped separately
below and are not claimed complete.

### Sanitized trace

**Build**

- Implement the existing sanitized-trace plan against
  `docs/spec.md#diagnostic-trace`.

**Gates**

- Turn, provider, tool, permission, cancellation, recovery, and child activity
  have correlated JSONL events with no content or credential fields.
- Trace startup/write failures satisfy the spec and stdout remains ACP-only.
- The cumulative milestone review covers changed-file accounting, existing
  compaction, and trace together before replacing the completed summary.

## Milestone: evaluation baseline

Establish evidence before changing edit behavior or adding memory machinery.

### ACP evaluation adapter

**Build**

- Port the useful Harbor adapter seam to drive the shipped binary through ACP.
- Add a versioned local task set covering edits, multi-file work, navigation,
  long context, permission denial, cancellation, and restart.

**Gates**

- A fake-provider smoke run proves setup, prompt, timeout, artifact capture,
  teardown, and failure classification without a paid request.
- Each run records Ox revision, task revision, model/provider settings, budget,
  repetitions, success criteria, latency, tokens/cost when supplied, retries,
  and failures. Missing provider usage is unknown, not zero.
- Task success is checked from expected files or task tests, not a model's
  declaration. Each task starts in a fresh workspace and all child work counts
  against its budget.
- Real-provider evaluations are on demand only under the repository's explicit
  authorization rule. Evaluation does not run as part of normal project checks.

## Milestone: context continuity

Make long tool loops and recovery as reliable as short turns.

### Provider-request context admission

**Build**

- Apply the specification's context budget to new prompts, tool continuations,
  and children; compact at complete model/tool boundaries.
- Extend durable compaction and checkpoints to those boundaries without changing
  ACP transcript replay.

**Gates**

- A long single turn and a long child both compact before exhausting context;
  pending input, tool schemas, and reserved output contribute to admission.
- Oversized input, unsupported multimodal sizing, an irreducible prefix, a
  failed summary, and cancellation produce bounded, useful outcomes.
- Crash/reload at compaction boundaries reconstructs identical provider history;
  complete tool pairs, cumulative usage, and original ACP replay are preserved.

### Interrupted-effect recovery

**Build**

- Audit and close dispatch/completion gaps in ordinary, delegated, and recovered
  tool execution using the specification's unknown-outcome contract.

**Gates**

- Fault injection before dispatch, after an external effect, and before its
  completion record proves that an unrecorded effect is never blindly repeated.
- Pending permission recovery cannot redispatch an already started sibling call.
- Persistence failure prevents further dispatch and false success; unrelated
  sessions and cancellation remain responsive.

## Milestone: session controls and working context

Add the configuration and context surfaces needed by later tools.

### Process configuration

**Build**

- Implement the process configuration transition in `docs/spec.md`; migrate
  process/browser/evaluation harnesses to public flags and explicit credential
  files.
- Update shipped settings and client setup guides when the behavior lands.

**Gates**

- All precedence and startup-error cases are proved through the real binary.
- Workspace files cannot set process controls. No shipped behavior reads `OX_*`
  or `OPENROUTER_*`; ordinary platform environment handling still works.
- Tests prove credential-file ownership/type checks, keyring-disabled operation,
  login/logout behavior, and absence of credentials in diagnostics and records.
- The live harness can consume `.env` without exposing its credential or adding
  a hidden shipped-binary test hook. No live provider run is required here.

### Session configuration options

**Build**

- Implement only `session/set_config_option` with the spec's mode, model, and
  reasoning selectors; partition activation inputs from per-turn selections.

**Gates**

- New/load/resume responses and setter responses carry complete current state;
  accepted updates are durable before acknowledgement and replay correctly.
- In-flight changes leave the current turn and pending approvals unchanged.
- Model incompatibility rejects atomically; reasoning reset, repeated setters,
  unknown values, and reload precedence have deterministic tests.
- Plan-mode tool exclusion is enforced at dispatch and for children, not just
  described in the prompt. Existing grants cannot bypass it.

### Todo state

**Build**

- Add durable todo state, a validated replacement tool, and ACP plan projection.

**Gates**

- Invalid replacement is atomic; every accepted replacement emits one matching
  plan update after persistence, including clearing the list.
- Restart, checkpoint, and compaction preserve the current list. Child calls
  cannot overwrite the parent list.

### Root instructions

**Build**

- Add activation-frozen root instructions to parent and child context.

**Gates**

- Missing, unreadable, oversized, symlinked, and invalid-text files follow the
  specification; no parent/nested file is implicitly loaded.
- Reactivation captures a changed instruction file while running and recovered
  turns retain their original instruction content.

### Workspace skills

**Build**

- Add confined discovery, metadata-only prompting, and explicit body loading.

**Gates**

- Catalog order, bounds, malformed entries, missing/unreadable directories,
  symlinks, and changed bodies have deterministic coverage.
- A fake-model process test loads a skill and its reference file, records what
  the model saw, and proves metadata does not authorize script execution.
- Parent and child see the same applicable catalog without access outside the
  workspace; loaded context survives restart.

### Form questions

**Build**

- Add a model-facing question tool backed by ACP form elicitation.

**Gates**

- Capability absence removes the tool; accepted, declined, cancelled, invalid,
  and interrupted responses have distinct durable outcomes.
- A default is never submitted automatically. Cancellation releases the form
  wait and an unrelated session can still complete.
- Wire requests and response validation match the pinned form schema; no URL
  completion notification or private question method is introduced.

## Milestone: external context tools

Build MCP first so search does not require another built-in service integration.

### MCP activation and dispatch

**Build**

- Implement stdio and Streamable HTTP tool clients, bounded discovery, atomic
  activation, permission projection, and session-owned cleanup.

**Gates**

- Local mock servers prove both transports, discovery failure cleanup,
  pagination, collisions, catalog limits, tool errors, deadlines, and shutdown.
- Secret-bearing headers and environments never appear in durable configuration,
  trace, errors, or model-visible tool metadata.
- A changed schema/destination invalidates grants and pending recovery; rotated
  credentials alone do not. Missing recovery credentials cause no dispatch.
- Cancellation and connection loss never silently retry a possibly executed
  call. Restart/replay emits recorded results without contacting the server.
- Wire fixtures prove per-request metadata, HTTP header parity, both HTTP
  response formats, and transport-specific cancellation for the selected MCP
  revision. SDK input buffering is bounded before parsing, not just after a tool
  result has already been allocated.
- Legacy revisions/SSE and unsupported input interactions fail explicitly;
  untrusted annotations cannot skip permissions or enter plan mode.

### Language-server context

**Build**

- Add explicit global server configuration and the specified navigation and
  diagnostics tools using Eta's focused implementation seams.

**Gates**

- A mock LSP proves initialization/query deadlines, cancellation, shutdown,
  malformed replies, and crash behavior without installed language servers.
- Non-ASCII positions, confined URIs, stale diagnostic versions, and client
  unsaved-file synchronization are exercised. Unknown diagnostics are not clean.
- No workspace-selected executable, implicit installation, automatic edit-time
  diagnostics, or mutation tool is added.

### Public web fetch and MCP search

**Build**

- Add bounded public web fetch and source-bearing output. Exercise search using
  an MCP fixture and document how the client supplies a search server.

**Gates**

- Tests cover redirected nonpublic addresses, DNS/address validation, URL
  credentials, response caps, decompression growth, timeout, and cancellation.
- Small and spilled results identify sources and untrusted content. Error paths
  do not leak headers or provider credentials.
- HTML/text/JSON conversion is deterministic; unsupported binary content fails.
- Search requires no additional Ox credential/configuration surface. A process
  fixture proves the search-to-fetch flow and source links in model context.

## Milestone: deliberate memory and delegation

Add explicit durable state after the recovery and context contracts are proven.

### Workspace memory

**Build**

- Add the specification's bounded, typed memory tools and deterministic text
  retrieval with expiry, supersession, and source-session deletion.

**Gates**

- Injected time proves expiry cannot be prolonged by reads; capacity and byte
  bounds are enforced on writes and retrieval.
- Restart, concurrent sessions, deletion, supersession, and separate worktrees
  preserve correct ownership and expose no stale facts to a new retrieval.
- Tool results persist retrieved content for replay. No background extraction,
  embedding request, or second authoritative index is introduced.

### Durable delegated queue

**Build**

- Add explicit bounded queue tools over existing nonrecursive delegation with
  durable dispatch, terminal outcomes, and explicit retry attempts.

**Gates**

- Queue transitions reject invalid IDs/states without changing accepted work.
- Restart distinguishes pending, completed, and interrupted tasks. A crash after
  a side effect never automatically repeats the task.
- Parent cancellation stops the child, pauses pending work, and returns through
  the owning ACP request. No detached worker remains after turn completion.
- Parent and child share policy and total request budget; nested updates and
  retained child context replay in the same order after restart.

## Milestone: measured editing and isolation

Close the remaining comparative questions with executable evidence, not a second
production implementation kept indefinitely.

### Edit primitive comparison

**Build**

- Run exact and Iota-style anchored edits on the same versioned edit tasks with
  matched models, prompts, request budgets, and fresh workspaces.

**Gates**

- Use at least 30 tasks with three runs per candidate, including repeated text,
  stale reads, Unicode, line-ending preservation, and multi-file failures.
- Record task success, retries, tokens, latency, and failure categories. Adopt
  anchors only for an absolute success improvement of at least five percentage
  points with no confinement, stale-read, preview, or text-preservation failure
  and no greater than ten percent increase in median total tokens.
- If the threshold is not met or evidence is unavailable, keep exact editing.
  Store the result with evaluation artifacts and ship one edit primitive.

### Client-owned worktree acceptance

**Build**

- Add real-Git process tests and a user guide for opening sessions in worktrees
  prepared by the client or user.

**Gates**

- Two worktree sessions do not mutate each other's working files through file
  tools. Canonical paths, spills, instructions, and memory use the supplied
  root.
- Close/delete preserve dirty and untracked worktree files and branches.
- Tests and documentation distinguish file confinement from host process
  privileges and shared Git metadata. No hidden worktree or rollback API exists.

## Deliberately outside the build queue

- Steering, input queues, background workers, session fork, and ACP v2 require a
  separate protocol adoption decision; v1 cancellation plus a new prompt is the
  supported redirection workflow.
- Semantic retrieval is a research candidate only after the explicit-memory
  baseline exposes retrieval misses. It needs a versioned relevance set and a
  demonstrated task-success improvement within recorded cost/privacy bounds
  before any storage/provider change is proposed. Plain retrieval remains the
  settled implementation.
- Agent-managed merge/abandon/rollback, OS sandboxing, automatic skill installs,
  MCP OAuth/sampling/resources/prompts, URL elicitation, personas, and a second
  model provider are not implied by the tools above.

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
- Not planned: `session/set_mode`; config options are the sole mode interface.
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
- Not planned: `elicitation/complete`; URL elicitation is excluded.

Form elicitation is included in the form-questions slice. The canonical v1
schema at the revision above governs coverage; an unchecked method is not
advertised merely because its types exist. `session/fork`, provider management,
and terminal authentication remain draft-specific. Existing terminal auth is
advertised only when the client explicitly negotiates it. Steering and ACP v2
are not commitments in this roadmap.
