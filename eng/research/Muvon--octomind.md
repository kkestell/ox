---
project: "Muvon/octomind"
repository: "https://github.com/Muvon/octomind"
revision: "4bffaab3d37a1259ddb9fb2170b8067efcf72cb3"
researched_at: "2026-09-19"
primary_language: "rust"
implementation_form: "native-agent"
process_model: "single-process"
session_owner: "acp-layer"
durability: "local-files"
cross_session_concurrency: "concurrent"
same_session_concurrency: "serialized"
event_delivery: "channel"
resume_strategy: "reconstruct"
overall_confidence: "high"
---

# Muvon/octomind ACP architecture

## Executive summary

- **ACP boundary:** Native. `octomind acp` runs the agent itself as an ACP server over stdio using the `agent_client_protocol` SDK; there is no wrapped process (`src/acp/mod.rs:49` (`run`), `src/acp/agent.rs:1677` (`serve`)). A `!Send`→`Send` bridge forwards each typed request over an mpsc channel to a single-threaded actor that owns all session state (`src/acp/agent.rs:1559`-`1647`).
- **Session model:** The ACP session ID is the octomind session name — a `YYMMDD-basename-HHMM-uuid` string that is also the stem of the durable transcript file (`src/session/chat/session/core.rs:142` (`generate_session_name`), `src/session/chat/session/core.rs:527`). Active `ChatSession`s live in an `Rc<RefCell<HashMap>>` inside `OctomindAgent` (`src/acp/agent.rs:56`); the same runtime code serves the WebSocket server.
- **Concurrency:** Different sessions' prompts run as independent `spawn_local` tasks and interleave on one thread — no global session lock (`src/acp/agent.rs:1633`). Prompts for the *same* session serialize on a per-session `tokio::sync::Mutex` shared with the inbox monitor and ext commands (`src/acp/mod.rs:32` (`SessionLocks`), `src/acp/agent.rs:827`).
- **Durability and replay:** Append-only, per-session zstd JSONL in the platform sessions directory; every message is persisted as one frame *before* entering memory ("atomic add", `src/session/chat/session/messages.rs:301`), and `save()` appends a full-state SUMMARY snapshot (`src/session/mod.rs:712` (`Session::save`)). Resume re-reads the whole log and reconstructs messages, runtime state, and synthetic tool results (`src/session/persistence.rs:803` (`load_session`)). Loaded history is **not** replayed to the ACP client — only to the model.
- **Event flow:** The session pipeline emits internal `ServerMessage`s into an unbounded mpsc channel; a per-turn forward task translates them to ACP `SessionUpdate` notifications (`src/acp/agent.rs:1203`, `src/acp/agent.rs:1217`). Assistant text and thinking arrive as whole-message chunks, not provider-token deltas.
- **Notable uncertainty:** Cross-session concurrency is real but single-threaded and coupled through process-global registries (notification senders, tool map, thread-local fallbacks); the code is careful (`with_session_id` scoping), but no test exercises two ACP prompts interleaving in one process.

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `native-agent` | The binary implements ACP directly over its own session runtime; no adapter subprocess | `src/acp/agent.rs:1677` (`serve`), `src/acp/mod.rs:15`-`18` |
| Process model | `single-process` | One OS process per `octomind acp` invocation serves every session on a single-threaded `LocalSet` | `src/acp/mod.rs:80`-`84`, `src/acp/agent.rs:50`, `src/acp/agent.rs:1685` |
| Session owner | `acp-layer` | `OctomindAgent` owns active `ChatSession`s and drives the shared session pipeline per request | `src/acp/agent.rs:51`-`82`, `src/acp/agent.rs:1009`-`1017` |
| Durability | `local-files` | Append-only `.jsonl.zst` transcript per session under the sessions dir | `src/session/chat/session/core.rs:527`, `src/session/persistence.rs:955` (`append_to_session_file`) |
| Cross-session concurrency | `concurrent` | One spawned task per request; only per-session locks; tasks interleave on one thread | `src/acp/agent.rs:1590`-`1647` (`run_actor`), `src/acp/mod.rs:30`-`32` |
| Same-session concurrency | `serialized` | Second prompt awaits the session's exclusion mutex (queued, not rejected) | `src/acp/agent.rs:819`-`828`, `src/acp/mod.rs:30`-`32` |
| Event delivery | `channel` | Pipeline → unbounded mpsc → forward task → channel-backed `ConnectionTo<Client>` | `src/session/output.rs:133`-`150` (`WebSocketSink`), `src/acp/agent.rs:1202`-`1217` |
| Resume strategy | `reconstruct` | Full log parse on load: SUMMARY + message lines + markers + runtime state | `src/session/persistence.rs:296` (`parse_log_lines`), `src/session/persistence.rs:803` (`load_session`) |

## System architecture

```text
ACP client (Zed / JetBrains)
   │  JSON-RPC over stdio (initialize / new_session / load_session / prompt / cancel / ext)
   ▼
agent_client_protocol SDK (Send handler shims)          src/acp/agent.rs:1702-1809
   │  Command enum + oneshot reply (unbounded mpsc)
   ▼
actor on tokio LocalSet (single thread, !Send ok)       src/acp/agent.rs:1593 (run_actor)
   │  spawn_local per request
   ▼
OctomindAgent                                           src/acp/agent.rs:51
   ├─ sessions: HashMap<session_id, (ChatSession, cwd)>
   ├─ session_locks: HashMap<session_id, Mutex<()>>     (same-session exclusion)
   ├─ cancellations: HashMap<session_id, SessionCancellation>
   └─ conn: ConnectionTo<Client>  ◀───────────────────── live session/update notifications
   ▼
ChatSession + shared pipeline (also used by CLI/WebSocket)
   ├─ prepare_for_api_call (task resolve, compression)  src/session/chat/session/api_prep.rs:24
   ├─ execute_api_call_and_process_response             src/session/chat/session/api_executor.rs:122
   │     └─ process_response loop (tool rounds)         src/session/chat/response.rs:445
   ├─ MCP tool servers (stdio/HTTP subprocesses)
   └─ WebSocketSink ──► unbounded mpsc ──► forward task ──► translate ──► ConnectionTo
        (ServerMessage)        (agent.rs:1203)  (agent.rs:1217)      (agent.rs:263)
   ▼
Durable storage: <sessions dir>/<session name>.jsonl.zst (append-only zstd JSONL)
   ▲ messages persisted at add time; SUMMARY snapshot per save()
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `OctomindAgent` | ACP handlers; session registry, locks, cancellations, client connection | Process | Sessions map, per-session locks, cancellation handles, CLI-override one-shots | `src/acp/agent.rs:51`-`82` |
| Actor + `Command` bridge | Moves `Send` SDK callbacks into the `!Send` LocalSet world; dispatches each request on its own task | Process | mpsc channel only | `src/acp/agent.rs:1559`-`1685` |
| `ChatSession` | One conversation: model config, message list, supervisor state, save | Session (map entry) | `Session { info, messages, session_file }` plus runtime fields | `src/session/chat/session/core.rs:421`-`479`, `src/session/mod.rs:527`-`532` |
| Inbox monitor task | Per-session background loop: schedules/inbox → full AI turns without a prompt | Session (until inbox removed) | none (uses session-scoped registries + the session lock) | `src/acp/agent.rs:396` (`spawn_inbox_monitor`) |
| Forward task | Drains the per-turn `ServerMessage` channel into ACP notifications | Prompt | none | `src/acp/agent.rs:1217`-`1316` |
| MCP tool servers | External tool processes/HTTP endpoints | Process (idempotent re-init per role) | tool registry (`TOOL_MAP`) | `src/acp/agent.rs:692`-`697` |
| Session files | Append-only transcript + metadata | Durable | messages, `SessionInfo` snapshots, log markers | `src/session/persistence.rs:942`-`969` |

### ACP surface

Transport is stdio: `ByteStreams` over tokio stdin/stdout, wrapped so stdin EOF fires a oneshot that shuts the connection down deterministically (`src/acp/agent.rs:1693`-`1700`, `src/acp/agent.rs:1835`-`1854`). The SDK is the `agent_client_protocol` crate; handlers are registered on `Agent.builder()` for `InitializeRequest`, `AuthenticateRequest`, `NewSessionRequest`, `LoadSessionRequest`, `PromptRequest`, `ClientRequest` (ext-method fallback), and `CancelNotification` (`src/acp/agent.rs:1702`-`1809`). Because SDK handler futures must be `Send` while the session machinery is `!Send`, each handler is a thin shim that sends a `Command` to the actor and awaits a oneshot reply (`src/acp/agent.rs:1652`-`1674` (`forward`)). `initialize` advertises `load_session: true`, image + embedded-context prompt capabilities, HTTP MCP transport, and an `octomind.dev` meta block (`src/acp/agent.rs:636`-`651`). A `session/update` stream carries everything; slash commands additionally arrive as prompts or via the `_octomind/command` ext method (`src/acp/commands.rs:33` (`COMMAND_NAMESPACE`), `src/acp/commands.rs:171` (`handle_ext_method`)). The ACP layer is native: it calls the same `setup_and_initialize_session` / `execute_api_call_and_process_response` functions the CLI and WebSocket server use (`src/session/chat/session/setup.rs:65`, `src/acp/agent.rs:700`-`707`).

### Runtime and process boundaries

All ACP work runs on one tokio `LocalSet` in the server process (`src/acp/mod.rs:80`-`84`). The actor dispatches every command via `spawn_local`, so a long prompt does not block `cancel` — cancel is additionally executed inline in the dispatch loop because it only flips a watch-channel flag (`src/acp/agent.rs:1604`-`1607`). Concurrency hygiene rests on three mechanisms: (1) task-local `CURRENT_SESSION_ID` scoping all session-keyed registries (inbox, notification senders, workdirs, roles) (`src/session/context.rs:83`-`106`); (2) per-session `tokio::sync::Mutex`es held across an entire prompt/inbox-turn/ext-command (`src/acp/agent.rs:56`-`62`, `src/acp/agent.rs:819`-`828`); (3) exclusive ownership by *removing* the `ChatSession` from the map while a turn runs and re-inserting it afterwards (`src/acp/agent.rs:1009`-`1017`, `src/acp/agent.rs:1401`-`1404`). Working directories are session-keyed when a session context is active, so two interleaved prompts do not clobber each other's cwd; the thread-local is only a fallback (`src/mcp/workdir.rs:48`-`61`). MCP tool servers are shared process-wide subprocesses, idempotently initialized per role (`src/acp/agent.rs:692`-`697`). Cleanup paths: sessions are never individually closed over ACP — they stay in the map until stdin EOF; after EOF the process waits until every session has no pending async work (`src/acp/agent.rs:136`-`175` (`wait_until_idle`), `src/acp/agent.rs:1810`-`1822`).

## LLM abstraction and integration

### Abstraction

The project uses a **named external LLM abstraction library: `octolib`** (crates.io `0.39.0`, compiled with its `llm` feature, `Cargo.toml:41`). It is not an in-repo abstraction and not a wrapped executable; `octolib` is a registry dependency (`Cargo.lock:3249`-`3252`) linked into the same process. Octomind's own `src/providers.rs` is explicitly a thin adapter over it — "Provider abstraction layer - now powered by octolib … adapter between Octomind and the octolib provider system" (`src/providers.rs:15`-`18`) — and re-exports octolib's `AiProvider` trait, concrete provider structs, and `ProviderFactory` under compatibility aliases (`src/providers.rs:25`-`32`). The model loop and provider HTTP/normalization live inside `octolib`, outside this checkout. The same crate also backs embeddings and the evaluation model, through separate entry points (`src/embeddings/mod.rs:33`, `src/supervisor/evaluate.rs:589`).

### Integration path

1. The session's `Vec<crate::session::Message>` is handed to `ChatCompletionWithValidationParams::from_profile` (`src/session/chat/session/api_executor.rs:418`-`422`).
2. `chat_completion_with_validation` resolves the provider for the model string via `ProviderFactory::get_provider_for_model` (`src/session/completion.rs:230`), gates on the provider's input-token limit, and builds the Octomind-side `ChatCompletionParams` (`src/session/completion.rs:277`-`315`).
3. `ChatCompletionParams::to_octolib_params` converts each message with `convert_message_to_octolib` (`src/providers.rs:314`), sets the 1h system-cache TTL, appends a synthetic `"Please continue."` user turn when the last non-system message is an assistant (`src/providers.rs:219`-`241`), attaches the cancellation watch, tools, schema, and the `X-Model-Purpose` routing header (`src/providers.rs:262`-`265`), and produces an `octolib::llm::ChatCompletionParams` (`src/providers.rs:200`-`310`).
4. MCP tool schemas are fetched from `crate::mcp::get_available_functions` and mapped to `octolib::llm::FunctionDefinition` (`src/providers.rs:272`-`298`).
5. The request is issued as one non-streaming call, `provider.chat_completion(octolib_params).await` (`src/session/completion.rs:345`); empty completions are retried within the request's `max_retries` budget (`src/session/completion.rs:317`-`368`).
6. The provider's `ProviderResponse` is normalized back by `convert_response_from_octolib`, mapping octolib tool calls to `crate::mcp::McpToolCall` (`src/providers.rs:537`-`559`).
7. `process_response` drives the tool loop: it emits `ServerMessage::ToolUse` before execution and `ServerMessage::ToolResult` after, then makes the follow-up call through `make_follow_up_api_call` (`src/session/chat/response.rs:445`, `:576`-`633`; `src/session/chat/response/tool_result_processor.rs:413`-`437`), repeating until a tool-free response.

Because `octolib` is compiled in, there is no delegated process boundary to trace past; the boundary is the `provider.chat_completion` trait call. What happens beyond it (wire format, auth, retries) is not visible in this checkout.

### Provider and tool boundary

- **Multi-provider and selection.** The abstraction is multi-provider. Model strings are `provider:model` (`config-templates/default.toml:105`-`106`, `src/commands/config.rs:125`); `ProviderFactory::parse_model` splits the prefix into a provider name and the concrete model (`src/session/chat/session/setup.rs:315`, `src/session/model_utils.rs:24`). Concrete providers re-exported by the adapter are `AnthropicProvider`, `OpenAiProvider`, `OpenRouterProvider`, `DeepSeekProvider`, `AmazonBedrockProvider`, `GoogleVertexProvider`, and `CloudflareWorkersAiProvider` (`src/providers.rs:26`-`28`); an `octohub` gateway provider is named in error handling and account plumbing (`src/session/chat/session/error_utils.rs:206`, `src/account.rs:20`-`21`). The model string grammar and registry are implemented in `octolib`, not here.
- **Provider-specific code.** Octomind contains no provider wire code; provider branches appear only as user-facing error hints keyed on the parsed provider name (`src/session/chat/session/error_utils.rs:202`-`236`). All provider-specific request/response normalization is inside `octolib`; the adapter only converts between Octomind and octolib types (`src/providers.rs:314`, `:537`).
- **Credentials/configuration.** API keys are environment-only: setting one through config is rejected with a directive to use `export <PROVIDER>_API_KEY` (`src/commands/config.rs:134`-`163`), and the surfaced env vars are `OPENROUTER_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_APPLICATION_CREDENTIALS`, `AWS_ACCESS_KEY_ID`, `CLOUDFLARE_API_TOKEN` (`src/commands/config.rs:425`-`430`). The legacy `ProvidersConfig`/`ProviderConfig` structs still carry `api_key` fields (`src/config/providers.rs:18`-`31`), but credential presence is checked by calling the octolib provider's `get_api_key()` at session setup (`src/session/chat/session/setup.rs:314`-`325`); the octohub gateway reads `OCTOHUB_API_KEY` (`src/account.rs:43`).
- **Tool schemas and loop.** Tool definitions originate from the MCP layer (`crate::mcp::get_available_functions`) and are converted to octolib `FunctionDefinition` with an optional last-tool cache-control marker (`src/providers.rs:272`-`298`). Tool-call arguments from history are normalized by `convert_to_generic_tool_calls` (`src/providers.rs:475`-`534`); tool routing/execution stays in-repo (`src/mcp/tool_map.rs`, `src/session/chat/response.rs`). The evaluation seam uses a distinct octolib factory, `EvaluationProviderFactory` / `octolib::evaluation::evaluate` (`src/supervisor/evaluate.rs:583`-`589`).

### Limits

- The `octolib` source is not present in the checkout (registry dependency, `Cargo.lock:3249`-`3252`), so provider HTTP transports, model-string parsing rules, auth/token refresh, retry/backoff internals, and any streaming API are not visible. Checked `src/providers.rs`, `src/session/completion.rs`, `src/session/model_utils.rs`, `Cargo.toml`, `Cargo.lock`.
- No provider-token streaming is invoked in this repository: every model call goes through `provider.chat_completion(...).await` (`src/session/completion.rs:345`, `:407`; `src/session/cache_keepalive.rs:211`), and a search of `src/` found no streaming completion consumer (the "stream" hits are the output sinks and zstd/tcp streams). Whether `octolib` could stream is **Not found** from this checkout.
- Credential precedence between the legacy config `api_key` fields and environment variables is resolved inside `octolib`; the in-repo code only observes `get_api_key()` success/failure (`src/session/chat/session/setup.rs:322`-`325`).

## Session model

### Identity and ownership

"Session" is `SessionInfo.name` (`src/session/mod.rs:344`-`432`). For ACP the mapping is one-to-one and stable across all three layers: ACP `session_id` == `chat_session.session.info.name` (`src/acp/agent.rs:709`) == stem of `<sessions dir>/<name>.jsonl.zst` (`src/session/chat/session/core.rs:527`). Names are generated at creation as `YYMMDD-<cwd basename>-HHMM-<4-char uuid>` (`src/session/chat/session/core.rs:141`-`159`) or inherited from one-shot CLI flags (`--name`/`--resume`/`--resume_recent`, consumed on the first `new_session` only, `src/acp/agent.rs:72`-`77`, `src/acp/agent.rs:187`-`199`). In-memory the active session is `(ChatSession, cwd)` in the agent's map; session-scoped side state (inbox, schedules, plan storage, notification senders, workdir) hangs off the task-local session ID, keyed by the same string (`src/session/context.rs:119`-`229`). There is no separate wrapped-agent ID and no lossy mapping.

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | `new_session` injects client MCP servers into a per-session config snapshot, initializes MCP, builds a `ChatSession`, sets up system prompt + cache, initializes session services (inbox/plan/schedule), registers cancellation, sends `available_commands_update`, spawns the inbox monitor | Transcript file is created lazily on first message write (SUMMARY line first) | Any setup error → `internal_error`; client-injected servers are scoped to a config clone, `self.config` untouched | `src/acp/agent.rs:661`-`767` (`new_session`), `src/session/chat/session/messages.rs:235`-`248` (`ensure_file_initialized`) |
| Load/resume | `load_session` resumes by client-supplied ID via `GenericSessionArgs { resume: Some(id) }`; explicit resume requires the file to exist and be parseable; same post-setup as create | None at load time (read-only reconstruction); subsequent writes go to the same file | Missing/corrupt file → `internal_error` surfaced to the client (no silent new-session fallback for explicit resumes) | `src/acp/agent.rs:1450`-`1530` (`load_session`), `src/session/chat/session/core.rs:540`-`548`, `src/session/chat/session/core.rs:773`-`781` |
| Prompt | See the full trace under "New prompt"; the session is removed from the map for exclusive processing, then re-inserted | User message, per-round assistant/tool messages, and a post-turn SUMMARY snapshot are appended | Errors re-insert the session before returning `internal_error` so later prompts still find it | `src/acp/agent.rs:769`-`1436` (`prompt`), `src/acp/agent.rs:1156`-`1171`, `src/acp/agent.rs:1397`-`1404` |
| Cancel | `cancel` notification flips the session's current watch-channel flag; the prompt path checks it before the API call, between tool rounds, and before the response is accepted | Cancelled rounds after tool execution leave no durable trace (see ordering section) | Return `StopReason::Cancelled`; unknown session is acknowledged no-op | `src/acp/agent.rs:1438`-`1448` (`cancel`), `src/session/cancellation.rs:160`-`196` |
| Close/delete | Not found as an ACP operation — no session/close handler, no map eviction except the inbox monitor exiting when the session's inbox was destroyed | Sessions persist on disk regardless | Process exit after stdin EOF + idle drain; telemetry recorded at disconnect | Searched `src/acp/` for close/delete/session list methods; only `initialize`, `authenticate`, `new_session`, `load_session`, `prompt`, `cancel`, `ext_method` exist (`src/acp/agent.rs:1702`-`1809`) |

### Durable representation

One file per session: `<sessions dir>/<name>.jsonl.zst`, appended as independent zstd frames, one JSON record per frame (`src/session/persistence.rs:953`-`969`). Record types: message lines (`Message` JSON, `src/session/mod.rs:100`-`123`), `SUMMARY` (full `SessionInfo` snapshot — the source of truth on reload), `STATS` (monotonic token/cost maxima), marker records `RESTORATION_POINT`, `COMPRESSION_POINT`, `TRUNCATION_POINT`, `OUTPUT_MODE_*`, `TOOL_CALL`, `COMMAND`, `KNOWLEDGE_ENTRY` (`src/session/persistence.rs:296`-`512`). Commit boundaries are per event: every user/system/tool/assistant message is serialized and appended *before* being pushed to the in-memory vector (`src/session/chat/session/messages.rs:255`-`261`, `:301`-`317`, `:454`-`462`, `:487`-`495`); `SessionInfo`-level state (tokens, cost, compression stats, anchor, evidence ledger) is committed by `chat_session.save()` appending a fresh SUMMARY, called after each turn and at several interior points (`src/acp/agent.rs:1394`-`1399`, `src/session/chat/session/messages.rs:84`-`88`, `src/session/chat/session/api_prep.rs:72`-`77`). Deliberately not persisted: the runtime-only active memory pack (materialized per request, removed before persistence, `src/session/chat/session/messages.rs:32`-`37`), self-report tokens (stripped before storage, `src/session/chat/response.rs:391`-`441`), inbox contents, and the in-memory session map itself. Stored history is authoritative for the model conversation; `SUMMARY` is authoritative for metadata with `STATS` as a newer-wins backstop (`src/session/persistence.rs:403`-`488`).

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | Each request is its own `spawn_local` task on the shared LocalSet; no global lock; they interleave at await points on one thread (not parallel threads) | `src/acp/agent.rs:1633`-`1637`, `src/acp/agent.rs:1593`-`1647` (`run_actor`) |
| Two prompts in the same session | `serialized` | Per-session `tokio::sync::Mutex`; the second prompt awaits acquisition (queued, not rejected, no cancel-previous); the lock is held for the whole prompt | `src/acp/agent.rs:819`-`828`, `src/acp/mod.rs:30`-`32` |
| Load/resume during an active prompt | Serialized onto the same session if the ID matches (load takes no lock, but the prompt holds exclusive map ownership; a load that recreates the entry mid-prompt would race the re-insert) | No lock acquisition in `load_session`; the map is `RefCell`-guarded, so interleaving is memory-safe but logical overlap is unguarded | `src/acp/agent.rs:1450`-`1530` (no `session_lock` call) |
| Delete/close during an active prompt | `n/a` | No close/delete exists; the closest analogue — the inbox monitor's session-gone check — re-queues the batch and exits | `src/acp/agent.rs:451`-`461` |
| Cancellation isolation | Per session (and per operation) | `SessionCancellation` swaps in a fresh watch channel per `new_operation()`, so cancelling session A never touches B, and orphaned tasks of an older operation keep seeing `true` | `src/session/cancellation.rs:49`-`65`, `src/session/cancellation.rs:160`-`165`, `src/acp/agent.rs:1438`-`1448` |

The critical section for a prompt is: acquire session mutex → remove `ChatSession` from the map → run pre-user inbox drain + main turn → re-insert → release. Because the map entry is *missing* while a turn runs, every other path that needs the session (monitor, ext commands, a queued prompt) must take the same lock first — the comments document that omitting this turned concurrent access into spurious `session not found` errors (`src/acp/agent.rs:57`-`62`, `src/acp/commands.rs:66`-`75`). Shared coupling beyond locks: the notification-sender registry is keyed by session so MCP progress lands on the right channel (`src/session/context.rs:124`-`157`, `src/mcp/process.rs:198`-`220`); the tool map and MCP server processes are process-global and initialized idempotently. The unbounded `ServerMessage` channel means a slow client never backpressures the agent, and unbounded `Command`/mpsc channels mean unbounded memory only if the client outpaces the single forward thread.

## Event and data flow

### New prompt: ACP client to live response

1. SDK shim forwards `PromptRequest` to the actor, which spawns a task running `prompt` (`src/acp/agent.rs:1749`-`1756`, `src/acp/agent.rs:1633`-`1637`).
2. Content blocks are split into text / base64 images / embedded video blobs; an empty prompt returns `EndTurn` immediately (`src/acp/agent.rs:772`-`815`).
3. Inside `with_session_id`, the per-session mutex is acquired (`src/acp/agent.rs:819`-`828`).
4. `/done` is intercepted (compression + optional trailing instructions); other `/commands` run through `process_command` with the result streamed as one `AgentMessageChunk`, then `EndTurn` (`src/acp/agent.rs:830`-`1007`).
5. The session is removed from the map for exclusive access; cwd restored; a fresh cancellation receiver is created *after* the pre-user inbox drain so it points at a live channel (`src/acp/agent.rs:1009`-`1039`, `src/acp/agent.rs:1147`-`1154`).
6. Due schedules and inbox messages that arrived before the prompt are drained and answered first, streaming like normal prompts (`src/acp/agent.rs:1041`-`1145`).
7. `run_pipe_if_enabled` (guardrails) may rewrite the input; pending image/video attached; `add_user_message` persists the user message atomically (`src/acp/agent.rs:1156`-`1187`, `src/session/chat/session/messages.rs:267`-`317`).
8. `prepare_for_api_call` resolves the task, runs conversation compression, and ensures cache markers (`src/session/chat/session/api_prep.rs:24`-`157`).
9. An unbounded mpsc + `WebSocketSink` is created and registered as the session's notification sender; a forward task translates each `ServerMessage` into `SessionUpdate` notifications — `AgentMessageChunk`, `AgentThoughtChunk`, `ToolCall` (InProgress with raw input), `ToolCallUpdate` (Completed/Failed with raw output), and `Cost`/`Evolution` as `SessionInfoUpdate` + `octomind.*` `_meta` blocks (`src/acp/agent.rs:1202`-`1316`).
10. `execute_api_call_and_process_response` awaits the provider response as a whole (`chat_completion_with_validation`, `src/session/chat/session/api_executor.rs:418`-`430`), then `process_response` runs the tool loop: emit `ToolUse` for each call, execute tools in parallel, emit `ToolResult`s, persist the assistant(tool_calls) message, persist tool messages, make the follow-up call, repeat until a tool-free final response, which is emitted as one `Assistant` message and persisted (`src/session/chat/response.rs:576`-`633`, `:882`-`947`, `:966`-`995`, `src/session/chat/response/tool_result_processor.rs:79`-`116`).
11. Supervisor post-turn machinery (verify-gate, plan reconcile, nudges) may recursively re-run the turn within the same prompt call (`src/session/chat/session/api_executor.rs:524`-`858`).
12. After the call: notification sender cleared, forward task drained, spending-stop and `octomind.verified`/`octomind.pending_work` meta notifications sent, `chat_session.save()` appends a SUMMARY, the session is re-inserted, and the inbox monitor is woken (`src/acp/agent.rs:1331`-`1407`).
13. The response returns `StopReason::Cancelled` if the cancellation watch is set, else `EndTurn`, with `octomind.pending_work`/`spending_stop` meta; a failed API call maps to `internal_error` (`src/acp/agent.rs:1409`-`1433`).

### Durable history to ACP client

1. Session load does **not** replay stored history to the client. `load_session` reconstructs the transcript from disk for the model's benefit and sends only `available_commands_update` before responding `LoadSessionResponse` (`src/acp/agent.rs:1450`-`1530`, `send_available_commands` at `src/acp/agent.rs:377`-`387`). No `SessionUpdate` for past messages, tool calls, or usage is generated anywhere in the load path (searched `src/acp/agent.rs` for `SessionUpdate` constructions outside the prompt/monitor paths: none).
2. Replay to the *model* happens on the next prompt: `prepare_for_api_call` / `chat_completion_with_validation` serialize the in-memory (reconstructed) message list directly, including cache markers, compression markers, and the `id` field used for provider conversation continuity (`src/session/chat/session/api_executor.rs:416`-`430`, `src/session/chat/response.rs:114`-`132`).
3. Information the client never recovers after reconnect/reload: prior assistant text, tool activity, and cost (except the session's cumulative totals if a client asks via ext commands). The inbox monitor does surface *future* injected messages as `UserMessageChunk` notifications, but that is live delivery, not replay (`src/acp/agent.rs:473`-`496`).

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `Message(role=user)` (+ images/videos) | none (client already has it) | message JSON line | Persisted before the model call, before any notification | `src/session/chat/session/messages.rs:285`-`319` |
| Injected inbox message | `Message` (system-managed wrapper) | `UserMessageChunk` with `[source] text` | message line via `add_inbox_batch` | Persisted before the answering API call | `src/acp/agent.rs:473`-`505`, `src/session/chat/session/messages.rs:416`-`429` |
| Assistant text (final) | `Message(role=assistant, id=response_id)` | one `AgentMessageChunk` (whole message) | message line | Notification emitted first (`response.rs:981`), persisted immediately after in `handle_final_response` | `src/session/chat/response.rs:966`-`995`, `:144`-`148` |
| Reasoning/thought | `ThinkingBlock` inside the assistant message | `AgentThoughtChunk` (whole block) | persisted only on tool-call rounds (`thinking` field); final-response persistence drops it | Emitted once per response before the round's tool execution | `src/session/chat/response.rs:73`-`82`, `:500`-`510`, `:340`-`360`; `src/session/chat/response.rs:116`-`132` (final, `thinking: None`) |
| Tool call | `McpToolCall` | `ToolCall` (InProgress, raw input) | assistant message with `tool_calls` JSON | Persisted *after* tools executed and results were emitted | `src/session/chat/response.rs:576`-`588`, `:892`-`900`, `:362`-`371` |
| Tool result | `McpToolResult` | `ToolCallUpdate` (Completed/Failed, raw output) | `Message(role=tool, tool_call_id, name)` | Persist-first inside `process_tool_results`, after the assistant message | `src/session/chat/response.rs:615`-`633`, `src/session/chat/response/tool_result_processor.rs:79`-`116`, `src/session/chat/session/messages.rs:432`-`477` |
| Completion/failure | `StopReason` decision in `prompt` | `PromptResponse` (EndTurn/Cancelled) + meta; `internal_error` on API failure | SUMMARY snapshot (tokens/cost/state) appended by `save()` just before the response | Persisted before the response is returned | `src/acp/agent.rs:1394`-`1433`, `src/session/mod.rs:711`-`722` |
| Usage/cost | `SessionInfo` counters, `CostPayload` | `SessionInfoUpdate` + `octomind.usage` `_meta` | counters folded into `SessionInfo`, committed with the next SUMMARY | Emitted at end of turn; durable on save | `src/session/chat/response.rs:1029`-`1050`, `src/acp/agent.rs:1253`-`1277` |

Persistence discipline is "atomic add" — build the message fully, append to the file, and only then push to memory, so a failed write leaves no orphaned in-memory state and no half-persisted tool sequence (`src/session/chat/session/messages.rs:301`-`317`, `:454`-`462`; rationale at `src/session/chat/session/messages.rs:487`-`495`). Exceptions to persist-before-notify exist: the final assistant message and the tool-round assistant message are both emitted (or their tool results emitted) before their durable write.

### Subsequent-prompt reconstruction

On resume, `parse_log_lines` streams the zstd log: the last `SUMMARY` fixes `SessionInfo`; message lines accumulate (respecting `RESTORATION_POINT`/`COMPRESSION_POINT`/`TRUNCATION_POINT`/`OUTPUT_MODE_REPLACE` clears); `TOOL_CALL` records rebuild assistant messages with `tool_calls` when the raw message line is absent; `COMMAND` records replay `/model`, `/role`, `/effort`; `STATS` entries only raise monotonic counters newer than the last SUMMARY (`src/session/persistence.rs:296`-`512`, `:835`-`868`). `reconstruct_messages` then applies runtime state, and `clean_interrupted_tool_calls` inserts synthetic `[Tool execution was interrupted by user]` tool results for any assistant tool call lacking a response, so the transcript is always API-valid (`src/session/persistence.rs:653`-`673`, `:195`-`281`). `ChatSession::initialize` recovers cost/spending checkpoints, evidence ledger, critical knowledge, turn-answer ledger, and recalculates token counters from the actual messages (`src/session/chat/session/core.rs:557`-`771`). The result is the full model context; lossy only in that compaction already replaced pre-marker messages at write time.

### Ordering, cancellation, failure, and backpressure

Guarantees: per-message atomic persistence gives a durable log whose ordering matches the in-memory message order; the per-session mutex serializes all writers to one session's file; cancellation is checked before each provider request, before tool execution, after tool execution, and before accepting the response (`src/session/chat/response.rs:314`-`320`, `:551`-`556`, `:882`-`889`, `src/session/chat/session/api_executor.rs:437`-`443`).

Gaps observed in the code:

- **Cancelled rounds vanish durably.** If cancellation arrives after tools executed but before the assistant message is added, the tools' side effects happened, `ToolUse`/`ToolResult` updates already reached the client, and the function returns without persisting anything for the round (`src/session/chat/response.rs:882`-`889` "Don't add assistant message since tools were cancelled"). A resumed session will not contain what the client saw.
- **Client-before-disk windows.** Final assistant text and tool-round assistant messages are emitted before their durable write (`src/session/chat/response.rs:981` vs `:147`; `:615`-`633` vs `:892`). A crash in that window loses them; on reload, `clean_interrupted_tool_calls` patches tool-call gaps but not lost text.
- **No provider-token streaming.** The provider response is awaited whole; `agent_message_chunk`/`agent_thought_chunk` notifications carry complete messages per round, so ACP clients see bursty rather than incremental output (`src/session/chat/session/api_executor.rs:430`, `src/session/chat/response.rs:981`).
- **No backpressure.** All channels (`Command`, `ServerMessage`, notification registry) are unbounded (`src/acp/agent.rs:1684`, `src/session/output.rs:133`-`150`); a slow client cannot stall the agent, but memory grows with undelivered updates.
- **Errors keep the session usable.** Every `prompt` error path re-inserts the session before returning, so a failed turn does not poison later prompts (`src/acp/agent.rs:1163`-`1170`, `:1196`-`1199`, `:1402`-`1404`).

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | Sessions map keyed by ACP session ID; per-session monitors | `src/acp/agent.rs:56`, `:727`-`729` |
| Concurrent work across sessions | `yes` | Independent request tasks + per-session locks; single thread interleaving, not parallelism | `src/acp/agent.rs:1633`-`1637`, `src/acp/mod.rs:30`-`32` |
| Same-session prompt exclusion | `yes` | Mutex serializes; queued prompts wait rather than being rejected | `src/acp/agent.rs:819`-`828` |
| Durable sessions | `yes` | Append-only zstd JSONL, atomic per-message commits | `src/session/persistence.rs:955`-`969` |
| Session list | `partial` | No ACP method; surfaced via `/list` slash command/ext and CLI-side `list_available_sessions` | `src/acp/agent.rs:338`, `src/session/persistence.rs:32`-`88` |
| Session load/resume | `yes` | `load_session` with explicit resume; advertised capability | `src/acp/agent.rs:639`, `:1450`-`1530` |
| History replay to ACP client | `no` | Load sends only `available_commands_update`; no transcript notifications | `src/acp/agent.rs:1522`-`1529` |
| Prior history reused by model | `yes` | Reconstructed message list is sent verbatim on the next prompt | `src/session/chat/session/api_executor.rs:418`-`430` |
| Prompt cancellation | `yes` | Watch-channel flag; `StopReason::Cancelled`; per-operation channels | `src/acp/agent.rs:1438`-`1448`, `src/session/cancellation.rs:55`-`65` |
| Tool-call progress updates | `yes` | `ToolCall` (InProgress) + `ToolCallUpdate` (Completed/Failed); MCP progress mapped to title patches | `src/acp/agent.rs:271`-`307` |
| Partial-output persistence | `partial` | Whole messages commit atomically; partial assistant text is never persisted, and cancelled post-tool rounds drop entirely | `src/session/chat/session/messages.rs:301`-`317`, `src/session/chat/response.rs:882`-`889` |
| Recovery after process restart | `yes` | Full log reconstruction incl. runtime state and interrupted-tool repair | `src/session/persistence.rs:803`-`823` |

## Design assessment

### Strengths

- **ACP is a thin, honest skin over one runtime.** All three frontends (CLI, ACP, WebSocket) share `ChatSession` and the response pipeline; the ACP layer only translates transport and reuses `OutputMode::WebSocket` plumbing, so behavior cannot drift per-frontend (`src/acp/agent.rs:1318`-`1329`).
- **Persist-before-push invariant.** Every message is durable before it can affect the next request, and interrupted tool sequences are repaired on load rather than truncated, preserving transcript validity for strict APIs like Anthropic (`src/session/chat/session/messages.rs:454`-`462`, `src/session/persistence.rs:195`-`281`).
- **Lock discipline is explicit and documented.** The removal-based exclusive ownership plus shared per-session mutex closes the exact race (map-empty "session not found") the comments describe, and the same lock covers monitor-driven turns and ext commands (`src/acp/agent.rs:57`-`62`, `src/acp/commands.rs:66`-`75`).
- **Cancellation is per-operation, not per-process.** Fresh watch channels per operation mean a stale receiver still observes its own cancellation — an uncommonly careful design (`src/session/cancellation.rs:49`-`65`).

### Tradeoffs and limitations

- **Single-threaded locality constrains the design.** The `!Send` actor/LocalSet architecture makes `Rc<RefCell>` safe but means all sessions share one thread and one set of global registries; the `!Send`/`Send` bridge (`Command` enum, `forward` shims) is pure overhead bought by the SDK's `Send` requirement (`src/acp/agent.rs:1545`-`1685`).
- **No history replay moves UI burden to the client.** An editor that reconnects or loads a session must live with a blank transcript unless it reads the file itself (`src/acp/agent.rs:1522`-`1529`).
- **Divergence between what the client saw and what is durable.** Emission-before-persistence for assistant messages plus the cancelled-round drop means the durable log can lag the client's view (`src/session/chat/response.rs:882`-`900`).
- **Whole-message events.** Because the pipeline emits complete responses, ACP clients get no incremental streaming and cannot render progress inside one model response (`src/session/chat/session/api_executor.rs:418`-`430`).

### Ideas relevant to Ox

- **Per-session mutex + map-removal ownership** (fact: `src/acp/agent.rs:1009`-`1017`) is a simple, auditable exclusion scheme worth comparing with Ox's approach; the tradeoff is that every other accessor must remember the lock, and the codebase documents violations it already hit.
- **The atomic-add persistence contract** (fact: `src/session/chat/session/messages.rs:301`) — persist the fully built message before mutating memory — is a cheap invariant that prevents a class of resume bugs Ox could adopt directly; the cost is a synchronous fs append on the turn's critical path.
- **The cancelled-round loss** (fact: `src/session/chat/response.rs:882`-`889`) is the counter-example: deciding *not* to persist after partial execution trades transcript fidelity for simplicity. Persisting the assistant(tool_calls) message plus a synthetic cancelled result (as its own load-time repair already does, `src/session/persistence.rs:195`-`281`) would keep the client and the file consistent; the tradeoff is writing records for work the user aborted.

## Unknowns and conflicts

- **Cross-session interleaving is untested at the ACP layer.** Tests cover single-session prompts, cancel-during-prompt, and monitor turns (`src/acp/agent_tests.rs:966`, `:1333`, `:1424`), but no test drives two sessions' prompts concurrently in one process; the concurrency conclusion rests on the task-per-request dispatch and lock structure, not an executed scenario (Inference).
- **Load-during-active-prompt overlap.** `load_session` acquires no session lock; if a client issued `load_session` for a session whose prompt is mid-turn, both would insert/remove map entries without mutual exclusion. No code path prevents it and none exercises it; the SDK may serialize requests, which the code does not rely on explicitly (`src/acp/agent.rs:1450`-`1530`).
- **Thinking persistence asymmetry.** Final assistant messages persist `thinking: None` while tool-round messages persist their thinking block (`src/session/chat/response.rs:116`-`132` vs `:340`-`360`). Whether this is intentional token hygiene or an oversight is not stated in code or docs.
- **Session list over ACP.** The `/list` command exists and `list_available_sessions` is durable-metadata-based, but no ACP client method exposes it; clients must use the ext command or a prompt (`src/acp/agent.rs:338`, `src/acp/commands.rs:33`).
- Searches for an ACP `session/close`, `session/list`, or explicit session eviction handler found nothing in `src/acp/` (`agent.rs:1702`-`1809` registers only the handlers listed above); sessions are evicted only by process exit.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `src/commands/acp.rs:55` (`execute`); `src/acp/mod.rs:49` (`run`); `src/acp/agent.rs:1677` (`serve`); `src/acp/agent.rs:1593` (`run_actor`); `src/acp/agent.rs:1652` (`forward`) | Process entry, LocalSet/actor bridge, SDK wiring, EOF shutdown |
| Session management | `src/acp/agent.rs:661` (`new_session`); `src/acp/agent.rs:1450` (`load_session`); `src/session/chat/session/core.rs:482` (`initialize`); `src/session/chat/session/core.rs:142` (`generate_session_name`) | Creation/resume, ID ↔ file mapping |
| Concurrency | `src/acp/mod.rs:32` (`SessionLocks`); `src/acp/agent.rs:819`-`828`; `src/acp/agent.rs:396` (`spawn_inbox_monitor`); `src/session/context.rs:83`-`106` | Same-session exclusion, monitor races, task-local scoping |
| Persistence | `src/session/persistence.rs:955` (`append_to_session_file`); `src/session/persistence.rs:296` (`parse_log_lines`); `src/session/chat/session/messages.rs:301`; `src/session/mod.rs:712` (`Session::save`) | Append-only store, atomic add, SUMMARY snapshots |
| Event translation | `src/acp/agent.rs:263` (`translate_server_message_to_acp`); `src/acp/agent.rs:1202`-`1316` (prompt forward task); `src/session/output.rs:133` (`WebSocketSink`); `src/websocket/protocol.rs:384` (`ServerMessage`) | Internal events → ACP notifications |
| Replay/reconstruction | `src/session/persistence.rs:803` (`load_session`); `src/session/persistence.rs:653` (`reconstruct_messages`); `src/session/chat/session/core.rs:557`-`771` | Durable history → model context; no client replay |
| Cancellation/errors | `src/session/cancellation.rs:55` (`SessionCancellation`); `src/acp/agent.rs:1438` (`cancel`); `src/session/chat/response.rs:314` (`check_cancellation`); `src/session/chat/response.rs:882`-`900` (cancelled-round drop) | Per-operation cancellation and its durability gap |
| LLM integration | `src/providers.rs:15`-`32` (octolib adapter + re-exports), `:200` (`to_octolib_params`), `:537` (`convert_response_from_octolib`); `src/session/completion.rs:219` (`chat_completion_with_validation`), `:345` (`provider.chat_completion`); `src/session/model_utils.rs:24` (provider selection); `Cargo.toml:41` (`octolib` `llm` feature) | Model abstraction boundary, provider dispatch, request/response conversion, tool schemas |

## Research notes

- **Revision inspected:** `4bffaab3d37a1259ddb9fb2170b8067efcf72cb3` (matches checkout at `tmp/llm-integration-research/Muvon--octomind`)
- **Primary evidence:** `src/acp/{mod,agent,commands}.rs`; `src/session/{mod,persistence,cancellation,output,context,workdir}.rs` (via `src/mcp/workdir.rs`); `src/session/chat/session/{core,setup,messages,api_prep,api_executor}.rs`; `src/session/chat/response.rs` + `response/tool_result_processor.rs`; `src/providers.rs`; `src/session/{completion,model_utils,cache_keepalive}.rs`; `src/config/{providers,model}.rs`; `src/commands/config.rs`; `src/websocket/protocol.rs`; `src/mcp/process.rs`; tests `src/acp/{agent_tests,mod_tests,commands_tests}.rs`, `src/session/chat/session/core_methods_tests.rs`
- **Relevant docs:** README (feature claims only, not used as evidence); repo `AGENTS.md` for architecture orientation; no ACP-specific doc files were relied on
- **Commands/tests run:** Read-only inspection; no tests executed (analysis of `agent_tests.rs`/`mod_tests.rs` used as corroboration: stdio e2e `acp_stdio_serves_initialize_new_session_prompt_and_eof`, `load_session_resumes_a_saved_session`, `cancel_during_a_prompt_returns_a_cancelled_stop_reason`)
- **Report confidence:** `high` — the ACP handlers, session pipeline, persistence, and concurrency mechanisms were each read end-to-end in the pinned revision, with tests corroborating the traced paths.
