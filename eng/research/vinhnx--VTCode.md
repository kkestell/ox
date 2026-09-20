---
project: "vinhnx/VTCode"
repository: "https://github.com/vinhnx/VTCode"
revision: "bf0db9fda60c6f3fe97050245212f40fae425ea7"
researched_at: "2026-09-19"
primary_language: "Rust"
implementation_form: "native-agent"
process_model: "single-process"
session_owner: "acp-layer"
durability: "memory-only"
cross_session_concurrency: "concurrent"
same_session_concurrency: "concurrent"
event_delivery: "direct"
resume_strategy: "reconstruct"
overall_confidence: "high"
---

# VTCode ACP architecture

## Executive summary

- **ACP boundary:** The `vtcode acp [zed|standard]` subcommand turns the binary into a stdio ACP server on `agent-client-protocol` 2.0.0 (`Cargo.toml:97`). `run_acp_agent` builds one `ZedAgent` and installs typed handlers — initialize, authenticate, session/new, session/load, session/set_config_option, session/prompt, session/cancel — plus VT Code extensions (`session/fork`, `session/rollback`, `session/compact`) over `agent_client_protocol::Stdio` (`crates/codegen/vtcode-acp/src/zed/session.rs:25-116`, `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:57-136`, `crates/codegen/vtcode-acp/src/zed/agent/lifecycle.rs:339-398`).
- **Session model:** A session is an entry in `ZedAgent.sessions: Arc<Mutex<HashMap<SessionId, SessionHandle>>>` (`crates/codegen/vtcode-acp/src/zed/agent/mod.rs:43`) with process-local counter IDs `vtcode-zed-session-<n>` (`crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:115-117`). Each holds config fields plus a `ThreadRuntimeHandle` — an in-memory message vector owned by the ACP layer, not the main TUI runloop (`crates/codegen/vtcode-core/src/core/threads.rs:192-278`).
- **Concurrency:** Each prompt is spawned as its own task via `cx.spawn` (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:292-318`), so different sessions run concurrently and unbounded. So do prompts within one session: there is no exclusion — `ThreadRuntimeHandle::begin_turn` exists but the bridge never calls it, so two same-session prompts interleave into one shared message list and share one cancel flag (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:325`, `crates/codegen/vtcode-core/src/core/threads.rs:252-264`).
- **Durability and replay:** Bridge sessions are memory-only: `append_message` mutates an in-memory `Vec<Message>` and the prompt path never writes to disk (`crates/codegen/vtcode-core/src/core/threads.rs:248-250`). `session/load` can reconstruct a thread from a JSON archive written by the interactive TUI/exec runloops, but only if the client supplies that archive's identifier — bridge IDs never match one (`crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:334-350,381-400`, `crates/codegen/vtcode-core/src/utils/session_archive.rs:1313-1341`). No history is replayed to the client on load.
- **Event flow:** Live events are sent directly as fire-and-forget `session/update` notifications from the prompt task — token and thought deltas, a synthetic plan, tool-call pending/in-progress/final updates (`crates/codegen/vtcode-acp/src/zed/agent/updates.rs:12-22`, `crates/codegen/vtcode-acp/src/zed/connection.rs:63-65`). `session/prompt` resolves only at turn end with a `StopReason`.
- **Notable uncertainty:** The scheduling behavior of the `agent-client-protocol` 2.0.0 dispatch loop (crate-internal, not vendored) is not established; bridge-side behavior is fully evidenced, and cross-prompt concurrency is proved by the bridge's own `cx.spawn` per prompt (see Unknowns).

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `native-agent` | The ACP server is native: the same binary embeds the protocol handlers and a dedicated agent loop inside `vtcode-acp`; no wrapped executable. The loop is a simplified duplicate of the main TUI runloop, not a reuse of it. | `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:320-606` (`run_prompt`) |
| Process model | `single-process` | One `vtcode` process on a multi-threaded tokio runtime serves the stdio connection and all sessions; no per-session subprocesses. | `src/main.rs:262-265`, `crates/codegen/vtcode-acp/src/zed/session.rs:65-120` |
| Session owner | `acp-layer` | `ZedAgent` (in the ACP crate) owns the session registry, per-session config, cancel flags, and conversation threads. The TUI agent runloop (`src/agent/runloop/`) is not involved in ACP serving. | `crates/codegen/vtcode-acp/src/zed/agent/mod.rs:38-56`, `crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:115-143` |
| Durability | `memory-only` | ACP sessions live only in process memory. The only disk write on the ACP path is workspace-trust config at startup. Session archives on disk are read (read-only) by `session/load`, but the bridge never writes them. | `crates/codegen/vtcode-core/src/core/threads.rs:248-250`, `crates/codegen/vtcode-acp/src/workspace.rs:65-67`, `crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:334-350` |
| Cross-session concurrency | `concurrent` | Every prompt spawns an independent task; sessions share no locks on the prompt path; no session-count limit. | `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:306-316`, `crates/codegen/vtcode-acp/src/zed/agent/mod.rs:113` |
| Same-session concurrency | `concurrent` | No exclusion mechanism is applied: the per-prompt task resets the shared cancel flag and appends to the shared thread; two same-session prompts interleave rather than reject or cancel each other. | `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:325`, `crates/codegen/vtcode-core/src/core/threads.rs:252-264` (unused `begin_turn`) |
| Event delivery | `direct` | Updates are sent synchronously from the prompt task via `ConnectionTo::send_notification`; no queue, channel, or polling layer in the bridge. | `crates/codegen/vtcode-acp/src/zed/agent/updates.rs:12-22`, `crates/codegen/vtcode-acp/src/zed/connection.rs:63-65` |
| Resume strategy | `reconstruct` | `session/load` rebuilds runtime state from an in-memory handle or from a durable archive snapshot (messages, metadata) into a fresh `ThreadRuntimeHandle`; no replay to the client. | `crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:381-400`, `crates/codegen/vtcode-core/src/core/threads.rs:77-84,412-422` |

## System architecture

```text
Zed / ACP client
   │  JSON-RPC over stdio (agent-client-protocol 2.0.0)
   ▼
┌─────────────────────── vtcode process (tokio multi-thread runtime) ───────────────────────┐
│  run_acp_agent (LocalSet)                                                                 │
│    ├─ SACP dispatch loop: handlers for initialize/new/load/config/prompt/cancel           │
│    │    └─ session/prompt → cx.spawn(one task per prompt)                                 │
│    ├─ ZedAgent (Arc, Send+Sync)                                                           │
│    │    ├─ sessions: Mutex<HashMap<SessionId, SessionHandle>>                             │
│    │    │      └─ SessionData { provider, model, primary_agent, … , ThreadRuntimeHandle } │
│    │    ├─ AcpToolRegistry + local CoreToolRegistry (read_file→client, list_files→local)  │
│    │    └─ ConnectionHandle → send_notification / request_permission / read_text_file     │
│    │              ▲                                        │ (reverse RPCs to client)     │
│    └─ per-prompt loop: LLMProvider.generate/stream (HTTP to model provider)               │
│           messages = system + primary-agent prompt + thread.messages()  (in-memory only)  │
└───────────────────────────────────────────────────────────────────────────────────────────┘
   reads (session/load only): ~/.vtcode/state/sessions/<identifier>.json  ← written by TUI/exec runs
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| SACP builder/dispatch loop | Framing, request routing, handler invocation | Connection | none (crate-internal buffers) | `crates/codegen/vtcode-acp/src/zed/session.rs:103-116` |
| `ZedAgent` | Session registry, tool/permission plumbing, capability snapshot | Process | sessions map, client capabilities, `OnceLock` connection handle | `crates/codegen/vtcode-acp/src/zed/agent/mod.rs:38-56`, `crates/codegen/vtcode-acp/src/lib.rs:68-74` |
| `SessionHandle`/`SessionData` | Per-session config and cancel flag | Session | `Mutex<SessionData>`, `AtomicBool` | `crates/codegen/vtcode-acp/src/zed/types.rs:152-168` |
| `ThreadRuntimeHandle` | In-memory conversation (messages, metadata, bounded event ring) | Session | `Mutex<ThreadSessionState>` + 512-entry event buffer | `crates/codegen/vtcode-core/src/core/threads.rs:17,131-167,192-278` |
| `run_prompt` | The ACP agent loop: provider calls, tool loop, update translation | Prompt | local `PlanProgress`, accumulated assistant text | `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:320-606` |
| Session archives (JSON) | Durable snapshots written by TUI/exec runs, read by ACP `session/load` | On disk | `SessionSnapshot` | `crates/codegen/vtcode-core/src/utils/session_archive.rs:1313-1341,1464-1476` |

### ACP surface

The transport is `agent_client_protocol::Stdio`, pinned to `=2.0.0` with the `unstable_auth_methods` feature (`Cargo.toml:97`). The bridge uses the builder API: `Agent.builder().name("vtcode")` wrapped by `install_handlers` and `install_lifecycle_handlers`, then `connect_with(Stdio::new(), …)` captures the `ConnectionTo<Client>` and stashes it in a global `OnceLock` and in the agent (`crates/codegen/vtcode-acp/src/zed/session.rs:88-116`, `crates/codegen/vtcode-acp/src/lib.rs:68`). `initialize` answers protocol V1 and advertises `embedded_context`, image/audio, HTTP MCP, and `load_session = true` (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:138-168`). The prompt handler is the only handler that spawns (`handlers.rs:292-318`); bridge comments document that RPCs back to the client must run inside spawned tasks or the dispatch loop deadlocks (`crates/codegen/vtcode-acp/src/zed/connection.rs:16-25`). The ACP layer is native, and the agent loop it drives is a second, bridge-local loop distinct from the production TUI runloop.

### Runtime and process boundaries

Everything runs in one process: `main` builds a multi-threaded tokio runtime (`src/main.rs:262-265`), dispatches `Commands::AgentClientProtocol` (`src/cli/dispatch/commands.rs:34-37`) to `handle_acp_command` (`src/cli/acp.rs:8-43`), which calls the adapter's `serve` (`crates/codegen/vtcode-acp/src/zed/mod.rs:15-33`). The connection is built inside a `tokio::task::LocalSet` (`zed/session.rs:65-120`); the bridge notes that `cx.spawn` tasks cross the "LocalSet-less task boundary", which is why `ZedAgent` and `SessionHandle` are `Send + Sync` (`crates/codegen/vtcode-acp/src/zed/types.rs:148-151`). Reverse RPCs (`fs/read_text_file`, `terminal/create`, `session/request_permission`) and provider HTTP calls happen inside the per-prompt spawned task. A separate legacy subsystem — vtcode acting as an ACP/JSON-RPC *client* toward Codex's app-server (`src/codex_app_server/`) — shares the transport helpers but not the session path studied here.

## LLM abstraction and integration

### Abstraction

VTCode uses a **bespoke internal abstraction**: the `LLMProvider` trait, whose canonical home is the `vtcode-llm` crate (`crates/codegen/vtcode-llm/src/provider/provider_trait.rs:172`; crate description `crates/codegen/vtcode-llm/Cargo.toml:7`). Providers are `Box<dyn LLMProvider>` values constructed from config; the trait exposes `generate`, `stream`, and `stream_normalized` (`crates/codegen/vtcode-llm/src/provider/provider_trait.rs:371-394`) and a set of capability predicates. `crates/codegen/vtcode-core/src/llm/` is a thin re-export/factory facade over that crate, not a second abstraction (`crates/codegen/vtcode-core/src/llm/mod.rs`, `crates/codegen/vtcode-core/Cargo.toml:130`).

Each provider adapter speaks its HTTP API directly through `reqwest`, not through a third-party LLM SDK: the shared base builds its own `reqwest::Client` (`crates/codegen/vtcode-llm/src/provider_base.rs:9,51-54`), OpenAI-compatible providers POST and parse SSE themselves (`crates/codegen/vtcode-llm/src/providers/openai_compat.rs:363-397`), and Anthropic likewise (`crates/codegen/vtcode-llm/src/providers/anthropic/provider.rs:417,480`).

`rig-core` 0.40 is a real but **peripheral** dependency (`crates/codegen/vtcode-llm/Cargo.toml:29`). It supplies selected wire types/adapters, not the model loop: reasoning-parameter construction (`crates/codegen/vtcode-llm/src/rig_adapter.rs:1-88`), OpenAI Responses stream deserialization (`crates/codegen/vtcode-llm/src/providers/shared/responses_adapter.rs:10-14`), ChatGPT auth types (`crates/codegen/vtcode-llm/src/providers/openai/backend_setup.rs:12`), and the `ToolDyn` trait bridge for the core tool registry (`crates/codegen/vtcode-core/src/tools/registry/inventory.rs:2-3`). **Inference:** because every `generate`/`stream` body issues its own `reqwest` call and rig is never used to build or drive a completion request, rig is not the abstraction library.

One provider is a **wrapped executable**: `CopilotProvider` spawns the official `copilot` CLI and drives it over ACP (`crates/codegen/vtcode-llm/src/providers/copilot.rs:1-60`, `crates/codegen/vtcode-llm/src/copilot/command.rs:15,113-164`). For Copilot the model loop is outside this repository; credentials are also delegated (`crates/codegen/vtcode-config/src/api_keys/credential_resolution.rs:98-107`, which likewise delegates `codex` to its app-server).

### Integration path

1. **Prompt → history.** The ACP prompt's content blocks are flattened to one string (`crates/codegen/vtcode-acp/src/zed/agent/prompt.rs:83-124`) and pushed as `Message::user` (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:327-329`). Request history is rebuilt each turn by `resolved_messages`: system prompt, then primary-agent prompt, then the in-memory thread (`crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:318-332`).
2. **Provider/model selection.** Provider and model strings come from `SessionData`, seeded from startup config (`crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:91-113`) and mutable through `session/set_config_option` (`crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:404-505`).
3. **Provider construction.** Per prompt, `build_session_provider` resolves the API key (`get_api_key_with_mode`, honoring the agent's storage mode) and calls `create_provider_with_config` (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:610-642`). That reaches `vtcode-core`'s `LLMFactory` (`crates/codegen/vtcode-core/src/llm/factory.rs:236-245`), which looks the provider up in a string-keyed registry of closures installed by the CGP substrate (`crates/codegen/vtcode-core/src/llm/factory.rs:99-130`, `crates/codegen/vtcode-core/src/llm/cgp.rs:387-416`). The same builder serves `session/compact` (`crates/codegen/vtcode-acp/src/zed/agent/lifecycle.rs:212`).
4. **Request construction.** The bridge fills the provider-neutral `LLMRequest` — `messages`, `model`, `stream`, `tools`, `tool_choice`, `reasoning_effort` (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:398-406,484-491`; type at `crates/codegen/vtcode-llm/src/provider/request.rs:97-118`). Each provider converts it to its wire body: OpenAI-compatible via `convert_request` (`crates/codegen/vtcode-llm/src/providers/openai_compat.rs:276`), Anthropic via `convert_to_anthropic_format` plus `request_builder/{messages,system,tools}.rs` (`crates/codegen/vtcode-llm/src/providers/anthropic/provider.rs:417`, `crates/codegen/vtcode-llm/src/providers/anthropic/request_builder/messages.rs:52`).
5. **Streaming.** `provider.stream(request)` returns `LLMStream` (`crates/codegen/vtcode-llm/src/provider/provider_trait.rs:374`, `crates/codegen/vtcode-llm/src/provider/response.rs:67`). The bridge consumes `stream.next()` and maps each `LLMStreamEvent` (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:408-475`). When tools are enabled it instead runs a `provider.generate` loop (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:476-589`).
6. **Tool-call handling.** `response.tool_calls` are pushed as `Message::assistant_with_tools`, executed by `execute_tool_calls` (permission → in-progress → execution → final update), pushed back as `Message::tool_response`, and the history is re-resolved before the next iteration (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:503-554`, `crates/codegen/vtcode-acp/src/zed/agent/tool_execution.rs:19-148`).
7. **Back to runtime events.** `LLMStreamEvent::Token`/`Reasoning` become `AgentMessageChunk`/`AgentThoughtChunk` (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:424-444`); `FinishReason` maps to an ACP `StopReason` (`crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:352-359`). Usage/cost accounting is provider-side (`crates/codegen/vtcode-llm/src/usage_cost.rs`).

### Provider and tool boundary

- **Provider-specific code** lives per adapter under `crates/codegen/vtcode-llm/src/providers/` (Anthropic, Gemini, OpenAI, OpenRouter, Ollama, …; re-exports in `crates/codegen/vtcode-llm/src/providers/mod.rs:6-93`). OpenAI-compatible providers share one shell plus a per-provider spec (`crates/codegen/vtcode-llm/src/providers/openai_compat.rs:408-469`); custom profiles route through `CustomProviderBackendRouter` (`crates/codegen/vtcode-llm/src/providers/custom_provider.rs`).
- **Credentials/configuration** live in `vtcode-config`: `get_api_key_with_mode` / `resolve_credential_with_mode` own source precedence (env → workspace `.env` → secure storage → OAuth → managed auth → local) (`crates/codegen/vtcode-config/src/api_keys/credential_resolution.rs:71-130`). `ProviderConfig` is the factory input (`crates/codegen/vtcode-llm/src/provider_config_types.rs`, re-exported at `crates/codegen/vtcode-core/src/llm/factory.rs:13`). The ACP bridge carries `AuthCredentialsStoreMode` on `ZedAgent` and passes it into key resolution (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:615-620`).
- **Request/response normalization** is provider-local; the shared vocabulary is `LLMRequest`, `LLMResponse`, `LLMStreamEvent`, `FinishReason`, and `Usage` (`crates/codegen/vtcode-llm/src/provider/request.rs`, `crates/codegen/vtcode-llm/src/provider/response.rs:29-36`). OpenAI SSE/stream decoding sits in `crates/codegen/vtcode-llm/src/providers/openai/provider/{generation,streaming}.rs`; Anthropic has its own `stream_decoder.rs`/`response_parser.rs`.
- **Tool schemas** are the provider-neutral `ToolDefinition`/`FunctionDefinition` (`crates/codegen/vtcode-llm/src/provider/tool.rs:26,146`), serialized per provider (e.g. `serialize_tools_openai_format` at `crates/codegen/vtcode-llm/src/providers/common.rs:136`, `build_tools` at `crates/codegen/vtcode-llm/src/providers/anthropic/request_builder/tools.rs:112`). On the ACP path, definitions come from `AcpToolRegistry` (`crates/codegen/vtcode-acp/src/tooling/catalog.rs:117-140`) plus core-registered local tools, gated by client capabilities and the active primary agent (`crates/codegen/vtcode-acp/src/zed/agent/tool_config.rs:24-47`).
- **Multi-provider selection:** yes. The factory registers 28 built-in provider keys plus runtime-registered custom providers (`crates/codegen/vtcode-core/src/llm/factory.rs:36-65,252-323`); the provider is chosen per session by name and resolved through `ModelResolver`/`ModelId` (`crates/codegen/vtcode-llm/src/model_resolver.rs:145`). ACP exposes the choice as a config option built from the factory's live provider list (`crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:219-254`).

### Limits

- For **Copilot** (and `codex`), the model loop is outside this repository: the provider spawns and drives an external executable, so that runtime's request/response handling is not visible here (`crates/codegen/vtcode-llm/src/copilot/command.rs:113-164`).
- The registry is a process-global `LazyLock<Mutex<LLMFactory>>` populated at startup (`crates/codegen/vtcode-core/src/llm/factory.rs:168-175`); the exact set of providers available to an ACP session depends on runtime config, not on the checkout alone.
- **Not found:** no vendored provider SDK and no third-party model-loop library; searched `crates/codegen/vtcode-llm/src` for `rig::` uses and found only `rig_adapter.rs`, `providers/shared/responses_adapter.rs`, `providers/openai/backend_setup.rs`, `providers/openai/request_builder.rs`, `providers/openai/provider/tests.rs` (test-only), plus the core `tools/registry/inventory.rs` trait bridge, and read every `generate`/`stream` implementation entry point listed above.

## Session model

### Identity and ownership

"Session" is an ACP `SessionId` minted as `vtcode-zed-session-<n>` by an `AtomicU64` (`crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:115-133`, `crates/codegen/vtcode-acp/src/zed/constants.rs:1`). The mapping is one-to-one to a `SessionHandle` in the registry and one-to-one to a `ThreadRuntimeHandle` started via `ThreadManager::start_thread_with_identifier` (`crates/codegen/vtcode-core/src/core/threads.rs:304-310`). The thread identifier is the session-id string; there is no separate workspace/session database key on the ACP path. Config fields (provider, model, reasoning effort, primary agent) live in `SessionData` and are mirrored into thread metadata (`session_state.rs:91-113`). The durable-archive identifier space is disjoint: `session/load` treats an unknown ID as an archive identifier to import, which only succeeds for archives named by other runloops (`session_state.rs:385-392`).

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | `register_session` allocates an ID, builds metadata from config, starts an empty thread, inserts the handle, sends `AvailableCommandsUpdate` | None (memory only) | Insertion failure is swallowed (`if let Ok`); response still returns the ID | `session_state.rs:115-133,363-378` |
| Load/resume | In-memory handle reused; otherwise `find_session_by_identifier` reads `<id>.json` and rebuilds a thread from the snapshot | Read-only import; nothing written | Unknown archive → `internal_error` with the cause | `session_state.rs:381-400,334-350` |
| Prompt | Spawned task resets cancel flag, resolves prompt content, pushes user message, drives provider/tool loop, pushes results, answers with `StopReason` | None (thread is memory) | Provider/permission errors surface as tool-failure reports or error responses; `send_update` errors are dropped | `handlers.rs:320-606` |
| Cancel | `CancelNotification` sets the session's `AtomicBool`; the loop checks it between events/iterations and returns `StopReason::Cancelled` | None; partial streamed text is discarded from history | Shared flag: affects any in-flight prompt on that session | `handlers.rs:285-290,418-421,592-594` |
| Close/delete | Not found — no handler for session close/delete; sessions live until process exit. Searched `vtcode-acp/src` for close/delete/`session/list` handlers and found none. | n/a | Registry entries are simply dropped at process exit | — |

### Durable representation

For ACP sessions there is deliberately none: `ThreadRuntimeHandle::append_message` only pushes onto an in-memory `Vec` under a `parking_lot::Mutex` (`crates/codegen/vtcode-core/src/core/threads.rs:248-250`), and the bridge starts threads with `start_thread_with_identifier` instead of the archive-reserving `start_thread` (`threads.rs:312-320`). A grep of `vtcode-acp/src` for `SessionArchive`/persist calls finds only `find_session_by_identifier`. The only durable write on the ACP path is the workspace-trust sync at startup (`crates/codegen/vtcode-acp/src/workspace.rs:47-70`). Durable *reading* is supported: `SessionSnapshot` JSON files (messages with tool-call fields preserved, metadata, progress) under the state `sessions/` directory, matched by exact file stem (`crates/codegen/vtcode-core/src/utils/session_archive.rs:759,1313-1341,1464-1476`). Those archives are authoritative for the TUI/exec runloops that write them; the bridge treats them as an import source. In-memory thread history is authoritative for ACP session context while the process lives.

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | `handle_prompt` spawns a task per prompt; sessions touch only their own `SessionData` mutex; no global prompt lock or session cap | `handlers.rs:306-316`, `mod.rs:43,113` |
| Two prompts in the same session | `concurrent` | No exclusion: no `begin_turn` call, no busy check; both loops `store(false)` the shared cancel flag and interleave `push_message` calls into one thread; both eventually answer | `handlers.rs:325`, `threads.rs:252-264` |
| Load/resume during an active prompt | Unknown (no guard) | `load_session` for a known ID just re-reads the handle and sends commands; a same-ID archive import would replace the registry entry (`guard.insert`) while a prompt task holds the old `SessionHandle` clone | `session_state.rs:334-350,385-392` |
| Delete/close during an active prompt | n/a | No delete/close exists | — |
| Cancellation isolation | per session | One `AtomicBool` per session, checked at ~6 points in the loop; not per request, so cancel hits all prompts of that session, and a new prompt resets it | `handlers.rs:285-290,325,418,479,498,540,560,575` |

**Inference:** cross-session prompt parallelism is directly evidenced by the spawned per-prompt tasks and disjoint state. Same-session interleaving is evidenced by the absence of any guard plus the shared-flag reset; the precise interleaving semantics (e.g., both responses sent, duplicated user messages in one thread) follow from code reading, not from an executed test. Additional coupling: `ThreadManager` is shared but stateless; `get_factory()` is mutex-guarded only during provider listing; `VtCodePaths`-based trust sync happens once at startup. The registry mutex is held only for map operations, never across awaits (the `compact_session` comment at `crates/codegen/vtcode-acp/src/zed/agent/lifecycle.rs:211-224` documents this discipline).

## Event and data flow

### New prompt: ACP client to live response

1. Client sends `session/prompt`; the handler spawns a task and returns control to the dispatch loop (`handlers.rs:292-318`).
2. The task resolves the session, resets `cancel_flag`, and flattens content blocks into one string; `ResourceLink` blocks are fetched from the client via `fs/read_text_file` when capabilities allow (`handlers.rs:320-329`, `crates/codegen/vtcode-acp/src/zed/agent/prompt.rs:83-155`).
3. The user message is pushed into the thread (the first durable-representation commit, still memory-only) (`handlers.rs:329`).
4. A provider is built per prompt from session provider/model plus credential config (`handlers.rs:331-363,608-642`); history is assembled as system prompt + primary-agent prompt + `thread.messages()` (`session_state.rs:318-332`).
5. Without tools (or when streaming and toolless), tokens stream and each delta is sent as `AgentMessageChunk`/`AgentThoughtChunk` (`handlers.rs:397-475`). With tools, a generate→tool_calls loop runs: assistant-with-tools is pushed, each call emits `ToolCall` (pending) → optional `session/request_permission` → `ToolCallUpdate` (in progress) → execution → final `ToolCallUpdate`; tool results are pushed as messages and the history is re-resolved (`handlers.rs:476-589`, `crates/codegen/vtcode-acp/src/zed/agent/tool_execution.rs:51-148`). `read_file` executes via the client (`tool_execution.rs` → `run_read_file`, `tool_execution_local.rs:123-160`); `list_files` and registered local tools run in-process against the workspace, with restricted tools denied (`tool_execution_local.rs:22-59`).
6. Terminal text is sent as one chunk and pushed as the final assistant message; the request resolves with `PromptResponse::new(stop_reason)` mapped from `FinishReason` (`handlers.rs:592-605`, `session_state.rs:352-359`).

### Durable history to ACP client

The bridge never replays history to the ACP client. `session/load` sends only an `AvailableCommandsUpdate` and returns config options; there is no conversion of stored messages into `session/update` notifications (`session_state.rs:381-400`). Searched `vtcode-acp/src` for any `SessionUpdate::UserMessageChunk`/history-replay emission on load — none exists. The client therefore cannot visualize prior turns from the agent; history flows only into model context on the next prompt.

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `Message::user` (flattened text) | none | thread `Vec<Message>` | pushed before any LLM call | `handlers.rs:327-329` |
| Assistant text | accumulated `String`; final `Message::assistant` | `AgentMessageChunk` per delta (streaming) or one chunk (toolless non-streaming) | pushed once at turn end; skipped if cancelled | after streaming completes / before response | `handlers.rs:424-434,557-571,592-594` |
| Reasoning/thought | `LLMStreamEvent::Reasoning` deltas | `AgentThoughtChunk` | none (not stored) | notification only | `handlers.rs:435-444,462-469,574-585` |
| Tool call | `Message::assistant_with_tools` | `ToolCall` (pending) + `ToolCallUpdate` (in progress) | pushed before execution | on tool-loop iteration | `handlers.rs:523-526`, `tool_execution.rs:83-114` |
| Tool result | `Message::tool_response` | final `ToolCallUpdate` with content/locations/raw_output | pushed after execution, before next iteration | after tool completes | `handlers.rs:537-539`, `tool_execution.rs:144-147` |
| Completion/failure | `FinishReason` | `PromptResponse(stop_reason)` | none beyond thread history | at request resolution | `handlers.rs:605`, `session_state.rs:352-359` |

Persistence (in-memory append) precedes notification for user and tool messages; assistant text notification precedes its commit. Notification errors are dropped at every call site (`drop(agent.send_update(...))`), so a failed send never stops the loop.

### Subsequent-prompt reconstruction

Each prompt rebuilds the full request from scratch: system prompt, primary-agent prompt, then `thread.messages()` (`session_state.rs:318-332`). After an archive-based load, the messages come from `SessionSnapshot.messages` (falling back to `progress.recent_messages`), preserving roles, tool calls, and tool-call IDs (`crates/codegen/vtcode-core/src/core/threads.rs:77-84,412-422`; `crates/codegen/vtcode-core/src/utils/session_archive.rs:345-380,509-527`). There is no automatic compaction on the ACP path; compaction exists only as the explicit `session/compact` extension, which fails closed if history changed during the LLM summarization (`crates/codegen/vtcode-acp/src/zed/agent/lifecycle.rs:201-243`). `session/fork` snapshots a thread into a new independent session; `session/rollback` truncates trailing user turns in place (`lifecycle.rs:163-198`).

### Ordering, cancellation, failure, and backpressure

Ordering is natural: updates are sent inline from the single per-prompt task, so per-session ordering equals execution ordering. Cancellation is cooperative and checked between stream events, before/after each generate, around tool execution, and before plan updates; a cancel mid-turn yields `StopReason::Cancelled` and discards the accumulated assistant text from history while already-pushed tool calls/results remain (`handlers.rs:418-421,478-501,540-543,592-594`). There is no retry on provider errors — the first `generate`/`stream` error fails the prompt response; tool failures become failure reports fed back to the model (`tool_execution.rs:133-138`). Backpressure is unaddressed: notifications are fire-and-forget on the connection (`connection.rs:63-65`), send errors are dropped, and no queue bound exists in the bridge — a slow client's effect is confined to whatever buffering the `agent-client-protocol` crate performs internally (not established here). Because same-session prompts share one cancel flag and one thread, a second prompt resets cancellation and mutates history under the first prompt's feet.

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | Registry map; no limit | `mod.rs:43,113` |
| Concurrent work across sessions | `yes` | Task per prompt; disjoint state | `handlers.rs:306-316` |
| Same-session prompt exclusion | `no` | No guard; interleaved prompts share flag and history | `handlers.rs:325`, `threads.rs:252-264` |
| Durable sessions | `no` | ACP sessions never written to disk | `threads.rs:248-250` |
| Session list | `no` | No such handler; targeted search found none | `handlers.rs:57-136` |
| Session load/resume | `partial` | Implemented for in-memory IDs and imported archives, but bridge IDs cannot survive restart | `session_state.rs:381-400` |
| History replay to ACP client | `no` | Load sends commands/config only | `session_state.rs:394-399` |
| Prior history reused by model | `yes` | `resolved_messages` extends with thread history; archives preserve tool-call structure | `session_state.rs:318-332`, `session_archive.rs:345-380` |
| Prompt cancellation | `yes` | Cooperative session-scoped flag; also gates permission requests | `handlers.rs:285-290`, `tool_execution.rs:91-119` |
| Tool-call progress updates | `yes` | Pending → in-progress → completed/failed chain | `tool_execution.rs:83-147` |
| Partial-output persistence | `partial` | Tool calls/results commit incrementally; streamed assistant text commits only at turn end and is lost on cancel | `handlers.rs:523-539,592-594` |
| Recovery after process restart | `partial` | Archive import exists, but `vtcode-zed-session-N` IDs never match an archive file, so ACP sessions are unrecoverable; only externally supplied archive IDs load | `session_state.rs:385-392`, `session_archive.rs:792-797` |

## Design assessment

### Strengths

- The bridge is small and inspectable: one crate (~5.3k lines under `zed/`) owns protocol, sessions, tools, and the loop, with typed handlers and a single documented spawn discipline (`crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:9-28`).
- Session config (provider/model/effort/primary agent) is first-class ACP surface with validation, and compaction fails closed if history changed during the LLM summarization (`lifecycle.rs:218-243`).
- Client-capability negotiation gates features: `read_file` only when the client advertises `fs.read_text_file`, terminal only when `terminal` is advertised (`tool_config.rs:68-102`).

### Tradeoffs and limitations

- Two agent loops now exist: the ACP bridge loop and the production TUI runloop. ACP sessions get a fraction of the runtime's behavior — no session persistence, no `ToolPermissionCache` for "allow always" (`permissions.rs:165-192`), no streaming when tools are enabled (`handlers.rs:387`) — so behavior diverges between surfaces.
- Same-session concurrency is unguarded despite the runtime offering `begin_turn` exclusion; interleaved prompts can interleave unrelated turns in one history and race the shared cancel flag (`handlers.rs:325`, `threads.rs:252-264`).
- Memory-only sessions make the advertised `load_session` capability mostly unreachable from the bridge's own IDs; recovery after restart requires the client to know a TUI-written archive identifier (`session_state.rs:385-392`).
- `AllowAlways`/`DenyAlways` outcomes are treated as once-only because no decision cache backs the bridge prompter, contrary to the docs' "policy persistence" claim for ACP mode (`docs/guides/zed-acp.md:307-309`).

### Ideas relevant to Ox

- Fact: VTCode shows the failure mode of notification-before-persistence — a cancelled turn's streamed text exists only in the client, and load-time replay is skipped, so the client and the model disagree about history. The tradeoff it accepted is zero write latency and no storage code in the protocol layer (Ox persists events at the boundary instead).
- Judgment: a per-session turn guard is cheap when state already lives behind one mutex (`begin_turn` here is 8 lines); adopting it early avoids the interleaved-history problem VTCode carries.
- Judgment: minting session IDs that cannot match durable records silently disables resume; if Ox ever adds an import path, ID namespaces should overlap deliberately or fail loudly.

## Unknowns and conflicts

- `agent-client-protocol` 2.0.0 dispatch internals (per-request task vs. inline handling for non-prompt handlers) are not established; only 2.1.0 was available locally for inspection. Bridge-visible behavior is cited from repo code; the crate's queue bounds for notifications are unknown.
- Docs/code conflict: `docs/guides/zed-acp.md:307-309` claims auto-approved tool prompts persist to the workspace policy file in ACP mode; the bridge prompter implements no persistence, and the workspace policy file is only applied at registry construction (`mod.rs:81-87`). Possibly the doc describes the TUI runloop's `ToolPermissionCache`, which the bridge does not use.
- Whether two same-session prompts in practice double-respond without client-side rejection was not exercised by any test; tests cover fork/rollback/compaction, permissions over a duplex channel, and session config, but not concurrent prompts (`crates/codegen/vtcode-acp/src/zed/agent/lifecycle.rs:452-532`, `crates/codegen/vtcode-acp/src/permissions.rs:218-314`).
- `session/load` during an active prompt replaces the registry entry when importing an archive for the same ID; the old handle keeps streaming detached from the registry. No code path prevents this; impact unmeasured.
- Searches performed: `grep -r "session/list|session_list"`, `grep -r "close|delete" handlers`, `grep -r "SessionArchive|persist" vtcode-acp/src`, `grep -r "begin_turn" vtcode-acp/src` — all negative except as cited.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `src/cli/acp.rs:8-43` (`handle_acp_command`); `crates/codegen/vtcode-acp/src/zed/session.rs:25-127` (`run_acp_agent`); `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:57-136` (`install_handlers`) | CLI → adapter → SACP wiring and handler surface |
| Session management | `crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:115-143,318-400` (`register_session`, `push_message`, `resolved_messages`, `load_session`); `crates/codegen/vtcode-acp/src/zed/types.rs:152-168` | Session identity, registry, per-session state |
| Concurrency | `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:292-318` (`handle_prompt` + `cx.spawn`); `crates/codegen/vtcode-core/src/core/threads.rs:252-264` (`begin_turn`, unused) | Prompt task spawn; absent same-session exclusion |
| Persistence | `crates/codegen/vtcode-core/src/core/threads.rs:240-250`; `crates/codegen/vtcode-core/src/utils/session_archive.rs:1313-1341` (`find_session_by_identifier`); `crates/codegen/vtcode-acp/src/workspace.rs:47-70` | Memory-only commits; archive import path; only disk write |
| Event translation | `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:397-606` (`run_prompt` branches); `crates/codegen/vtcode-acp/src/zed/agent/tool_execution.rs:51-148` (`execute_tool_call`); `crates/codegen/vtcode-acp/src/zed/agent/updates.rs:12-34` | LLM/tool events → ACP notifications |
| Replay/reconstruction | `crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:334-350` (`attach_thread_from_archive`); `crates/codegen/vtcode-core/src/core/threads.rs:77-84,412-422` (`ThreadBootstrap::from_listing`) | Archive → model context; absence of client replay |
| LLM abstraction | `crates/codegen/vtcode-llm/src/provider/provider_trait.rs:172,371-394` (`LLMProvider`); `crates/codegen/vtcode-llm/src/providers/mod.rs:6-93` (adapters); `crates/codegen/vtcode-llm/src/rig_adapter.rs:1-88`, `crates/codegen/vtcode-llm/src/providers/shared/responses_adapter.rs:10-14` (peripheral `rig-core` uses) | Bespoke trait + per-provider `reqwest` adapters; rig is not the loop |
| LLM integration path | `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:327-336,398-475,610-642` (`run_prompt`, `build_session_provider`); `crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:318-332,352-359` (history, finish mapping); `crates/codegen/vtcode-acp/src/zed/agent/tool_execution.rs:19-148` (tool calls) | Prompt → provider → stream → tool loop → ACP events |
| Provider registry/selection | `crates/codegen/vtcode-core/src/llm/factory.rs:36-65,99-130,168-175,236-245` (`LLMFactory`); `crates/codegen/vtcode-core/src/llm/cgp.rs:387-416` (CGP registration); `crates/codegen/vtcode-llm/src/model_resolver.rs:145`; `crates/codegen/vtcode-acp/src/zed/agent/session_state.rs:219-254,404-505` | Multi-provider string registry; ACP config-option selection |
| Credentials | `crates/codegen/vtcode-config/src/api_keys/credential_resolution.rs:71-130` (`get_api_key_with_mode`); `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:615-620` | Source precedence and ACP storage-mode handoff |
| Tool schemas | `crates/codegen/vtcode-llm/src/provider/tool.rs:26,146`; `crates/codegen/vtcode-llm/src/providers/common.rs:136`, `crates/codegen/vtcode-llm/src/providers/anthropic/request_builder/tools.rs:112`; `crates/codegen/vtcode-acp/src/tooling/catalog.rs:117-140`, `crates/codegen/vtcode-acp/src/zed/agent/tool_config.rs:24-47` | Neutral definitions → provider wire; ACP capability/agent gating |
| Cancellation/errors | `crates/codegen/vtcode-acp/src/zed/agent/handlers.rs:285-290` (`handle_cancel`), `418-421,478-543,592-605`; `crates/codegen/vtcode-acp/src/permissions.rs:145-192` | Flag scope, cancelled-turn data loss, permission failures |

## Research notes

- **Revision inspected:** `bf0db9fda60c6f3fe97050245212f40fae425ea7`
- **Primary evidence:** `crates/codegen/vtcode-acp/src/zed/**` (session, connection, agent handlers/lifecycle/session_state/prompt/tool_execution/tool_config/updates/types), `crates/codegen/vtcode-core/src/core/threads.rs`, `crates/codegen/vtcode-core/src/utils/session_archive.rs`, `crates/codegen/vtcode-acp/src/permissions.rs`, `crates/codegen/vtcode-acp/src/tooling/catalog.rs`, `src/cli/acp.rs`, `src/cli/dispatch/commands.rs`, `src/main.rs`; for the LLM layer, `crates/codegen/vtcode-llm/src/{lib.rs,provider/**,providers/**,rig_adapter.rs,tool_bridge.rs,model_resolver.rs}`, `crates/codegen/vtcode-core/src/llm/{factory.rs,cgp.rs,mod.rs}`, and `crates/codegen/vtcode-config/src/api_keys/credential_resolution.rs`; tests in `crates/codegen/vtcode-acp/src/zed/mod.rs`, `permissions.rs`, `session_state.rs`, `lifecycle.rs`, and `tests/acp_integration.rs`.
- **Relevant docs:** `crates/codegen/vtcode-acp/AGENTS.md`, `src/AGENTS.md`, `docs/guides/zed-acp.md` (one conflict noted), root `AGENTS.md` crate table.
- **Commands/tests run:** Read-only inspection (`grep`, targeted file reads, `git rev-parse`); `agent-client-protocol` 2.1.0 source consulted for background only; no tests executed.
- **Report confidence:** `high` — all material claims cite production code at the pinned revision; the only unevidenced area (crate-internal dispatch scheduling) is isolated and labeled.
