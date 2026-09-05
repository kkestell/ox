# Ox Implementation Roadmap

`docs/spec.md` owns target behavior, `eng/architecture.md` owns durable design,
and this file owns build order and evidence of completion. User guides describe
shipped behavior. No implementation milestone is currently queued. Any future
milestone and its slices are ordered by position, use concrete gates, and
receive a separate bounded plan before implementation.

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
| Compaction                   | `personal/beta/src/compaction.rs`; `personal/eta/internal/agent/compact.go`; `personal/delta/cmd/fleur/compaction.go`            | Preserve paired tool groups, transcript replay, and scoped parent and child projections.                                                                       |
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

## Most recently completed: measured editing and isolation

Ox retains exact editing based on versioned comparative evidence and now
documents and verifies client-owned worktrees through real-Git process tests.

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
- [x] `session/set_config_option`
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
- [x] `elicitation/create`

Notifications Ox sends to an ACP client:

- [x] `session/update`
- Not planned: `elicitation/complete`; URL elicitation is excluded.

The canonical v1 schema at the revision above governs coverage. `session/fork`,
provider management, and terminal authentication remain draft-specific. Existing
terminal auth is advertised only when the client explicitly negotiates it.
Steering and ACP v2 are not commitments in this roadmap.
