---
project: "stakpak/agent"
repository: "https://github.com/stakpak/agent"
revision: "760cd2b5984d29c2d513bb15ca33e995fae45f17"
researched_at: "2026-09-19"
primary_language: "rust"
implementation_form: "native-agent"
process_model: "single-process"
session_owner: "acp-layer"
durability: "mixed"
cross_session_concurrency: "concurrent"
same_session_concurrency: "concurrent"
event_delivery: "channel"
resume_strategy: "unsupported"
overall_confidence: "high"
---

# stakpak/agent ACP architecture

## Executive summary

- **ACP boundary:** ACP terminates inside the `stakpak` CLI binary (`stakpak acp`). `StakpakAcpAgent` implements the `agent-client-protocol` 0.9.3 `Agent` trait over stdio (`cli/src/commands/acp/server.rs:1335` (`run_stdio`), `cli/src/commands/acp/server.rs:1552` (`impl acp::Agent`)). There is no wrapped agent process: the same process runs the protocol handlers, the model loop, tool execution, and an in-process MCP server.
- **Session model:** The ACP `SessionId` is the UUID of a session created in a storage backend (Stakpak API or local SQLite) during `new_session` (`cli/src/commands/acp/server.rs:1766`-1769). Runtime conversation state is a single shared `Mutex<Vec<ChatMessage>>` plus a single `Cell<Option<Uuid>>` "current session" slot (`cli/src/commands/acp/server.rs:38`,41) — many session IDs collapse onto one runtime history.
- **Concurrency:** The ACP crate spawns every request/notification handler independently on a `LocalSet`, and nothing in this agent serializes prompts (`rpc.rs:263`-296 of the crate; no guard in `prompt`). Concurrent prompts in different sessions — or the same session — interleave on the shared history, one `current_session_id`, and process-global cancel broadcast channels. Cancellation in one session cancels every in-flight stream/tool in the process.
- **Durability and replay:** Sessions and a parent-linked chain of checkpoints (each holding the full `Vec<ChatMessage>` as JSON) are persisted per completed model turn via `AgentClient::save_checkpoint` (`libs/api/src/client/provider.rs:349`-355,808-832), to the Stakpak HTTP API or to SQLite at `~/.stakpak/data/local.db`. `load_session` restores only the session pointer; no history is replayed to the client and none is reloaded into the model context (`cli/src/commands/acp/server.rs:1779`-1800).
- **Event flow:** Live updates (`AgentMessageChunk`, `ToolCall`, `ToolCallUpdate`, `Plan`, permission requests) are funneled through internal mpsc channels to writer tasks that call the connection; each send waits on a oneshot ack (`cli/src/commands/acp/server.rs:237`-255,1436-1477). Durable records are plain `ChatMessage`s, not ACP updates; partial/cancelled turns are never checkpointed.
- **Notable uncertainty:** Whether the remote Stakpak API merges server-side history based on the injected `X-Session-Id` header (`libs/api/src/client/provider.rs:856`-862) is not observable in this repo; client-side, resume of conversation context is not implemented.

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `native-agent` | ACP handlers and the agent loop (model streaming, tool orchestration, permissions) live in the same binary and process; no adapter over another agent executable. The ACP layer reuses the shared `AgentClient`/stakai/MCP libraries that other CLI modes use. | `cli/src/commands/acp/server.rs:28`-62,1552; `cli/src/commands/mod.rs:591`-606 |
| Process model | `single-process` | One `stakpak acp` process serves the stdio connection; the MCP tool server runs as an in-process tokio task behind a local HTTPS listener plus proxy; user-configured external MCP servers may be subprocesses (tooling only). | `cli/src/commands/acp/server.rs:1355`-1425; `cli/src/commands/agent/run/mcp_init.rs:135`-216 |
| Session owner | `acp-layer` | The authoritative conversation is the ACP agent's in-memory `messages` vec and `current_session_id`; the storage backend only receives session/checkpoint writes and is never read back into the runtime by the ACP path. | `cli/src/commands/acp/server.rs:38`,41,1722-1734,1819-1832; `libs/api/src/client/provider.rs:706`-744 |
| Durability | `mixed` | Backend chosen at startup: Stakpak API `/v1/sessions` when an API key is configured, otherwise embedded SQLite (libsql) at `~/.stakpak/data/local.db`. Sessions plus checkpoint chains; checkpoints store the full message list. | `libs/api/src/client/mod.rs:103`,198-217; `libs/api/src/local/migrations/v002_nullable_columns.rs:28`-37,62-73; `libs/api/src/stakpak/client.rs:83`-135 |
| Cross-session concurrency | `concurrent` | Handlers run without serialization, but there is exactly one runtime history, one current-session slot, one model, and global cancel channels — so concurrent sessions are not isolated and corrupt each other rather than queue. | `agent-client-protocol` 0.9.3 `rpc.rs:263`-296 (spawn per request); `cli/src/commands/acp/server.rs:38`,41,50-51,2113-2163 |
| Same-session concurrency | `concurrent` | No exclusion between prompts in one session either; a second `session/prompt` is handled while the first is awaiting, interleaving messages in the shared history. | `cli/src/commands/acp/server.rs:1802`-2111 (no guard); crate `rpc.rs:270`-280 |
| Event delivery | `channel` | Live updates go agent → `session_update_tx` (unbounded mpsc) → writer task → `conn.session_notification()`; permission requests and native FS ops use their own channels; each send awaits a oneshot ack. | `cli/src/commands/acp/server.rs:33`,237-255,1436-1477; `cli/src/commands/acp/fs_handler.rs:37`-78 |
| Resume strategy | `unsupported` | `load_session` is advertised and accepted but only re-points `current_session_id` and returns model state; it performs no storage read, no runtime reconstruction, and no replay. Durable history exists but is not consumed on resume. | `cli/src/commands/acp/server.rs:1589`-1591,1779-1800 |

## System architecture

```text
ACP client (e.g. Zed)                          stakpak acp (single process)
┌────────────────┐        stdio JSON-RPC      ┌──────────────────────────────────────┐
│                │◄──────────────────────────►│ AgentSideConnection (acp 0.9.3)      │
│  initialize /  │  session/prompt, cancel    │  · spawns each request handler       │
│  newSession /  │  notifications, responses  │    via spawn_local (LocalSet)        │
│  loadSession / │                            │  · single outgoing channel → stdout  │
└────────────────┘                            └───────────────┬──────────────────────┘
                                                              │ &self handlers
                                              ┌───────────────▼──────────────────────┐
                                              │ StakpakAcpAgent (impl acp::Agent)    │
                                              │  messages: Mutex<Vec<ChatMessage>>   │
                                              │  current_session_id: Cell<Option>    │
                                              │  stream/tool cancel: broadcast(1)    │
                                              │  tool loop + streaming translation   │
                                              └───┬───────────────┬──────────────────┘
                     session_update_tx /          │               │ chat_completion_stream
                     permission_tx / fs_tx        │               ▼
                              (mpsc)              │      AgentClient (stakai)
                                  ▼               │       ├─ LLM providers (HTTP)
                    writer tasks ──► conn ──► ACP │       └─ session_storage
                    (chunks, tool updates,        │           ├─ StakpakStorage ──► Stakpak API
                     plan, permission)            │           │    /v1/sessions (+checkpoints)
                                                  │           └─ LocalStorage ────► SQLite
                                                  │               sessions + checkpoints   ~/.stakpak/data/local.db
                                                  │ McpClient ──► in-process MCP server + proxy
                                                  │              (local HTTPS; external servers optional)
                                                  └──────────────────────────────────────────┘
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `AgentSideConnection` (crate) | JSON-RPC transport over stdio; spawns per-request handler futures onto a `LocalSet`; one serialized outgoing queue | process (per `run_stdio`) | outgoing message queue | crate 0.9.3 `rpc.rs:54`-90,263-296; `cli/src/commands/acp/server.rs:1421`-1425 |
| `StakpakAcpAgent` | ACP handlers, prompt loop, streaming translation, tool orchestration, permissions | process (single shared instance, cloned per use) | `messages` history, `current_session_id`, `model`, cancel broadcast senders, active tool calls | `cli/src/commands/acp/server.rs:28`-62 |
| Notification/permission/FS writer tasks | Forward queued updates/requests to the connection; ack via oneshot | connection | none (channels only) | `cli/src/commands/acp/server.rs:1436`-1477; `cli/src/commands/acp/fs_handler.rs:37`-78 |
| `AgentClient` | LLM routing via stakai, session/checkpoint persistence, hooks (context trimming) | process | none durable in itself; reads/writes `session_storage` | `libs/api/src/client/mod.rs:111`-237 |
| `LocalStorage` / `StakpakStorage` | Durable sessions + checkpoint chain (SQLite or HTTP) | process | SQLite file / remote records | `libs/api/src/local/storage.rs:23`-114; `libs/api/src/stakpak/storage.rs:3` |
| In-process MCP server + proxy + `McpClient` | Tool execution (stakpak tools, external servers) | process | tool registry, shutdown handles | `cli/src/commands/agent/run/mcp_init.rs:135`-249; `cli/src/commands/acp/server.rs:1365`-1386 |

### ACP surface

Transport is stdio (`tokio::io::stdin/stdout` adapted) driven by `AgentSideConnection` from the `agent-client-protocol` 0.9.3 crate with the `unstable_session_model` feature (`cli/Cargo.toml`; `cli/src/commands/acp/server.rs:1335`-1342,1421-1425). `initialize` (`cli/src/commands/acp/server.rs:1553`) stores client capabilities and advertises `load_session(true)`, image and embedded-context prompt capabilities, and MCP http/sse; it also offers a `stakpak` browser auth method when no credentials exist, handled by `authenticate` (`1601`-1687). Implemented methods: `new_session` (`1689`), `load_session` (`1779`), `prompt` (`1802`), `cancel` (`2113`), `set_session_model` (`2165`, unstable feature). There is no session list, delete, or extension surface. The ACP layer is native to the agent, not an adapter: `prompt` itself contains the model-turn loop and tool scheduling described below.

### Runtime and process boundaries

Everything runs in one process on a tokio runtime; because the ACP crate's futures are `!Send`, `run_stdio` builds a `LocalSet` and uses `spawn_local` for the connection, notification writer, permission writer, FS handler, and progress tasks (`cli/src/commands/acp/server.rs:1355`-1504). Handler concurrency is therefore single-threaded interleaving at `.await` points. The MCP "server" is an in-process tokio task bound to a local HTTPS port, with a proxy client pooling upstreams (external user-configured servers can be subprocesses) (`cli/src/commands/agent/run/mcp_init.rs:135`-249). Tool execution goes through `McpClient` and races a cancellation broadcast receiver, sending an rmcp `CancelledNotification` to the server when tripped (`cli/src/commands/agent/run/tooling.rs:110`-152). Ctrl+C triggers a clean shutdown of the stdio loop (`cli/src/commands/acp/server.rs:1342`-1350).

## LLM abstraction and integration

### Abstraction

The project uses a named third-party LLM abstraction library, `stakai`, vendored in-workspace as `libs/ai` (package `stakai`, `libs/ai/Cargo.toml:2`; depended on by `cli/Cargo.toml` and re-exported by `stakpak-api`, `libs/api/src/lib.rs:22`). It is neither a provider SDK used directly nor a wrapped executable: the model loop is in this repository, and inference goes through stakai's own `Provider` trait (`libs/ai/src/provider/trait_def.rs:9`-31), a `ProviderRegistry` (`libs/ai/src/registry/mod.rs:13`-15), and the `Inference` client (`libs/ai/src/client/mod.rs:20`-25). `AgentClient` holds a `StakAIClient` wrapper (`libs/api/src/client/mod.rs:114`) around `stakai::Inference` (`libs/shared/src/models/stakai_adapter.rs:676`-678). Dispatch is the runtime registry keyed by `Model.provider`, not the unused static `ProviderDispatcher` (`libs/ai/src/client/mod.rs:202`-205,316-319; `libs/ai/src/provider/dispatcher.rs:41`-59 returns `Not implemented yet`).

### Integration path

1. `StakpakAcpAgent::prompt` joins text blocks into a `ChatMessage`, appends it to the shared history, and calls `client.chat_completion_stream(model, messages, tools, None, session_id, None)` (`cli/src/commands/acp/server.rs:1806`-1862).
2. `AgentClient::chat_completion_stream` builds an `AgentState`, runs `BeforeRequest` hooks, calls `initialize_session`, then spawns a task that calls `run_agent_completion` (`libs/api/src/client/provider.rs:272`-322).
3. `run_agent_completion` runs `BeforeInference` hooks (`libs/api/src/client/provider.rs:841`-845); the registered `TaskBoardContextHook` reduces the `Vec<ChatMessage>` for budget and builds the `LLMInput` used for inference, with `llm_input.model = ctx.state.active_model` (`libs/api/src/local/hooks/task_board_context/mod.rs:45`-94; state at `libs/api/src/models.rs:558`-571,636-647).
4. It injects the `X-Session-Id` header and calls `stakai.chat_stream(LLMStreamInput)` (`libs/api/src/client/provider.rs:856`-862,877-883).
5. `StakAIClient::chat_stream` converts `LLMMessage`→`stakai::Message` and `LLMTool`→`stakai::Tool` (`libs/shared/src/models/stakai_adapter.rs:755`-772; conversions at `25`-44,154-164), assembles a `GenerateRequest`, and calls `self.inference.stream(&request)` (`780`-790).
6. `Inference::stream` → `stream_internal` → `registry.get_provider(&request.model.provider)` → `provider.stream(request)` (`libs/ai/src/client/mod.rs:316`-319; `libs/ai/src/registry/mod.rs:32`-37).
7. The concrete provider converts the request to its wire format and opens an SSE stream (`libs/ai/src/providers/stakpak/provider.rs:138`-156); its `stream.rs` parses SSE chunks into `StreamEvent`s (`libs/ai/src/providers/stakpak/stream.rs:70`-143,146-279).
8. `StakAIClient::chat_stream` consumes the `StreamEvent` stream, accumulates text and tool-call deltas, forwards each as a `GenerationDelta` through a channel, and returns a final `LLMCompletionResponse` (`libs/shared/src/models/stakai_adapter.rs:798`-945; `from_stakai_stream_event` at `176`-225).
9. `run_agent_completion` joins the stakai call with a receiver that re-emits deltas as `StreamMessage::Delta`, then converts the final message to `ChatMessage` (`libs/api/src/client/provider.rs:864`-918); `chat_completion_stream`'s stream yields `ChatCompletionStreamResponse` chunks (`387`-419) consumed by `process_acp_streaming_response_with_cancellation` (`cli/src/commands/acp/server.rs:1139`).
10. Tool-call handling is in the ACP layer, not stakai: `GenerationDelta::ToolUse` deltas accumulate into `ToolCallAccumulator`/`active_tool_calls` (`cli/src/commands/acp/server.rs:1286`-1290), are executed via MCP or the client FS, results are appended as `ChatMessage` tool messages, and the loop re-invokes `chat_completion_stream` (`1892`-2095). stakai carries tool schemas in and tool-call deltas out; it never executes tools.

### Provider and tool boundary

Provider-specific code, request/response normalization, and auth headers live under `libs/ai/src/providers/<name>/` (`anthropic`, `openai`, `gemini`, `openrouter`, `stakpak`, `copilot`, `bedrock`), each with `convert.rs`/`types.rs`/`stream.rs` (e.g. `libs/ai/src/providers/stakpak/provider.rs:93`-156; `libs/ai/src/providers/anthropic/convert.rs:45,368`). Credentials and configuration are assembled in the CLI from `AppConfig` (`cli/src/config/app.rs:805`-817,836-852) into `LLMProviderConfig`/`ProviderConfig`; `StakAIClient::new` maps these to a `ProviderRegistry` via `build_provider_registry_direct` (`libs/shared/src/models/stakai_adapter.rs:682`-695,529-667). The abstraction is multi-provider; the provider is selected per request from `Model.provider` (`libs/ai/src/client/mod.rs:317`). Tool schemas originate from MCP: `rmcp::model::Tool` → shared `Tool` (`cli/src/commands/agent/run/helpers.rs:93`-120) → `LLMTool` (`libs/shared/src/models/integrations/openai.rs:418`-427) → `stakai::Tool` (`libs/shared/src/models/stakai_adapter.rs:154`-164) → provider wire format (`libs/ai/src/providers/openai/convert.rs:25`-41). The ACP model list comes from the registry's per-provider `list_models()` (`libs/api/src/client/provider.rs:568`-583); Anthropic/OpenAI/Gemini read the models.dev catalog cache (`libs/ai/src/registry/models_dev.rs:153`; e.g. `libs/ai/src/providers/anthropic/provider.rs:61`-67), while Stakpak fetches `/v1/models` (`libs/ai/src/providers/stakpak/provider.rs:158`-176).

### Limits

- The remote Stakpak inference API and the models.dev catalog (`libs/ai/src/registry/models_dev.rs:17`) are external; only client-side request construction and response parsing are visible.
- The ACP default model is derived heuristically from a config string by substring matching rather than from the registry, and provider ids outside `find_model`'s `PROVIDERS` list (`libs/api/src/lib.rs:42`) are not resolved by it (`cli/src/commands/acp/server.rs:131`-159).
- The registry's provider-id set is fixed by the `ProviderConfig` variants handled in `build_provider_registry_direct`; ids not produced there are visible only in code (`libs/shared/src/models/stakai_adapter.rs:529`-667).
- Whether `X-Session-Id` causes server-side history merging is not determinable from this checkout.

## Session model

### Identity and ownership

A "session" is a row in the storage backend plus the ACP `SessionId` string derived from its UUID. `new_session` creates the durable session via `client.create_session(...)` and returns `cloud_session.session_id` as the ACP session ID (`cli/src/commands/acp/server.rs:1759`-1776), so the ACP ID ↔ storage session mapping is 1:1 by construction. The runtime mapping is lossy in the other direction: there is one `Cell<Option<Uuid>>` `current_session_id` and one `Mutex<Vec<ChatMessage>>` history for the whole process (`cli/src/commands/acp/server.rs:38`,41). Whichever session was created or loaded last owns the slot; a prompt on an older session still reads/writes the same history and routes persistence to the last-set ID (`1846`-1862 uses `current_session_id`, while notifications use the request's `args.session_id`). `next_session_id: Cell<u64>` (`34`) is vestigial — IDs no longer come from a local counter. The model conversation is exactly the in-memory `Vec<ChatMessage>`; durable checkpoints are a write-side record, never read back into the runtime by the ACP path.

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | Credential gate (API key or provider keys, with a config re-read from disk); clears the shared history, keeping only the system message; builds title `ACP: <cwd folder>`; calls `create_session` on the storage backend; sets `current_session_id`; returns model list | Session row + initial checkpoint containing only `[system]` (or a `"New session"` user message) and the cwd | `auth_required` before any state change; `internal_error` if storage fails — note the shared history is already cleared before storage is contacted, destroying the previous session's runtime context | `cli/src/commands/acp/server.rs:1695`-1776; `libs/api/src/local/storage.rs:344`-399 |
| Load/resume | Parses the UUID, sets `current_session_id`, returns models. No storage read, no history reload, no notifications | None | `invalid_params` for a non-UUID; nonexistent/foreign IDs are accepted silently | `cli/src/commands/acp/server.rs:1779`-1800 |
| Prompt | See the event-flow traces; user message appended, streamed turns, tool loop, per-turn checkpoints | One checkpoint per completed model turn (full message list, parent-linked) | Stream/tool failure → `internal_error` response; cancellation → `StopReason::Cancelled` with no checkpoint for the interrupted turn | `cli/src/commands/acp/server.rs:1802`-2111; `libs/api/src/client/provider.rs:302`-366 |
| Cancel | Broadcasts on the process-global stream and tool cancel channels; appends `TOOL_CALL_CANCELLED` tool results for active calls into the shared history | None directly; placeholders enter the next checkpoint | Ignores `args.session_id` — cancels all sessions' work | `cli/src/commands/acp/server.rs:2113`-2163 |
| Close/delete | Not found in the ACP surface — the `Agent` impl has no delete; `SessionStorage::delete_session` exists but is only reached from the non-ACP `stakpak sessions` CLI | n/a | n/a | `libs/api/src/storage.rs:55`-56; `cli/src/commands/acp/server.rs:1552`-2200 (no method) |

### Durable representation

Storage is selected once at startup: `StakpakStorage` (HTTP `/v1/sessions`, `/v1/sessions/{id}/checkpoints`) when a Stakpak API key is configured, otherwise `LocalStorage` — SQLite via libsql at `~/.stakpak/data/local.db` (`libs/api/src/client/mod.rs:103`,198-217; `libs/api/src/stakpak/client.rs:83`-207; `cli/src/commands/acp/server.rs:125`-130 passes `store_path: None`). The SQLite schema has `sessions` (id, title, visibility, status, cwd, timestamps) and `checkpoints` (id, session_id, parent_id, state, timestamps) (`libs/api/src/local/migrations/v002_nullable_columns.rs:28`-37,62-73). The unit of persistence is a checkpoint whose `state` is the full `Vec<ChatMessage>` serialized as JSON (`libs/api/src/storage.rs:285`-305), parent-linked to the previous checkpoint. Writes happen inside `chat_completion_stream`'s spawned task after a model response completes (`libs/api/src/client/provider.rs:349`-355,808-832); SQLite inserts are per-statement with no multi-row transaction (`libs/api/src/local/storage.rs:559`-583). Not persisted: ACP notifications, tool progress, reasoning traces (there are none as separate records), cancelled/partial turns, and any association between a chunk and a checkpoint. Stored history is authoritative for audit/memory (the remote backend also receives `X-Session-Id` on LLM requests, `libs/api/src/client/provider.rs:856`-862) but is not the source of model context for the ACP path — that is the in-memory vec.

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | The crate spawns each request handler independently on the `LocalSet`; no registry or per-session state exists, so both prompts share one history, one `current_session_id`, one model, and the same cancel channels. Not isolated. | crate `rpc.rs:263`-296; `cli/src/commands/acp/server.rs:38`-51 |
| Two prompts in the same session | `concurrent` | Same as above — no exclusion, no queue, no rejection. Message pushes are individually mutex-guarded, so interleaving yields a corrupted but valid-looking history. | `cli/src/commands/acp/server.rs:1819`-1832,1892-1896 (brief mutex scopes, no turn lock) |
| Load/resume during an active prompt | Interleaves silently | `load_session` only writes the `Cell` and returns; an active prompt keeps streaming (its notifications still use the request's session ID) while the storage routing switches to the newly loaded ID | `cli/src/commands/acp/server.rs:1779`-1800,1848 |
| Delete/close during an active prompt | n/a | No ACP delete exists; nothing to race | `cli/src/commands/acp/server.rs:1552`-2200 |
| Cancellation isolation | global (process-wide) | `cancel` broadcasts on shared `stream_cancel_tx`/`tool_cancel_tx` and ignores the session ID; every in-flight stream and tool in the process is cancelled and placeholder results are appended to the one shared history | `cli/src/commands/acp/server.rs:50`-51,2113-2163 |

Additional coupling: the `model` selection is global (`set_session_model` writes the shared `RwLock`, `2165`-2199), so one session's model switch affects all. The context-trimming hook (`TaskBoardContextHook`, keep last 5 assistant messages / 80% budget) is registered per `AgentClient` and thus shared (`libs/api/src/client/mod.rs:219`-228). There is no critical section spanning a whole prompt; the only serialization is the per-notification ack handshake. Spawned tasks are not proof of safe concurrency here — they are the mechanism by which shared state gets interleaved.

## Event and data flow

### New prompt: ACP client to live response

1. Client sends `session/prompt`; `Agent::prompt` joins `ContentBlock::Text` texts with spaces; non-text blocks (images) become empty strings despite the advertised image capability (`cli/src/commands/acp/server.rs:1806`-1816).
2. The user message is appended to the shared history under a short mutex lock (`1819`-1822).
3. A snapshot of the history is sent to `client.chat_completion_stream(model, messages, tools, None, current_session_id, None)` (`1829`-1862). In `AgentClient`, before-request hooks run, `initialize_session` resolves the durable session (or creates one) and demands an active checkpoint (`libs/api/src/client/provider.rs:302`,706-744), then a tokio task runs the stakai streaming completion, forwarding deltas through a bounded (100) channel (`305`,314-368,864-895).
4. The ACP server consumes the stream in `process_acp_streaming_response_with_cancellation` (`cli/src/commands/acp/server.rs:1139`-1333): raw text accumulates in `current_streaming_message`; `<checkpoint_id>` tags are stripped; a buffer holds back partial XML tags so `<scratchpad>`/`<todo>` blocks can be converted to markdown headers and todos re-emitted as `Plan` notifications (`656`-755,1246-1257; `cli/src/commands/acp/utils.rs:27`-82).
5. Each non-empty text delta is sent as `SessionUpdate::AgentMessageChunk` through `session_update_tx`; the handler awaits a oneshot ack that the writer task sends after `conn.session_notification()` returns — i.e., after queuing into the connection's outgoing channel (`1265`-1282,1436-1453; crate `rpc.rs:97`-110). Order is preserved by the single outgoing queue.
6. When the stream ends, the held-back buffer is flushed as a final chunk (`1298`-1315) and the full assistant message (text + accumulated tool calls) is appended to the history (`1892`-1896). Concurrently, the client's spawned task has saved a checkpoint of the whole message list (`provider.rs:328`-366).
7. Tool loop (`cli/src/commands/acp/server.rs:1951`-2095): for each tool call — `ToolCall` notification (Pending) with diff content for file writes (`858`-869,834-856); permission request unless auto-approved, forwarded to the client over `permission_request_tx` (`288`-371,1455`-1477`); `InProgress` update (`909`-917); execution either via native ACP FS (read/write routed to the editor when the client advertises `fs` capabilities, `919`-950; `cli/src/commands/acp/fs_handler.rs:81`-286) or via MCP `run_tool_call` (`955`-977); `Completed`/`Failed` update with `rawOutput` (`987`-1114); tool result appended to history (`1059`-1062).
8. A follow-up `chat_completion_stream` runs with the updated history; the loop repeats until no tool calls remain, then returns `PromptResponse(StopReason::EndTurn)` (`2038`-2095,2110). Cancellation at any point returns `StopReason::Cancelled` (`1869`-1873,1974,2021-2026,2065).

### Durable history to ACP client

Not found. `load_session` (`cli/src/commands/acp/server.rs:1779`-1800) sends no `session/update` notifications — the only `SessionUpdate` variants produced anywhere in `cli/src/commands/acp/` are `AgentMessageChunk`, `ToolCall`, `ToolCallUpdate`, and `Plan` (grep over the module; no `UserMessageChunk`, no replay path). Durable checkpoints are also not read into the runtime: `initialize_session` uses `get_session` only to obtain the session and active-checkpoint IDs for parent linkage (`libs/api/src/client/provider.rs:706`-744) and discards the checkpoint's messages. Consequently, after a process restart plus `load_session`, the model context is just the system message and the next checkpoint chains off the old one while containing none of its messages — the durable chain continues by ID but its content forks.

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `ChatMessage(role=user)` pushed to shared history | none | Included in the checkpoint saved when the turn's first model completion finishes | After model response, in the completion task | `cli/src/commands/acp/server.rs:1819`-1822; `libs/api/src/client/provider.rs:328`-366 |
| Assistant text | Accumulated raw text in `current_streaming_message`; final `ChatMessage(role=assistant)` | `AgentMessageChunk` per filtered delta + final flush | The assistant message inside the same turn's checkpoint | Checkpoint after full response received; chunks streamed live before that | `cli/src/commands/acp/server.rs:1240`-1282,1892-1896; `provider.rs:334`-355 |
| Reasoning/thought | n/a (no reasoning channel; scratchpad/todo XML is embedded in text) | Converted to markdown text chunks; `<todo>` items additionally as `Plan` | Embedded in the assistant text only | Same as assistant text | `cli/src/commands/acp/server.rs:592`-655,1246-1257 |
| Tool call | `ToolCallAccumulator` output; entry in `active_tool_calls` | `ToolCall` (Pending, diff content for writes) + `ToolCallUpdate` (InProgress) | `tool_calls` on the assistant message in the turn's checkpoint | Same checkpoint as the assistant text | `cli/src/commands/acp/server.rs:1286`-1290,858`-917`; `provider.rs:1318`-1323 |
| Tool result | `ChatMessage(role=tool)` with text content or `TOOL_CALL_REJECTED`/`TOOL_CALL_CANCELLED` placeholder | `ToolCallUpdate` Completed/Failed with `rawOutput` | Tool message in the *next* model turn's checkpoint | After the follow-up completion finishes | `cli/src/commands/acp/server.rs:987`-1114,2010-2014 |
| Completion/failure | Turn end / `STREAM_CANCELLED` / stream error | `PromptResponse(EndTurn)` / `(Cancelled)` / `internal_error` | Checkpoint only on completed turns; cancelled turns save nothing | Response returned after tool loop drains | `cli/src/commands/acp/server.rs:1869`-1877,2110; `provider.rs:316`,328-347 |

### Subsequent-prompt reconstruction

Within one process, the next prompt's model context is the in-memory `self.messages` verbatim (`cli/src/commands/acp/server.rs:1829`-1832) — no filtering, compaction, or replay at the ACP layer. Inside `AgentClient`, before inference the registered `TaskBoardContextHook` may merge/trim the message list for budget reasons and record trimming metadata (`libs/api/src/client/mod.rs:219`-228; per AGENTS.md, trimming state flows through `AgentState.metadata` into the saved checkpoint). After restart or a load without preceding prompts, the context contains only the system message; prior durable history is never reloaded (see the previous section).

### Ordering, cancellation, failure, and backpressure

- **Ordering:** All live updates share one unbounded outgoing queue in the connection, so delivery order matches send order (crate `rpc.rs:97`-110,153-234). The per-chunk ack (`cli/src/commands/acp/server.rs:1281`) additionally prevents the agent from interleaving its own updates.
- **Backpressure:** Weak. The ack fires when the notification is queued, not when the client consumes it; the outgoing channel is unbounded, so a slow client lets the buffer grow. Real bounds exist only on the internal model-stream channels (100 slots, `libs/api/src/client/provider.rs:305`,866).
- **Cancellation:** Broadcast channels of capacity 1, subscribed fresh per stream/tool operation, so stale signals do not leak (`cli/src/commands/acp/server.rs:186`-188,1179,769). `cancel` is process-global: it stops every in-flight stream/tool, appends `TOOL_CALL_CANCELLED` placeholders to the shared history, and the interrupted prompt returns `StopReason::Cancelled` (`2113`-2163,1869-1873). The client-side completion task detects the dropped consumer and skips the checkpoint (`provider.rs:316`,328-347); the underlying provider stream is still drained to completion by the `tokio::join!` in `run_agent_completion` (`878`-893) — tokens are spent, output discarded.
- **Failure:** Stream or tool errors become `internal_error` prompt responses (`1874`-1877,974`-984`). If the notification writer task dies (first send error), every subsequent ack fails and the prompt errors out (`1446`-1453).
- **Partial output:** Chunks already delivered stay with the client, but neither the partial assistant text nor in-flight tool results are checkpointed; the next prompt continues from the last completed turn, with cancellation placeholders covering acknowledged tool calls.

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `partial` | Multiple IDs can be created and returned, but all map onto one runtime history/current-session slot; creating a new session wipes the shared context | `cli/src/commands/acp/server.rs:38`-41,1722-1734 |
| Concurrent work across sessions | `no` | Handlers interleave but share history, model, and global cancel channels; nothing isolates sessions | `cli/src/commands/acp/server.rs:50`-51,2113-2163 |
| Same-session prompt exclusion | `no` | No turn lock or rejection; concurrent prompts interleave in the shared history | `cli/src/commands/acp/server.rs:1802`-2111 |
| Durable sessions | `yes` | Sessions + parent-linked checkpoint chains in SQLite or Stakpak API | `libs/api/src/client/mod.rs:198`-217; `libs/api/src/local/migrations/v002_nullable_columns.rs:62`-73 |
| Session list | `no` | Not exposed over ACP; only the non-ACP `stakpak sessions` CLI and storage trait | `libs/api/src/storage.rs:34`-37; `cli/src/commands/acp/server.rs:1552`-2200 |
| Session load/resume | `partial` | Handler exists and keeps durable bookkeeping, but restores no runtime state | `cli/src/commands/acp/server.rs:1779`-1800 |
| History replay to ACP client | `no` | No session/update notifications on load anywhere in the module | grep of `cli/src/commands/acp/` for `SessionUpdate::` |
| Prior history reused by model | `partial` | Within a live process, via the in-memory vec; never from durable storage | `cli/src/commands/acp/server.rs:1829`-1832; `libs/api/src/client/provider.rs:706`-744 |
| Prompt cancellation | `yes` | Broadcast-based, but process-global in scope | `cli/src/commands/acp/server.rs:2113`-2163 |
| Tool-call progress updates | `partial` | ToolCall/ToolCallUpdate status transitions are emitted; MCP progress messages are forwarded with an empty session ID (`TODO` in code) | `cli/src/commands/acp/server.rs:1479`-1504,858`-1114` |
| Partial-output persistence | `no` | Checkpoints only after completed model turns; cancelled/partial turns discarded | `libs/api/src/client/provider.rs:328`-366 |
| Recovery after process restart | `partial` | Durable sessions survive and the checkpoint chain continues, but conversation context is lost and history replay is absent | `cli/src/commands/acp/server.rs:1779`-1800; `provider.rs:706`-744 |

## Design assessment

### Strengths

- The whole ACP surface is one readable 2,525-line module with a single state struct, making the data flow easy to audit end to end (`cli/src/commands/acp/server.rs:28`-62).
- Live-update delivery is funneled through one channel and one writer task per purpose, giving naturally ordered, race-free emission on the wire (`1436`-1477).
- File reads/writes are delegated to the ACP client when it advertises FS capabilities, so the editor's unsaved-buffer state participates in tool execution, with capability gating covered by unit tests (`919`-950; tests at `2227`-2255).

### Tradeoffs and limitations

- One shared runtime session behind many ACP session IDs: `new_session` clears the only history, `load_session` re-points a single `Cell`, and `set_session_model` is global. Multi-session clients get cross-contaminated context rather than an error (`cli/src/commands/acp/server.rs:38`-41,1722-1734,2165-2199).
- Durable checkpoints are write-only for the ACP path: resume neither replays history to the client nor rebuilds model context, so advertised `load_session` overpromises relative to behavior (`1779`-1800; `provider.rs:706`-744).
- Process-global cancellation means one client's stop button aborts every concurrent prompt's streams and tools (`2113`-2163).
- Persistence granularity is per model turn with full-history snapshots, so cancelled turns are lost entirely and each checkpoint duplicates the whole conversation (cost grows quadratically with turn count) (`provider.rs:349`-355).
- Prompt content blocks other than text are silently dropped (images become empty strings) even though `image(true)` is advertised (`cli/src/commands/acp/server.rs:1806`-1816,1592`-1595`).

### Ideas relevant to Ox

- The per-notification oneshot-ack handshake (send → writer → ack) is a cheap way to serialize outbound ACP updates without locks; the tradeoff is that the ack only means "queued", so it provides ordering, not real backpressure (`cli/src/commands/acp/server.rs:237`-255).
- Holding the full conversation as one `Vec<ChatMessage>` with checkpoint-per-turn snapshots is simple but makes resume and multi-session cheap to get wrong; Ox's event-log-with-replay design already avoids this, and this repo is a concrete illustration of why runtime state and durable records need to be reconciled at load time.
- Streaming-side translation (buffering partial XML tags before emission, `cli/src/commands/acp/server.rs:694`-755) shows the cost of letting prompt-format concerns leak into the transport layer; keeping such filters out of the protocol path keeps chunk semantics honest.

## Unknowns and conflicts

- **Remote history merge:** The Stakpak API receives `X-Session-Id` on LLM calls (`libs/api/src/client/provider.rs:856`-862). Whether the remote side merges stored history server-side (which would mitigate the missing local resume) cannot be determined from this repo; client-side code sends only the caller-provided messages.
- **Capability vs. behavior conflict:** `initialize` advertises `load_session(true)` and image prompts, while `load_session` restores nothing and image blocks are dropped in `prompt` (`cli/src/commands/acp/server.rs:1592`-1595,1779`-1800`,1806-1816). Both are implementation facts; the discrepancy is with the advertised capabilities, not between docs and code.
- **Progress session ID:** MCP tool progress is emitted with `SessionId::new("")` behind a `TODO` (`cli/src/commands/acp/server.rs:1489`); how clients treat it is untested here.
- **Unexercised paths:** `authenticate` (`github` legacy method), browser auth, and `cancel_stream` (request IDs are always `None` from the ACP path) were read but not executed. Tests in the repo cover FS-delegation gating, model mapping, and XML conversion only; no end-to-end ACP session tests exist (`cli/tests/` contains only `ak_cli.rs`).
- **Searches performed:** `SessionUpdate::` across `cli/src/commands/acp/` (no replay variants); `get_session|list_checkpoints` references (only `initialize_session`); `progress_tx` call sites; `delete_session` call sites (none from ACP); the published `agent-client-protocol` 0.9.3 source for request dispatch (`rpc.rs:263`-296).

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `cli/src/commands/mod.rs:552`-606; `cli/src/commands/acp/server.rs:1335` (`run_stdio`), `1552` (`impl acp::Agent`) | CLI wiring, stdio transport, capability advertisement |
| Session management | `cli/src/commands/acp/server.rs:1689` (`new_session`), `1779` (`load_session`), `38`,41 (`current_session_id`, `messages`) | ID allocation, single runtime slot, context clearing |
| Concurrency | `agent-client-protocol` 0.9.3 `rpc.rs:263`-296; `cli/src/commands/acp/server.rs:1802`-2111,2113-2163 | Per-request spawn, absence of prompt exclusion, global cancel |
| Persistence | `libs/api/src/client/provider.rs:272` (`chat_completion_stream`), `695` (`initialize_session`), `808` (`save_checkpoint`); `libs/api/src/local/storage.rs:344`,559; `libs/api/src/local/migrations/v002_nullable_columns.rs:28`-73; `libs/api/src/stakpak/client.rs:83`-207 | Checkpoint-per-turn writes, backend selection, schema |
| Event translation | `cli/src/commands/acp/server.rs:1139` (`process_acp_streaming_response_with_cancellation`), `758` (`process_tool_calls_with_cancellation`), `227`-285, `cli/src/commands/acp/utils.rs:27`-82 | Stream→chunk/tool-update conversion, XML filtering, acks |
| LLM integration | `libs/ai/src/provider/trait_def.rs:9`; `libs/ai/src/registry/mod.rs:13`; `libs/ai/src/client/mod.rs:202`-205,316-319; `libs/shared/src/models/stakai_adapter.rs:25`-44,154-164,676-951; `libs/api/src/client/provider.rs:272`-434,835-918; `libs/api/src/local/hooks/task_board_context/mod.rs:45`-94; `libs/ai/src/providers/stakpak/{provider,stream}.rs` | stakai provider trait/registry/dispatch, message/tool conversion, streaming and tool-call delta path |
| Replay/reconstruction | `cli/src/commands/acp/server.rs:1779`-1800; `libs/api/src/client/provider.rs:706`-744 | Load does not replay or reload; checkpoints used for linkage only |
| Cancellation/errors | `cli/src/commands/acp/server.rs:186`-188,1191`-1203`,2113-2163; `cli/src/commands/agent/run/tooling.rs:110`-152; `libs/api/src/client/provider.rs:314`-366 | Broadcast scope, tool cancel race, skipped checkpoints |

## Research notes

- **Revision inspected:** `760cd2b5984d29c2d513bb15ca33e995fae45f17`
- **Primary evidence:** `cli/src/commands/acp/{server,fs_handler,utils,mod}.rs`; `cli/src/commands/mod.rs`; `cli/src/commands/agent/run/{mcp_init,tooling}.rs`; `libs/api/src/{lib,storage}.rs`, `libs/api/src/client/{mod,provider}.rs`, `libs/api/src/local/{storage,migrations/*}.rs`, `libs/api/src/stakpak/{storage,client}.rs`; tests in `cli/src/commands/acp/server.rs` and `utils.rs`
- **Relevant docs:** `README.md` ACP section (lines 451-484, consistent with code); repo `AGENTS.md` (interactive-mode flows; used only to locate code, and for the context-trimming metadata note); `docs/` has no ACP-specific page
- **Commands/tests run:** Read-only inspection; downloaded `agent-client-protocol` 0.9.3 crate source to verify request dispatch semantics (`rpc.rs:263`-296). No project tests executed.
- **Report confidence:** `high` — the ACP path is a small, fully read module plus a fully read persistence layer; the only soft spots (remote server behavior, unexercised auth paths) are called out explicitly.
