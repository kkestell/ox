---
project: "anomalyco/opencode"
repository: "https://github.com/anomalyco/opencode"
revision: "fee476bb90043a1012abda156dd9af9e5c71b19d"
researched_at: "2026-09-19"
primary_language: "TypeScript"
implementation_form: "adapter"
process_model: "single-process"
session_owner: "agent-runtime"
durability: "embedded-database"
cross_session_concurrency: "concurrent"
same_session_concurrency: "serialized"
event_delivery: "channel"
resume_strategy: "reconstruct"
overall_confidence: "high"
---

# opencode ACP architecture

## Executive summary

- **ACP boundary:** The `opencode acp` CLI command runs an `AgentSideConnection` from `@agentclientprotocol/sdk` over stdin/stdout ndjson (`packages/opencode/src/cli/cmd/acp.ts:55-61`). It is a translation adapter: every ACP request is converted into a call on the opencode SDK, which talks HTTP to an opencode server that the same process started a moment earlier (`packages/opencode/src/cli/cmd/acp.ts:25-30`).
- **Session model:** An ACP session ID is exactly the opencode server's session ID. The ACP layer keeps only a thin in-memory per-session record (model/variant/mode choice, known-part metadata) (`packages/opencode/src/acp/session.ts:24-33`); the conversation, its runtime state, and all durable state are owned by the opencode server layer in the same process.
- **Concurrency:** Different sessions run concurrently — each session gets its own `Runner` fiber gate (`packages/opencode/src/session/run-state.ts:52-69`). Within one session, model execution is serialized: a prompt that arrives mid-run persists its user message and then joins the active run (steering) rather than starting a competing loop (`packages/opencode/src/effect/runner.ts:115-138`).
- **Durability and replay:** Everything durable lives in one SQLite file under the XDG data directory (`packages/core/src/database/database.ts:43-57`). Writes go through a durable event log with per-session monotonic sequences; read models (`session`, `message`, `part` tables) are projected transactionally (`packages/core/src/event.ts:237-324`, `packages/core/src/session/projector.ts:260-328`). `loadSession` replays stored messages to the client; `resumeSession` restores config state without replay (`packages/opencode/src/acp/service.ts:238`, `295-334`).
- **Event flow:** Live updates flow from in-process event bus → global SSE endpoint → ACP subscription loop → `sessionUpdate` notifications, handled strictly in order (`packages/opencode/src/event-v2-bridge.ts:35-44`, `packages/opencode/src/server/routes/instance/httpapi/handlers/global.ts:25-58`, `packages/opencode/src/acp/event.ts:152-165`). The prompt response is held until the subscription observes the session go idle, so streamed updates precede the response (`packages/opencode/src/acp/event.ts:74-91`).
- **Notable uncertainty:** Two session runtimes coexist in the server layer graph (a legacy V1 path and a newer V2 core in `packages/core/src/session`); the routes ACP exercises use the V1 `SessionPrompt` runtime at this revision, but the V2 wiring was not traced exhaustively.

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `adapter` | ACP handlers translate protocol requests into opencode SDK (HTTP) calls and translate server events into ACP notifications; the agent runtime is the opencode server, reached over loopback HTTP even though it shares the process | `packages/opencode/src/acp/agent.ts:24-30`, `packages/opencode/src/cli/cmd/acp.ts:25-30` |
| Process model | `single-process` | One process hosts the ACP stdio server, the opencode HTTP server, and the SQLite database connection; no per-session subprocess | `packages/opencode/src/cli/cmd/acp.ts:19-64` |
| Session owner | `agent-runtime` | Conversation and durable state are owned by opencode server services (`Session`, `SessionPrompt`, `SessionRunState`); the ACP layer owns only in-memory config state keyed by the same session ID | `packages/opencode/src/acp/session.ts:95-100`, `packages/opencode/src/session/session.ts:486-494` |
| Durability | `embedded-database` | Single SQLite file (`opencode.db`) via Drizzle; durable event log plus projected read-model tables | `packages/core/src/database/database.ts:43-57`, `packages/core/src/event.ts:239-242` |
| Cross-session concurrency | `concurrent` | Independent per-session runners forked into a shared scope; no global lock across sessions | `packages/opencode/src/session/run-state.ts:35-69` |
| Same-session concurrency | `serialized` | `Runner.ensureRunning` joins an active run to the caller instead of starting a second loop; model turns execute one at a time per session | `packages/opencode/src/effect/runner.ts:115-138` |
| Event delivery | `channel` | Server publishes to an in-process bus; consumers receive an SSE stream (`/global/event`) that the ACP subscription reads sequentially and converts to `sessionUpdate` calls | `packages/opencode/src/server/routes/instance/httpapi/handlers/global.ts:25-58`, `packages/opencode/src/acp/event.ts:152-165` |
| Resume strategy | `reconstruct` | Load/resume re-fetch sessions and messages over the SDK and rebuild ACP-side state; history is replayed on load/fork only | `packages/opencode/src/acp/service.ts:211-247`, `295-334` |

## System architecture

```text
+--------------------------------------------------------------- one OS process (bun) ----------------+
|                                                                                                     |
|  ACP client (editor)                                                                opencode server |
|      |  JSON-RPC ndjson over stdio                                           Effect HTTP server     |
|      v  (requests)                                    SDK (HTTP)              |                     |
|  +------------------------+   AgentSideConnection   +------------------+  request/response        |
|  | ACP adapter            |------------------------>| loopback HTTP    |----+                     |
|  | src/acp/*              |     sessionUpdate ^     |  (port 4096/0)   |    v                     |
|  |  agent.ts  service.ts  |                         +------------------+   SessionPrompt (V1)     |
|  |  event.ts  session.ts  |                                ^                SessionRunState          |
|  +------------------------+                                |                per-session Runner       |
|        | in-memory: ACPSession.Ref map,                     |                        |                |
|        | snapshots, MCP registry                            | SSE                    v                |
|        +-- knownParts metadata cache            +-----------+----------+       LLM stream + tools      |
|                                                 | /global/event (SSE)   |             |                |
|                                                 | GlobalBus (in-proc)   |<--- EventV2Bridge (publish)  |
|                                                 +-----------------------+             |                |
|                                                                                  EventV2 (durable)       |
|                                                                                       |                  |
|                                              +----------------------------------------+----------+       |
|                                              | SQLite: opencode.db (XDG data dir)             |       |
|                                              |  event log (per-session seq) + projectors      |       |
|                                              |  session / message / part / session_message    |       |
|                                              +------------------------------------------------+       |
+-----------------------------------------------------------------------------------------------------+
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| CLI `acp` command | Starts the HTTP server, wires stdin/stdout to `AgentSideConnection` | process | none | `packages/opencode/src/cli/cmd/acp.ts:19-71` |
| ACP adapter (`src/acp/*`) | Protocol handlers, ACP↔opencode translation, replay, permission bridge | process | in-memory session records, directory snapshots, MCP registry, part-metadata cache | `packages/opencode/src/acp/service.ts:75-92`, `packages/opencode/src/acp/session.ts:95-100` |
| ACP event subscription | SSE consumer; converts bus events to `sessionUpdate`; idle tracking | process | idle waiters, tool-start dedupe, shell snapshots | `packages/opencode/src/acp/event.ts:39-91` |
| opencode HTTP server | Hosts session/prompt/abort/messages/event APIs | process | HTTP routes, websocket tracker | `packages/opencode/src/server/server.ts:73-115` |
| `SessionPrompt` (V1 runtime) | User-message admission, model loop, tool orchestration | per run | loop state, assistant message under construction | `packages/opencode/src/session/prompt.ts:1052-1071`, `1081-1341` |
| `SessionRunState`/`Runner` | Per-session execution gate: run/shell/cancel | per instance (process) | `Map<SessionID, Runner>` | `packages/opencode/src/session/run-state.ts:35-69`, `packages/opencode/src/effect/runner.ts:39-52` |
| `SessionProcessor` | Consumes LLM events; persists parts; cleanup on abort | per assistant message | text/reasoning/tool part accumulators | `packages/opencode/src/session/processor.ts:98`, `553-611` |
| EventV2 + projectors | Durable event log with per-aggregate seq; projects to SQLite read models | process + disk | `EventTable`, `EventSequenceTable`, `SessionTable`, `MessageTable`, `PartTable`, V2 tables | `packages/core/src/event.ts:205-330`, `packages/core/src/session/projector.ts:210-452` |

### ACP surface

Transport is ndjson over stdio via `ndJsonStream` and `AgentSideConnection` (`packages/opencode/src/cli/cmd/acp.ts:55-61`). The `Agent` class implements the ACP agent interface by running Effect programs from `ACPService.make` and mapping typed errors to ACP `RequestError`s (`packages/opencode/src/acp/agent.ts:32-93`). `initialize` advertises `loadSession`, MCP http/sse capabilities, embedded context and image prompt capabilities, and session capabilities `close`, `fork`, `list`, `resume` (`packages/opencode/src/acp/service.ts:112-136`), plus one auth method (`opencode-login`, `packages/opencode/src/acp/service.ts:96-110`). Handlers exist for `newSession`, `loadSession`, `listSessions`, `resumeSession`, `closeSession`, `forkSession`, `setSessionConfigOption`, `setSessionMode`, `setSessionModel`, `prompt`, and `cancel` (`packages/opencode/src/acp/service.ts:497-592`). The ACP layer is a native part of the opencode binary but an adapter by structure: it holds no conversation state and delegates all agent work through the SDK.

### Runtime and process boundaries

There is one process and one SQLite connection pool. The "boundary" between ACP adapter and agent runtime is a loopback HTTP hop plus an SSE hop, even though both live in-process. Concurrency units are Effect fibers: each session run is forked into a shared scope by its `Runner` (`packages/opencode/src/effect/runner.ts:83-91`), and the ACP subscription runs its own loop fiber (`packages/opencode/src/acp/event.ts:144-150`). Tools may spawn child processes (e.g. shell) but never own session state. Cleanup paths: `closeSession` removes ACP-side state and aborts the backing session best-effort (`packages/opencode/src/acp/service.ts:347-355`); ACP `cancel` only aborts the backing session and keeps the ACP session registered (corroborated by `packages/opencode/test/acp/service-session.test.ts:708-724`).

## LLM abstraction and integration

### Abstraction

The default model path uses the **Vercel AI SDK** (`ai`, catalog-pinned to `6.0.168`, `package.json:67`) as a third-party abstraction: `src/session/llm.ts` imports `streamText`/`wrapLanguageModel` from `"ai"` and calls `streamText(...)` for each turn (`packages/opencode/src/session/llm.ts:9`, `280-353`). Provider SDKs are consumed through AI SDK provider packages (`@ai-sdk/openai`, `@ai-sdk/anthropic`, `@ai-sdk/google`, `@ai-sdk/openai-compatible`, …), loaded by the provider registry and returned as `LanguageModelV3` values (`packages/opencode/src/provider/provider.ts:113-140`, `1896-1925`).

The repository also ships a bespoke internal abstraction, the workspace package **`@opencode-ai/llm`** (`packages/llm/`): an Effect-Schema-first core with its own request model, protocols, provider facades, routes, and HTTP transport (`packages/llm/src/route/client.ts:344-426`, `packages/llm/src/providers/index.ts:1-11`). It is not the default. It is reached only through the opt-in native runtime adapter, gated by `RuntimeFlags.experimentalNativeLlm` (`OPENCODE_EXPERIMENTAL_NATIVE_LLM`) (`packages/opencode/src/effect/runtime-flags.ts:54`, `packages/opencode/src/session/llm.ts:226-269`).

The model loop is inside this repository: `SessionPrompt.runLoop` drives the per-turn iteration and `SessionProcessor.process` consumes the model stream (`packages/opencode/src/session/prompt.ts:1081-1341`, `packages/opencode/src/session/processor.ts:641-697`). No executable or external service owns the loop.

### Integration path

1. **History conversion.** Each turn re-reads durable history (`MessageV2.filterCompactedEffect`, `packages/opencode/src/session/prompt.ts:1092`) and converts stored parts to AI SDK messages with `MessageV2.toModelMessagesEffect` (`packages/opencode/src/session/prompt.ts:1262`; `packages/opencode/src/session/message-v2.ts:131-415`). Provider-specific message rewrites are applied later by the wrapped-model middleware (`ProviderTransform.message`, `packages/opencode/src/session/llm.ts:325-343`; `packages/opencode/src/provider/transform.ts:465`).
2. **Model and provider selection.** The turn's model is the persisted user-message choice, resolved with `Provider.getModel(providerID, modelID)` (`packages/opencode/src/session/prompt.ts:1141`). `LLM.run` concurrently loads the language model, config, provider info, and auth (`packages/opencode/src/session/llm.ts:95-103`); `Provider.getLanguage` selects the provider package from `model.api.npm` and returns an AI SDK `LanguageModelV3` (`packages/opencode/src/provider/provider.ts:1896-1925`).
3. **Request construction.** `LLMRequestPrep.prepare` builds the system prompt, generation params, headers, tool record, and merged provider options (`packages/opencode/src/session/llm/request.ts:56-206`). The default path passes these to `streamText(...)` with the tools and a `wrapLanguageModel` middleware (`packages/opencode/src/session/llm.ts:280-353`). The native path lowers the same input to a canonical `LLMRequest` via `LLMNative.request` (`packages/opencode/src/session/llm/native-request.ts:181-194`) and calls `LLMClient.stream(request)` (`packages/opencode/src/session/llm/native-runtime.ts:103-113`).
4. **Streaming.** Default: `result.fullStream` is adapted into `@opencode-ai/llm` `LLMEvent`s by `LLMAISDK.toLLMEvents` (`packages/opencode/src/session/llm.ts:372-378`; `packages/opencode/src/session/llm/ai-sdk.ts:77-289`). Native: `LLMClient.stream` compiles the request through the route's protocol/transport and yields `LLMEvent`s directly (`packages/llm/src/route/client.ts:344-380`, `417-426`).
5. **Tool-call handling.** In the default path the AI SDK owns tool dispatch: `streamText` receives `tools`/`activeTools` whose `execute` handlers are opencode tools (`packages/opencode/src/session/llm.ts:317-318`; built in `packages/opencode/src/session/tools.ts:92-101`), and emits `tool-call`/`tool-result` parts that `ai-sdk.ts` forwards as `LLMEvent`s. In the native path `native-runtime.ts` adapts opencode tools to `NativeTool` and dispatches non-provider `tool-call` events through `ToolRuntime.dispatch` (`packages/opencode/src/session/llm/native-runtime.ts:115-137`, `169-193`; `packages/llm/src/tool-runtime.ts:23-35`). The multi-turn loop is opencode's `runLoop`: after a turn it re-reads history and calls `handle.process` again (`packages/opencode/src/session/prompt.ts:1213-1286`, `1334-1335`).
6. **Back to runtime events.** `SessionProcessor.handleEvent` converts each `LLMEvent` into persisted parts and part deltas (text, reasoning, tool transitions, step finish) (`packages/opencode/src/session/processor.ts:278-551`), which then flow through EventV2 and SSE to the ACP layer (see Event and data flow).

### Provider and tool boundary

Provider-specific code lives in two layers. The default path keeps it in `src/provider/`: `BUNDLED_PROVIDERS` maps `model.api.npm` to `@ai-sdk/*` factory imports (`packages/opencode/src/provider/provider.ts:113-140`), a `custom()` table holds per-provider SDK/model-selection quirks (Anthropic beta headers, OpenAI Responses, Copilot endpoint choice, …) (`packages/opencode/src/provider/provider.ts:174-245`), and `src/provider/transform.ts` holds provider-specific request/response normalization (`ProviderTransform.message`, `ProviderTransform.providerOptions`, `ProviderTransform.schema`) (`packages/opencode/src/provider/transform.ts:465`, `1562`). The native path keeps provider-specific code in `packages/llm/src/protocols/*` and `packages/llm/src/providers/*`; `native-request.ts` selects a facade from `model.api.npm` (`packages/opencode/src/session/llm/native-request.ts:165-178`).

Credentials and configuration are opencode-owned: `LLM.run` reads provider info and `auth.get(providerID)` and passes them to `LLMRequestPrep.prepare` (`packages/opencode/src/session/llm.ts:95-113`); the `Auth` service holds OAuth/API/well-known credentials (`packages/opencode/src/auth/index.ts:14-36`). The native adapter reads the key from `provider.options.apiKey ?? provider.key` (`packages/opencode/src/session/llm/native-runtime.ts:64`).

Tool schemas live with the opencode tool registry: `SessionTools.resolve` builds AI SDK tools from `ToolRegistry` with `ToolJsonSchema.fromTool` normalized by `ProviderTransform.schema` (`packages/opencode/src/session/tools.ts:92-101`). The native adapter re-derives JSON Schema from the AI SDK tool via `asSchema(...).jsonSchema` or a passthrough `jsonSchema` field (`packages/opencode/src/session/llm/native-runtime.ts:162-167`).

The abstraction is multi-provider. The catalog is loaded from models.dev (`ModelsDev.Service`, `packages/opencode/src/provider/provider.ts:1397-1406`) and each model carries `api.npm`, which selects both the AI SDK provider package and the native facade; there is no runtime provider registry keyed independently of the model. The native gate additionally restricts support to `openai`, `anthropic`, and `opencode*` providers on three npm packages (`packages/opencode/src/session/llm/native-runtime.ts:54-59`).

### Limits

- **AI SDK internal stepping.** Whether `streamText` performs one model step or an internal multi-step tool loop at this revision cannot be read from the checkout: no `stopWhen`/`maxSteps` is set in `packages/opencode/src` or `packages/llm/src`, and `node_modules` is absent, so the resolved AI SDK implementation is unavailable. opencode's `runLoop` is the authoritative multi-turn driver. Checked: `packages/opencode/src/session/llm.ts`, `packages/opencode/src/session/prompt.ts`, `packages/llm/src/`. **Not found.**
- **Credential acquisition internals.** OAuth refresh, key persistence, and provider-key precedence inside `Auth` were not traced; only the `Auth.Info` union and the call sites above are established.
- **Native route coverage.** The `@opencode-ai/llm` provider facades are broader than the native gate; reachability is bounded by the npm check in `native-runtime.ts:57-59`, but the full behavior of each `packages/llm` protocol was not exercised.

## Session model

### Identity and ownership

"Session" is one opencode conversation, identified by a `ses_*` ID minted by the server on `session.create` (`packages/opencode/src/session/session.ts:499-538`). The ACP layer adopts that ID verbatim: `newSession` returns `state.id` from the server response (`packages/opencode/src/acp/service.ts:186-206`). Mapping is one-to-one: ACP session ID = runtime session ID = durable `session.id` primary key. The ACP layer's own `ACPSession.Info` is a cache of UI-facing config (selected model, variant, mode, cwd, registered MCP servers, `knownParts` part metadata) (`packages/opencode/src/acp/session.ts:24-33`); losing it is recoverable via load/resume because the durable session row stores the last agent/model choice (`packages/opencode/src/session/prompt.ts:672-689` writes it; `packages/opencode/src/acp/service.ts:1089-1102` restores it). Subagent task sessions exist in the runtime (parent/child via `parentID`, `packages/opencode/src/session/session.ts:596-604`) but are not exposed as separate ACP sessions.

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | `newSession` builds a directory snapshot (providers, agents, commands), asks the server to create a session with default model/agent, stores ACP-side state, registers client-supplied MCP servers, and asynchronously sends `available_commands_update` | `SessionV1.Event.Created` → `session` row in SQLite | MCP registration errors are swallowed per server; snapshot cached per session | `packages/opencode/src/acp/service.ts:163-209`, `1010-1059`; projector `packages/core/src/session/projector.ts:214-233` |
| Load/resume | `loadSession` fetches session + all messages, restores model/variant/mode (durable row first, then newest user/assistant message), replays messages to the client. `resumeSession` does the restore with the last 20 messages but **no replay** | none beyond reads; ACP-side state rebuilt in memory | Missing session fails with `SessionNotFoundError` → ACP `invalidParams` | `packages/opencode/src/acp/service.ts:211-247`, `295-334`, `1089-1180`; test `packages/opencode/test/acp/service-session.test.ts:652` |
| Prompt | Converts ACP content blocks to opencode parts, detects slash commands, calls `session.prompt`/`session.command`/`session.summarize` inside `runUntilIdle`, then maps the resulting assistant message to a `stopReason` | User message + parts persisted before the model loop; assistant message/parts persisted during the loop | Server errors map to `end_turn`/`cancelled`/`max_tokens`/`refusal` or ACP errors (`authRequired`, `internalError`) based on the assistant message's error | `packages/opencode/src/acp/service.ts:509-590`, `839-888` |
| Cancel | ACP `cancel` → HTTP abort → `SessionRunState.cancel`: cancels background jobs, interrupts the session's run fiber | Interrupted assistant message finalized with `AbortedError`; partial text persisted; running tool parts marked `error`/`interrupted` | Cancel of a non-existent ACP session errors; backing-abort failure is logged, not raised | `packages/opencode/src/acp/service.ts:357-360`; `packages/opencode/src/session/run-state.ts:77-86`; `packages/opencode/src/session/processor.ts:553-611`; `packages/opencode/src/session/prompt.ts:1203-1219` |
| Close/delete | `closeSession` removes ACP in-memory state and aborts the backing run; it does **not** delete the durable session | none (durable history retained) | Best-effort: abort failure is logged and ignored | `packages/opencode/src/acp/service.ts:347-355` |

### Durable representation

Storage is a single SQLite file at `<XDG data>/opencode/opencode.db` (or channel-suffixed) (`packages/core/src/database/database.ts:43-57`, `packages/core/src/global.ts:11-14`). The write path is event-sourced: `Session.updateMessage`/`updatePart`/`updatePartDelta` publish typed events (`packages/opencode/src/session/session.ts:629-643`, `877-885`); durable events are committed inside a SQLite transaction that assigns the next per-session sequence from `EventSequenceTable` and runs all projectors before the transaction commits (`packages/core/src/event.ts:237-324`). Projectors upsert `SessionTable`, `MessageTable` (message.updated), and `PartTable` (message.part.updated), maintaining session cost/token roll-ups from `step-finish` parts (`packages/core/src/session/projector.ts:260-328`). `session.created`/`message.updated`/`message.part.updated` carry `durable: { aggregate: sessionID, version: 1 }`; `message.part.delta` does not (`packages/schema/src/v1/session.ts:502-507`, `596-641`), so streaming deltas are broadcast-only and never persisted as deltas. The read path used by ACP (`sdk.session.messages`) pages `MessageTable` ordered by `time_created, id` and hydrates parts from `PartTable` (`packages/opencode/src/session/message-v2.ts:425-441`, `98-107`; `packages/opencode/src/session/session.ts:828-851`). The stored history is authoritative: the model loop re-reads it every turn via `MessageV2.filterCompactedEffect` (`packages/opencode/src/session/prompt.ts:1092`). A second generation of tables (`session_message`, `session_input`) is projected from newer durable events (`packages/core/src/session/projector.ts:348-374`), but the ACP-visible read path uses the first-generation tables.

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | Each session has its own `Runner` in a per-instance map; runs are forked fibers; the HTTP server handles requests concurrently; SQLite writes serialize at the database | `packages/opencode/src/session/run-state.ts:52-69`, `packages/opencode/src/effect/runner.ts:83-91` |
| Two prompts in the same session | `serialized` | `Runner.ensureRunning` under a `SynchronizedRef`: if a run is active, the caller persists its user message first (`SessionPrompt.prompt`) and then awaits the *existing* run's completion; the active loop re-reads durable messages each iteration, so the joined prompt is processed at the next tool-call boundary (steering) | `packages/opencode/src/effect/runner.ts:115-138`; `packages/opencode/src/session/prompt.ts:1052-1071`, `1092`, `1106-1130`, `1343-1347` |
| Load/resume during an active prompt | unknown (no guard) | Load/resume only write the ACP-side state map and replay events; no busy check exists on that path | `packages/opencode/src/acp/service.ts:211-247`, `295-334` |
| Delete/close during an active prompt | Best-effort abort | `closeSession` removes ACP state and calls abort, which interrupts the run fiber; durable rows are not deleted | `packages/opencode/src/acp/service.ts:347-355` |
| Cancellation isolation | per session | `SessionRunState.cancel` targets one session's runner (and its background subagent jobs); other sessions unaffected | `packages/opencode/src/session/run-state.ts:77-86`, `111-143` |

Shared coupling: all sessions share one SQLite file and one `GlobalBus`/SSE fan-out, and the ACP layer's `registeredMcp`/`sessionSnapshots` maps are keyed by session. The critical section for same-session serialization is the `SynchronizedRef` inside `Runner` — it is released as soon as the state transition is decided; the prompt caller then waits on a `Deferred`, not a lock (`packages/opencode/src/effect/runner.ts:115-138`). A second prompt whose user message lands after the running loop's final message re-read can be left answered-by-nothing: the loop breaks on the stale view, the joined caller returns the previous run's last assistant message, and the new user message remains durable but unprocessed. **Inference** from the re-read/break ordering (`packages/opencode/src/session/prompt.ts:1092`, `1106-1130`); not exercised by a test found in this revision.

## Event and data flow

### New prompt: ACP client to live response

1. ACP `prompt` looks up the ACP session, resolves model/variant/mode (persisting the model choice if unset), and converts ACP content blocks to opencode parts via `promptContentToParts` (`packages/opencode/src/acp/service.ts:509-518`; `packages/opencode/src/acp/content.ts:26-117`).
2. The request runs inside `events.runUntilIdle(sessionId, …)`, which first waits for the SSE subscription to be connected and registers an idle waiter *before* issuing the HTTP call (`packages/opencode/src/acp/event.ts:74-91`).
3. The adapter calls `sdk.session.prompt` (or `session.command` for known slash commands, `session.summarize` for `/compact`) over loopback HTTP (`packages/opencode/src/acp/service.ts:522-586`).
4. The server's `SessionPrompt.prompt` persists the user message and parts through durable events (`packages/opencode/src/session/prompt.ts:1046-1071`), then `loop` → `ensureRunning` starts or joins the run (`packages/opencode/src/session/prompt.ts:1343-1347`).
5. The run loop sets status `busy` (published as `session.status`, `packages/opencode/src/session/status.ts:39-48`), builds the assistant message, and streams the LLM via `SessionProcessor.process` (`packages/opencode/src/session/prompt.ts:1089`, `1186-1219`, `1272-1286`).
6. Processor events persist parts and publish bus events: `text-start` upserts an empty text part, each `text-delta` publishes `message.part.delta`, `text-end` persists the final part (`packages/opencode/src/session/processor.ts:500-546`); tool calls persist `tool` parts through pending/running/completed/error transitions (`packages/opencode/src/session/processor.ts:123-205`).
7. `EventV2Bridge` re-publishes every event onto `GlobalBus`; the SSE endpoint streams it (`packages/opencode/src/event-v2-bridge.ts:35-44`; `packages/opencode/src/server/routes/instance/httpapi/handlers/global.ts:25-58`).
8. The ACP subscription handles events in order: `message.part.updated` records part metadata and emits `tool_call`/`tool_call_update`; `message.part.delta` emits `agent_message_chunk` or `agent_thought_chunk` for assistant text/reasoning (`packages/opencode/src/acp/event.ts:93-106`, `191-259`, `295-394`). `permission.asked` events are queued per session and turned into ACP `requestPermission` requests whose answer is posted back to the server (`packages/opencode/src/acp/permission.ts:37-97`).
9. When the loop exits, status goes `idle`; the subscription resolves the idle waiter, so every turn update has been delivered before `session.prompt`'s response is processed. The adapter then sends a `usage_update` notification and returns `PromptResponse` with `stopReason: end_turn` (or an error-mapped reason) and echoes `userMessageId` when the client supplied one (`packages/opencode/src/acp/event.ts:82-90`; `packages/opencode/src/acp/service.ts:542-543`, `839-888`, `895-908`).

### Durable history to ACP client

1. `loadSession` (and `forkSession` after forking) fetches the session and all messages via `sdk.session.messages` — the SQLite `message`/`part` read models (`packages/opencode/src/acp/service.ts:211-247`, `362-407`; `packages/opencode/src/session/session.ts:828-851`).
2. `replayMessages` iterates messages sequentially and awaits each `replayMessage` (`packages/opencode/src/acp/service.ts:692-699`).
3. `replayMessage` converts each stored part: text/file/reasoning parts become `user_message_chunk`/`agent_message_chunk`/`agent_thought_chunk` (reasoning keyed by part ID, others by message ID); tool parts emit `tool_call` plus a terminal `tool_call_update` synthesized from the stored state (`packages/opencode/src/acp/event.ts:108-142`, `295-339`). Non-user/assistant messages and non-convertible parts are skipped; images become ACP image content (`packages/opencode/src/acp/content.ts:190-239`). A failed `sessionUpdate` is caught per message so replay continues (test: `packages/opencode/test/acp/event.test.ts:460-475`).
4. `resumeSession` deliberately performs no replay — it only restores model/variant/mode state (test: `packages/opencode/test/acp/service-session.test.ts:652-692`).

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `SessionV1.User` + parts | none (echoed `userMessageId` in response) | `message` + `part` rows via durable `message.updated`/`message.part.updated` | Before the model loop starts; committed in the durable transaction | `packages/opencode/src/session/prompt.ts:1046-1047`; `packages/core/src/session/projector.ts:260-273` |
| Assistant text | `TextPart` accumulated in processor | `agent_message_chunk` per delta | Full `TextPart` upserted at `text-end` (deltas not persisted) | Part commit at stream boundary; delta notification continuous | `packages/opencode/src/session/processor.ts:500-546`; `packages/opencode/src/acp/event.ts:214-259` |
| Reasoning/thought | `ReasoningPart` in `reasoningMap` | `agent_thought_chunk` per delta | `ReasoningPart` upserted at reasoning end / cleanup | Same as text | `packages/opencode/src/session/processor.ts:207-214`; `packages/opencode/src/acp/event.ts:246-258` |
| Tool call | `ToolPart` (pending → running) | `tool_call`, then running `tool_call_update` (bash output snapshots deduped) | `ToolPart` rows upserted per state change | Commit per `updatePart` | `packages/opencode/src/session/processor.ts:216-205ff`; `packages/opencode/src/acp/event.ts:295-394` |
| Tool result | `ToolPart` completed/error | terminal `tool_call_update` | Final `ToolPart` state incl. output/metadata | Commit at completion | `packages/opencode/src/session/processor.ts:160-205`; `packages/opencode/src/acp/event.ts:307-338` |
| Completion/failure | Assistant `finish` + `error`, `step-finish` part | `PromptResponse` stopReason; `usage_update` notification | Assistant message upserted; usage rolled into `session` row | Commit at turn end before response is mapped | `packages/opencode/src/session/prompt.ts:1288-1317`; `packages/core/src/session/projector.ts:35-109`, `310-328`; `packages/opencode/src/acp/service.ts:839-888` |

Ordering between notification and persistence: for assistant text, the durable full-part commit at `text-end` happens before the final assistant-message commit and long before the prompt response, while individual chunk notifications race ahead of the commit — the client can receive chunks for a part that would be lost if the process died mid-stream. For tool parts, each state change is committed and broadcast from the same `updatePart` call, so both views advance together.

### Subsequent-prompt reconstruction

Each loop iteration re-reads the session's durable history with `MessageV2.filterCompactedEffect`, which drops compacted prefixes, then converts the projected `WithParts` list into provider messages via `MessageV2.toModelMessagesEffect` (`packages/opencode/src/session/prompt.ts:1092-1096`, `1257-1263`; `packages/opencode/src/session/message-v2.ts:521-578`). There is no delegation or separate model-context store: SQLite message/part rows are the model context, modulo compaction and reminder/tool-bookkeeping parts. Orphaned interrupted tool parts (`error` + `metadata.interrupted`) are excluded from triggering a prefill continuation (`packages/opencode/src/session/prompt.ts:96-100`, `1106-1109`).

### Ordering, cancellation, failure, and backpressure

- **Ordering:** The SSE subscription is a single sequential loop — one event is fully handled (all `sessionUpdate` writes awaited) before the next (`packages/opencode/src/acp/event.ts:160-164`). ACP clients therefore see a total order per connection. The prompt response is additionally gated on observing `session.status: idle` after the turn's events (`packages/opencode/src/acp/event.ts:74-91`).
- **Cancellation:** `cancel` interrupts the run fiber; `finalizeInterruptedAssistant` marks the assistant message aborted, and processor cleanup persists partial text, closes reasoning parts, and marks running tools as errored with `interrupted` metadata before going idle (`packages/opencode/src/session/prompt.ts:1203-1219`; `packages/opencode/src/session/processor.ts:553-611`). The ACP response maps `MessageAbortedError` to `stopReason: "cancelled"` (`packages/opencode/src/acp/service.ts:858-863`). If the SSE stream disconnects mid-turn, idle waiters are rejected and the prompt request fails even though the server-side run may continue (`packages/opencode/src/acp/event.ts:174-182`).
- **Failure/retry:** Provider auth failures map to ACP `authRequired` (`packages/opencode/src/acp/service.ts:879-881`, `1204-1218`); other assistant errors map to `max_tokens`, `refusal`, or `internalError`. LLM streaming has a retry seam in `src/session/llm.ts` (`packages/opencode/src/session/llm.ts:357-370`).
- **Backpressure:** The bus-to-SSE queue is unbounded per subscriber (`Queue.offerUnsafe`, `packages/opencode/src/server/routes/instance/httpapi/handlers/global.ts:28-34`), so a slow ACP client cannot stall the agent or other sessions; it only delays its own subscription loop and, via `runUntilIdle`, its own prompt response. There is no bounded queue or drop policy anywhere in this path.

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | In-memory ACP map plus unbounded server sessions; `listSessions` merges live and server-backed entries | `packages/opencode/src/acp/session.ts:95-100`, `packages/opencode/src/acp/service.ts:249-293` |
| Concurrent work across sessions | `yes` | Per-session runners forked independently | `packages/opencode/src/session/run-state.ts:52-69` |
| Same-session prompt exclusion | `partial` | Model execution serialized via runner join (steering); a joined prompt racing the final loop iteration may be left unprocessed | `packages/opencode/src/effect/runner.ts:115-138`; `packages/opencode/src/session/prompt.ts:1092`, `1106-1130` |
| Durable sessions | `yes` | SQLite event log + projected rows | `packages/core/src/event.ts:237-324`, `packages/core/src/session/projector.ts:260-328` |
| Session list | `yes` | Cursor-paginated by update time; live ACP sessions merged in | `packages/opencode/src/acp/service.ts:249-293` |
| Session load/resume | `yes` | `loadSession` replays; `resumeSession` restores state only | `packages/opencode/src/acp/service.ts:211-247`, `295-334` |
| History replay to ACP client | `yes` | On load/fork; sequential, per-part conversion | `packages/opencode/src/acp/event.ts:108-142` |
| Prior history reused by model | `yes` | Durable rows re-read and converted each turn, minus compacted prefix | `packages/opencode/src/session/prompt.ts:1092`, `1257-1263` |
| Prompt cancellation | `yes` | Runner interrupt → aborted assistant message → `stopReason: cancelled` | `packages/opencode/src/session/run-state.ts:77-86`, `packages/opencode/src/acp/service.ts:858-863` |
| Tool-call progress updates | `yes` | `tool_call` + running/completed/error updates; bash output dedupe | `packages/opencode/src/acp/event.ts:295-394` |
| Partial-output persistence | `yes` | Deltas broadcast-only, but partial text/reasoning is persisted at text-end or abort cleanup | `packages/opencode/src/session/processor.ts:526-546`, `569-583` |
| Recovery after process restart | `partial` | Durable history survives; ACP config state and replay require an explicit load/resume; no automatic continuation of in-flight runs | `packages/opencode/src/acp/session.ts:95-100` (memory-only), `packages/core/src/database/database.ts:43-57` |

## Design assessment

### Strengths

- The ACP adapter is stateless with respect to conversation content: it can crash or restart without losing history, and session identity is a single ID shared across protocol, runtime, and storage (`packages/opencode/src/acp/service.ts:186-206`).
- The durable event log with per-session sequences gives transactionally consistent read models; projectors run inside the commit transaction, so projected rows and the event log cannot diverge (`packages/core/src/event.ts:237-324`).
- Live delivery ordering is easy to reason about: one sequential SSE consumer per client, plus an idle handshake that guarantees updates precede the prompt response (`packages/opencode/src/acp/event.ts:74-91`, `160-164`).
- Same-session "steering" is built from two simple pieces — durable user-message admission and a run loop that re-reads history — rather than a dedicated queue (`packages/opencode/src/session/prompt.ts:1046-1071`, `1092`).

### Tradeoffs and limitations

- The loopback HTTP + SSE hops exist inside one process, adding serialization, connection lifecycle (reconnect loop with 1s sleep, `packages/opencode/src/acp/event.ts:144-150`), and auth headers to what could be direct calls; a dropped SSE stream fails prompts that are still executing server-side.
- Two storage generations and two session runtimes coexist (V1 `SessionPrompt`/`message`/`part` used by these routes; V2 core wired in the server graph), so readers must verify which path a given route serves (`packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts:52`; `packages/opencode/src/server/routes/instance/httpapi/server.ts:65-68,299-301`).
- Streaming deltas are never persisted, so any crash between deltas and `text-end` loses streamed output unless the abort cleanup runs; recovery after restart also does not continue interrupted runs.
- Close semantics are asymmetric: `closeSession` frees ACP state but keeps the durable session, and `cancel` after close of an unknown session errors — clients must track which IDs are live (`packages/opencode/src/acp/service.ts:347-360`).

### Ideas relevant to Ox

- The idle-handshake pattern (`runUntilIdle`: register waiter before issuing the request, resolve on a lifecycle event) is a cheap way to get "notifications before response" ordering without sequence numbers; the tradeoff is that it couples the response to the health of the notification channel — Ox's persisted-event design in `src/acp.rs` can achieve the same guarantee by replaying from the log instead of from a live stream.
- Durable-events-plus-projectors gives a single commit point for "what happened" and "what is readable"; for Ox's scale (per-workspace SQLite, `src/sessions.rs`), writing events and projected rows in one transaction is directly comparable, but Ox can likely skip the separate event log table unless replay/audit needs it.
- Persisting a sentinel on interrupted tool parts (`metadata.interrupted`) so later prompts neither resume nor re-send them is a small mechanism that makes abort/retry safe (`packages/opencode/src/session/prompt.ts:96-100`); Ox's cancellation path could adopt the same marker when persisting interrupted tool calls.

## Unknowns and conflicts

- **V1/V2 runtime split:** `SessionV2`/`SessionExecution` are constructed in the server layer graph (`packages/opencode/src/server/routes/instance/httpapi/server.ts:299-301`), but every route ACP exercises was observed to use the V1 `SessionPrompt` service. Whether any request path can dispatch to the V2 runner at this revision was not exhaustively traced. Searches: handler imports in `src/server/routes/instance/httpapi/handlers/`, grep for `SessionPrompt.Service`.
- **Same-session join race:** The scenario where a joined prompt is admitted but never processed (see Concurrency table) is inferred from code ordering; no test covers it at this revision.
- **Load/resume during an active prompt:** No guard exists on the ACP load/resume path; the observable result (duplicate replay, or state overwrite while running) was not exercised. Searched `src/acp/service.ts` for busy checks — none found.
- **Docs vs code:** The repository README and docs describe user-facing features; this report relies only on code and tests under `packages/opencode/test/acp/` and `packages/opencode/test/server/`, which agreed with the implementation paths inspected.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `packages/opencode/src/cli/cmd/acp.ts:19-71` (`handler`), `packages/opencode/src/acp/agent.ts:24-93` (`init`, `Agent`, `run`) | Process topology, transport, adapter-to-service wiring, error mapping |
| Session management | `packages/opencode/src/acp/service.ts:163-247` (`newSession`, `loadSession`), `295-360` (`resumeSession`, `closeSession`, `cancel`), `packages/opencode/src/acp/session.ts:95-100` (state map) | ACP session lifecycle and its thin in-memory state |
| Concurrency | `packages/opencode/src/effect/runner.ts:115-138` (`ensureRunning`), `171-202` (`cancel`), `packages/opencode/src/session/run-state.ts:52-94` | Serialization/join semantics and per-session isolation |
| Persistence | `packages/core/src/event.ts:205-330` (`commitDurableEvent`), `packages/core/src/session/projector.ts:210-452`, `packages/core/src/database/database.ts:43-57`, `packages/opencode/src/session/session.ts:629-643` (`updateMessage`/`updatePart`) | Durable commit boundary, projections, storage location |
| Event translation | `packages/opencode/src/acp/event.ts:93-165`, `191-394`, `packages/opencode/src/acp/content.ts:26-117`, `packages/opencode/src/acp/tool.ts:38-120`, `packages/opencode/src/event-v2-bridge.ts:35-62` | Bus → SSE → ACP conversion for parts, tools, permissions |
| Replay/reconstruction | `packages/opencode/src/acp/service.ts:692-699` (`replayMessages`), `1089-1180` (`restoreSession`), `packages/opencode/src/acp/event.ts:108-142` (`replayMessage`), `packages/opencode/src/session/prompt.ts:1092`, `1257-1263` | Load-time replay and model-context rebuild |
| LLM integration | `packages/opencode/src/session/llm.ts:85-383` (`LLM.run`, `stream`), `packages/opencode/src/session/llm/{request,ai-sdk,native-runtime,native-request}.ts`, `packages/opencode/src/provider/provider.ts:113-140`, `174-245`, `1896-1925`, `packages/opencode/src/provider/transform.ts:465`, `1562`, `packages/opencode/src/session/tools.ts:92-101`, `packages/llm/src/route/client.ts:344-426`, `packages/llm/src/tool-runtime.ts:23-35` | Default AI SDK path vs opt-in `@opencode-ai/llm` native runtime; model-call site, provider/tool boundary, credential and schema locations |
| Cancellation/errors | `packages/opencode/src/session/processor.ts:553-611` (`cleanup`), `packages/opencode/src/session/prompt.ts:1203-1219` (`finalizeInterruptedAssistant`), `packages/opencode/src/acp/service.ts:839-888` (`promptResponse`), `packages/opencode/src/acp/error.ts:63-93` | Abort persistence and stopReason/error mapping |

## Research notes

- **Revision inspected:** `fee476bb90043a1012abda156dd9af9e5c71b19d`
- **Primary evidence:** `packages/opencode/src/acp/*` (agent, service, session, event, content, tool, permission, error, usage, config-option, directory, profile); `packages/opencode/src/cli/cmd/acp.ts`; `packages/opencode/src/cli/network.ts`; `packages/opencode/src/server/server.ts` and `src/server/routes/instance/httpapi/handlers/{global,session}.ts`; `packages/opencode/src/session/{prompt,session,processor,run-state,status,message-v2,llm,tools}.ts` and `packages/opencode/src/session/llm/{request,ai-sdk,native-runtime,native-request}.ts`; `packages/opencode/src/provider/{provider,transform}.ts`; `packages/opencode/src/auth/index.ts`; `packages/opencode/src/effect/{runner,runtime-flags}.ts`; `packages/opencode/src/event-v2-bridge.ts`; `packages/core/src/{event.ts,global.ts,database/database.ts,session/{projector,sql}.ts}`; `packages/schema/src/v1/session.ts`; `packages/llm/src/{route/client.ts,tool-runtime.ts,providers/index.ts,protocols/*}`. Tests read: `packages/opencode/test/acp/{service-session,event}.test.ts`.
- **Relevant docs:** `packages/opencode/AGENTS.md` (module conventions and V2 session-core invariants; used only to flag the V1/V2 duality, not as behavioral evidence).
- **Commands/tests run:** Read-only inspection (git rev-parse, file reads, greps). No tests executed.
- **Report confidence:** `high` — all load-bearing claims trace to production code read at the pinned revision; the residual unknowns are explicitly listed above.
