---
project: "agentclientprotocol/codex-acp"
repository: "https://github.com/agentclientprotocol/codex-acp"
revision: "d7b07c1b44a28890cdf3d5450f8974a812db5ae2"
researched_at: "2026-09-19"
primary_language: "TypeScript"
implementation_form: "adapter"
process_model: "hybrid"
session_owner: "wrapped-agent"
durability: "wrapped-agent"
cross_session_concurrency: "concurrent"
same_session_concurrency: "unknown"
event_delivery: "mixed"
resume_strategy: "delegate"
overall_confidence: "high"
---

# codex-acp ACP architecture

## Executive summary

- **ACP boundary:** A stdio ACP agent on `@agentclientprotocol/sdk`; `src/index.ts:139-172` wires every ACP method to `CodexAcpServer`. The adapter is not the agent: it spawns the Codex app-server (`codex app-server`) as a child process at startup (`src/index.ts:97-103`, `src/CodexJsonRpcConnection.ts:15-43`) and translates ACP requests into Codex JSON-RPC calls and Codex notifications into ACP `session/update` notifications.
- **Session model:** An ACP session ID is exactly the Codex thread ID (`src/CodexAcpClient.ts:615`). The adapter keeps a thin in-memory `SessionState` (model/mode/usage/title/failure state) per session (`src/CodexAcpServer.ts:161-193`); the thread, model loop, tools, and all durable state are owned by the wrapped Codex process.
- **Concurrency:** Prompts for different sessions race freely, matched to callers by thread+turn ID (`src/__tests__/CodexACPAgent/CodexAcpClient.test.ts:1289`). Within one session, no adapter guard excludes a second `session/prompt`; both reach `turn/start` on the same thread and the outcome depends on unobservable Codex-side behavior.
- **Durability and replay:** The adapter persists nothing conversational — its only disk write is a log file (`src/Logger.ts:45`). Codex owns thread persistence (`Thread.path` rollout files, `src/app-server/v2/Thread.ts:70`) and resume via `thread/resume` (`src/CodexAcpClient.ts:519-529`). `session/load` replays the returned `Thread` as ACP updates (`src/CodexAcpServer.ts:1990-2023`); `session/resume` returns models/modes without replay.
- **Event flow:** Codex notifications arrive on one JSON-RPC stdio connection, are routed per thread ID, and pushed through a serialized per-session promise chain (`src/CodexAcpClient.ts:910-927`) into `CodexEventHandler`, which converts each event into one ACP `session/update` (`src/CodexEventHandler.ts:392-428`, `src/ACPSessionConnection.ts:18-23`).
- **Notable uncertainty:** What Codex does when a second `turn/start` targets an in-progress thread (queue or reject) is defined outside this repo; generated types hint at a queue (`QueuedSubmission`, `thread/queue/changed`) but the adapter ignores that notification (`src/CodexEventHandler.ts:648`).

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `adapter` | Translates ACP ↔ Codex app-server JSON-RPC; agent loop, tools, approvals live in the spawned Codex process | `src/index.ts:97-129`, `src/CodexAcpClient.ts:929-957` |
| Process model | `hybrid` | One adapter process serves all sessions and spawns exactly one shared Codex app-server child; no per-session processes | `src/CodexJsonRpcConnection.ts:15-43`, `src/index.ts:97-103` |
| Session owner | `wrapped-agent` | Codex owns thread/turn/item state and durable storage; the adapter owns only in-memory UI/config state keyed by the same session ID | `src/CodexAcpServer.ts:161-193`, `src/CodexAcpClient.ts:615` |
| Durability | `wrapped-agent` | Codex writes thread rollout files; adapter writes only a log file, plus delegated title/archive calls that mutate Codex records | `src/app-server/v2/Thread.ts:70`, `src/Logger.ts:45`, `src/TitleGenerator.ts:88-91` |
| Cross-session concurrency | `concurrent` | No cross-session locks; per-session queues serialize only within one session; concurrent prompts on two sessions are tested | `src/CodexAcpClient.ts:910-927`, `src/__tests__/CodexACPAgent/CodexAcpClient.test.ts:1289` |
| Same-session concurrency | `unknown` | No adapter-level guard rejects or queues a second `session/prompt` on a busy session; both are forwarded as `turn/start`, and Codex's response is outside this repo. Steering/goal turns are the exception: they await the previous prompt's completion | `src/CodexAcpServer.ts:2581`, `src/CodexAcpServer.ts:1638-1648` |
| Event delivery | `mixed` | Events cross the subprocess boundary as JSON-RPC stdio notifications, then are delivered in-process as directly awaited `session/update` writes, serialized per session by a promise chain | `src/CodexAppServerClient.ts:173-215`, `src/CodexAcpClient.ts:910-927`, `src/ACPSessionConnection.ts:18-23` |
| Resume strategy | `delegate` | Resume delegates to Codex `thread/resume` by thread ID; the adapter then converts the returned `Thread` payload into ACP updates for `session/load` | `src/CodexAcpClient.ts:519-580`, `src/CodexAcpServer.ts:1990-2023` |

## System architecture

```text
ACP client (editor / IDE)
   |  JSON-RPC ndjson over stdio   (requests, responses, session/update notifications)
   v
+-------------------------------- one adapter process (Node.js) --------------------------------+
|  @agentclientprotocol/sdk  acp.agent(...)  -- src/index.ts:139-172                            |
|        |                                                                                      |
|        v                                                                                      |
|  CodexAcpServer (src/CodexAcpServer.ts)                                                       |
|    sessions: Map<sessionId, SessionState>        <-- in-memory UI/config state only           |
|    activePrompts / pendingTurnStarts / generations / closingSessions maps                     |
|        |  turn/start, turn/interrupt, thread/start|resume|list|archive, model/list ...        |
|        v                                                                                      |
|  CodexAcpClient + CodexAppServerClient (JSON-RPC client over child stdio)                     |
|    per-thread notificationHandlers, staleTurnIds, awaitTurnCompleted resolvers                |
+---------------------------------------------------|-------------------------------------------+
                                                    | JSON-RPC ndjson (stdin/stdout, no jsonrpc field)
                                                    v
                              +------------------ one shared Codex app-server child ------------------+
                              |  threads / turns / items, model loop, shell + MCP tools, approvals    |
                              |  durable thread rollout files on disk (Thread.path)                   |
                              |  pushes: item/*, turn/started, turn/completed, error, thread/*        |
                              +-----------------------------------------------------------------------+

Live:    Codex --notifications--> adapter (serialize per session) --session/update--> client
Replay:  session/load --> thread/resume + history read --> Thread --> ACP updates --> client
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| ACP stdio server (`acp.agent`) | Transport, request dispatch to `CodexAcpServer` | Adapter process | none | `src/index.ts:139-172` |
| `CodexAcpServer` | Session lifecycle, prompt/cancel orchestration, capability negotiation | Adapter process | `sessions`, `activePrompts`, `pendingTurnStarts`, generation/close-fence maps | `src/CodexAcpServer.ts:276-287` |
| `CodexAcpClient` | Session-facing Codex operations; per-session serialized notification queues | Adapter process | `sessionNotificationQueues`, subagent subscriptions | `src/CodexAcpClient.ts:114-116, 873-927` |
| `CodexAppServerClient` | Typed JSON-RPC client; routes notifications by thread ID; turn-completion promises; stale-turn suppression | Adapter process | `notificationHandlers`, `pendingTurnCompletionResolvers`, `staleTurnIds` | `src/CodexAppServerClient.ts:154-169` |
| Codex app-server child | Model loop, tools, approvals, thread persistence | Child process, shared by all sessions; restarted on provider change (`src/CodexAcpServer.ts:1108-1138`) | Thread/turn/item state, rollout files, auth | `src/CodexJsonRpcConnection.ts:15-43`, `src/app-server/v2/Thread.ts:70` |

### ACP surface

Transport is newline-delimited JSON-RPC over stdio via the `@agentclientprotocol/sdk` `acp.agent` builder; every standard ACP method plus extension methods (`_session/steering`, goal control, async-task stop, auth status) is registered in one place (`src/index.ts:139-172`). `initialize` negotiates capabilities — `loadSession: true`, embedded context and images, session list/fork/close/delete/resume, and AIR/JetBrains `_meta` extensions — and forwards client info to the Codex app-server (`src/CodexAcpServer.ts:335-407`, `src/CodexAcpClient.ts:137`). The ACP layer is a pure adapter: no model calls, no tool execution, and no approval decisions are made here; `CodexApprovalHandler`/`CodexElicitationHandler` translate Codex approval requests into ACP `session/request_permission` and elicitation requests (`src/CodexAcpServer.ts:2810-2826`).

### Runtime and process boundaries

The adapter process starts one Codex app-server child at boot (`src/index.ts:97-103`) and kills it ~2s after the ACP client closes stdin (`src/index.ts:105-114`). All sessions multiplex over this single child connection. A provider change tears the child down and restarts it, awaiting all active prompts first and re-resuming every session afterward (`src/CodexAcpServer.ts:1028-1085`). The adapter wraps every Codex call in `runWithProcessCheck`, converting a dead child into a typed ACP error carrying recent stderr (`src/CodexAcpServer.ts:3326-3343`). Concurrency control is by `Promise`-based state machines (per-session notification chains, generation counters, close fences), not locks; Node's single thread makes the critical sections the awaited segments, not mutex regions.

## LLM abstraction and integration

### Abstraction

The project does not call a model API. It is an adapter around a wrapped executable: `startCodexConnection` spawns `codex app-server` — the bundled `@openai/codex` binary, or `CODEX_PATH` — as a child process and speaks newline-delimited JSON-RPC over its stdio (`src/CodexJsonRpcConnection.ts:15-43`, `src/index.ts:97-103`). That JSON-RPC boundary is the only LLM-facing surface in this repository. There is no third-party LLM abstraction library, no provider SDK, and no bespoke model abstraction; `package.json:67-70` lists only `@agentclientprotocol/sdk`, `@openai/codex`, `diff`, `open`, `vscode-jsonrpc`, and `zod`, and a `src`-wide search for `fetch(`/`openai`/`anthropic`/AI-SDK imports finds no model call. The two internal modules named after the boundary are `CodexAppServerClient`, a typed JSON-RPC request/notification client (`src/CodexAppServerClient.ts:150-171`), and `CodexAcpClient`, ACP-shaped session operations over that client (`src/CodexAcpClient.ts:97-127`). The model loop, provider registry, context construction, and tool execution live outside this repository, inside the Codex app-server child; the adapter only sends `turn/start` and consumes the resulting notifications (`src/CodexAppServerClient.ts:291-315`).

### Integration path

A normal prompt crosses the model boundary as follows.

1. ACP `session/prompt` reaches `CodexAcpServer.prompt`, which resolves the session's current model from the in-memory `SessionState` (`src/CodexAcpServer.ts:2744-2756`, `src/CodexAcpServer.ts:2942`).
2. Message conversion: `sendPrompt` calls `buildPromptItems` to map ACP `ContentBlock[]` into Codex `UserInput[]` (text, image data URLs, resource/context blocks; audio is dropped) (`src/CodexAcpClient.ts:940`, `src/CodexAcpClient.ts:1152-1182`). Prior conversation is neither converted nor re-sent; it remains in the Codex thread.
3. Model and provider selection: the model id (base model plus reasoning effort) comes from the ACP `model`/`reasoning_effort` session config options (`src/CodexAcpServer.ts:1352-1357`, `src/ModelConfigOption.ts:36-69`), whose catalog is fetched from Codex through paginated `model/list` (`src/CodexAcpClient.ts:1112-1123`). The provider is a Codex `model_provider` name drawn from the launch `MODEL_PROVIDER`/`CODEX_CONFIG`, persisted Codex config, or an ACP override (`src/index.ts:79-85`, `src/CodexAcpClient.ts:313-320`, `src/CodexAcpClient.ts:789-796`) and passed to `thread/start`/`thread/resume` (`src/CodexAcpClient.ts:603-607`, `src/CodexAcpClient.ts:523-529`).
4. Request construction: `sendPrompt` assembles a Codex `TurnStartParams` carrying `input`, `model`, `effort`, `summary`, `serviceTier`, `approvalPolicy`, `approvalsReviewer`, and `sandboxPolicy` (`src/CodexAcpClient.ts:946-956`; shape at `src/app-server/v2/TurnStartParams.ts:14-55`).
5. Send: `CodexAppServerClient.runTurn` calls `turnStart`, which issues the JSON-RPC `turn/start` request and awaits the matching `turn/completed` (`src/CodexAppServerClient.ts:291-315`). This request is the delegated boundary — provider dispatch, HTTP, retries, and the token stream are inside Codex.
6. Streaming: Codex pushes `item/*`, `turn/*`, and `thread/*` notifications; `CodexAppServerClient`'s `onUnhandledNotification` handles them (turn completion, thread settings, goal/status captures) and routes them through per-thread handlers (`src/CodexAppServerClient.ts:173-215`). `CodexAcpClient.subscribeToSessionEvents` serializes each thread's events onto a promise chain (`src/CodexAcpClient.ts:873-900`), and `CodexEventHandler.handleNotification` converts each event into an ACP `session/update` (`src/CodexEventHandler.ts:392-428`). Assistant deltas (`item/agentMessage/delta`) become `agent_message_chunk`; reasoning deltas become `agent_thought_chunk`.
7. Tool-call handling: tools run inside Codex. The adapter translates Codex item events into ACP `tool_call`/`tool_call_update` through `CodexToolCallMapper` (for example `createDynamicToolCallUpdate` at `src/CodexToolCallMapper.ts:166`), and answers Codex approval/elicitation requests by converting them to ACP permission/elicitation requests (`src/CodexAppServerClient.ts:217-270`).
8. Conversion back to runtime events: each translated event is delivered as an ACP `session/update`; the prompt resolves with `end_turn`/`cancelled` after the notification queue drains (`src/CodexAcpServer.ts:2991-3024`).

The only adapter-initiated model call besides a user turn is title generation: `TitleGenerator` starts an ephemeral Codex thread and runs a `turn/start` with a fixed title model and JSON output schema, then writes the result back with `thread/set_name` (`src/TitleGenerator.ts:60-92`).

### Provider and tool boundary

Provider-specific code is not in this repository. The adapter models one configurable provider slot, `openai` (`src/CodexAcpClient.ts:70`), exposed through ACP `providers/list|set|disable` (`src/CodexAcpClient.ts:387-475`; server entry points `src/CodexAcpServer.ts:1009-1026`). Setting it writes a Codex `model_providers` config entry — `base_url`, `http_headers`, and `wire_api` — and selects provider id `custom-gateway` (`src/CodexAcpClient.ts:65-71`, `src/CodexAcpClient.ts:346-381`, `src/CodexAcpClient.ts:1342-1352`). Only the OpenAI Responses wire API is supported (`src/CodexAcpClient.ts:93-95`), so the abstraction is single-provider (OpenAI) with a configurable base URL and headers rather than multi-provider. A provider change restarts the Codex child and re-resumes sessions (`src/CodexAcpServer.ts:1013-1026`, `src/CodexAcpServer.ts:1028-1085`).

Credentials and configuration: API keys are read from the ACP `_meta["api-key"]` field or the `CODEX_API_KEY`/`OPENAI_API_KEY` environment variables and handed to Codex through `account/login` (`src/CodexAcpClient.ts:177-186`, `src/CodexAcpClient.ts:262-273`, `src/CodexAuthMethod.ts:5-6`); ChatGPT and gateway authentication also delegate to Codex (`src/CodexAcpClient.ts:188-260`). The adapter stores only the gateway routing it applied, not credential material.

Request and response normalization lives in two places: `buildPromptItems` normalizes ACP input to Codex `UserInput[]` (`src/CodexAcpClient.ts:1152-1182`), and `CodexEventHandler` plus `CodexToolCallMapper` normalize Codex events to ACP updates. The typed shapes are generated from Codex (`src/app-server/v2/*`; `generate-types` script in `package.json`), not authored here.

Tool schemas: the adapter defines none. Tools, their JSON schemas, and the execution loop are Codex-side; the generated `src/app-server/Tool.ts` (`name`, `inputSchema`, `outputSchema`) is only the shape Codex reports. The adapter's tool work is limited to configuring MCP servers into Codex config (`src/CodexAcpClient.ts:725-773`, `src/CodexAcpClient.ts:821-840`) and rendering tool-call events (`src/CodexToolCallMapper.ts`). **Not found:** any adapter-side handler for Codex's client-executed dynamic tool request (`item/tool/call`, present in the generated `ServerRequest` union at `src/app-server/ServerRequest.ts:19`). Checked `src/CodexAppServerClient.ts`, `src/CodexAcpServer.ts`, and `src/CodexEventHandler.ts`; `CodexAppServerClient` registers `onRequest` only for command, file-change, permissions, MCP elicitation, and user-input approvals (`src/CodexAppServerClient.ts:217-270`), so dynamic tool calls are surfaced as events but not serviced.

### Limits

- The actual provider endpoint, HTTP client, retry/streaming behavior, context-window management, and tool implementations are inside the Codex binary and are not visible from this checkout; only the JSON-RPC requests and notifications crossing `codex app-server`'s stdio are.
- The provider catalog is opaque: the adapter forwards a `model_provider` name and lists models via `model/list`, but the set of providers Codex supports and how it routes them is not defined here.
- Credential storage and refresh are Codex-owned; the adapter observes only login-completed and account-updated notifications.
- Tool schemas and the tool loop are Codex-owned; this repository contains only generated type shapes and event mappers.
- **Not found:** any direct model HTTP call, provider SDK, or LLM abstraction import — checked `package.json`, `src/index.ts`, `src/CodexJsonRpcConnection.ts`, `src/CodexAppServerClient.ts`, `src/CodexAcpClient.ts`, and a `src`-wide search for `fetch(`/`openai`/`anthropic`/AI-SDK imports.

## Session model

### Identity and ownership

"Session" means one Codex thread. `newSession` returns `threadStart().thread.id` as the ACP session ID (`src/CodexAcpClient.ts:603-622`); resume/load/fork use that ID as `threadId` (`src/CodexAcpClient.ts:528, 568`). The mapping is one-to-one, with two lossy side-systems: fork mints a new thread ID from the source thread (`src/SessionFork.ts:31-43`), and native subagents create child threads routed through the root session's notification pipeline (`src/subagents/CodexSubagentSubscriptions.ts:56-84`). The adapter's `SessionState` (`src/CodexAcpServer.ts:161-193`) holds only presentation/config state: model and reasoning effort, modes, usage, rate limits, title, typed session failure, per-session routers for subagents/background tasks/compactions. The conversation itself lives solely in Codex.

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | `thread/start` with merged config (MCP servers, trust, sandbox roots); models fetched; `SessionState` installed | Codex creates and persists a new thread | Errors mapped via `handleError` (logout/config hints); MCP-startup and command publications are async | `src/CodexAcpServer.ts:609-731`, `src/CodexAcpClient.ts:599-623` |
| Load/resume | `thread/resume` (`excludeTurns: true`), then history read (paginated cursor or full) for load only; `SessionState` rebuilt; generation guard discards stale opens | None by itself; Codex reattaches to the stored thread | Stale open after close is aborted and the thread unsubscribed (`cleanupStaleSessionOpen`) | `src/CodexAcpClient.ts:519-593`, `src/CodexAcpServer.ts:553-574, 1877-1988` |
| Prompt | Slash-command check, then `turn/start`; events streamed until `turn/completed` | Codex appends turn/items to the thread | Cancel/close interrupts; typed failures recorded on `SessionState` | `src/CodexAcpServer.ts:2744-3239` |
| Cancel | ACP notification → `turn/interrupt`; Codex replies `turn/completed (interrupted)` → prompt resolves `stopReason: "cancelled"` | Turn persisted as interrupted (Codex-side) | Interrupt RPC failure is logged, not thrown; close also marks the turn stale so late events are swallowed | `src/CodexAcpServer.ts:3355-3364, 2677-2716`, `src/CodexAppServerClient.ts:577-581, 939-958` |
| Close/delete | Close: interrupt turn, drain prompt, `thread/unsubscribe`, drop all per-session maps under a close fence. Delete: close if local, then `thread/archive` | Codex drops the subscription; archive removes the thread from lists | Generation check prevents a concurrent re-open from being deleted | `src/CodexAcpServer.ts:866-923`, `src/CodexAcpClient.ts:625-636` |

### Durable representation

All conversation durability is Codex-owned: threads are stored as rollout files exposed read-only to the adapter as `Thread.path` (`src/app-server/v2/Thread.ts:70`). The adapter never writes conversation records; its reads of the rollout file are a fallback used only when structured `Thread` history lacks legacy content (`src/ResponseItemHistoryFallback.ts:42-58`). Agent-side mutations of durable state are delegated calls: `thread/set_name` for AI-generated titles (via an ephemeral, non-persisted title thread, `src/TitleGenerator.ts:60-92`) and `thread/archive` for deletion (`src/CodexAcpClient.ts:634-636`). What is deliberately not persisted by the adapter: token usage, rate limits, mode/config selections, session failure records — all rebuilt per load or turn. Stored history is authoritative in Codex; the adapter treats it as a replay source. Flush/commit timing inside Codex is not observable here.

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | No shared gate; each prompt awaits its own `turn/completed`, matched by thread+turn ID; per-session queues prevent cross-talk | `src/CodexAppServerClient.ts:768-773, 840-859`, `src/__tests__/CodexACPAgent/CodexAcpClient.test.ts:1289` |
| Two prompts in the same session | `unknown` | `trackActivePrompt` overwrites `activePrompts[sessionId]` without rejecting the old entry; both prompts call `turn/start` on the same thread. Codex-side queueing/rejection is outside this repo (a `QueuedSubmission` type and `thread/queue/changed` notification exist; the adapter ignores the latter) | `src/CodexAcpServer.ts:2531-2583`, `src/CodexEventHandler.ts:648`, `src/app-server/v2/QueuedSubmission.ts` |
| Load/resume during an active prompt | Guarded | Close fence + generation counters: a load that starts while the session closes aborts, unsubscribes the thread, and throws; a close during load increments the generation so the load's install is discarded | `src/CodexAcpServer.ts:540-607, 655-663, 1919-1922` |
| Delete/close during an active prompt | Interrupt then drain | Close interrupts the turn (`resolveInterruptedTurn`), awaits the prompt's completion promise, then unsubscribes and deletes state under the fence | `src/CodexAcpServer.ts:866-901` |
| Cancellation isolation | Per session (per prompt object) | Each prompt gets its own `AbortController` and cancel/close signals; stale turns are marked so their notifications are swallowed at the app-server client boundary and their approval requests auto-cancel | `src/CodexAcpServer.ts:2531-2623`, `src/CodexAppServerClient.ts:218-270, 939-958` |

Shared coupling beyond sessions: the single Codex child process is a shared failure domain — its exit fails background tasks for all sessions (`src/CodexAcpServer.ts:1098-1106`) and every call surfaces `Codex process has exited` (`src/CodexAcpServer.ts:3335-3339`). Provider changes serialize globally: all active prompts across sessions must finish before the child restarts (`src/CodexAcpServer.ts:1035-1046`).

## Event and data flow

### New prompt: ACP client to live response

1. ACP `session/prompt` arrives; the adapter awaits any in-flight provider restart and resolves `SessionState` (`src/CodexAcpServer.ts:2749-2756`).
2. A per-prompt `ActivePrompt` is registered (overwriting any prior entry) and the client's abort signal is observed (`src/CodexAcpServer.ts:2768-2777`).
3. A prompt-scoped `CodexEventHandler` plus approval/elicitation handlers are created; `subscribeToSessionEvents` installs (replaces) the thread's notification routing into the serialized per-session queue (`src/CodexAcpServer.ts:2798-2848`, `src/CodexAcpClient.ts:873-900`).
4. Slash commands are checked first; handled commands short-circuit to a prompt response after draining notifications (`src/CodexAcpServer.ts:2854-2932`).
5. `sendPrompt` converts ACP content blocks to Codex `UserInput[]` (`src/CodexAcpClient.ts:1152-1182`) and calls `turn/start` via `runTurn`; the returned turn ID sets `sessionState.currentTurnId` and resolves any pending cancel awaiting turn identity (`src/CodexAppServerClient.ts:295-315`, `src/CodexAcpServer.ts:2964-2985`).
6. Live events flow: Codex notification → `onUnhandledNotification` (stale-turn filter, turn-completion resolution) → `notify()` by thread ID → enqueue → `CodexEventHandler.handleNotification` → `createUpdateEvent` → `ACPSessionConnection.update` → ACP `session/update` (`src/CodexAppServerClient.ts:173-215, 826-838`; `src/CodexEventHandler.ts:392-428, 513+`; `src/ACPSessionConnection.ts:18-23`).
7. The prompt awaits `Promise.race([sendPromptPromise, closeSignal, cancelBeforeTurnStarted])`; after `turn/completed` it drains the notification queue, flushes pending errors/plan updates, and finalizes subagents (`src/CodexAcpServer.ts:2991-3014`).
8. Response: `end_turn` with usage/quota, `cancelled` if interrupted, a typed-failure `end_turn` payload, or a thrown error; the `finally` block flips routing to session-scoped handling, disposes the handler, and completes the prompt (`src/CodexAcpServer.ts:3016-3024, 3161-3165, 3194-3238`).

### Durable history to ACP client

1. ACP `session/load` → `getOrCreateSessionWithHistory`: `thread/resume` (`excludeTurns: true`) followed by either paginated `thread/readHistory` from `turnsBackwardsCursor` or `threadReadWithHistory` (`src/CodexAcpClient.ts:559-580`); `session/resume` performs the same resume but never replays history (`src/CodexAcpServer.ts:808-825`).
2. `streamThreadHistory` publishes the title, then for each turn → each item emits `createHistoryUpdates` results in order, awaiting each `session/update` before the next (`src/CodexAcpServer.ts:1990-2023`).
3. For subagent-capable clients, `streamNativeThreadHistory` folds child-thread history into `subagent_spawned`/`subagent_state_update` sequences, reading each child via `threadReadWithHistory` (`src/CodexAcpServer.ts:2025-2130`).
4. For non-subagent clients, a fallback parses the Codex rollout file directly and merges its updates with structured items by de-duplication keys (`src/CodexAcpServer.ts:2004-2019, 3367-3422`; `src/ResponseItemHistoryFallback.ts:42-58`).
5. Not reconstructible as updates: `functionCallOutput`, `hookPrompt`, and `sleep` items are skipped (`src/CodexAcpServer.ts:2241-2244`), and `collabAgentToolCall` items are dropped in the native path (`src/CodexAcpServer.ts:2116`). Background terminals are re-attached from history (`src/CodexAcpServer.ts:2076-2084`).

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `UserInput[]` in `turn/start` | none (response only) | Codex thread item | Inside Codex, unobservable | `src/CodexAcpClient.ts:940-956` |
| Assistant text | `item/agentMessage/delta` | `agent_message_chunk` | Codex agentMessage item | Codex-side | `src/CodexEventHandler.ts:522-524, 707-710` |
| Reasoning/thought | `item/reasoning/*Delta` | `agent_thought_chunk` | Codex reasoning item | Codex-side | `src/CodexEventHandler.ts:771-795` |
| Tool call | `item/started` (commandExecution, mcpToolCall, fileChange, ...) | `tool_call` | Codex item | Codex-side | `src/CodexEventHandler.ts:801-850` |
| Tool result | `item/completed` + `outputDelta`/`progress` | `tool_call_update` | Codex item | Codex-side | `src/CodexEventHandler.ts:591-596, 852-923` |
| Completion/failure | `turn/completed`, `error` | prompt response (`end_turn`/`cancelled`/typed failure) | Turn status in Codex | After queue drain + flushes in the adapter | `src/CodexEventHandler.ts:553-563`, `src/CodexAcpServer.ts:3001-3033` |

The adapter persists none of these; durability is entirely Codex-side. Per-session notification ordering is enforced by the promise chain (`src/CodexAcpClient.ts:910-927`); the prompt response is returned only after the queue drains, so all streamed updates precede the response.

### Subsequent-prompt reconstruction

The adapter never rebuilds model context: each prompt sends only the new input plus per-turn overrides (model, effort, approval policy, sandbox) (`src/CodexAcpClient.ts:940-956`). Prior history reaches the model because the thread remains live inside Codex between turns; on a fresh adapter or after `thread/resume`, Codex reconstructs its own context from durable storage. **Inference:** the model's consumption of prior history is delegated and not visible in this repo; the evidence is that `turn/start` carries no history and resume uses `excludeTurns: true` with no adapter-side history re-upload.

### Ordering, cancellation, failure, and backpressure

Ordering: Codex notifications are processed strictly in arrival order per session (the serialized chain), and each update is awaited before the next is sent — a slow ACP client backpressures the queue and can delay `waitForSessionNotifications`, stalling that prompt's completion (other sessions keep running). The queue is unbounded (promise chain, no size limit). Cancellation: `session/cancel` → `turn/interrupt`; if the turn hasn't started, `getInterruptibleTurnId` waits on `pendingTurnStarts` (cancel) or resolves it null (close) (`src/CodexAcpServer.ts:2718-2742`). Partial streamed text is not rolled back by the adapter — it remains whatever the client rendered; Codex-side persistence of partial items is unobservable. Failure: a dead Codex process becomes a typed ACP error with stderr tail; typed session failures are recorded on `SessionState` and delivered as `session_info_update` `_meta` records (`src/CodexEventHandler.ts:302-326`). Retry: `error` with `willRetry` renders a warning-level failure rather than ending the turn (`src/CodexEventHandler.ts:313-316`). Late events after a prompt completes are routed to session-scoped handling; after close they are dropped (`src/CodexEventHandler.ts:302`, `src/CodexAppServerClient.ts:829-834`).

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | `sessions` map; snapshot test shows session-scoped routing | `src/CodexAcpServer.ts:276, 769-772`, `src/__tests__/CodexACPAgent/CodexAcpClient.test.ts:1274-1287` |
| Concurrent work across sessions | `yes` | Turn-completion matching by thread+turn ID; per-session queues only | `src/CodexAppServerClient.ts:161, 768-773`, `src/__tests__/CodexACPAgent/CodexAcpClient.test.ts:1289` |
| Same-session prompt exclusion | `no` | No adapter guard rejects/queues a second prompt; exclusion (if any) is Codex-internal | `src/CodexAcpServer.ts:2581`; contrast the steering path's drain-first guard at `src/CodexAcpServer.ts:1638-1648` |
| Durable sessions | `yes` | Codex rollout files + `thread/list`, `thread/resume` | `src/app-server/v2/Thread.ts:70`, `src/CodexAcpClient.ts:1042-1065` |
| Session list | `yes` | `session/list` → `thread/list` with cwd/provider filtering | `src/CodexAcpServer.ts:847-864`, `src/CodexAcpClient.ts:1042-1072` |
| Session load/resume | `yes` | `thread/resume` by ID; load also returns history | `src/CodexAcpClient.ts:519-593` |
| History replay to ACP client | `partial` | Full on `session/load` (including subagent and rollout fallback); none on `session/resume` or `fork` | `src/CodexAcpServer.ts:711, 781-806, 808-825` |
| Prior history reused by model | `yes` | Delegated: adapter sends only new input; Codex holds the thread (Inference — Codex internals unobservable) | `src/CodexAcpClient.ts:940-956, 523-529` |
| Prompt cancellation | `yes` | `turn/interrupt` plus client-abort observation and stale-turn suppression | `src/CodexAcpServer.ts:3355-3364, 2594-2623` |
| Tool-call progress updates | `yes` | `tool_call`/`tool_call_update`, command output deltas, MCP progress, terminal interaction | `src/CodexEventHandler.ts:591-596, 801-923` |
| Partial-output persistence | `unknown` | Adapter persists nothing; Codex-side persistence of partial deltas is unobservable | `src/Logger.ts:45` (only adapter write) |
| Recovery after process restart | `yes` | Sessions survive adapter restart via Codex storage; the e2e restart test exists but is `it.skip` at this revision | `src/CodexAcpClient.ts:519-529`, `src/__tests__/CodexACPAgent/e2e/acp-e2e-session-persistence.test.ts:22-41` |

## Design assessment

### Strengths

- Clean adapter boundary: the ACP surface maps onto a small set of Codex verbs (`turn/start`, `turn/interrupt`, `thread/*`), so agent behavior upgrades with the Codex dependency without adapter changes (`src/CodexAppServerClient.ts:287-809`).
- Thorough race discipline at the adapter edge: generation counters, close fences, stale-turn marking, and "flip routing before disposal" prevent late notifications, stale approvals, and zombie turns from corrupting live prompts (`src/CodexAcpServer.ts:540-607`, `src/CodexAppServerClient.ts:939-958`, `src/CodexAcpServer.ts:3194-3197`).
- Per-session serialized event queues give deterministic client-side ordering with no cross-session head-of-line blocking (`src/CodexAcpClient.ts:910-927`).
- Durability ownership is unambiguous: one component (Codex) owns persistence, so resume reduces to one RPC (`thread/resume`) rather than adapter-side replay bookkeeping.

### Tradeoffs and limitations

- With no conversation state of its own, the adapter reconstructs `SessionState` heuristically on load (title from first user message or thread preview; `sessionTitleSource: "unknown"` blocks AI retitling) (`src/CodexAcpServer.ts:2149-2151, 693`).
- Same-session prompt behavior is delegated opacity: the adapter cannot answer whether a second prompt queues, rejects, or corrupts ordering, and it silently drops thread notifications when no prompt has ever subscribed (`src/CodexAppServerClient.ts:829-834`) — events between session creation and the first prompt are lost to the client.
- The shared Codex child is a single failure and restart domain: one crash surfaces in every session, and a provider restart pauses all sessions and can partially fail re-resume (`src/CodexAcpServer.ts:1055-1075, 3335-3339`).
- Load-time history fidelity depends on two representations (structured items plus rollout-file fallback) reconciled by string de-duplication keys — inherently best-effort (`src/CodexAcpServer.ts:3367-3422`).

### Ideas relevant to Ox

- The generation-counter + close-fence pattern is a lightweight alternative to a per-session state machine for load/close/prompt races; its cost here is several parallel maps (`sessionGenerations`, `sessionOpenGenerations`, `closingSessions`) that must stay consistent — a single per-session state object would be easier to reason about (judgment, based on `src/CodexAcpServer.ts:276-287`).
- Draining an ordered event queue before returning the prompt response is a simple, strong client contract (all updates precede the response); Ox could adopt this invariant without storage machinery (fact: `src/CodexAcpClient.ts:902-908`; judgment on adoption).
- The stale-turn registry (mark a turn, swallow its events, auto-cancel its approvals) cleanly isolates cancellation from a streaming event stream (`src/CodexAppServerClient.ts:939-958`).
- Delegating durability wholesale to the wrapped agent minimizes adapter code but makes partial-output persistence and restart fidelity unverifiable from the adapter repo — a tradeoff worth weighing explicitly in a similar boundary.

## Unknowns and conflicts

- Same-session concurrent prompts: targeted searches found no adapter-side guard (`grep` for `activePrompts` guards, prompt-entry checks in `prompt()`); Codex's `turn/start` behavior on a busy thread is outside this repository. The generated `QueuedSubmission`/`thread/queue/changed` types suggest queueing exists in Codex, but the adapter ignores that notification (`src/CodexEventHandler.ts:648`).
- Codex-side durability timing (when turns/items are flushed to rollout files) is unobservable here; `Thread.path` is documented as "path to the thread on disk" (`src/app-server/v2/Thread.ts:70`) with no write path in this repo.
- The e2e test proving recovery across adapter restart is `it.skip` (`src/__tests__/CodexACPAgent/e2e/acp-e2e-session-persistence.test.ts:22`), so restart recovery is supported by mechanism evidence (resume by ID) but not by a passing test.
- Notifications before the first prompt of a session have no registered thread handler and are silently dropped (`src/CodexAppServerClient.ts:826-838`); whether Codex emits anything client-visible in that window (e.g., thread status changes) is unknown.
- Docs conflict signal: `AGENTS.md` instructs contributors to prefer `thread/*`/`turn/*`/`item/*` events over the deprecated `codex/event/*` API; the code consistently uses the new surfaces, so no code/doc disagreement was found — but the deprecated API's behavior was not verified.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `src/index.ts:139-172` (`startAcpServer`), `src/CodexAcpServer.ts:335` (`initialize`), `src/CodexJsonRpcConnection.ts:15` (`startCodexConnection`) | Method wiring, capability negotiation, child-process spawn |
| Session management | `src/CodexAcpServer.ts:609` (`tryCreateSession`), `src/CodexAcpServer.ts:1877` (`getOrCreateSessionWithHistory`), `src/CodexAcpClient.ts:519, 559, 599, 625, 634` | Create/load/resume/fork/close/delete mapped to thread verbs |
| Concurrency | `src/CodexAcpServer.ts:2531` (`trackActivePrompt`), `src/CodexAcpServer.ts:540-607` (generations/fences), `src/CodexAcpClient.ts:910-927` (`enqueueSessionNotification`), `src/CodexAcpServer.ts:1490` (`executeOrQueueSteeringRequest`) | Prompt tracking, race guards, per-session serialization, steering queue |
| Persistence | `src/app-server/v2/Thread.ts:70` (`path`), `src/ResponseItemHistoryFallback.ts:42-58`, `src/Logger.ts:45`, `src/TitleGenerator.ts:60-92` | Durability ownership: Codex files, adapter reads fallback, only log writes |
| Event translation | `src/CodexEventHandler.ts:392-428` (`handleNotification`), `src/CodexEventHandler.ts:513+` (`createUpdateEvent`), `src/CodexToolCallMapper.ts`, `src/ACPSessionConnection.ts:18` (`update`) | Codex events → ACP session updates |
| Replay/reconstruction | `src/CodexAcpServer.ts:1990-2130` (`streamThreadHistory`, `streamNativeThreadHistory`), `src/CodexAcpServer.ts:3367` (`mergeHistoryUpdates`), `src/CodexAcpClient.ts:559-580` | Load-time replay pipeline and dedup fallback |
| LLM integration | `src/CodexJsonRpcConnection.ts:15-43` (spawn `codex app-server`), `src/CodexAppServerClient.ts:291-315` (`runTurn`/`turn/start`), `src/CodexAcpClient.ts:929-957` (`sendPrompt`), `src/CodexAcpClient.ts:1152-1182` (`buildPromptItems`), `src/CodexAcpClient.ts:387-475` (`providers/*`), `src/TitleGenerator.ts:60-92` | Wrapped-executable boundary: model/provider selection, request construction, credentials, and provider config all delegate to Codex |
| Cancellation/errors | `src/CodexAcpServer.ts:3355` (`cancel`), `src/CodexAcpServer.ts:2677-2742` (`interruptSessionTurn`, `getInterruptibleTurnId`), `src/CodexAppServerClient.ts:939-958` (`handleStaleTurnNotification`), `src/CodexAcpServer.ts:3326-3343` (`runWithProcessCheck`) | Cancel paths, stale-turn suppression, process-exit conversion |

## Research notes

- **Revision inspected:** `d7b07c1b44a28890cdf3d5450f8974a812db5ae2`
- **Primary evidence:** `src/index.ts`, `CodexAcpServer.ts`, `CodexAcpClient.ts`, `CodexAppServerClient.ts`, `CodexEventHandler.ts`, `ACPSessionConnection.ts`, `CodexJsonRpcConnection.ts`, `SteeringQueue.ts`, `ResponseItemHistoryFallback.ts`, `TitleGenerator.ts`, `SessionFork.ts`, `subagents/CodexSubagentSubscriptions.ts`, generated `app-server/v2` types; tests under `src/__tests__/CodexACPAgent/` (unit + e2e).
- **Relevant docs:** `README.md`, `AGENTS.md`, `docs/subagent-sessions.md` (referenced), upstream Codex app-server README link in `AGENTS.md` (not fetched; behavior cited from code only).
- **Commands/tests run:** Read-only inspection (grep/reads) against the pinned checkout; no tests executed.
- **Report confidence:** `high` — the adapter's control flow, session ownership, and event plumbing are directly traceable; residual uncertainty is confined to Codex-internal behavior explicitly marked as delegated or unobservable.
