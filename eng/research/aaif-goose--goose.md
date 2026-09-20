---
project: "aaif-goose/goose"
repository: "https://github.com/aaif-goose/goose"
revision: "2090ad1c65ddb39497601a936a9fe17d66254bfe"
researched_at: "2026-09-19"
primary_language: "Rust"
implementation_form: "native-agent"
process_model: "single-process"
session_owner: "agent-runtime"
durability: "embedded-database"
cross_session_concurrency: "concurrent"
same_session_concurrency: "rejected"
event_delivery: "direct"
resume_strategy: "reconstruct"
overall_confidence: "high"
---

# goose ACP architecture

## Executive summary

- **ACP boundary:** The `goose acp` command runs an ACP agent natively inside the goose crate over stdio via the `agent-client-protocol` Rust SDK (`crates/goose/src/acp/server.rs:2652-2673`, `crates/goose-cli/src/cli.rs:2804-2807`); dispatch routes ACP requests to `GooseAcpAgent` methods (`crates/goose/src/acp/server/dispatch.rs:4-147`). A reverse bridge (`AcpProvider`) also lets goose delegate to *external* ACP agents as subprocess-backed providers (`crates/goose/src/acp/provider.rs:61-139`).
- **Session model:** An ACP session ID is exactly a goose `sessions` row ID; the ACP layer's in-memory `GooseAcpSession` holds only an `Arc<Agent>` handle (`crates/goose/src/acp/server.rs:229-242`). Conversation and provider bindings live in the shared `SessionManager`/`AgentManager` used by all goose frontends (`crates/goose/src/acp/server.rs:340`, `crates/goose/src/execution/manager.rs:33-47`).
- **Concurrency:** Sessions are independent; each prompt runs as its own task (`crates/goose/src/acp/server/dispatch.rs:124-141`). A second prompt for a session with an active run is rejected via a shared `ActiveRunRegistry` ("session already has active run"), with steering as the alternative (`crates/goose/src/acp/server.rs:2277-2284`, `crates/goose/src/execution/active_run.rs:32-65`).
- **Durability and replay:** SQLite (sqlx) with `sessions`, `messages`, and `usage_ledger` tables; every `add_message` is its own `BEGIN IMMEDIATE` transaction (`crates/goose/src/session/session_manager.rs:1027-1100`, `1919-1962`). `session/load` re-reads the conversation and replays it to the client as ACP chunk and tool-call updates (`crates/goose/src/acp/server/load_session.rs:108-218`).
- **Event flow:** The ACP handler pulls `AgentEvent`s from the agent's reply stream and sends `SessionNotification`s inline per event (`crates/goose/src/acp/server.rs:2120-2253`). The agent persists conversation effects before publishing tool-confirmation messages, but streams ordinary assistant text chunks *before* the merged message is persisted (`crates/goose/src/agents/state_machine/session.rs:116-149`, `crates/goose-agent/src/inference.rs:445-464`).
- **Notable uncertainty:** goose has two agent loops (legacy `agent.rs` loop and an opt-in state machine, `GOOSE_STATE_MACHINE=1`); both were traced, but per-line behavior differs in persistence timing, and the legacy path was only partially read.

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `native-agent` | goose itself implements the ACP agent side in-process; no wrapped executable behind the server. Separately, `AcpProvider` wraps *external* ACP agents as model providers (goose as ACP client) | `crates/goose/src/acp/server.rs:2652-2673`, `crates/goose/src/acp/provider.rs:676-707` |
| Process model | `single-process` | One process hosts the ACP server, agent runtime, and SQLite pool; MCP extensions are subprocesses but only as tool backends. The HTTP/roaming transport creates one `GooseAcpAgent` per connection, still in-process | `crates/goose/src/acp/server.rs:2707-2726`, `crates/goose/src/acp/server.rs:2675-2705` |
| Session owner | `agent-runtime` | Durable session state is owned by `SessionManager` (goose core, shared with CLI/desktop); in-memory agents by `AgentManager`; the ACP layer keeps only a session→agent map and the active-run registry | `crates/goose/src/acp/server.rs:320-347`, `crates/goose/src/execution/manager.rs:33-47` |
| Durability | `embedded-database` | Single SQLite database in the data dir via sqlx; messages, sessions, and usage ledger rows committed per operation | `crates/goose/src/session/session_manager.rs:713-718`, `994-1143` |
| Cross-session concurrency | `concurrent` | Prompts for different sessions run as independent spawned tasks; no cross-session lock. Shared touchpoints: SQLite (short transactions), the agent LRU cache, and the shared active-run registry keyed by session | `crates/goose/src/acp/server/dispatch.rs:124-141`, `crates/goose/src/execution/manager.rs:16` |
| Same-session concurrency | `rejected` | `start_prompt_run` fails with `AgentRunExists` → ACP `invalid_params` "session already has active run"; steering is the offered alternative | `crates/goose/src/execution/active_run.rs:43-48`, `crates/goose/src/acp/server.rs:1907-1919` |
| Event delivery | `direct` | The prompt task consumes the agent's event stream and calls `cx.send_notification` directly per event; a bounded internal mpsc(32) couples the machine's effect handler to the stream | `crates/goose/src/acp/server.rs:2140-2237`, `crates/goose/src/agents/agent.rs:1989` |
| Resume strategy | `reconstruct` | Load re-reads the session row + conversation from SQLite, rebuilds extensions/provider, replays history to the client, and resumes pending tool confirmations | `crates/goose/src/acp/server/load_session.rs:382-486` |

## System architecture

```text
ACP client (editor/IDE)
   │ JSON-RPC over stdio (`goose acp`) or HTTP/WS (`goose serve`)
   ▼
agent-client-protocol SDK ──► GooseAcpHandler (dispatch.rs)
                                 │
                                 ▼
                     GooseAcpAgent  (per connection)
                       sessions: Mutex<HashMap<id, GooseAcpSession{Arc<Agent>}>>
                       active_runs: ActiveRunRegistry (shared per AcpServer)
                       session_manager: SessionManager ──► SQLite (sessions/messages/usage_ledger)
                       agent_manager: AgentManager (LRU of Arc<Agent>, cap 100)
                                 │
                                 ▼
                        Agent::reply(user_message)
                          ├─ persist user message (SQLite)
                          ├─ legacy loop (agent.rs) or state machine (ops_*)
                          │    └─ Provider.stream(...)  ◄── AcpProvider → external agent subprocess (optional)
                          └─ AgentEvent stream ──► forward_agent_stream ──► SessionNotification (live)
                                                        └─► replay on session/load
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `GooseAcpHandler` | Maps ACP dispatches to agent methods; spawns request tasks | connection | none | `crates/goose/src/acp/server/dispatch.rs:4-30` |
| `GooseAcpAgent` | ACP session registry, capabilities, prompt/cancel/load orchestration, event translation | server (one per stdio process; one per HTTP connection) | `sessions` map, `closed_session_ids`, client capability cells | `crates/goose/src/acp/server.rs:320-347` |
| `ActiveRunRegistry` | One active run (prompt or live) per session; cancel tokens | shared per `AcpServer` | `runs_by_session` map | `crates/goose/src/execution/active_run.rs:26-29`, `crates/goose/src/acp/server_factory.rs:27-44` |
| `AgentManager` | Builds/caches `Arc<Agent>` per session (LRU 100), serializes creation per session | server | LRU cache, per-session creation locks | `crates/goose/src/execution/manager.rs:16`, `33-47`, `110-165` |
| `Agent` | Model loop (legacy or state machine), extensions, provider, steer queues | per session while cached | provider, extension manager, steer queues | `crates/goose/src/agents/agent.rs:561-601` |
| `SessionManager`/`SessionStorage` | Durable sessions, messages, usage | server | SQLite pool | `crates/goose/src/session/session_manager.rs:316-318`, `713-718` |

### ACP surface

Transport is stdio for `goose acp` (`crates/goose/src/acp/server.rs:2707-2726`) and HTTP/WebSocket via `agent-client-protocol-http` for `goose serve`, where each connection lazily creates its own `GooseAcpAgent` (`crates/goose/src/acp/server.rs:2675-2705`); roaming connections share one `ActiveRunRegistry` so guards work across connections (`crates/goose/src/acp/server_factory.rs:79-88`). `on_initialize` records client fs/terminal/MCP capabilities into set-once cells and advertises `load_session`, list/delete/close capabilities, image + embedded-context prompt capabilities, and a `goose-provider` auth method (`crates/goose/src/acp/server.rs:1789-1848`). `InitializeRequest` is handled inline before pipelined requests because later handlers read the capability cells (`crates/goose/src/acp/server/dispatch.rs:27-36`). The ACP layer is native; the only subprocess boundary on the server side is `AcpProvider`, which spawns an external ACP agent (e.g. `claude-acp`) as goose's model provider, translating goose prompts into ACP `PromptRequest`s and ACP updates back into goose `MessageStream` items (`crates/goose/src/acp/provider.rs:606-623`, `676-707`).

### Runtime and process boundaries

Everything relevant runs as tokio tasks in one process. Each ACP request is spawned off the dispatch chain (`crates/goose/src/acp/server/dispatch.rs:47`, `84`, `128`). The prompt task holds an `ActiveRunDropGuard` that cancels the token and discards pending steers if the future is dropped mid-run (`crates/goose/src/acp/server.rs:257-278`, `2289-2294`). In the state machine path, a bounded `mpsc::channel::<AgentEvent>(32)` connects the machine's effect handler to the reply stream (`crates/goose/src/agents/agent.rs:1987-2010`); the legacy loop streams within one async_stream future. A process-wide default `SessionStorage` exists as a `LazyLock`, but the ACP path constructs its own `SessionManager` over the same data dir (`crates/goose/src/session/session_manager.rs:58-59`, `crates/goose/src/acp/server.rs:949-955`).

## LLM abstraction and integration

### Abstraction

goose uses a **bespoke internal abstraction**, not a third-party LLM library or provider SDK. The model-call interface is the `Provider` trait in the shared `goose-provider-types` crate: `stream(model_config, system, messages, tools) -> MessageStream` (`crates/goose-provider-types/src/base.rs:474`, `487-493`), with a default `complete` that collects the stream (`crates/goose-provider-types/src/base.rs:495-504`). `MessageStream` is a boxed stream of `(Option<Message>, Option<ProviderUsage>)` chunks (`crates/goose-provider-types/src/base.rs:327-332`). Concrete implementations live in two crates: HTTP/SSE providers in `goose-providers` (`crates/goose-providers/src/openai.rs`, `anthropic.rs`, `google.rs`, `ollama.rs`, `openrouter.rs`, …) and the registry plus CLI/ACP-backed providers in `goose::providers` (`crates/goose/src/providers/`). HTTP is done directly with `reqwest` (`Cargo.toml:56`); there is no `rig`, `genai`, or official `openai`/`anthropic` SDK dependency in the workspace manifest. A subset of providers instead **wrap an executable or external service**: `ClaudeCodeProvider` spawns the `claude` CLI subprocess (`crates/goose/src/providers/claude_code.rs:675-745`) and `AcpProvider` wraps an external ACP agent over stdio (`crates/goose/src/acp/provider.rs:677-820`). For those, the boundary is the spawned process/ACP connection and the actual model loop is outside this repository.

### Integration path

1. **Provider/model selection.** The ACP new-session path resolves a provider name and `ModelConfig` from recipe settings, request `_meta`, or the global default (`crates/goose/src/acp/server/new_session.rs:187-221`) and persists them on the session row (`crates/goose/src/acp/server/new_session.rs:228-233`). On agent creation/restore, `Agent::restore_provider_from_session` reads `session.provider_name` and `session.model_config`, looks the name up in the registry, and constructs the provider with `create_with_working_dir` (`crates/goose/src/agents/agent.rs:3748-3799`). `Agent::update_provider` swaps the provider and re-persists the selection (`crates/goose/src/agents/agent.rs:3590-3641`).
2. **History/message conversion.** The loop loads the persisted conversation and the inference runner projects it to what the provider sees: agent-visible messages only, unanswered prior tool requests dropped, role-alternation fixed, and consecutive same-role messages merged (`crates/goose-agent/src/inference.rs:344-382`; legacy equivalent at `crates/goose/src/agents/reply_parts.rs:352-365`).
3. **Request construction.** `Provider::stream` receives the model config, system prompt, converted messages, and `&[Tool]` (`crates/goose-agent/src/inference.rs:383-391`; legacy call at `crates/goose/src/agents/agent.rs:2679-2687`). Each concrete provider builds the wire payload through shared format code: OpenAI `create_request_with_options` (`crates/goose-provider-types/src/formats/openai.rs:1686-1809`) and Anthropic `create_request_for_model` (`crates/goose-provider-types/src/formats/anthropic.rs:903`).
4. **Streaming.** HTTP providers POST via `ApiClient::response_post` (`crates/goose-providers/src/api_client.rs:563-566`) and parse SSE with `stream_openai_compat` (`crates/goose-providers/src/openai_compatible.rs:237-259`) / `response_to_streaming_message`, which accumulates tool-call deltas and yields partial-text, complete-tool-call chunks (`crates/goose-provider-types/src/formats/openai.rs:1233-1392`). Anthropic streams via its own `stream` implementation (`crates/goose-providers/src/anthropic.rs:404-419`).
5. **Streaming consumer.** `InferenceRunner::infer` drives `stream.next()` inside a `tokio::select!` with the cancel token, records usage, and turns each chunk into an `InferenceEffect` (`crates/goose-agent/src/inference.rs:420-494`). The goose-specific wrapper `GooseInferenceProvider` canonicalizes mangled tool names and annotates the advertised-tool list before handing the stream back (`crates/goose/src/agents/state_machine/ops_llm.rs:100-171`).
6. **Tool-call handling.** The provider yields tool calls as complete `ToolRequest` content in the message (contract documented at `crates/goose-provider-types/src/base.rs:327-332`); the runtime's tool execution operation executes them and appends `ToolResponse` messages (`crates/goose/src/agents/agent.rs:1716-1720`), after which the loop re-invokes inference.
7. **Conversion back to runtime events.** Effects map to `AgentEvent`s in the state machine (`GooseEffect::Conversation(AppendMessage)` → `AgentEvent::Message`/`MessageUsage`, `GooseEffect::RecordUsage` → `AgentEvent::Usage`; `crates/goose/src/agents/state_machine/session.rs:118-145`), which the ACP layer then translates (see Event and data flow). The legacy loop yields `AgentEvent`s directly from its own stream (`crates/goose/src/agents/agent.rs:2679-2687`).

For a subprocess/ACP-backed provider the trace ends at the delegated boundary: goose sends the prompt and receives ACP updates, but the inner agent's model selection, request construction, and tool loop are **not visible** in this repository (`crates/goose/src/acp/provider.rs:820`).

### Provider and tool boundary

- **Provider-specific code** lives in per-provider modules: HTTP clients in `crates/goose-providers/src/` and registry/CLI/ACP wrappers in `crates/goose/src/providers/`. Wire-format and request/response normalization are shared in `crates/goose-provider-types/src/formats/` (`openai.rs`, `anthropic.rs`, `google.rs`).
- **Credentials/configuration** are read by provider constructors from env and `Config`: OpenAI resolves `OPENAI_API_KEY`/custom headers via `config.get_secrets` and chooses `AuthMethod::BearerToken` (`crates/goose/src/providers/openai_def.rs:96-114`); declarative/custom providers resolve keys through `ConfigKeyResolver` → `Config::get_secret` (`crates/goose/src/providers/custom_provider_config.rs:15-20`, `crates/goose/src/config/base.rs:912`). Auth is applied per request in `ApiClient::send_request` (`crates/goose-providers/src/api_client.rs:583-613`). Secrets are stored in the system keyring or a secrets file when the keyring is disabled (`crates/goose/src/config/base.rs:81-90`).
- **Request/response normalization.** `format_tools` converts internal `rmcp::model::Tool` values to provider JSON (`crates/goose-provider-types/src/formats/openai.rs:666-686`; Anthropic at `crates/goose-provider-types/src/formats/anthropic.rs:536`); responses are normalized back to internal `Message`/`ProviderUsage` (`crates/goose-provider-types/src/formats/openai.rs:708`), and HTTP statuses map to `ProviderError` variants in `crates/goose-providers/src/http_status.rs`.
- **Tool schemas** originate from MCP extensions as `rmcp::model::Tool` objects, fetched by `ExtensionManager::get_prefixed_tools` / `list_tools_from_extension` (`crates/goose/src/agents/extension_manager/mod.rs:671`, `680`) and schema-normalized in the same module (`crates/goose/src/agents/extension_manager/mod.rs:870`); they are filtered, sorted, and (in toolshim mode) converted to text in `prepare_inference_tools` / `prepare_tools_for_provider` (`crates/goose/src/agents/reply_parts.rs:244-317`).
- **Multi-provider.** Yes. The `ProviderRegistry` maps provider names to constructors (`crates/goose/src/providers/provider_registry.rs:96-363`), and the built-in set is registered in `init_registry` (`crates/goose/src/providers/init.rs:57-179`). A provider is selected by name string (recipe `goose_provider`, request `_meta.provider`, or global config default) and looked up via `get_from_registry` / `create_with_working_dir` (`crates/goose/src/providers/init.rs:253`, `271`).

### Limits

- For wrapped-executable and wrapped-service providers (`ClaudeCodeProvider`, `AcpProvider`, and other CLI providers), the model loop, provider selection, and tool execution of the delegated agent are **Not found** in this checkout; only goose-side translation is visible (`crates/goose/src/providers/claude_code.rs`, `crates/goose/src/acp/provider.rs`).
- Upstream provider API behavior (rate limits, exact error payload semantics, model-side tool-call formatting) is **Not found**; only the client's construction and status/error mapping are in the checkout (`crates/goose-providers/src/http_status.rs`, `crates/goose-provider-types/src/formats/`).
- The runtime credential backend is environment-dependent (system keyring vs. secrets file) and not fixed by the code (`crates/goose/src/config/base.rs:81-90`).

## Session model

### Identity and ownership

The code documents the mapping directly: "The ACP session ID maps directly to a `sessions` row" (`crates/goose/src/acp/server.rs:229-242`, `2268-2269`). Session IDs are generated by SQLite as `{YYYYMMDD}_{n}` (`crates/goose/src/session/session_manager.rs:1627-1636`). One ACP session maps one-to-one to: a `sessions` row (working dir, provider name, model config, extension data, goose mode, usage), the ordered `messages` rows, a cached `Arc<Agent>` in `AgentManager` keyed by that ID, and an optional entry in `GooseAcpAgent.sessions`. New sessions get `SessionType::Acp` unless `_meta.client` (→ `User`) or `_meta.hidden` is set (`crates/goose/src/acp/server/new_session.rs:281-291`); a migration back-fills legacy rows named "ACP Session" to type `acp` (`crates/goose/src/session/session_manager.rs:1429-1438`). When the provider is itself a nested ACP agent, the inner agent's session ID is stored in message inference metadata and re-associated on load via `provider.resume` (`crates/goose/src/acp/server.rs:203-223`, `crates/goose/src/acp/provider.rs:686-707`) — a one-to-many layering (goose session → inner agent session).

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | Validate cwd, resolve provider/model/extensions/recipe, `create_session`, apply initial config, build and register agent | Row inserted immediately (`BEGIN IMMEDIATE`), then updated with provider/model/extension data | `cleanup_failed_new_session` deletes the row, drops the in-memory agent and session entry | `crates/goose/src/acp/server/new_session.rs:36-73`, `105-125` |
| Load/resume | `get_session(id, include_messages=true)`, reconcile cwd/provider, replay history to client, register agent, resume nested provider session, resend or resume pending tool confirmations, clear `closed_session_ids` | Working dir/provider updates may be written; no message changes | Errors map to `resource_not_found`/internal; state-machine turns resumed in a spawned task with a drop guard | `crates/goose/src/acp/server/load_session.rs:382-486`, `251-380` |
| Prompt | Claim run in registry, convert blocks to a `Message`, `agent.reply`, forward stream, send usage + `PromptResponse` | User message persisted by the agent before streaming; responses persisted per effect/iteration | `clear_active_run` + active-run-id notification on every error path | `crates/goose/src/acp/server.rs:2263-2368` |
| Cancel | Look up cancel token in registry, cancel it | None directly; agent synthesizes a cancellation response message that is persisted | Unknown sessions only warn | `crates/goose/src/acp/server.rs:2412-2429`, `crates/goose-agent/src/inference.rs:474-479` |
| Close/delete | Close: mark closed, cancel run, drop agent and session entry (row kept; blocked from reuse until load). Delete: same plus `delete_session` row removal | Close none; delete removes session+messages | Both cancel the active run first | `crates/goose/src/acp/server.rs:2622-2645`, `crates/goose/src/acp/server/manage_sessions.rs:98-115` |

### Durable representation

SQLite via sqlx: `sessions` (id, name, session_type, working_dir, provider_name, model_config_json, goose_mode, usage columns, recipe, parent id), `messages` (autoincrement id, message_id, session_id, role, content_json, created_timestamp, metadata_json), and `usage_ledger` (`crates/goose/src/session/session_manager.rs:1027-1100`). Ordering key is `(created_timestamp, id)`; `add_message` clamps a message's timestamp to be at least the latest stored one so prepared replies never sort ahead (`crates/goose/src/session/session_manager.rs:1887`, `1924-1933`). Commit boundary is one `BEGIN IMMEDIATE` transaction per message (or per conversation replace/compaction); `updated_at` is bumped in the same transaction (`crates/goose/src/session/session_manager.rs:1919-1962`). Visibility flags in metadata (`user_visible`/`agent_visible`) mark agent-only kickoffs and tool pairs; compaction replaces the conversation wholesale (`crates/goose/src/session/session_manager.rs:1964-1993`). Stored history is authoritative: the runtime reloads it per turn (`crates/goose/src/agents/state_machine/session.rs:198-213`) rather than keeping a parallel in-memory transcript.

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | Each `PromptRequest` is a spawned task; registry is keyed by session; SQLite writes are short per-message transactions | `crates/goose/src/acp/server/dispatch.rs:124-141`, `crates/goose/src/execution/active_run.rs:28` |
| Two prompts in the same session | `rejected` | `ActiveRunRegistry.start_prompt_run` under one `Mutex<HashMap>` returns `AgentRunExists`; mapped to `invalid_params` suggesting `_goose/unstable/session/steer` | `crates/goose/src/execution/active_run.rs:32-65`, `crates/goose/src/acp/server.rs:1907-1921` |
| Load/resume during an active prompt | partial | Not blocked: load re-registers the agent and replays history; a pending-confirmation state-machine turn would fail `start_active_run` while a prompt holds the session. Ordinary loads during a run are unguarded | `crates/goose/src/acp/server/load_session.rs:260-266` (Inference) |
| Delete/close during an active prompt | cancel-then-remove | Close/delete cancel the run token before dropping state; prompt task then observes cancellation and clears the (already removed) run | `crates/goose/src/acp/server.rs:2626-2641`, `crates/goose/src/acp/server/manage_sessions.rs:103-113` |
| Cancellation isolation | per session | Cancel token is fetched by session ID from the registry; other sessions unaffected | `crates/goose/src/acp/server.rs:2418-2424` |

Shared-state coupling beyond the registry: `AgentManager` serializes agent creation per session via per-session locks and evicts via a 100-entry LRU (a running agent could in principle be evicted under churn; re-creation restores provider/extensions from the session row, `crates/goose/src/execution/manager.rs:206-234`). SQLite serializes writers at transaction scope, including across processes (`crates/goose/src/session/session_manager.rs:994-1007`). `forward_agent_stream` also verifies the session is still registered on every message event and errors out otherwise (`crates/goose/src/acp/server.rs:2150-2155`).

## Event and data flow

### New prompt: ACP client to live response

1. Dispatch spawns a task per `PromptRequest` (`crates/goose/src/acp/server/dispatch.rs:124-141`).
2. `on_prompt` resolves (lazily creating) the session agent, then atomically claims a run: run_id `run_{uuid}`, fresh `CancellationToken`, registry insert; rejection ends the request (`crates/goose/src/acp/server.rs:2263-2284`).
3. A drop guard is armed; the new `activeRunId` is announced via a `SessionInfoUpdate` meta notification (`crates/goose/src/acp/server.rs:2289-2305`).
4. Prompt blocks (text/image/resource) become one user `Message` (`crates/goose/src/acp/server.rs:1321-1359`, `2316`).
5. `agent.reply` persists the user message first (state machine: `crates/goose/src/agents/agent.rs:1790-1792`; legacy: `2288-2292`) and returns an `AgentEvent` stream (`Message`, `Usage`, `MessageUsage`, `McpNotification`, `HistoryReplaced`; `crates/goose-agent/src/events.rs:9-18`).
6. The loop body (machine ops or legacy loop) runs inference/tools; streamed chunks are emitted as they arrive, tool execution produces request/response message effects (`crates/goose-agent/src/inference.rs:445-464`, `crates/goose/src/agents/state_machine/ops_toolcalling.rs:1021`).
7. `forward_agent_stream` translates each event into ACP: per-content-item `AgentMessageChunk`/`UserMessageChunk`/`AgentThoughtChunk`, initial `ToolCall` (status Pending) on `ToolRequest`, `ToolCallUpdate` (Completed/Failed) on `ToolResponse`, `RequestPermissionRequest` on `ActionRequired::ToolConfirmation`, usage notifications on `Usage` (`crates/goose/src/acp/server.rs:2140-2237`, `1361-1562`, `crates/goose/src/acp/server/tool_calls/conversion.rs:118-146`, `294-296`).
8. On stream end, the run is cleared, final usage is fetched from SQLite and pushed, and the `PromptResponse` carries `StopReason` plus totals (`crates/goose/src/acp/server.rs:2352-2367`, `728-743`).

### Durable history to ACP client

1. `handle_load_session` reads the session **with** conversation from SQLite (`crates/goose/src/acp/server/load_session.rs:391-398`).
2. `replay_conversation_to_client` filters to `is_user_visible` messages and maps each content item: text/image → `UserMessageChunk`/`AgentMessageChunk`, `ToolRequest` → initial `ToolCall` (with persisted enrichment titles), `ToolResponse` → `ToolCallUpdate`, thinking → `AgentThoughtChunk`; per-message usage is re-sent for goose-capable clients (`crates/goose/src/acp/server/load_session.rs:24-36`, `108-218`).
3. An optional `replayTail` meta trims the replay to roughly the last N messages, snapping to a turn boundary so tool pairs stay together; the skip count is reported via `replaySkipped` meta (`crates/goose/src/acp/server/load_session.rs:91-100`, `446-452`).
4. After replay, the agent is registered, the nested provider session resumed, and pending tool confirmations either re-sent as permission requests or drive a resumed state-machine turn in a spawned task (`crates/goose/src/acp/server/load_session.rs:414-482`).
5. Events that cannot be reconstructed: transient system notifications are skipped in replay (`load_session.rs:199`), and streamed chunk boundaries are preserved only insofar as each persisted message replays as one chunk.

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `Message` (user) | none (echoed only for steers) | one `messages` row | persisted before streaming begins | `crates/goose/src/agents/agent.rs:1790-1792`, `2288-2292` |
| Assistant text | streamed chunks → accumulated `AppendMessage` effects | `AgentMessageChunk` per chunk, live | merged/consecutive messages persisted as rows after emission | state machine: persist at effect application; legacy: batch at loop-iteration end | `crates/goose-agent/src/inference.rs:445-464`, `493`; `crates/goose/src/agents/state_machine/session.rs:53-55`; `crates/goose/src/agents/agent.rs:3499-3502` |
| Reasoning/thought | `Thinking` content in message | `AgentThoughtChunk` | inside message content_json | with its message | `crates/goose/src/acp/server.rs:1397-1405`, `load_session.rs:182-190` |
| Tool call | `ToolRequest` content | `ToolCall` (Pending) + optional enrichments | assistant message row (with tool_meta) | with the turn's message effects | `crates/goose/src/acp/server.rs:1513-1545`, `crates/goose/src/session/session_manager.rs:701-710` |
| Tool result | `ToolResponse` content | `ToolCallUpdate` (Completed/Failed) | user-role message row | with the turn's message effects | `crates/goose/src/acp/server.rs:1547-1562`, `conversion.rs:294-296` |
| Completion/failure | end of stream / `MessageContent::Error` / provider errors | `PromptResponse` (stop reason) or `AgentMessageChunk` for error text | error rows persisted (auth errors persisted+yielded; some legacy error texts yielded unpersisted) | end of turn | `crates/goose/src/acp/server.rs:1655-1684`, `crates/goose/src/agents/agent.rs:3230-3263` |

Usage is persisted (ledger + session columns) before `AgentEvent::Usage` is emitted, so ACP usage updates always reflect committed data (`crates/goose/src/agents/state_machine/usage.rs:62-79`, `crates/goose/src/acp/server.rs:2214-2217`).

### Subsequent-prompt reconstruction

On the next prompt, the runtime reloads the conversation from SQLite and rebuilds provider input by projecting agent-visible messages and merging consecutive same-role messages (`crates/goose-agent/src/inference.rs:377-382`, `crates/goose/src/agents/state_machine/session.rs:33-36`, `198-213`). The legacy loop likewise reloads the session at prompt start (`crates/goose/src/agents/agent.rs:2136-2150`). Model context is therefore reconstructed from the same rows used for replay; compaction or `/clear` rewrite history via `replace_conversation` and surface `HistoryReplaced` (`crates/goose/src/agents/agent.rs:3164-3168`). For nested ACP providers, goose sends a bounded handoff memo instead of raw history when the inner agent cannot resume (`crates/goose/src/acp/provider.rs:635-666`).

### Ordering, cancellation, failure, and backpressure

- Ordering: events are forwarded strictly in stream order on one task; per-session run exclusion prevents interleaved turns. Replay preserves `(created_timestamp, id)` order with clamped timestamps (`crates/goose/src/session/session_manager.rs:1924-1933`).
- Cancellation: the token is checked between events and inside inference via `tokio::select!`; a cancellation response message is synthesized, emitted, and persisted (`crates/goose-agent/src/inference.rs:420-479`). The ACP request returns `StopReason::Cancelled`. Pending steers are discarded when the run clears (`crates/goose/src/acp/server.rs:1923-1946`). Legacy-path persistence batches per iteration, so a cancel between batches can lose the in-flight batch (Inference from `crates/goose/src/agents/agent.rs:2586-2589`, `3499-3502`).
- Failure: errors become ACP `internal_error` responses or error-content messages; a failed mid-turn usage update only warns (`crates/goose/src/acp/server.rs:2218-2229`, `2232-2235`).
- Backpressure: the internal event channel is bounded (32); a slow client drains `forward_agent_stream` slowly, and `Emitter::emit` awaits propagate that backpressure to the machine loop — a slow client can stall its own turn, but not other sessions (`crates/goose/src/agents/agent.rs:1989`, `crates/goose-agent/src/operation.rs:253-255`).

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | Registry + LRU keyed by session ID | `crates/goose/src/acp/server.rs:321`, `manager.rs:34` |
| Concurrent work across sessions | `yes` | Independent spawned prompt tasks; no cross-session lock | `crates/goose/src/acp/server/dispatch.rs:128` |
| Same-session prompt exclusion | `yes` | Second prompt rejected with "session already has active run"; steering offered | `crates/goose/src/acp/server.rs:1907-1919` |
| Durable sessions | `yes` | SQLite sessions/messages/usage rows | `crates/goose/src/session/session_manager.rs:1027-1100` |
| Session list | `yes` | Paged list (50/page) limited to user/scheduled/acp types | `crates/goose/src/acp/server/list_sessions.rs:15`, `48-75` |
| Session load/resume | `yes` | Full load path incl. lazy activation from disk for unknown-but-stored IDs | `crates/goose/src/acp/server/load_session.rs:382-486`, `crates/goose/src/acp/server.rs:1859-1891` |
| History replay to ACP client | `yes` | Chunks + tool calls replayed; optional `replayTail` | `crates/goose/src/acp/server/load_session.rs:108-218` |
| Prior history reused by model | `yes` | Conversation reloaded per turn; consecutive messages merged for provider | `crates/goose-agent/src/inference.rs:377-382` |
| Prompt cancellation | `yes` | Token-based, per session; Cancelled stop reason | `crates/goose/src/acp/server.rs:2412-2429`, `735-743` |
| Tool-call progress updates | `yes` | Pending initial call, Completed/Failed updates, MCP notifications as updates | `crates/goose/src/acp/server.rs:2196-2203`, `conversion.rs:118-146` |
| Partial-output persistence | `partial` | Complete messages persisted as produced; streamed chunks are emitted before the merged message is persisted, and not themselves persisted | `crates/goose-agent/src/inference.rs:445-464`, `crates/goose/src/agents/state_machine/session.rs:53-55` |
| Recovery after process restart | `yes` | Load reconstructs agent from the session row; pending tool confirmations resume on load; nested provider session re-associated | `crates/goose/src/acp/server/load_session.rs:414-482`, `crates/goose/src/acp/server.rs:203-223` |

## Design assessment

### Strengths

- Session ownership sits in storage, not the protocol layer: the state machine re-reads persisted state each step, so ACP, CLI, and desktop share one durable model (`crates/goose/src/agents/state_machine/session.rs:169-239`).
- The active-run registry makes same-session exclusion explicit and connection-independent, with a drop guard covering abnormal task termination (`crates/goose/src/acp/server.rs:249-278`).
- Replay is turn-boundary-aware (`replayTail`), avoiding split tool pairs — a subtle detail most replays get wrong (`crates/goose/src/acp/server/load_session.rs:86-100`).

### Tradeoffs and limitations

- Live-vs-durable ordering is not uniform: streamed text is emitted before persistence while tool confirmations are persisted before emission; clients cannot assume durability on receipt (`crates/goose/src/agents/state_machine/session.rs:116-136`).
- The two agent loops persist at different points (per-effect vs per-iteration batch), so cancellation semantics differ by configuration (`crates/goose/src/agents/agent.rs:3499-3502`).
- Session close is a process-local, in-memory concept (`closed_session_ids`); the durable row survives and is reactivated by a later load, which may surprise clients expecting close to be terminal (`crates/goose/src/acp/server.rs:2622-2645`, `load_session.rs:484`).

### Ideas relevant to Ox

- A tiny registry mapping ACP session → runtime handle plus an atomic "claim run" step is a cheap, effective same-session guard; the cost is a second process-local map that must stay consistent with close/delete. (Fact: goose added a drop guard after observing leaked runs.)
- Persist-before-emit for state-changing events (tool confirmations, usage) makes redelivery on load trustworthy; the tradeoff is added latency between model output and client visibility. (Researcher judgment.)
- Turn-boundary-aware tail replay is directly relevant to Ox's replay design. (Fact: implemented in `load_session.rs:86-100`.)

## Unknowns and conflicts

- Legacy loop cancellation-durability: whether the in-flight `messages_to_add` batch is persisted when the cancel `break` fires between batches was not exhaustively traced (searched `crates/goose/src/agents/agent.rs` lines 2540-3530); the state machine path persists via effects before yielding confirmations.
- Which loop runs by default depends on `GOOSE_STATE_MACHINE` (off by default at this revision) and per-request `_meta.goose.unrolledAgentLoop` (`crates/goose/src/agents/state_machine/mod.rs:72-76`, `crates/goose/src/acp/server.rs:421-426`); documentation describing either loop as the only path would disagree with code.
- "Not found": a mechanism preventing `session/load` while a prompt is active (searched `load_session.rs`, `active_run.rs`, `dispatch.rs`); load is not guarded against an in-flight run.
- "Not found": automatic reattachment/replay of sessions after process restart without an explicit client `session/load` (searched `server.rs`, `server_factory.rs`); recovery is client-driven.
- Doc conflict noted in code: `goose acp`'s data-dir TODO at `crates/goose/src/acp/server.rs:947` says global `Paths::in_state_dir` reads ignore the injected `data_dir`, so tests may not fully isolate storage.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `crates/goose/src/acp/server.rs:2652-2726` (`serve`, `run`), `crates/goose/src/acp/server/dispatch.rs:4-147` (`handle_dispatch_from`) | Transport, capability negotiation, request routing |
| Session management | `crates/goose/src/acp/server/new_session.rs:36-125`, `crates/goose/src/acp/server/load_session.rs:382-486`, `crates/goose/src/acp/server/manage_sessions.rs:98-115` | Create/load/close/delete lifecycles |
| Concurrency | `crates/goose/src/execution/active_run.rs:26-171`, `crates/goose/src/acp/server.rs:1893-1974`, `crates/goose/src/execution/manager.rs:33-47` | Run exclusion, cancel tokens, agent LRU |
| Persistence | `crates/goose/src/session/session_manager.rs:994-1143`, `1617-1714`, `1919-1993` | Schema, session/message CRUD, commit boundaries |
| Event translation | `crates/goose/src/acp/server.rs:2120-2253` (`forward_agent_stream`), `1361-1562`, `crates/goose/src/acp/server/tool_calls/conversion.rs:94-160`, `crates/goose/src/acp/tool_call_notifier.rs:20-38` | AgentEvent → ACP updates |
| Replay/reconstruction | `crates/goose/src/acp/server/load_session.rs:24-218`, `crates/goose-agent/src/inference.rs:377-382` | History → client; history → model input |
| Cancellation/errors | `crates/goose/src/acp/server.rs:2412-2429`, `crates/goose-agent/src/inference.rs:420-491`, `crates/goose/src/acp/server.rs:1655-1684` | Cancel token flow, synthesized responses, error mapping |
| LLM abstraction | `crates/goose-provider-types/src/base.rs:327-332`, `472-504` (`Provider` trait, `MessageStream`), `crates/goose/src/providers/provider_registry.rs:96-363`, `crates/goose/src/providers/init.rs:57-179` | Provider trait, registry, built-in provider set |
| LLM integration path | `crates/goose-agent/src/inference.rs:344-494`, `crates/goose/src/agents/state_machine/ops_llm.rs:100-171`, `crates/goose/src/agents/reply_parts.rs:341-411`, `crates/goose/src/agents/agent.rs:2679-2687`, `3748-3799` | History projection, stream consumer, provider restore/selection |
| Request/stream normalization | `crates/goose-provider-types/src/formats/openai.rs:666-686`, `1686-1809`, `1233-1392`, `crates/goose-provider-types/src/formats/anthropic.rs:536`, `903`, `crates/goose-providers/src/openai_compatible.rs:237-259`, `crates/goose-providers/src/api_client.rs:583-613` | Tool schemas, payload construction, SSE parsing, auth |
| Provider credentials | `crates/goose/src/providers/openai_def.rs:96-114`, `crates/goose/src/providers/custom_provider_config.rs:15-20`, `crates/goose/src/config/base.rs:81-90`, `912` | API keys, keyring/secrets-file storage |
| Delegated model boundary | `crates/goose/src/providers/claude_code.rs:675-745`, `crates/goose/src/acp/provider.rs:677-820` | Wrapped CLI/ACP providers; inner model loop outside repo |

## Research notes

- **Revision inspected:** `2090ad1c65ddb39497601a936a9fe17d66254bfe`
- **Primary evidence:** `crates/goose/src/acp/server.rs` and `server/*` submodules; `crates/goose/src/acp/provider.rs`; `crates/goose/src/execution/{active_run,manager}.rs`; `crates/goose/src/session/session_manager.rs`; `crates/goose/src/agents/agent.rs`; `crates/goose/src/agents/state_machine/{mod,session,usage,ops_llm}.rs`; `crates/goose-agent/src/{events,inference,operation}.rs`; `crates/goose/src/acp/server_factory.rs`. Unit tests read as corroboration (e.g. `active_run.rs` tests, `load_session.rs` replay tests, `state_machine/tests/*`).
- **Relevant docs:** `AGENTS.md` (state-machine migration note; legacy and state-machine paths both maintained).
- **Commands/tests run:** Read-only inspection (grep/read) of the pinned checkout; no tests executed.
- **Report confidence:** `high` — the ACP server, session store, and concurrency paths were read directly at the pinned revision; residual uncertainty is confined to legacy-loop edge behavior, labeled Inference above.
