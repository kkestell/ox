# Agent Project References

## Personal projects

### Feature matrix

| Capability | Alpha | Beta | Gamma | Delta | Epsilon | Zeta | Eta | Theta | Iota | Kappa | Lambda | Mu |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| ACP v1 stdio server | ✓ | ✓ | — | — | — | — | — | — | — | — | — | — |
| ACP authentication and logout | ✓ | — | — | — | — | — | Partial | — | — | — | — | — |
| ACP session create, prompt, cancel, and load | ✓ (plus list/resume/close/delete) | ✓ | — | ✓ | ✓ | Partial (create, list, load, prompt) | — | ✓ | — | ✓ | — | — |
| ACP session list, resume, close, and delete | ✓ (cursor-paginated list) | Partial (resume/load only) | — | Partial (list/resume) | Partial (list/resume) | Partial (list/load) | Partial (resume by ID) | Partial (resume by ID) | — | — | — | Partial (list/load) |
| Mid-turn steering / queued user input | ✓ | — | — | — | — | — | — | — | — | — | — | — |
| Typed JSON-RPC/protocol input validation | ACP/JSON-RPC boundary | ACP boundary | ✓ | HTTP/JSON boundary | OpenAPI-generated JSON-RPC | Pydantic JSON-RPC boundary | — | Strict tool-call JSON | NDJSON agent wire | JSON-RPC boundary | — | Typed webview boundary |
| Protocol conformance / event-sequence checker | — | — | ✓ | — | — | — | — | — | — | — | — | — |
| Concurrent sessions | ✓ | ✓ | ✓ | Partial (one active turn/workspace) | Partial (one active turn) | Partial (shared workspace) | — | — | ✓ | — | — | — |
| Interactive user interface | VS Code React webview | — | — | Browser web UI | VS Code React webview | VS Code React webview | Bubble Tea TUI | Bubble Tea TUI | Browser web UI | VS Code React webview | — | VS Code webview |
| Headless entry point | ACP runtime binary | ACP prompt client | Demo client | — | JSON-RPC server | JSON-RPC server | `mer-cli` | — | `ox-agent` NDJSON runner | JSON-RPC server | Async library API | — |
| Configuration precedence | `AMBER_MODEL`, workspace, global | CLI, TOML, defaults | — | XDG JSON + workspace defaults | — | VS Code settings | User, workspace, defaults | Global, workspace, defaults | CLI, environment, JSON defaults | VS Code settings | Environment/runtime config | Extension + workspace config |
| Credential storage / lookup | Env + OS keyring | Env, `.env` | — | JSON API key + `gh auth` | VS Code SecretStorage | VS Code SecretStorage + `.env` fallback | Env + OS keychain | Env + workspace/global `.env` | Environment | VS Code SecretStorage | Environment | VS Code secret storage |
| Durable append-only session log | ✓ | ✓ | ✓ | ✓ | — | — | ✓ | — | — | — | — | ✓ |
| Lossless replay and continuation after restart | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | Partial | — | Partial |
| Torn-write recovery | ✓ | ✓ | ✓ | ✓ | Atomic replace | Atomic replace | ✓ | — | — | Atomic replace | — | — |
| Interrupted-turn recovery / stable reissued interaction | Partial (restart recovery) | — | ✓ | ✓ (tool calls) | — | — | — | — | — | — | — | Partial |
| Atomic append-before-publish session mutation | ✓ | — | ✓ | ✓ | — | — | — | — | — | — | — | Ordered write queue |
| Session checkpoint / compact replay journal | — | — | ✓ | ✓ | — | — | Compaction log | — | — | — | — | — |
| Session-frozen request prefix and tool declarations | ✓ | ✓ (fixed startup system prompt and tool set reused per turn) | — | — | — | — | Partial (snapshotted per turn, not at session level) | Partial (prefix/model only) | — | — | — | Partial (live instance only) |
| Session activation file locking | ✓ | — | — | — | — | Partial (per-session save locks) | — | — | — | — | — | — |
| Streaming model/tool lifecycle | ✓ | ✓ | ✓ | ✓ | Partial | ✓ | ✓ | ✓ | ✓ | Partial | Partial | ✓ |
| Observable nested activity tree | ✓ | Partial | ✓ | — | — | Partial (persona/delegate) | Partial | Partial (one level) | — | — | — | ✓ |
| Typed lifecycle event stream | ACP updates | Partial | ✓ | SSE | JSON-RPC notifications | JSON-RPC notifications | ✓ | In-process stream | NDJSON + SSE | JSON-RPC events | ✓ | ✓ |
| Live provider integration | OpenRouter | OpenRouter | Fake DSL only | OpenRouter | OpenAI/OpenRouter | OpenAI, Anthropic, Google, xAI | Multi-provider | OpenRouter | OpenRouter | OpenAI/OpenRouter | LiteLLM client | OpenRouter (fixed) |
| Provider routing and model catalog | ✓ | ✓ | — | — | Partial (OpenAI/OpenRouter) | Partial (selected profile) | ✓ | Partial (routing + metadata lookup) | ✓ | Partial (configured profiles) | — | — |
| OpenRouter model catalog cache | ✓ (disk cache with refresh) | ✓ (disk cache with background refresh) | — | — | — | — | Partial (ETag-backed models.dev multi-provider cache) | — | — | — | — | Partial (in-memory model metadata) |
| Provider retry / streaming resilience | ✓ | ✓ | — | Partial (timeouts) | — | — | ✓ | — | — | — | — | — |
| Raw reasoning-detail round-trip | ✓ | — | ✓ | ✓ | — | — | ✓ | — | ✓ | — | — | — |
| Context-window metadata / occupancy | ✓ | ✓ | — | ✓ | — | Partial (request/tool counts) | ✓ | ✓ | ✓ | — | — | ✓ |
| Context compaction | — | ✓ | — | ✓ | — | — | ✓ | ✓ | — | — | — | ✓ |
| Client-facing permission decisions | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | TUI approval | ✓ | ✓ | ✓ | — | ✓ |
| Reusable per-tool permission memory | ✓ (activation-scoped) | ✓ | ✓ | — | — | — | Partial | Partial (shell rules) | — | — | — | — |
| Rule-scoped shell permission grants | ✓ | — | Partial | — | — | — | — | ✓ | — | — | — | — |
| Operational modes / agent roles | Subagent tool set | Tool selection | Plan/act/yolo | — | — | YAML personas | Configurable roles | Tool selection | — | — | — | — |
| User elicitation / explicit missing-information outcome | — | — | ✓ | — | ✓ | — | — | — | — | ✓ | ✓ | — |
| Symlink-safe workspace confinement | ✓ | ✓ | ✓ | Partial (TOCTOU caveat) | — | ✓ | ✓ | ✓ | Partial (approval-gated escape) | Partial (unchecked traversal) | — | Partial |
| Workspace trust-aware activation | ✓ | — | — | — | — | — | — | — | — | — | — | — |
| Remote/virtual workspace filesystem support | — | — | — | — | — | — | — | — | — | — | — | ✓ |
| Paged UTF-8 file reading | ✓ | ✓ | ✓ | Partial (bounded stream) | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | Target files only | ✓ |
| Read, write, exact edit tools | ✓ | ✓ | ✓ | ✓ | Partial | ✓ | ✓ | ✓ | ✓ | ✓ | Structured changes | ✓ |
| Read-evidence-guarded file mutation | ✓ | Partial (exact unique old-text reread; no durable evidence token) | Partial | Partial (exact edit) | Partial (old-text match) | ✓ (mtime) | ✓ | — | ✓ (hashline anchors) | — | — | — |
| Confined glob and grep | ✓ | ✓ | ✓ | ✓ | ✓ | Partial (no glob) | ✓ | ✓ | ✓ | Partial (no traversal check) | — | ✓ |
| Permissioned shell execution | ✓ | ✓ | ✓ | ✓ | — | ✓ | ✓ | ✓ | ✓ | ✓ | Verification runner | ✓ |
| Live process-output presentation | ✓ | — | ✓ | ✓ | — | — | ✓ | ✓ | — | — | — | — |
| Concurrent model-requested tool execution | ✓ | — | — | — | — | — | ✓ | — | — | — | — | — |
| Bounded output with spill/reopen | ✓ | ✓ | Partial | ✓ | — | ✓ | ✓ | ✓ | ✓ | Partial (truncate only) | Partial | Partial |
| Changed-file accounting | — | ✓ | — | ✓ | — | Partial (notifications) | — | — | — | ✓ | — | — |
| Workspace instructions (`AGENTS.md`) | — | — | — | — | — | — | ✓ | — | — | — | — | ✓ |
| On-demand skills | — | — | — | — | — | — | ✓ | — | — | — | — | ✓ |
| MCP support | — (explicitly unsupported) | — | — | — | — | — | — | — | — | — | — | ✓ |
| Delegated subagents | ✓ | ✓ | ✓ | — | — | ✓ | ✓ | ✓ | — | — | — | ✓ |
| Language-server navigation/diagnostics | — | — | — | — | Partial (tunnel only) | ✓ | ✓ | — | — | ✓ | — | — |
| Web search/fetch | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| Todo / plan-state tool | — | — | — | — | — | ✓ | ✓ | — | ✓ | ✓ | ✓ | ✓ |
| Verify-and-repair loop | — | — | — | — | — | — | — | — | — | — | ✓ | — |
| Structured full-file change proposal | — | — | — | — | — | — | — | — | — | — | ✓ | — |
| Verification-command discovery / allowlist | — | — | — | — | — | — | — | — | — | — | ✓ | — |
| Constraint verification and bounded retry diagnosis | — | — | — | — | — | — | — | — | — | — | ✓ | — |
| Usage/cost accounting | ✓ | ✓ | — | ✓ | — | Partial (usage only) | ✓ | ✓ | ✓ | — | — | ✓ |
| Sanitized trace telemetry | Partial (stderr logs) | ✓ | — | — | — | — | — | — | — | — | — | Partial |
| Headless runner / demo client | — | ✓ | ✓ | — | JSON-RPC server | JSON-RPC server | ✓ | — | ✓ | JSON-RPC server | — | — |
| Harbor evaluation adapter | — | ✓ | — | — | — | — | ✓ | — | — | — | — | — |
| Safe GFM Markdown transcript rendering | ✓ | — | — | ✓ | ✓ | — | — | ✓ | ✓ | Partial | — | — |
| VS Code runtime supervision and restart | ✓ | — | — | — | — | ✓ | — | — | — | — | — | — |
| Browser/IDE end-to-end harness | VS Code + Playwright | — | — | ✓ | — | — | — | — | — | — | — | ✓ |
| Isolated per-session Git worktrees and branches | — | — | — | — | — | — | — | — | ✓ | — | — | — |
| Guarded merge-or-abandon session closure | — | — | — | — | — | — | — | — | ✓ | — | — | — |
| Persistent workspace session-pane layout | — | — | — | — | — | — | — | — | ✓ | — | — | — |
| Replayable SSE transcript stream | Partial (replayable ACP updates over NDJSON, not SSE) | — | — | ✓ | — | — | — | — | ✓ | — | — | — |
| Paged durable transcript browsing | Partial (durable full replay, no pagination) | — | Partial | ✓ | — | — | Partial (durable replay plus local TUI viewport; no paged API) | — | Partial (durable replay exists; no paginated API) | — | — | — |
| Embedded interactive workspace terminal | Partial (live shell output; no stdin or terminal UI) | — | — | ✓ | — | — | Partial (confirmed Bash with live output; no PTY/input) | — | Partial (Bash output captured; no PTY, stdin, or terminal UI) | — | — | — |
| File explorer/editor and Git change view | — | — | — | Partial (file explorer/editor implemented; change/diff view is a stub) | Partial (file/diff navigation) | — | — | — | — | Partial (file/diff only) | — | — |
| GitHub pull-request inbox and workspace status | — | — | — | ✓ | — | — | — | — | — | — | — | — |
| Atomic durable session snapshots | — | — | Partial | Partial (fsynced append-only journal, not atomic whole-session snapshots) | ✓ | ✓ | Partial (fsynced append-only JSONL, not atomic snapshots) | ✓ | Partial (atomic temp-file replacement without fsync) | ✓ | — | Partial (durable rewrite, not atomic) |
| Mutation content/diff preview before approval | ✓ | Partial (raw mutation arguments; no computed diff) | Partial | Partial (raw mutation arguments visible; no computed diff) | — | — | — | ✓ | Partial (raw tool arguments shown; no computed diff) | — | — | Partial (raw args, no diff) |
| Whole-process-group shell cancellation | ✓ | Partial (kills direct shell child, not its process group) | — | ✓ | — | — | ✓ | ✓ | Partial (timeout kills direct Bash child only) | — | — | — |
| Persistent semantic workspace memory with typed TTLs and cross-session retrieval | — | — | — | — | — | — | — | — | — | ✓ | — | Partial (persistent AGENTS.md context only) |
| Automatic task decomposition with a durable per-session subtask queue | Partial (model-invoked delegated tasks, no durable queue) | — | — | — | — | — | Partial (manual todo list; no automatic decomposition or dedicated queue) | Partial (model-invoked subagent delegation with persisted child histories; no queue) | Partial (model-invoked todo list is transcript-persisted; no queue) | ✓ | — | Partial (delegated tasks/todos, no durable queue) |
| Automatic active-editor file/selection context attachment | — | — | — | — | ✓ | — | — | — | — | ✓ | — | — |
| Post-mutation settled LSP diagnostic feedback | — | — | — | — | — | — | ✓ | — | — | ✓ | — | — |
| Ephemeral Git snapshots and destructive rollback | — | — | — | — | — | ✓ | — | — | — | — | — | — |
| Workspace-overridable YAML personas | — | — | — | — | — | ✓ | ✓ | — | — | — | — | — |
| OpenAPI-described custom JSON-RPC with generated bindings | — | — | — | — | ✓ | — | — | — | — | ✓ | — | — |
| Tool-driven IDE file preview and diff navigation | — | — | — | Partial (tool-triggered file/editor refresh; no diff navigation) | ✓ | ✓ | — | Partial (TUI approval previews; no IDE navigation) | — | ✓ | — | — |
| LLM-generated session titles | — | — | — | — | ✓ | — | — | — | ✓ | ✓ | — | ✓ |

### Detailed repository catalog

### Alpha

**Path:** `references/repos/personal/alpha`

An ACP v1 stdio coding agent packaged as a VS Code extension, with a React
webview chat UI and a separately supervised Go runtime. It supports ACP
authentication/logout, session create/list/load/resume/close/delete, cursor
pagination, canonical workspace identity and trust-aware settings, activation
file locks, append-only versioned JSONL logs, torn-tail repair, replay,
interrupted-turn recovery, and frozen request-prefix configuration. Its
OpenRouter client streams text, reasoning, fragmented parallel tool calls, and
usage/cost; retries transient failures, validates catalog-backed reasoning
settings, and maintains a disk cache of model metadata. The runtime supports
queued mid-turn steering, correlated cancellation, client approvals with
activation-scoped tool and shell-rule grants, parallel tool batches, and
durable nested subagents.

The built-in tools provide symlink-safe `os.Root` confinement, paged UTF-8
reads, gitignore-aware glob/regex grep, atomic read-evidence-guarded writes and
exact edits, process-group shell execution with live output and bounded spill
files, and readable-only spill roots. The extension preserves pending state
across webview reloads, renders safe GFM Markdown, supervises runtime restart,
and has unit, runtime integration, extension-host, and Playwright end-to-end
coverage. The key seams are `runtime/internal/agent/`,
`runtime/internal/openrouter/`, `runtime/internal/tools/`,
`runtime/internal/workspace/`, `runtime/internal/settings/`,
`runtime/internal/credentials/`, `extension/src/`, and `extension/webview/`.

The feature audit confirms approval previews and whole-process-group shell
cancellation, but not atomic snapshots, Git worktrees/rollback, transcript
pagination, IDE views, semantic memory, LSP feedback, YAML personas, or
generated JSON-RPC. Delegated tasks are model-invoked without a durable queue.
Evidence includes `runtime/internal/tools/shell.go`,
`extension/webview/approval.ts`, `runtime/internal/agent/store.go`, and
`runtime/internal/tools/task.go`.

### Beta

**Path:** `references/repos/personal/beta`

The strongest product foundation. It is a concurrent ACP v1 stdio agent with a
streaming OpenRouter tool loop, per-session permission memory, cancellation,
load/replay, lossless versioned JSONL sessions, usage accounting, and a
one-shot ACP client. Its seven-tool coding loop provides symlink-safe workspace
confinement, atomic writes and exact edits, ignore-aware glob/grep, shell
process handling, output spills, compaction, and bounded non-recursive
subagents. Its useful implementation seams are `src/acp.rs`, `src/agent.rs`,
`src/{tools,workspace,spill,session}.rs`, `src/openrouter.rs`, and
`src/{compaction,subagent,trace}.rs`.

The feature audit confirms fixed session configuration and an OpenRouter model
cache, with partial guarded edits, mutation previews, and direct-child shell
cancellation. It found no VS Code supervision, worktrees, transcript stream,
IDE views, Git rollback, YAML personas, generated JSON-RPC, or LLM titles.
Evidence includes `src/models.rs`, `src/tools.rs`, `src/acp.rs`, and
`src/workspace.rs`.

### Gamma

**Path:** `references/repos/personal/gamma`

A draft 0.1 JSON-RPC protocol and deterministic reference implementation. Its
durable sessions use append-and-fsync transaction records, sequence-numbered
event replay, checkpoints, cloned-state rollback on append failure, and torn-tail
repair. It models a complete tree of turns, streams, tools, processes, and
subagents; recovery completes abandoned descendants first and safely reissues
durable callbacks with stable identities. Modes, reusable approvals, elicitation,
workspace-safe tools, and turn-owned process cancellation are all concrete. The
fake prompt-DSL model drives real tool behavior, while `internal/checker/`
validates tree and event invariants. The most useful seams are
`internal/agent/{engine,runner,state,store}.go`, `internal/tools/`, and
`protocol/`.

The feature audit confirms reasoning-detail round-trip and partial shell-rule,
read-guarded mutation, transcript replay, snapshots, and approval-preview
support. It found no auth/logout, trust activation, worktrees, SSE stream,
interactive terminal, IDE views, Git rollback, YAML personas, generated
bindings, or LLM titles. Evidence includes `internal/llm/llm.go`,
`internal/agent/runner.go`, `internal/tools/files.go`, and
`internal/agent/store.go`.

### Delta

**Path:** `references/repos/personal/delta`

A local loopback Go server with a Svelte browser workspace UI. Configured
workspaces have durable JSONL conversation journals, session list/resume,
paginated transcript browsing, reconnecting SSE, restart recovery for
interrupted tool calls, and checkpointed context compaction. The fixed
OpenRouter model streams text, reasoning details, tool calls, and usage; a
single turn runs per workspace, with browser confirmations for writes, exact
edits, and shell commands.

Its six-tool loop provides bounded streamed reads, atomic writes, re-read
guarded exact edits, symlink-aware workspace confinement, gitignore-aware
glob/grep, and shell process groups with live output and private spill files.
The UI adds a file explorer, editor, Git status and diff views, an interactive
terminal, and a GitHub pull-request inbox that polls configured accounts and
workspace remotes. Useful seams are `cmd/fleur/{agent,session,events,tools}.go`,
`cmd/fleur/{fs,search,shell,terminal,github}.go`, `cmd/fleur/server.go`, and
`web/src/{App.svelte,components/,lib/transcript.svelte.ts}`.

The feature audit confirms paged transcripts, an interactive terminal, a
GitHub pull-request inbox, and process-group shell cancellation. Durable
snapshots, mutation previews, and IDE file/diff support are partial; semantic
memory, task decomposition, active-editor context, LSP feedback, Git rollback,
YAML personas, and generated JSON-RPC are absent. Evidence includes
`cmd/fleur/session.go`, `cmd/fleur/terminal.go`, `cmd/fleur/github.go`, and
`cmd/fleur/shell.go`.

### Epsilon

**Path:** `references/repos/personal/epsilon`

A VS Code React-webview coding agent with a Python/LangChain backend using a
generated, OpenAPI-described JSON-RPC contract. It atomically replaces each
completed session snapshot, reloads saved conversations, injects active-editor
or selection context into prompts, and provides approval-gated writes, paged
reads, glob/regex search, multi-question elicitation, generated titles, and
file/diff navigation. The extension exposes a typed tunnel to VS Code's
language-service providers, although the backend has no corresponding agent
tool. Useful seams are `taikonaut/{agent,app,session_manager,file_tools}.py`,
`protocol/contract.yaml`, and `extension/src/`.

The feature audit confirms generated JSON-RPC bindings, active-editor context,
IDE preview/diff navigation, and LLM-generated titles. Git rollback and YAML
personas are absent. Evidence includes `protocol/contract.yaml`,
`taikonaut/generated/protocol_models.py`, `extension/src/generated/rpc.ts`,
and `taikonaut/app.py`.

### Zeta

**Path:** `references/repos/personal/zeta`

A VS Code extension with a Python `pydantic-ai` backend connected over
line-delimited JSON-RPC. It persists model history and UI messages under
`.fleur/chats`, supports selectable workspace-overridable YAML personas with
scoped tool sets and delegation, and snapshots mutations in an ephemeral bare
Git repository outside the workspace. A separately approved rollback hard
resets the worktree and cleans untracked files. Useful seams are
`fleur/{main,workspace,history,task_storage}.py`, `fleur/agents/personas.py`,
and `extension/fleur/src/`.

The feature audit confirms ephemeral Git snapshots and rollback, YAML personas,
and tool-driven IDE preview/diff navigation; its JSON-RPC bindings are
handwritten and session titles are deterministic rather than LLM-generated.
Evidence includes `fleur/main.py`, `fleur/agents/personas.py`,
`fleur/sessions.py`, and `extension/fleur/src/extension.ts`.

### Eta

**Path:** `references/repos/personal/eta`

The broadest end-user coding harness. It has a Bubble Tea TUI and headless
runner, a multi-provider streaming loop with retry and compaction, configurable
primary and child agent roles, permission patterns and queued approvals,
workspace `AGENTS.md` and skills, append-only session logs, and Harbor support.
Its tool inventory adds LSP navigation/diagnostics, web search/fetch, todo
state, shell process groups, and concurrent tool execution to the ordinary file
and search tools. The most useful seams are `internal/agent/`,
`internal/{permissions,session,lsp,skills,agents}/`, and `harbor/`.

The feature audit confirms model-catalog caching, reasoning round-trip,
read-guarded mutation, process-group cancellation, settled LSP feedback, and
YAML personas. Session listing, transcript browsing, terminal interaction,
snapshots, and task decomposition are partial; workspace trust, Git rollback,
IDE views, and generated JSON-RPC are absent. Evidence includes
`internal/llm/catalog.go`, `internal/agent/tool.go`,
`internal/agent/tools/bash.go`, `internal/lsp/client.go`, and
`internal/agents/agents.go`.

### Theta

**Path:** `references/repos/personal/theta`

A single-process Go coding agent with a Bubble Tea terminal UI, streamed
OpenRouter tool loop, interactive approvals, configurable tool selection, and
provider routing. It stores owner-only versioned JSON session snapshots
atomically, resumes by ID, reconstructs transcripts including one-level
subagent activity, and compacts context from per-model context metadata. Its
confined tools provide paged UTF-8 reads, writes and exact edits,
gitignore-aware glob/grep, process-group shell execution with live bounded
output and durable spills, plus non-recursive delegated subagents. Useful seams
are `cmd/ox/`, `internal/{session,agent,tools,tui}/`.

The feature audit confirms atomic durable snapshots, whole-process-group shell
cancellation, and TUI mutation previews; delegated child histories are
persisted, but semantic memory, active-editor context, LSP feedback, Git
rollback, YAML personas, and generated JSON-RPC bindings are absent. Evidence
includes `internal/session/snapshot.go`, `internal/tools/shell.go`,
`internal/tui/preview.go`, and `internal/agent/subagent.go`.

### Iota

**Path:** `references/repos/personal/iota`

A local Rust browser coding agent with an Axum host, a no-build browser UI, and
one NDJSON-driven `ox-agent` subprocess per session. Each session runs in an
isolated Git worktree and branch; the host restores saved sessions and pane
layout, replays buffered lifecycle events over SSE after reconnect, and offers
guarded merge or abandon teardown. JSON snapshot persistence captures every
committed message, while the OpenRouter loop streams text, tool calls,
reasoning details, and usage. It provides user-approved tools for paged
hashline reads, anchored exact edits, writes, glob/grep, bounded shell output
with spill files, and todo state. The useful seams are `crates/{app,domain}/`,
`crates/{bin-web,bin-agent}/`, `crates/agent-host/`, and
`crates/{adapter-fs,adapter-git,adapter-llm,adapter-storage}/`.

The feature audit confirms durable replay, atomic replacement snapshots,
approval previews, and persisted todo-driven delegation, while terminal,
shell cancellation, and transcript browsing remain partial. It found no IDE
file view, GitHub inbox, LSP feedback, Git rollback, YAML personas, or custom
generated JSON-RPC. Evidence includes `crates/adapter-storage/src/lib.rs`,
`crates/app/src/tools/bash.rs`, `crates/app/src/tools/todo_write.rs`, and
`crates/bin-web/src/sse.rs`.

### Kappa

**Path:** `references/repos/personal/kappa`

A Python JSON-RPC backend paired with a React VS Code webview. It stores atomic
per-session JSON snapshots and persistent task queues, supports approvals,
structured user questions, file and shell tools, and an LSP tunnel for
navigation, rename, and diagnostics. Its distinctive feature is SQLite- and
Chroma-backed semantic workspace memory, which extracts categorized facts with
TTLs and supersession, retrieves them into turns, and persists them across
sessions. Useful seams are `taikonaut/{agent,memory_manager,task_queue,
file_tools,lsp_tools}.py`, `extension/src/backend.ts`, and `tests/`.

The feature audit confirms semantic workspace memory, durable task queues,
active-editor context, settled LSP feedback, generated JSON-RPC bindings,
IDE preview/diff navigation, and LLM-generated titles. Git rollback and
workspace-overridable YAML personas are absent. Evidence includes
`taikonaut/memory_manager.py`, `taikonaut/task_queue.py`,
`protocol/contract.yaml`, and `extension/src/generated/rpc.ts`.

### Lambda

**Path:** `references/repos/personal/lambda`

A Python async library for an LLM-driven multi-file replacement loop. It
sanitizes requests, validates a strict workspace path and planned-file boundary,
has the model propose full file contents, runs allowlisted discovered checks with
captured bounded output and timeouts, feeds failures into a bounded retry loop,
then emits a structured diagnosis on exhaustion. Its typed event stream makes
each proposal, write, check, feedback, and abort explicit. It overwrites before
verification and has no rollback, while routing, clarification, and durable
memory remain partial or absent. The most useful seams are
`taikonaut/flows/code_change/{flow,checks,events,models}.py` and `session/`.

The feature audit found all unresolved LiteLLM capabilities absent: no ACP
auth/session lifecycle, locking, model catalog cache, reasoning round-trip,
workspace trust, evidence-guarded mutation, safe transcript renderer, VS Code
supervision, Git isolation/rollback, IDE views, semantic memory, task queue,
LSP feedback, YAML personas, generated JSON-RPC, or LLM titles. Evidence
includes `taikonaut/api.py`, `taikonaut/session/session.py`,
`taikonaut/flows/code_change/flow.py`, and `taikonaut/core/llm_client.py`.

### Mu

**Path:** `references/repos/personal/mu`

A Svelte/TypeScript VS Code extension around a DeepAgents/LangGraph agent. It
uses `vscode.workspace.fs` for local, remote, and virtual workspaces; validates
the typed webview boundary; gates write/edit/shell calls (including subagent
calls) through user approvals; starts namespaced MCP servers; discovers skills
and `AGENTS.md`; and tracks model cost and cache use. Workspace-local JSONL
persists exact transcript events and full LangChain context, with corruption and
dangling-tool repair. The most useful seams are `src/agent/`,
`src/{sessions,chatController}.ts`, `src/shared/`, and `e2e/drive.mjs`, which
launches an isolated VS Code Extension Development Host and drives nested
webviews with Playwright.

The feature audit confirms typed webview validation and IDE integration, but
found no auth/logout, session locking, reasoning-detail round-trip, shell-rule
grants, trust activation, transcript replay, terminal, GitHub inbox, LSP
feedback, Git rollback, YAML personas, or generated JSON-RPC. Model metadata,
task delegation, snapshots, and mutation previews are partial. Evidence
includes `src/agent/agentSession.ts`, `src/model/openrouter.ts`,
`src/sessions/sessionStore.ts`, and `webview/App.svelte`.

## Third-party repositories

These repositories are a deliberately small, implementation-oriented set. Each
one covers an Ox roadmap problem directly; general-purpose agent frameworks and
redundant editor products are excluded. The checkouts are shallow snapshots,
and their exact revisions are recorded below so research remains reproducible.

### Protocol and interoperability

| Directory | Project | Language | Snapshot | Research use |
| --- | --- | --- | --- | --- |
| `protocol/agent-client-protocol` | [ACP specification](https://github.com/agentclientprotocol/agent-client-protocol) | Rust/schema/docs | `8e3eb8f` (2026-08-16) | Canonical v1 wire schema, capability negotiation, versioning, RFDs, and schema generation. This is the protocol source of truth. |
| `protocol/acp-go-sdk` | [ACP Go SDK](https://github.com/coder/acp-go-sdk) | Go | `0845a3b` (2026-06-02) | Typed agent/client APIs, bidirectional stdio JSON-RPC, cancellation, notification barriers, extension methods, generated unions, and JSON parity tests. Start here for the Ox ACP boundary. |
| `protocol/zed-acp` | [Zed ACP client](https://github.com/zed-industries/zed) | Rust | `8968bf7` (2026-08-17) | Sparse checkout of `crates/acp_thread` and `crates/agent_servers`; use as the primary real-client interoperability oracle for permissions, files, terminals, elicitation, and session updates. |
| `protocol/typescript-sdk` | [ACP TypeScript SDK](https://github.com/agentclientprotocol/typescript-sdk) | TypeScript | `7585334` (2026-08-16) | Cross-implementation checks for connection behavior, runtime validation, generated types, and protocol evolution. |
| `protocol/rust-sdk` | [ACP Rust SDK](https://github.com/agentclientprotocol/rust-sdk) | Rust | `7d8291d` (2026-08-14) | Cross-implementation checks for routing, connection lifecycle, generated protocol types, and the behavior used by Rust ACP agents. |

### Model providers and streaming

| Directory | Project | Language | Snapshot | Research use |
| --- | --- | --- | --- | --- |
| `providers/fantasy` | [Fantasy](https://github.com/charmbracelet/fantasy) | Go | `49fe8d5` (2026-08-12) | Compact multi-provider abstraction used by Crush: streaming, tools, schemas, retries, provider normalization, and OpenRouter support. |
| `providers/openrouter-go-sdk` | [OpenRouter Go SDK](https://github.com/OpenRouterTeam/go-sdk) | Go | `4592ea8` (2026-08-14) | Official generated API surface for models, providers, Responses, SSE, pagination, errors, and retries. Treat as a wire/API reference until its beta API is evaluated. |
| `providers/openrouter-ai-sdk-provider` | [OpenRouter AI SDK provider](https://github.com/OpenRouterTeam/ai-sdk-provider) | TypeScript | `b96b207` (2026-07-23) | Behavioral oracle and regression tests for fragmented tool arguments, reasoning-detail signatures, usage anomalies, provider routing, and interrupted streams. Port semantics, not abstractions. |

### Coding agents

| Directory | Project | Language | Snapshot | Research use |
| --- | --- | --- | --- | --- |
| `coding-agents/crush` | [Crush](https://github.com/charmbracelet/crush) | Go | `240c487` (2026-08-16) | Primary third-party Go product reference. Focus on `internal/agent`, `session`, `permission`, `shell`, `workspace`, and provider integration. |
| `coding-agents/gemini-cli` | [Gemini CLI](https://github.com/google-gemini/gemini-cli) | TypeScript | `2a87e7b` (2026-08-14) | Production ACP agent with modular stdio transport, RPC dispatch, concurrent session management, cancellation, filesystem proxying, and mocked-response integration tests. |
| `coding-agents/codex` | [Codex](https://github.com/openai/codex) | Rust | `9ded177` (2026-08-16) | Mature reference for streaming loop state, cancellation, process execution, sandboxing, approvals, tool lifecycle, and durable state. |
| `coding-agents/goose` | [Goose](https://github.com/aaif-goose/goose) | Rust/TypeScript | `3810898` (2026-08-14) | Independent ACP server and client, durable sessions, permission projection, provider abstraction, extensions, and MCP integration. |
| `coding-agents/opencode` | [OpenCode](https://github.com/anomalyco/opencode) | TypeScript | `a0f8dcc` (2026-08-17) | Product-scale provider normalization, tool execution, permissions, persistence, server/client separation, skills, and subagents. |
| `coding-agents/pi` | [Pi](https://github.com/earendil-works/pi) | TypeScript | `d3ab2af` (2026-08-16) | Readable separation of multi-provider API, agent core, and coding agent; useful for event streams, prompt caching, steering/follow-up queues, tool batching, extensions, and fake-provider tests. |
| `coding-agents/plandex` | [Plandex](https://github.com/plandex-ai/plandex) | Go | `e2d7720` (2025-10-03) | Alternative Go designs for large-context planning, cumulative diff isolation, plan versioning, rollback, syntax validation, and automated repair. |

### Focused Go infrastructure

| Directory | Project | Language | Snapshot | Research use |
| --- | --- | --- | --- | --- |
| `infrastructure/go-keyring` | [go-keyring](https://github.com/zalando/go-keyring) | Go | `66b55cc` (2026-07-24) | Cross-platform macOS Keychain, Linux Secret Service, and Windows Credential Manager access, including an in-memory testing seam. |
