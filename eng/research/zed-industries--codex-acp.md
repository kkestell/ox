---
project: "zed-industries/codex-acp"
repository: "https://github.com/zed-industries/codex-acp"
revision: "296069e841634cd4bb9bc4515602d836e49231ec"
researched_at: "2026-09-19"
primary_language: "Rust"
implementation_form: "adapter"
process_model: "single-process"
session_owner: "shared"
durability: "wrapped-agent"
cross_session_concurrency: "concurrent"
same_session_concurrency: "serialized"
event_delivery: "direct"
resume_strategy: "delegate"
overall_confidence: "high"
---

# codex-acp (Rust) ACP architecture

## Executive summary

- **ACP boundary:** A single stdio ACP server built on `agent-client-protocol` 0.14 (`Cargo.toml:21`). `CodexAgent::serve` registers handlers for initialize, auth, new/load/resume/list/close session, prompt, cancel, and mode/config requests (`src/codex_agent.rs:122-307`). The adapter links the codex-rs crates as libraries and runs the agent loop in-process; it does not spawn a Codex CLI subprocess (`Cargo.toml:24-37`, `src/lib.rs:62-70`).
- **Session model:** An ACP session ID is the codex `ThreadId` rendered as a string, one-to-one with a codex thread (`src/codex_agent.rs:309-311`). The adapter holds an `Arc<Thread>` per session (`src/codex_agent.rs:63`); each `Thread` spawns a `ThreadActor` that mediates one wrapped `CodexThread` (`src/thread.rs:310-349`, `src/thread.rs:2741-2762`). Conversation state, the model loop, and tools live inside codex-core.
- **Concurrency:** Sessions are independent: each has its own actor task and codex thread, and every ACP request is handled in a spawned task (`src/codex_agent.rs:169-256`). Within a session, the actor serializes message handling but a prompt does not occupy the actor — `handle_prompt` submits an op and returns immediately (`src/thread.rs:3173-3282`). The wrapped runtime serializes model turns: a second same-session prompt is submitted while the first runs and codex-core folds it into the active turn as steering input rather than starting a parallel turn.
- **Durability and replay:** The adapter persists nothing conversational. codex-core owns rollout files, a thread store, and an optional SQLite state DB under `codex_home` (`src/codex_agent.rs:87-108`). `session/load` reads the rollout and converts stored items into ACP updates before responding (`src/codex_agent.rs:669-681`, `src/thread.rs:3358-3372`); `session/resume` restores model context without replay (`src/codex_agent.rs:627-650`).
- **Event flow:** The actor's `select` loop pulls codex events, routes them by submission ID to a per-prompt `PromptState`, and converts each event directly into ACP `session/update` notifications — fire-and-forget, no adapter queue (`src/thread.rs:2790-2818`, `src/thread.rs:2643-2650`). The `session/prompt` request resolves only at turn end: `TurnComplete` → `EndTurn`, `TurnAborted`/shutdown → `Cancelled`, `Error` → error response (`src/thread.rs:1351-1397`).
- **Notable uncertainty:** What a steered same-session second prompt receives as its own ACP stop reason is not established: codex-core emits terminal events under the active task's submission ID, and the steered submission's response channel may never resolve (dependency behavior, see Unknowns).

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `adapter` | Translates ACP ↔ codex-rs `Op`/`EventMsg`; the model loop, tools, and approvals live in the linked codex-core library, not in this repo | `Cargo.toml:24-37`, `src/thread.rs:3173-3282`, `src/thread.rs:1086-1508` |
| Process model | `single-process` | One binary serves all sessions; codex-core runs in-process. Subprocesses exist only below the runtime (MCP stdio servers, exec/sandbox helpers) | `src/main.rs:6-12`, `src/lib.rs:62-70`, `src/codex_agent.rs:88-94` |
| Session owner | `shared` | The adapter owns the ACP-facing session object (actor, routing, permission interactions, client connection); codex-core owns the conversation, tools, and persisted thread | `src/thread.rs:310-349`, `src/codex_agent.rs:97-108` |
| Durability | `wrapped-agent` | codex-core writes rollout files plus thread-store/state-DB records under `codex_home`; the adapter writes nothing conversational | `src/codex_agent.rs:87-108`, `src/codex_agent.rs:660-681` |
| Cross-session concurrency | `concurrent` | No cross-session locks; per-session actors and threads run on the shared tokio runtime; the sessions map mutex is held only for map access | `src/codex_agent.rs:63`, `src/thread.rs:342`, `src/codex_agent.rs:313-321` |
| Same-session concurrency | `serialized` | The adapter applies no exclusion — a second prompt is submitted immediately — but the wrapped runtime processes submissions one at a time and steers input into the active turn, so no parallel model turn exists | `src/thread.rs:2839-2845`, `src/thread.rs:3263-3279`; dependency `openai/codex@f221438` `codex-rs/core/src/session/handlers.rs` (`user_input_or_turn_inner` → `steer_input`) |
| Event delivery | `direct` | The actor calls `ConnectionTo::send_notification` directly per event; the ACP library handles framing/writing to stdout. No adapter-side channel or batching | `src/thread.rs:90-112`, `src/thread.rs:2643-2650` |
| Resume strategy | `delegate` | Runtime resume delegates to codex-core's `resume_thread_from_rollout` on the rollout path; the adapter additionally reconstructs client-facing history itself for `session/load` | `src/codex_agent.rs:685-696`, `src/codex_agent.rs:708-710` |

## System architecture

```text
ACP client (editor / IDE)
   |  JSON-RPC over stdio  (session/prompt, session/update notifications, permission requests)
   v
+------------------------- one process: codex-acp binary -------------------------+
|  agent-client-protocol 0.14 (framing, dispatch)  -- src/codex_agent.rs:122-307  |
|        |                                                                        |
|        v                                                                        |
|  CodexAgent (src/codex_agent.rs)                                                |
|    sessions: Mutex<HashMap<SessionId, Arc<Thread>>>      <-- in-memory registry |
|    thread_manager / thread_store / state_db (codex-core handles)                |
|        |  one Arc<Thread> per session                                           |
|        v                                                                        |
|  Thread + ThreadActor task (src/thread.rs:2741-2818)   per session              |
|    message_rx (unbounded mpsc): Prompt/Cancel/SetMode/Shutdown/Replay/...       |
|    select { messages, permission resolutions, thread.next_event() }             |
|        |  Op::UserInput / Op::Interrupt / ...      ^ Event{ id, EventMsg }      |
|        v                                           |                            |
|  CodexThread (codex-core, in-process library)  ---+                             |
|    session loop, model requests, tools, approvals, rollout recorder             |
|        |  tool subprocesses: MCP stdio servers, exec server, sandbox helper     |
+--------|------------------------------------------------------------------------+
         v
  codex_home: rollout files, thread store, state DB (SQLite)   <-- durable
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `CodexAgent` | ACP handler dispatch, session registry, auth, session config | process | `sessions` map, `session_roots` map, client capabilities, config, codex-core handles | `src/codex_agent.rs:49-66` |
| `Thread` (wrapper) | Public async API over the actor; keeps the actor task alive | session | `message_tx`, direct `CodexThreadImpl` handle | `src/thread.rs:310-317` |
| `ThreadActor` | Serializes per-session operations; routes codex events by submission ID | session task | `submissions` map (`PromptState`), config, dedup state | `src/thread.rs:2741-2762` |
| `SessionClient` | Emits ACP notifications and permission requests for one session | session | session ID + `ConnectionTo<Client>` | `src/thread.rs:2597-2614` |
| `CodexThread` (codex-core) | Model loop, tools, approvals, rollout persistence | session | conversation, pending input queue, sandboxed exec | `src/codex_agent.rs:570-576`, `src/thread.rs:212-230` |
| Rollout files / thread store / state DB | Durable thread history and metadata | process / disk | rollout items, thread metadata index | `src/codex_agent.rs:87-95`, `src/codex_agent.rs:660-681` |

### ACP surface

Transport is stdio: `run_main` wraps tokio stdin/stdout into `ByteStreams` and passes them to `Agent::builder()...connect_to` (`src/lib.rs:64-70`, `src/codex_agent.rs:305`). The protocol implementation is the `agent-client-protocol` crate (pinned `=0.14.0` with the `unstable` feature, `Cargo.toml:21`); the adapter implements no wire framing itself. `initialize` hardcodes protocol version V1, stores client capabilities in a shared mutex, and advertises prompt capabilities (embedded context, images), HTTP MCP support, `load_session`, and session close/list/resume (`src/codex_agent.rs:440-477`). Auth is negotiated via three methods (ChatGPT browser login, `CODEX_API_KEY`, `OPENAI_API_KEY`, `src/codex_agent.rs:463-476`, `src/codex_agent.rs:479-544`). Every request handler is registered with `cx.spawn`, so requests are processed concurrently by the ACP layer (`src/codex_agent.rs:139-256`); cancellation arrives as a `CancelNotification` (`src/codex_agent.rs:258-273`). The ACP layer is an adapter boundary: all agent behavior is delegated to the codex-core library running in the same process — README calls it an "adapter around the Codex CLI", but no CLI subprocess is involved in the agent loop; only packaging (`npm/bin/codex-acp.js`) distributes the binary.

### Runtime and process boundaries

One tokio multi-thread runtime hosts everything. Per session there is exactly one actor task (`tokio::spawn(actor.spawn())`, `src/thread.rs:342`) whose `select` loop (biased: messages, then permission resolutions, then codex events) is the serialization point for session operations (`src/thread.rs:2790-2818`). Permission requests are handled in their own spawned tasks that post resolutions back to the actor through a second unbounded channel (`src/thread.rs:901-934`); this keeps the actor responsive while a permission request blocks on the client. Codex-core internally runs a per-thread submission loop task; the adapter only sees `submit(op) -> submission_id` and `next_event() -> Event` (`src/thread.rs:212-230`). Subprocesses (MCP stdio servers, the exec server managed by `EnvironmentManager`, sandbox helpers) are spawned and owned inside codex-core, below the adapter boundary (`src/codex_agent.rs:88-94`). There is no daemon, no per-session process, and no shared queue across sessions.

## LLM abstraction and integration

### Abstraction

`codex-acp` uses neither a named third-party LLM abstraction library (no Rig, LangChain, or equivalent) nor a provider SDK directly. Its dependencies are the ACP protocol crate and the `codex-*` crates from `openai/codex` (tag `rust-v0.137.0`, resolved to `f221438b691b8f749d98f22077c93ebe01923fbe`), linked as in-process libraries (`Cargo.toml:21-37`). The adapter's entire model-facing interface is the two-method `CodexThreadImpl` trait — `submit(Op)` and `next_event() -> Event` — implemented by codex-core's `CodexThread` (`src/thread.rs:212-230`). It constructs no provider request and consumes no provider stream. This makes `codex-core` a wrapped in-process library runtime, not a wrapped executable or service: the model loop is outside this repository (`src/lib.rs:62-70`).

Inside the dependency the abstraction is bespoke. `codex-model-provider` defines a `ModelProvider` trait and its `create_model_provider` factory (`dependency codex-rs/model-provider/src/provider.rs:83`, `:148`); `codex-model-provider-info` holds provider descriptors and the built-in provider registry `built_in_model_providers` (`dependency codex-rs/model-provider-info/src/lib.rs:409-441`); `codex-core`'s `ModelClient`/`ModelClientSession` perform the calls (`dependency codex-rs/core/src/client.rs:220`, `:238`). The adapter observes only normalized codex `EventMsg`s, which `PromptState::handle_event` converts to ACP updates (`src/thread.rs:1086-1508`).

### Integration path

1. ACP `session/prompt` reaches `handle_prompt`, which converts ACP content blocks to codex `UserInput` items via `build_prompt_items` (text, image, resource link/embedded text; audio dropped) (`src/thread.rs:3179`, `src/thread.rs:3697-3732`).
2. The actor submits `Op::UserInput` (or a slash-command op) through `CodexThreadImpl::submit` and registers a `PromptState`; it never builds model input or conversation context itself (`src/thread.rs:3263-3279`).
3. codex-core's per-thread turn loop (`run_turn`) drains queued input and snapshots conversation history into model input with `sess.clone_history().for_prompt(...)` (`dependency codex-rs/core/src/session/turn.rs:225-233`, `dependency codex-rs/core/src/context_manager/history.rs:119`).
4. Model selection is already resolved in `TurnContext` (`model_info`, `provider`), built from `Config` when the thread was created (`dependency codex-rs/core/src/session/turn_context.rs:61-64`, `:185-242`); `codex-acp` passes that `Config` into `ThreadManager::start_thread`/`resume_thread_from_rollout` (`src/codex_agent.rs:570-576`, `:689-696`).
5. Request construction: `run_sampling_request` builds tools via `built_tools` and a `Prompt` carrying `router.model_visible_specs()`, then calls `ModelClientSession::stream(prompt, &model_info, ..., reasoning_effort, ...)` (`dependency codex-rs/core/src/session/turn.rs:960-970`, `:1029`, `:1785-1795`). `build_responses_request` assembles the Responses API body with `create_tools_json_for_responses_api(&prompt.tools)` and `prompt.get_formatted_input()` (`dependency codex-rs/core/src/client.rs:738-790`; re-export at `dependency codex-rs/tools/src/lib.rs:106`).
6. Streaming: `stream` selects the provider wire API and transport (Responses HTTP or WebSocket) and returns a `ResponseStream` (`dependency codex-rs/core/src/client.rs:1590-1630`); `try_run_sampling_request` consumes `ResponseEvent`s (text/reasoning deltas, `OutputItemDone`, `Completed`) (`dependency codex-rs/core/src/session/turn.rs:1860-2180`).
7. Tool-call handling: tool calls in `OutputItemDone` are dispatched through `ToolCallRuntime`/`ToolRouter` by `handle_output_item_done`; tool results are queued as `ResponseInputItem`s and appended to the conversation, and the loop re-samples until the model stops requesting tools (`dependency codex-rs/core/src/session/turn.rs:1000-1005`, `:1876-1877`, `:1931`).
8. Conversion back to runtime events: codex-core emits `EventMsg`s on the thread; the adapter's actor receives them through `next_event()` and routes them by submission ID to `PromptState::handle_event`, which emits ACP `session/update` notifications (`src/thread.rs:212-230`, `src/thread.rs:3688-3694`, `src/thread.rs:1086-1508`).

Not visible from this repository: the provider wire protocol and request body, transport and retry behavior, the streaming parser, and tool execution — all inside codex-core and its crates.

### Provider and tool boundary

- Provider-specific code lives in the `codex-model-provider`/`codex-model-provider-info` dependency crates. In `codex-acp` the only provider-specific logic is the auth gate on `config.model_provider_id == "openai"` (`src/codex_agent.rs:323-331`).
- Credentials/configuration: `codex-login`'s `AuthManager`, constructed with `codex_home` and `chatgpt_base_url` in `CodexAgent::new` (`src/codex_agent.rs:77-83`). ACP auth offers ChatGPT browser login, `CODEX_API_KEY`, and `OPENAI_API_KEY` (`src/codex_agent.rs:463-476`, `:499-541`). Base configuration comes from `Config::load_with_cli_overrides_and_harness_overrides` (`src/lib.rs:42-55`), and the adapter forwards the whole `Config` to codex-core (`src/codex_agent.rs:97-108`).
- Request and response normalization is inside `ModelClient` (`dependency codex-rs/core/src/client.rs:738-790`); none of it is in this repository.
- Tool schemas are built inside the dependency (`create_tools_json_for_responses_api`, `dependency codex-rs/tools/src/lib.rs:106`; registry/spec plan under `dependency codex-rs/core/src/tools/`). `codex-acp` contributes only MCP server configuration (`src/codex_agent.rs:345-433`) and translates codex tool events into ACP `ToolCall`/`ToolCallUpdate` (`src/thread.rs:1249-1341`).
- Multi-provider and selection: the wrapped runtime is multi-provider — `built_in_model_providers` registers `openai`, `amazon-bedrock`, `ollama`, and `lmstudio` (`dependency codex-rs/model-provider-info/src/lib.rs:36-47`, `:409-441`) — and the active provider is chosen by the `model_provider_id` config key resolved during config loading (`dependency codex-rs/core/src/config/mod.rs:612-616`, `:3037-3050`). `codex-acp` does not expose provider selection over ACP; its per-session config options are only `mode`, `model`, and `reasoning_effort` (`src/thread.rs:3064-3077`). Model and reasoning effort are selectable and are pushed to the runtime with `Op::ThreadSettings` (`src/thread.rs:2997-3001`, `:3080-3123`, `:3128-3164`).
- How a non-OpenAI provider would be selected through the ACP surface: **Not found** (checked `src/codex_agent.rs`, `src/thread.rs`, `src/lib.rs`, `Cargo.toml`).

### Limits

- Provider selection is unreachable through ACP: a search of all of `src/` found no provider config option beyond the OpenAI auth gate (`src/codex_agent.rs:324`); only model and reasoning-effort options exist (`src/thread.rs:3064-3077`).
- Provider wire behavior (Responses vs chat-completions, headers, base URLs, retries, WebSocket fallback) and response-to-`EventMsg` normalization are defined in the `openai/codex` dependency, not this checkout; they were read at the pinned revision and may differ at other revisions.
- Whether `codex-acp` is usable end-to-end with a non-OpenAI provider cannot be determined here: the auth methods and `check_auth` gate are OpenAI-specific (`src/codex_agent.rs:324`, `:463-466`), while the runtime provider is config-driven.
- Codex config precedence (config.toml, managed config, CLI `-c` overrides) is resolved inside `codex-config` and is not visible from this repository.
- Any LLM call outside `codex-core`: **Not found** (checked `Cargo.toml`, `src/lib.rs`, `src/codex_agent.rs`, `src/thread.rs`).

## Session model

### Identity and ownership

A "session" is one entry in `CodexAgent.sessions`: an `Arc<Thread>` keyed by `SessionId`. `SessionId` is `ThreadId.to_string()` — the codex thread's UUID (`src/codex_agent.rs:309-311`), produced by `thread_manager.start_thread` for new sessions (`src/codex_agent.rs:570-578`) or taken from the client and resolved to a rollout path for load/resume (`src/codex_agent.rs:660-667`). The mapping is one-to-one across all four layers: ACP `SessionId` = adapter `Thread` (actor) = codex-core `CodexThread`/conversation = rollout file found by ID string. `list_sessions` projects persisted thread-store records back into the same ID space (`src/codex_agent.rs:771`). A second map, `session_roots`, records each session's working directory "for filesystem sandboxing" (`src/codex_agent.rs:65`), but nothing in this revision ever reads it — it is write-only state (`src/codex_agent.rs:580-583`, `src/codex_agent.rs:714-717`, `src/codex_agent.rs:792-795`).

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | `check_auth`, build per-session config (cwd + client MCP servers merged), `thread_manager.start_thread`, wrap in `Thread`, call `thread.load()` for modes/config options, insert into registry | codex-core creates the thread and begins a rollout | Start failure returns an ACP error; the registry insert happens only after load succeeds | `src/codex_agent.rs:554-605` |
| Load/resume | Find rollout path by session ID; `load` additionally reads rollout history; `resume_thread_from_rollout` rebuilds the codex thread; `load` replays history to the client before responding | Existing rollout is reopened/continued by codex-core | Unknown session ID → `resource_not_found`; rollout read/resume errors → internal error | `src/codex_agent.rs:652-723` |
| Prompt | `thread.prompt` sends a `Prompt` message; the actor builds `UserInput` items (or slash-command ops), submits the op, registers a `PromptState`, and returns a completion receiver | codex-core records the user message into the rollout | Submit failure → error to caller; turn end resolves the receiver (EndTurn/Cancelled/error) | `src/codex_agent.rs:799-809`, `src/thread.rs:3173-3282` |
| Cancel | Actor clears pending permission interactions and submits `Op::Interrupt` | Turn abort is recorded by codex-core | Errors logged, not surfaced to a request (cancel is a notification) | `src/codex_agent.rs:811-815`, `src/thread.rs:3328-3335` |
| Close/delete | Actor submits `Op::Shutdown`; `ShutdownComplete` resolves any in-flight prompt as `Cancelled`; then `thread_manager.remove_thread` and registry removal | Rollout remains on disk; nothing deleted | Shutdown errors propagate to the close request | `src/codex_agent.rs:780-798`, `src/thread.rs:3337-3344`, `src/thread.rs:1391-1397` |

Re-loading a session that is already in the registry silently overwrites the registry entry with a new actor and a freshly resumed codex thread; no guard rejects or coordinates with the previous instance (`src/codex_agent.rs:718`).

### Durable representation

All durable state is owned by the wrapped codex runtime under `config.codex_home`: rollout files (one per thread), a `ThreadStore` used for listing/metadata, and an optional SQLite `StateDbHandle` index (`src/codex_agent.rs:87-95`). The adapter's only durable-adjacent write is `set_project_trust_level` when a mode change trusts the project (`src/thread.rs:3313-3319`), which is codex-core configuration, not conversation history. Stored rollout items are the authoritative history: `RolloutItem::EventMsg` for messages/reasoning/lifecycle and `RolloutItem::ResponseItem` for tool calls and outputs, read back verbatim for replay (`src/codex_agent.rs:669-681`, `src/thread.rs:3358-3372`). Within codex-core, events are appended to rollout storage as they are emitted (dependency `openai/codex@f221438`, `codex-rs/core/src/session/mod.rs` `send_event_raw`); the adapter performs no writes and controls no flush points. Not persisted in a replayable form: streaming deltas, plan updates, token usage, warnings, and mode/config changes (replay skips them, `src/thread.rs:3398-3403`).

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | Independent actors and codex threads; ACP handlers spawn per request; no shared lock beyond the brief registry mutex | `src/codex_agent.rs:245-256`, `src/thread.rs:342` |
| Two prompts in the same session | `serialized` | No adapter guard: the actor handles the second `Prompt` immediately and submits a second op (`thread.rs:2839-2845`, `thread.rs:3263-3279`). codex-core's per-thread submission loop then steers the second `UserInput` into the active Regular turn instead of spawning a parallel turn | dependency `openai/codex@f221438` `codex-rs/core/src/session/handlers.rs` (`user_input_or_turn_inner`), `codex-rs/core/src/session/mod.rs:3212-3281` (`steer_input`) |
| Load/resume during an active prompt | unknown (unguarded) | `restore_session` overwrites the registry entry and starts a new codex thread from the rollout; the old actor keeps running until its channels close. No detection of the active prompt | `src/codex_agent.rs:652-723`, `src/codex_agent.rs:718` |
| Delete/close during an active prompt | partial cleanup | `close_session` submits `Op::Shutdown` and removes registry entries; `ShutdownComplete` resolves the in-flight prompt with `Cancelled` before the response returns | `src/codex_agent.rs:780-798`, `src/thread.rs:1391-1397` |
| Cancellation isolation | per session | `CancelNotification` targets one session ID; the actor submits `Op::Interrupt` only for its own codex thread | `src/codex_agent.rs:811-815`, `src/thread.rs:3328-3335` |

The critical section for same-session operations is the actor loop itself: only one `ThreadMessage` is handled at a time, and prompt handling does not span the turn (the completion receiver is awaited by the ACP request task, not the actor). This means cancel, mode changes, and config changes interleave with a running turn by design. Two subtleties: (1) events are routed by submission ID, and per-submission `PromptState`s are litter-collected once their response sender is closed (`src/thread.rs:2810-2816`, `src/thread.rs:888-893`); (2) since the second same-session prompt never becomes the active task in codex-core, its `PromptState` may never observe a terminal event under its own ID — the observable end-to-end behavior of that second ACP request is the report's main uncertainty (see Unknowns). Shared coupling across sessions is limited to the process-wide auth manager, models manager, thread store, and state DB (`src/codex_agent.rs:97-108`).

## Event and data flow

### New prompt: ACP client to live response

1. ACP delivers `session/prompt`; the spawned handler checks auth, looks up `Arc<Thread>` by session ID, and awaits `thread.prompt(request)` (`src/codex_agent.rs:245-256`, `src/codex_agent.rs:799-809`).
2. `Thread::prompt` sends `ThreadMessage::Prompt` over the unbounded channel and awaits a receiver of the eventual stop reason (`src/thread.rs:373-387`).
3. The actor's `handle_prompt` converts ACP content blocks to `UserInput` (text, images as data URLs, resource links/embedded text; audio dropped) (`src/thread.rs:3179`, `src/thread.rs:3697-3732`).
4. A leading slash command is rewritten to dedicated ops: `/compact`, `/init` (a canned prompt), `/review`, `/review-branch`, `/review-commit`, `/logout` (`src/thread.rs:3181-3251`, `src/thread.rs:4081-4103`).
5. The op is submitted via `CodexThreadImpl::submit`, returning a submission ID; a `PromptState` keyed by that ID is registered (`src/thread.rs:3263-3279`).
6. codex-core runs the turn; the actor's select loop receives each `Event{id, msg}` and routes it to the matching `PromptState` (`src/thread.rs:3688-3694`).
7. `PromptState::handle_event` converts events to ACP updates: reasoning deltas → `AgentThoughtChunk`, message deltas → `AgentMessageChunk` (a full `AgentMessage` is sent only if no deltas were seen, `src/thread.rs:1187-1193`), exec/patch/MCP/web-search/image/dynamic tools → `ToolCall`/`ToolCallUpdate` sequences (`src/thread.rs:1086-1508`).
8. Approval-required events spawn a permission request to the client; the resolution is posted back through the actor and converted into `Op::ExecApproval`/`Op::PatchApproval`/`Op::RequestPermissionsResponse`/`Op::ResolveElicitation` (`src/thread.rs:901-934`, `src/thread.rs:936-1083`).
9. Terminal events end the request: `TurnComplete` → `PromptResponse(EndTurn)`, `TurnAborted`/`ShutdownComplete` → `Cancelled`, `Error` → ACP error with the message payload (`src/thread.rs:1351-1397`). Token counts stream as `UsageUpdate` notifications (`src/thread.rs:1128-1137`).

### Durable history to ACP client

Replay happens only on `session/load` (`session/resume` skips it, `src/codex_agent.rs:627-650`).

1. `restore_session` resolves the rollout path via `find_thread_path_by_id_str` and, for load, fetches `RolloutRecorder::get_rollout_history` (`InitialHistory::Resumed`/`Forked` items) (`src/codex_agent.rs:660-681`).
2. The codex thread is resumed from the rollout so the model context is rebuilt inside codex-core (`src/codex_agent.rs:685-696`).
3. Before responding, the actor replays items in stored order: `EventMsg::UserMessage` → user chunk, `AgentMessage`/`AgentReasoning`(+raw) → agent/thought chunks, `ThreadGoalUpdated` → agent text; `ResponseItem` tool calls (`FunctionCall`, `LocalShellCall`, `CustomToolCall` incl. apply-patch parsing, `WebSearchCall`, `ImageGenerationCall`) and their outputs → completed `ToolCall`/`ToolCallUpdate` notifications (`src/thread.rs:3358-3372`, `src/thread.rs:3376-3404`, `src/thread.rs:3536-3686`).
4. Only then is the `LoadSessionResponse` (modes + config options) returned (`src/codex_agent.rs:708-723`). Everything not covered by those two conversions — plans, token usage, warnings, deltas — is silently dropped from replay (`src/thread.rs:3398-3403`, `src/thread.rs:3683-3684`).

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `Op::UserInput` (`UserInput` items) | none (echoed back only via codex `UserMessage` event → ignored live) | `RolloutItem::EventMsg(UserMessage)` (replayed) | codex-core, at event emission | `src/thread.rs:3253-3260`, `src/thread.rs:1141-1149` |
| Assistant text | `AgentMessageContentDelta` / `AgentMessage` | `AgentMessageChunk` notifications | `EventMsg::AgentMessage` (full text; deltas not replayed) | codex-core, as events are emitted | `src/thread.rs:1150-1193` |
| Reasoning/thought | `ReasoningContentDelta`, `AgentReasoning` | `AgentThoughtChunk` | `EventMsg::AgentReasoning`(+raw) | codex-core | `src/thread.rs:1160-1199` |
| Tool call | exec/patch/MCP/web/image/dynamic events | `ToolCall` (in-progress) + `ToolCallUpdate`s | `RolloutItem::ResponseItem` function/custom/shell calls | codex-core, when the item finalizes | `src/thread.rs:1249-1341`, `src/thread.rs:3540-3660` |
| Tool result | `ExecCommandEnd`, `McpToolCallEnd`, `PatchApplyEnd`, outputs | `ToolCallUpdate` (completed/failed, raw output) | `ResponseItem::*Output` | codex-core | `src/thread.rs:1259-1264`, `src/thread.rs:1782-1817`, `src/thread.rs:3569-3648` |
| Completion/failure | `TurnComplete`/`TurnAborted`/`Error`/`ShutdownComplete` | `session/prompt` response (EndTurn/Cancelled/error) | turn boundary recorded by codex-core | codex-core, at turn end | `src/thread.rs:1351-1397` |

Persistence is entirely inside codex-core and happens per event, before or concurrently with the adapter's notification send; the adapter itself batches nothing and has no commit step.

### Subsequent-prompt reconstruction

The adapter never rebuilds model context. For a session continued in-process, codex-core holds the conversation in memory and appends to it. For a loaded/resumed session, codex-core reconstructs the conversation from the rollout during `resume_thread_from_rollout` (`src/codex_agent.rs:689-696`); the adapter's `replay_history` is purely client-facing and feeds nothing back into the runtime. The `/compact` command triggers codex-core compaction (`src/thread.rs:3183`), and codex-core's own context-compaction events are surfaced to the client only as an informational text chunk (`src/thread.rs:1455-1458`).

### Ordering, cancellation, failure, and backpressure

- Ordering: notifications are sent synchronously in event order from the single actor, so ACP updates for one session are total-ordered (`src/thread.rs:2643-2650`); the ACP library's internal write buffering is outside this repo. Slash-command availability is sent from a separate task 200 ms after load, a deliberate ordering hack (`src/thread.rs:2825-2833`).
- Backpressure: both adapter channels are unbounded; notification sends are fire-and-forget (errors only logged). A slow client is handled (or not) by the ACP library and OS stdio buffering, which this repo does not bound or observe.
- Cancellation: `Op::Interrupt` → codex-core aborts the active turn → `TurnAborted` resolves the prompt with `Cancelled` (`src/thread.rs:1384-1390`). Pending permission interactions are detached so late client responses drain without touching the aborted submission; detached tasks keep running so ACP can route the required `Cancelled` outcome (`src/thread.rs:895-899`, `src/thread.rs:5527-5617` tests).
- Failure: codex `Error` events fail the prompt request with the error payload (`src/thread.rs:1370-1383`); stream errors are logged and the turn continues (`src/thread.rs:1361-1369`). If `next_event()` errors, the actor loop breaks — the session actor dies and subsequent messages fail with internal errors (`src/thread.rs:2802-2808`, `src/thread.rs:373-387`).
- Partial output: streamed deltas already delivered to the client are not reconciled with durable state on abort; on resume, clients get full messages from the rollout, so a client that kept partial deltas may duplicate text on replay.

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | Registry of `Arc<Thread>` + one actor each | `src/codex_agent.rs:63`, `src/thread.rs:342` |
| Concurrent work across sessions | `yes` | Independent actors/threads; handlers spawned per request | `src/codex_agent.rs:169-256` |
| Same-session prompt exclusion | `partial` | No adapter guard; codex-core steers the second prompt into the active turn rather than rejecting or queueing it as a new turn; the second request's stop-reason delivery is unverified | `src/thread.rs:2839-2845`; dependency `openai/codex@f221438` `session/mod.rs:3212-3281` |
| Durable sessions | `yes` | Rollout files + thread store + state DB owned by codex-core | `src/codex_agent.rs:87-95` |
| Session list | `yes` | Paged (25), sorted by update time, filtered by cwd and allowed sources | `src/codex_agent.rs:725-778` |
| Session load/resume | `yes` | Both `LoadSession` (with replay) and `ResumeSession` (without) | `src/codex_agent.rs:607-650` |
| History replay to ACP client | `partial` | Messages, reasoning, and tool calls replayed; plans, usage, warnings, and deltas are dropped | `src/thread.rs:3358-3686` |
| Prior history reused by model | `yes` | `resume_thread_from_rollout` rebuilds codex-core conversation | `src/codex_agent.rs:685-696` |
| Prompt cancellation | `yes` | `Op::Interrupt` → `TurnAborted` → `StopReason::Cancelled` | `src/thread.rs:3328-3344`, `src/thread.rs:1384-1390` |
| Tool-call progress updates | `yes` | Begin/end updates for exec (incl. output deltas), patches, MCP, web search, image generation, dynamic tools | `src/thread.rs:1249-1341` |
| Partial-output persistence | `no` | Streaming deltas exist only live; durable records hold full messages/items | `src/thread.rs:1187-1193`, `src/thread.rs:3376-3404` |
| Recovery after process restart | `yes` | Load/resume from rollout files by session ID | `src/codex_agent.rs:652-723` |

## Design assessment

### Strengths

- The per-session actor is a single, easily-audited serialization point: one loop owns all session operations and all event routing, with stale permission responses guarded by interaction IDs (`src/thread.rs:943-960`).
- Event routing by submission ID with self-cleaning `PromptState`s keeps multi-turn bookkeeping small and avoids global locks (`src/thread.rs:3688-3694`, `src/thread.rs:2810-2816`).
- Durability is delegated wholesale to codex-core; the adapter holds no conversation state to lose or corrupt, so resume is exactly the wrapped runtime's native path.
- Unit tests drive the actor through a scripted `CodexThreadImpl`, exercising real interleavings (parallel exec events, blocked approvals, detached permission drains) without a model (`src/thread.rs:4636-4740`, `src/thread.rs:5126-5196`).

### Tradeoffs and limitations

- Two conversion paths for the same data (live `EventMsg` → ACP in `PromptState::handle_event`, stored items → ACP in `replay_*`) must be kept semantically aligned by hand; they already differ (deltas vs full messages, skipped event classes), so replayed UI can diverge from what a live client saw (`src/thread.rs:1086-1508` vs `src/thread.rs:3358-3686`).
- The adapter trusts codex-core's event taxonomy exhaustively: each new `EventMsg` variant forces a decision here, and silent `_ => {}` arms mean unrecognized behavior disappears (`src/thread.rs:1476-1506`).
- Same-session concurrent prompts are accepted without exclusion, and the second request's lifecycle depends on unobserved codex-core routing — an ambiguity the adapter could reject cheaply (`src/thread.rs:2839-2845`).
- `session_roots` is maintained but never read, and the 200 ms delayed `AvailableCommandsUpdate` is a timing-dependent workaround (`src/codex_agent.rs:65`, `src/thread.rs:2825-2833`).
- Unbounded channels plus fire-and-forget notification sends give the adapter no backpressure signal from the client (`src/thread.rs:329`, `src/thread.rs:2643-2650`).

### Ideas relevant to Ox

- Routing runtime events to per-prompt state by an opaque submission ID (rather than a session-wide "current prompt") cleanly supports interleaved operations like cancel and config changes mid-turn; the cost is depending on the runtime to tag every event, which is exactly where codex-core's steering behavior becomes ambiguous. A session-owned actor with explicit turn ownership (reject or queue a second prompt at the boundary) would remove that ambiguity — the tradeoff is rejecting legitimate steering-style input.
- Keeping one conversion function per direction (live and replay) doubles the drift surface; Ox's persisted-event-first design can make replay a replay of the same conversion output that was streamed, at the cost of persisting what the client actually saw.
- Delegating durability to the wrapped runtime keeps the adapter thin, but the adapter then cannot control replay fidelity; anything the runtime does not record (plans, usage, deltas) is unrecoverable for clients.

## Unknowns and conflicts

- Steered second prompt's ACP response: the pinned codex-core emits `TurnComplete`/`TurnAborted` under the active task's submission ID (`openai/codex@f221438`, `codex-rs/core/src/session/turn.rs`, `codex-rs/core/src/session/input_queue.rs` `turn_state_for_sub_id` matches only the active task). The adapter resolves a prompt only on an event under its own submission ID (`src/thread.rs:3688-3694`, `src/thread.rs:1351-1397`), so a steered second prompt may hang until cancel/shutdown. Not exercised by any test in this repo; labeled Inference from dependency source.
- codex-core internals (submission loop ordering, rollout write filtering, resume reconstruction) are defined in the dependency, not here; I read selected files at the pinned rev `f221438b691b8f749d98f22077c93ebe01923fbe` remotely. Behavior at other codex versions may differ.
- Whether codex `AgentMessageContentDelta` events are themselves written to rollouts (and merely unused by replay) could not be determined from this repository.
- `session_roots` has no reader in this revision; its intended sandboxing consumer is absent (searched all of `src/`).
- README describes the project as an "adapter around the Codex CLI", but the implementation links codex-rs as in-process libraries; the only subprocess boundary is tool execution/MCP below codex-core (`Cargo.toml:24-37`).
- `InitializeRequest.client_info` is explicitly unhandled (`src/codex_agent.rs:444`).
- Searches for a close/delete path that removes durable history found none: close only shuts down the in-memory thread; deletion exists nowhere (checked `close_session`, `thread_manager` usage, `ThreadStore` calls).

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `src/lib.rs:23-73` (`run_main`), `src/codex_agent.rs:122-307` (`serve`) | Transport, handler registration, capability negotiation |
| Session management | `src/codex_agent.rs:554-605` (`new_session`), `src/codex_agent.rs:652-723` (`restore_session`), `src/codex_agent.rs:780-798` (`close_session`), `src/thread.rs:310-461` (`Thread`) | Creation, load/resume, close, and the wrapper API |
| Concurrency | `src/thread.rs:2790-2818` (`ThreadActor::spawn`), `src/thread.rs:2820-2901` (`handle_message`), `src/codex_agent.rs:313-321` (`get_thread`) | The serialization point and registry locking |
| Persistence | `src/codex_agent.rs:87-108` (`CodexAgent::new`), `src/codex_agent.rs:660-681` (rollout lookup), `src/thread.rs:3313-3319` (trust write) | Where durable state lives and who writes it |
| Event translation | `src/thread.rs:1086-1508` (`PromptState::handle_event`), `src/thread.rs:2597-2739` (`SessionClient`), `src/thread.rs:3697-3732` (`build_prompt_items`) | Live codex event → ACP conversion in both directions of a turn |
| LLM integration | `src/thread.rs:212-230` (`CodexThreadImpl`), `src/codex_agent.rs:77-108` (auth/config → `ThreadManager`), `src/thread.rs:3080-3164` (model/effort selection); dependency `openai/codex@f221438` `codex-rs/model-provider/src/provider.rs:83`, `codex-rs/core/src/client.rs:738-790`, `codex-rs/core/src/session/turn.rs:135-233` | Where the model boundary sits and how prompts reach the provider |
| Replay/reconstruction | `src/thread.rs:3358-3372` (`handle_replay_history`), `src/thread.rs:3376-3404` (`replay_event_msg`), `src/thread.rs:3536-3686` (`replay_response_item`) | Durable history → ACP client conversion |
| Cancellation/errors | `src/thread.rs:3328-3344` (`handle_cancel`/`handle_shutdown`), `src/thread.rs:1351-1397` (terminal events), `src/thread.rs:895-960` (permission detach/resolve) | How cancellation and failure resolve prompts and interactions |

## Research notes

- **Revision inspected:** `296069e841634cd4bb9bc4515602d836e49231ec` (verified via `git rev-parse HEAD` in the checkout; commit dated 2026-07-22).
- **Primary evidence:** `src/lib.rs`, `src/main.rs`, `src/codex_agent.rs`, `src/thread.rs` (production path read end to end); `src/thread.rs:4106-5685` tests (`test_prompt`, `test_delta_deduplication`, `test_parallel_exec_commands`, `test_detached_permission_request_drains_late_response`, `test_thread_shutdown_bypasses_blocked_permission_request`) used as corroboration.
- **Relevant docs:** `README.md` (claims adapter-over-CLI; see conflict above); `npm/README.md`, `npm/bin/codex-acp.js` skimmed for packaging only.
- **Commands/tests run:** Read-only inspection (`git log`, `grep`, targeted file reads). Dependency sources at the pinned `openai/codex@f221438` (`codex-rs/core/src/codex_thread.rs`, `session/mod.rs`, `session/handlers.rs`, `session/input_queue.rs`, `state/turn.rs`) fetched read-only to answer the same-session concurrency question. No tests executed.
- **Report confidence:** `high` — the adapter is small, fully read, with direct line evidence; residual uncertainty is confined to codex-core dependency behavior, which is cited at its pinned revision.
