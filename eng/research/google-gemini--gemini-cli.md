---
project: "google-gemini/gemini-cli"
repository: "https://github.com/google-gemini/gemini-cli"
revision: "cfbcaa8df13ea4610bb379b377b56d62980c0032"
researched_at: "2026-09-19"
primary_language: "TypeScript"
implementation_form: "native-agent"
process_model: "single-process"
session_owner: "acp-layer"
durability: "local-files"
cross_session_concurrency: "concurrent"
same_session_concurrency: "cancel-previous"
event_delivery: "direct"
resume_strategy: "reconstruct"
overall_confidence: "high"
---

# gemini-cli ACP architecture

## Executive summary

- **ACP boundary:** ACP is native to the CLI. When started with `--experimental-acp`, the normal `gemini` entry point branches into `runAcpClient` (`packages/cli/src/gemini.tsx:874`, `packages/cli/src/acp/acpStdioTransport.ts:15`), which builds an ndjson stdio stream and an `AgentSideConnection` from `@agentclientprotocol/sdk` 0.16.1 (`packages/cli/src/acp/acpStdioTransport.ts:25-29`). There is no second agent process; the ACP layer calls the same in-process `@google/gemini-cli-core` runtime the interactive TUI uses.
- **Session model:** A "session" is a UUID created in `AcpSessionManager.newSession` (`packages/cli/src/acp/acpSessionManager.ts:62`), stored as an ACP-layer `Map<string, Session>` (`acpSessionManager.ts:34`), and threaded into a per-session core `Config` as `sessionId`/`promptId` (`packages/core/src/config/config.ts:994-995`). One ACP session maps 1:1 to one `Config`, one `GeminiChat`, and one durable JSONL transcript file.
- **Concurrency:** Sessions are isolated objects with independent configs, chats, and transcript files; the SDK dispatches each incoming JSON-RPC message without awaiting the previous one, so prompts in different sessions interleave freely. Within one session, a new prompt aborts the previous one (`packages/cli/src/acp/acpSession.ts:311-314`).
- **Durability and replay:** History is appended synchronously (`fs.appendFileSync`) as line-delimited JSON to `~/.gemini/tmp/<projectHash>/chats/session-<timestamp>-<id8>.jsonl` (`packages/core/src/services/chatRecordingService.ts:499-519`, `:559-573`). `loadSession` reads that file, converts it to model history with `convertSessionToClientHistory`, resumes the chat, and replays the stored messages to the client as chunk notifications (`acpSessionManager.ts:175-206`, `packages/cli/src/acp/acpSession.ts:241-309`).
- **Event flow:** Live events are translated 1:1 in `Session.prompt`'s turn loop: core stream events become awaited `sessionUpdate` notifications (`acpSession.ts:412-485`, `:650-657`). Writes go through the SDK's serialized write queue; no intermediate channel or buffer exists.
- **Notable uncertainty:** `loadSession` replaces an existing `Session` and disposes it, but disposal does not abort an in-flight prompt (`acpSessionManager.ts:197-202`, `acpSession.ts:190-196`); two writers can then share one transcript file.

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `native-agent` | The CLI binary itself is the ACP agent; protocol code lives beside the TUI and shares the core runtime. | `packages/cli/src/gemini.tsx:874-876`, `packages/cli/src/acp/acpStdioTransport.ts:25-29` |
| Process model | `single-process` | One Node process serves all ACP sessions over stdio; MCP servers it launches are per-session children, not agents. | `acpStdioTransport.ts:15-35`, `acpSessionManager.ts:295-326` |
| Session owner | `acp-layer` | `AcpSessionManager` mints the UUID and owns the active-session map; core owns the conversation content under that ID. | `acpSessionManager.ts:33-41`, `packages/core/src/config/config.ts:994-995` |
| Durability | `local-files` | Append-only JSONL transcripts under the project temp dir; no database. | `packages/core/src/services/chatRecordingService.ts:499-573` |
| Cross-session concurrency | `concurrent` | Independent session objects; SDK processes messages concurrently. | SDK `dist/acp.js` `#processMessage` (not awaited); `acpSessionManager.ts:34` |
| Same-session concurrency | `cancel-previous` | `Session.prompt` aborts `pendingPrompt` before starting. | `packages/cli/src/acp/acpSession.ts:311-314` |
| Event delivery | `direct` | Handlers `await` `connection.sessionUpdate`, which resolves when the SDK write queue flushes the notification. | `acpSession.ts:650-657`; SDK `#sendMessage` write queue |
| Resume strategy | `reconstruct` | Durable JSONL is parsed and converted back into chat history; recording continues against the same file. | `acpSessionManager.ts:175-187`, `chatRecordingService.ts:424-463` |

## System architecture

```text
ACP client (editor)
  │  JSON-RPC over ndjson stdio
  ▼
┌─────────────────── gemini-cli Node process (single process) ───────────────────┐
│  AgentSideConnection (ACP SDK 0.16.1)  ── ndJsonStream(stdin, stdout)          │
│        │  dispatches each request/notification concurrently                    │
│        ▼                                                                       │
│  GeminiAgent (acpRpcDispatcher)                                                │
│   initialize/authenticate/newSession/loadSession/cancel/prompt/setMode/Model   │
│        │                                                                       │
│        ▼                                                                       │
│  AcpSessionManager ── Map<sessionId, Session>                                  │
│        │                          │                                            │
│        │                          ▼                                            │
│        │                Session (per ACP session)                              │
│        │                 ├─ turn loop: prompt → runTool → next turn            │
│        │                 └─ sendUpdate → connection.sessionUpdate (live out)   │
│        ▼                                                                       │
│  per-session Config (loadCliConfig, sessionId=ACP UUID)                        │
│   ├─ GeminiClient → GeminiChat → model API (streaming)                         │
│   │      └─ ChatRecordingService ── appendFileSync ──▶ chats/*.jsonl (disk)    │
│   ├─ ToolRegistry, MessageBus/PolicyEngine                                     │
│   └─ MCP servers (child processes, per session)                                │
│                                                                                │
│  loadSession: read JSONL ─▶ convertSessionToClientHistory ─▶ resumeChat        │
│               └─▶ streamHistory: stored messages ─▶ sessionUpdate (replay out) │
└────────────────────────────────────────────────────────────────────────────────┘
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `runAcpClient` / `AgentSideConnection` | Transport: ndjson framing, JSON-RPC dispatch, outbound write queue | Process | none | `acpStdioTransport.ts:15-35` |
| `GeminiAgent` | ACP method surface; auth state for the connection; owns `AcpSessionManager` | Process (connection) | API key/base URL auth details | `packages/cli/src/acp/acpRpcDispatcher.ts:21-38` |
| `AcpSessionManager` | Session creation/loading, per-session config construction, session map | Process | `Map<sessionId, Session>` | `acpSessionManager.ts:33-56` |
| `Session` (ACP) | Prompt turn loop, `@file` resolution, tool execution, permission requests, ACP event translation | Session | `pendingPrompt` AbortController, call-id counter | `acpSession.ts:65-73` |
| `Config` (core, per session) | Tools, MCP, policy engine, storage, auth; implements `AgentLoopContext` | Session | `sessionId`, `promptId`, `Storage` | `acpSessionManager.ts:333`, `packages/core/src/config/config.ts:994-995,1313` |
| `GeminiChat` | Model conversation: in-memory history, streaming, rollback on failure | Session | `agentHistory`, `sendPromise` serialization | `packages/core/src/core/geminiChat.ts:376,489-509` |
| `ChatRecordingService` | Durable transcript append/replay | Session (resumable across processes) | conversation file path, cached record | `chatRecordingService.ts:402-416` |

### ACP surface

Transport is line-delimited JSON over stdin/stdout (`acpStdioTransport.ts:20-29`). The SDK's `AgentSideConnection` routes `initialize`, `session/new`, `session/load`, `session/prompt`, `session/cancel`, `session/set_mode`, `authenticate`, and unstable variants to `GeminiAgent` (`acpRpcDispatcher.ts:40-235`). `initialize` advertises `loadSession: true`, image/audio/embedded-context prompt capabilities, and http/sse MCP capabilities (`acpRpcDispatcher.ts:91-102`). Client-side fs capabilities are consumed through `AcpFileSystemService`, which proxies reads/writes to the client inside the workspace and falls back to local disk otherwise (`packages/cli/src/acp/acpFileSystemService.ts:51-87`). The ACP layer is native: protocol handling, session management, and the agent runtime are modules of the same CLI package with no translation subprocess.

### Runtime and process boundaries

Everything runs on one Node.js event loop. Each incoming SDK message is handled via an un-awaited `#processMessage`, so requests and notifications — including `session/cancel` during a prompt — run concurrently (ACP SDK 0.16.1 `dist/acp.js`, `Connection` class). Outbound messages are serialized through a promise-chained `#writeQueue`; `await this.connection.sessionUpdate(...)` returns once the notification is written to stdout, not when the client acknowledges it. Each `newSession`/`loadSession` builds a fresh `Config` (`acpSessionManager.ts:248,333`), which starts that session's MCP servers and tool registry — sessions are heavy, independent islands. Cleanup is process-exit driven (`acpStdioTransport.ts:34` awaits `connection.closed.finally(runExitCleanup)`); the SDK never calls the implemented `dispose` methods.

## LLM abstraction and integration

### Abstraction

The project uses a **bespoke internal abstraction**, the `ContentGenerator` interface, over the provider SDK `@google/genai` 1.30.0 and a hand-written HTTP client for Google's Code Assist backend. It is not a named third-party LLM abstraction library (no LangChain/Vercel AI SDK/LiteLLM in `packages/core/package.json`), and it is not a wrapped executable: model inference is a remote HTTP call, not a bundled runtime.

- `ContentGenerator` declares `generateContent`, `generateContentStream`, `countTokens`, `embedContent` (`packages/core/src/core/contentGenerator.ts:39-61`).
- `createContentGenerator` selects one of two implementations by `AuthType` (`packages/core/src/core/contentGenerator.ts:210-421`):
  - `CodeAssistServer implements ContentGenerator` — a bespoke client that posts to `https://cloudcode-pa.googleapis.com/v1internal` through `google-auth-library`'s `AuthClient.request` and parses an SSE stream (`packages/core/src/code_assist/server.ts:77-87`, `:415-522`, `:73-74`).
  - `GoogleGenAI`'s `models` object from `@google/genai` (`packages/core/src/core/contentGenerator.ts:385-410`), used for Gemini API key, Vertex AI, and Gateway auth.
- The interface is decorated by `LoggingContentGenerator` (`packages/core/src/core/loggingContentGenerator.ts:149-155`), `ModelMappingContentGenerator` (`packages/core/src/core/modelMappingContentGenerator.ts:20-28`), `RecordingContentGenerator`, and `FakeContentGenerator`, all chosen in the same factory (`contentGenerator.ts:216-228`, `:298-309`, `:417-419`).
- `BaseLlmClient` is a second bespoke wrapper for stateless utility calls (JSON generation, embeddings, classification) that resolves a model config and retries over the same `ContentGenerator` (`packages/core/src/core/baseLlmClient.ts:123-128`, `:289-424`).
- Model routing additionally uses `LocalLiteRtLmClient`, a direct `GoogleGenAI` client pointed at a local LiteRT-LM server for classifier decisions (`packages/core/src/core/localLiteRtLmClient.ts:15-39`).
- The inference loop is outside this repository: both implementations serialize a request and consume a streamed response, with no local model execution. The agent/tool loop is in this repository (see below).

### Integration path

1. ACP `Session.prompt` converts the incoming prompt to Gemini `Part[]` and calls `geminiClient.sendMessageStream(currentParts, signal, promptId)` (`packages/cli/src/acp/acpSession.ts:406-410`).
2. `GeminiClient.sendMessageStream` → `processTurn` runs turn bookkeeping, context management/compression, and token-limit checks, then chooses a model via `ModelRouterService.route` (or a sticky `currentSequenceModel`), refines it with `applyModelSelection`, and calls `setTools(modelToUse)` (`packages/core/src/core/client.ts:614-807`).
3. `Turn.run` delegates to `GeminiChat.sendMessageStream` (`packages/core/src/core/turn.ts:284-292`).
4. `GeminiChat.sendMessageStream` awaits the prior send promise (per-chat serialization), records the user message through `ChatRecordingService`, snapshots request history, and calls `makeApiCallAndProcessStream` (`packages/core/src/core/geminiChat.ts:480-675`).
5. `makeApiCallAndProcessStream` scrubs and coalesces history, resolves the model with `resolveModel` and availability config with `applyModelSelection`, assembles a `GenerateContentConfig` carrying `systemInstruction`, `tools`, and `abortSignal`, then calls `config.getContentGenerator().generateContentStream({model, contents, config}, prompt_id, role)` (`packages/core/src/core/geminiChat.ts:870-1090`).
6. At the provider boundary:
   - Code Assist: `CodeAssistServer.generateContentStream` maps `GenerateContentParameters` to the Code Assist wire shape with `toGenerateContentRequest` (`packages/core/src/code_assist/converter.ts:129-143`), POSTs to `:streamGenerateContent?alt=sse` (`packages/core/src/code_assist/server.ts:470-489`), parses `data:` frames (`server.ts:491-521`), and normalizes each frame with `fromGenerateContentResponse` (`converter.ts:145-161`).
   - Gemini API/Vertex/Gateway: the `@google/genai` SDK's `models.generateContentStream`.
7. `processStreamResponse` consumes the `AsyncGenerator<GenerateContentResponse>`, buffers text/thought/function-call parts, consolidates them, validates the stream, records the model turn, and yields the chunks onward (`packages/core/src/core/geminiChat.ts:1354-1669`).
8. `Turn.run` maps each chunk to `GeminiEventType.Content`, `Thought`, `ToolCallRequest`, `Finished`, or `Error` (`packages/core/src/core/turn.ts:294-414`).
9. ACP `Session.prompt` maps those events to `sessionUpdate` notifications, collects `ToolCallRequest`s, executes them in-process via `runTool`, and feeds the resulting `functionResponse` parts back as the next turn's input (`packages/cli/src/acp/acpSession.ts:412-602`).

For the Code Assist path, request/response conversion is fully visible in `packages/core/src/code_assist/converter.ts`, but the model loop on the server is not. For the SDK path, transport and retry internals are not visible in the checkout because the dependency is not vendored (no `node_modules`).

### Provider and tool boundary

- Provider-specific code lives in `packages/core/src/core/contentGenerator.ts` (factory and auth-type routing) and `packages/core/src/code_assist/` (`server.ts`, `converter.ts`, `oauth2.ts`, `setup.ts`, `types.ts`) for the Code Assist/Vertex wire protocol.
- Credentials and configuration: `createContentGeneratorConfig` resolves API key, Vertex project/location, base URL, proxy, and custom headers (`packages/core/src/core/contentGenerator.ts:141-208`); API keys come from the argument, environment, or keychain via `loadApiKey` (`packages/core/src/core/apiKeyCredentialStorage.ts:34`); OAuth uses `getOauthClient` (`packages/core/src/code_assist/oauth2.ts:431`). All are wired by `Config.refreshAuth` (`packages/core/src/config/config.ts:1570-1614`). The ACP path selects the auth type from settings or a supplied base URL (`packages/cli/src/acp/acpSessionManager.ts:71-85`).
- Request/response normalization: `packages/core/src/code_assist/converter.ts` converts between `@google/genai` types and the Code Assist/Vertex request and response shapes, including usage metadata (`converter.ts:315-326`); `ModelMappingContentGenerator` rewrites model IDs before dispatch (`packages/core/src/core/modelMappingContentGenerator.ts:42-55`).
- Tool schemas: each tool declares an `@google/genai` `FunctionDeclaration` (`packages/core/src/tools/tools.ts:423-432`) and may vary it by model (`getSchema(modelId)`, `tools.ts:426`). `ToolRegistry.getFunctionDeclarations(modelId)` aggregates them, including MCP tools whose parameter schemas come from the connected server at discovery time (`packages/core/src/tools/tool-registry.ts:663-714`; `packages/core/src/tools/mcp-tool.ts:505-535`). The array is attached to the request as `tools` (`packages/core/src/core/geminiChat.ts:955-962`). Tool execution is outside the model abstraction: the ACP turn loop runs each requested tool and returns function responses (`acpSession.ts:586-602`).
- Multi-provider: **No.** There is a single vendor (Google): Gemini models, plus Gemma models used by the local router (`packages/core/src/config/models.ts:54-102`). Access is reached through several auth backends, selected by `AuthType` (`packages/core/src/core/contentGenerator.ts:63-70`): `LOGIN_WITH_GOOGLE`/`COMPUTE_ADC` (Code Assist), `USE_GEMINI` (Gemini API key), `USE_VERTEX_AI`, `LEGACY_CLOUD_SHELL`, and `GATEWAY` (custom base URL). `getAuthTypeFromEnv` derives it from environment variables (`contentGenerator.ts:80-100`). No OpenAI/Anthropic/other vendor adapters exist; a search across `packages/core/src` for those names found no non-test matches.

### Limits

- The `@google/genai` 1.30.0 SDK transport, retry, and streaming internals are not in the checkout (no `node_modules` is present). Claims about that path are bounded to how the SDK is invoked (`contentGenerator.ts:385-410`), not its implementation.
- The Code Assist backend at `cloudcode-pa.googleapis.com` is a hosted service; its model routing, serving, and tier logic are opaque beyond the request/response types in `code_assist/types.ts` and `converter.ts`.
- MCP tool schemas are supplied at runtime by connected MCP servers (`mcp-tool.ts:505-535`) and are not statically enumerable from the checkout.
- "Provider" here means a Google auth backend, not a distinct model vendor. There is no provider registry or plugin interface beyond the `createContentGenerator` branch; adding a non-Google provider would require a new `ContentGenerator` implementation.
- The model-selection policy inputs that depend on remote state (experiments, quota, availability) are fetched at runtime (`config.ts:1621-1636`); their server-side definitions are not visible.

## Session model

### Identity and ownership

The ACP session ID is a `randomUUID()` minted per `newSession` (`acpSessionManager.ts:62`). It becomes (a) the key in the manager's `Map`, (b) `Config.sessionId` and `Config.promptId` (`packages/core/src/config/config.ts:994-995`), (c) `Storage.sessionId` for session-scoped directories (`config.ts:1313`, `packages/core/src/config/storage.ts:37-48`), and (d) the `ChatRecordingService` session ID in the transcript filename (`chatRecordingService.ts:414`, `:503-519`). The mapping is 1:1: one ACP session → one `Config`/`GeminiClient`/`GeminiChat` → one transcript file. On `loadSession`, the client-supplied ID is matched to a file by the first 8 UUID characters in the filename plus a full-ID check inside the file metadata (`packages/cli/src/utils/sessionUtils.ts:415-440`); a resumed session keeps writing to the same file (`chatRecordingService.ts:424-463`), so durable identity survives restarts.

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | Mint UUID, load settings for `cwd`, build per-session config, `refreshAuth`, initialize (starts MCP), `startChat`, register session | New JSONL transcript created lazily on first recorded message with metadata line | Auth failure → `RequestError(-32000)`; session not registered | `acpSessionManager.ts:58-162` |
| Load/resume | Resolve file via `SessionSelector`, `convertSessionToClientHistory`, `geminiClient.resumeChat`, replace existing `Session` (dispose + set), fire-and-forget `streamHistory` | Recording service re-binds to the existing file and appends thereafter | Unresolvable session → thrown error to client; corrupt file falls back to in-memory record | `acpSessionManager.ts:164-229`, `chatRecordingService.ts:424-477` |
| Prompt | Abort previous pending prompt, resolve `@`-parts/commands, loop `sendMessageStream` → tools → next turn, aggregating usage | User message appended before API call; model turn + tool-call records appended as they complete | Errors mapped to `RequestError`; aborts return `{stopReason:'cancelled'}` and roll back history + record | `acpSession.ts:311-627`, `geminiChat.ts:791-821` |
| Cancel | `session.cancelPendingPrompt` aborts the stored `AbortController` | Rollback appends a `$set messages` snapshot trimming the aborted turn | Throws if `pendingPrompt` is null; after a normal completion the stale controller is aborted harmlessly | `acpSession.ts:198-205`, `geminiChat.ts:792-821` |
| Close/delete | Not found — no ACP close/list handler exists; `dispose` exists on `Session`/manager but the SDK never calls it | n/a | Process exit runs `runExitCleanup` | Searched `packages/cli/src/acp/` for `closeSession`/`listSessions`; SDK `dist/acp.js` (no `dispose` call); `acpStdioTransport.ts:34` |

### Durable representation

Storage is an append-only JSONL file per session at `<global temp dir>/<projectHash>/chats/session-<timestamp>-<id8>.jsonl` (`chatRecordingService.ts:479-519`, `storage.ts:230-233`). Line kinds: a metadata object (`sessionId`, `projectHash`, timestamps), message records (`id`, `timestamp`, `type: user|gemini|info|error|warning`, `content`, optional `displayContent`, `thoughts`, `tokens`, `toolCalls`), `$set` metadata updates, and `$rewindTo` compaction markers (`chatRecordingService.ts:559-646`; replay at `:133-252`). Every append is a synchronous `fs.appendFileSync` — no batching or async flush, so a record is on disk before the caller proceeds (`:559-573`). Full rewrites happen only when a file is unreadable (atomic temp+rename, `:580-640`). Not persisted: token-by-token model output (only the consolidated turn), the synthetic in-memory environment-context turn, and locally-answered slash-command output. Stored history is authoritative for resume only; the running conversation is fed from `GeminiChat`'s in-memory history (`geminiChat.ts:641`).

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | Separate `Session`/`Config`/`GeminiChat`/recording files; SDK dispatches requests without awaiting prior ones; no cross-session lock | SDK `#processMessage`; `acpSessionManager.ts:34` |
| Two prompts in the same session | `cancel-previous` | `Session.prompt` calls `this.pendingPrompt?.abort()` then installs a new controller; core additionally serializes stream sends per chat via `sendPromise` | `acpSession.ts:311-314`; `geminiChat.ts:489,505-509` |
| Load/resume during an active prompt | Old prompt is not stopped | `loadSession` disposes the old `Session` (unsubscribes listeners only) and replaces the map entry; the in-flight prompt keeps running against the old chat and can append to the same file the resumed session now owns | `acpSessionManager.ts:197-202`; `acpSession.ts:190-196` |
| Delete/close during an active prompt | n/a | No close/delete RPC exists | Not found (see Lifecycle) |
| Cancellation isolation | per session | `cancel` looks up one session and aborts only its `pendingPrompt`; other sessions unaffected | `acpRpcDispatcher.ts:189-198`; `acpSession.ts:198-205` |

Shared state that couples sessions: the process-wide `coreEvents` emitter (approval-mode changes are filtered per session ID, `acpSession.ts:178-188`), the shared `LoadedSettings` (authentication writes user-scope settings, `acpRpcDispatcher.ts:162-166`), and the model API itself (per-account rate limits). Per-session MCP startup multiplies subprocesses across sessions but does not serialize them. The single global event loop means CPU-heavy synchronous transcript appends briefly stall all sessions.

## Event and data flow

### New prompt: ACP client to live response

1. Client sends `session/prompt`; the SDK validates and calls `GeminiAgent.prompt`, which looks up the `Session` (`acpRpcDispatcher.ts:200-209`).
2. `Session.prompt` aborts any previous pending prompt and installs a new `AbortController` (`acpSession.ts:311-314`), waits for MCP init (`:316`), and generates a per-prompt `promptId` (`:318`).
3. `#resolvePrompt` converts ACP content blocks into Gemini parts, resolving `@file` references — including a permission round-trip for out-of-workspace reads — into inline content (`acpSession.ts:945-1520`).
4. Slash/`$` commands are intercepted and answered locally without the model (`acpSession.ts:341-360`, `packages/cli/src/acp/acpCommandHandler.ts:45-57`).
5. Each turn calls `geminiClient.sendMessageStream(parts, signal, promptId)` (`acpSession.ts:406-410`); `GeminiChat.sendMessageStream` first awaits any prior stream (`geminiChat.ts:489`) and appends the user message to the durable record (`:547-553`).
6. The turn loop maps stream events to ACP: `Content` → `agent_message_chunk`, `Thought` → `agent_thought_chunk`, `ToolCallRequest` → collected, `Finished` → usage capture, `Error` → thrown `RequestError` (`acpSession.ts:412-485`); each mapping is delivered via awaited `sendUpdate` (`:650-657`).
7. Collected tool requests run sequentially in `runTool`: optional `requestPermission` round-trip with an embedded pending `tool_call` (`acpSession.ts:753-781`), an `in_progress` `tool_call` otherwise (`:820-829`), execution, then completed/failed `tool_call_update` (`:838-847`, `:902-910`) and a durable tool-call record via `chat.recordCompletedToolCalls` (`:864-890`, `:912-939`).
8. Tool results become the next turn's input (`acpSession.ts:590-602`); the loop ends on `end_turn`/`max_turn_requests`/`max_tokens`/`cancelled` and returns a `PromptResponse` with aggregated token usage (`:362-627`).

### Durable history to ACP client

1. `loadSession` resolves the transcript file and loads the full `ConversationRecord` (`acpSessionManager.ts:175-178`, `sessionUtils.ts:539-568`).
2. `convertSessionToClientHistory` filters info/error/warning records and ignorable user content (commands, context blocks), rebuilds model turns including tool calls and synthetic tool-response pairings, and feeds the result to `resumeChat` (`packages/core/src/utils/sessionUtils.ts:110-228`, `packages/core/src/core/client.ts:339-344`).
3. Separately, `Session.streamHistory` walks the raw stored messages and replays them as `user_message_chunk`, `agent_thought_chunk`, `agent_message_chunk`, and terminal-state `tool_call` notifications (success→completed, error→failed; diffs reconstructed from `resultDisplay`) (`acpSession.ts:241-309`). It is invoked without await, so the `loadSession` response can return before replay finishes (`acpSessionManager.ts:204-206`). Unlike model-context conversion, replay does not filter internal context messages, so an expanded `@file` user turn is replayed in full.

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | Gemini `Part[]` after `@`-resolution | none (no echo) | `user` message record | Synchronously inside `sendMessageStream` before the API call | `geminiChat.ts:529-553`; `acpSession.ts` (no user chunk emitted) |
| Assistant text | Stream chunks consolidated per turn | `agent_message_chunk` per chunk (live) | one `gemini` message with consolidated text | After the stream completes, before next turn | `geminiChat.ts:1354,1490-1668` |
| Reasoning/thought | Buffered `ThoughtSummary` | `agent_thought_chunk` (live) | `thoughts` array on the gemini message | With the turn's model record | `geminiChat.ts:1634-1668`; `acpSession.ts:431-438` |
| Tool call | `ToolCallRequestInfo` | `tool_call`/embedded permission request | `toolCalls[]` on the gemini record | After `tool_call_update` notification is sent, before the loop continues | `acpSession.ts:820-846,864-890` |
| Tool result | `ToolResult` → function-response parts | `tool_call_update` completed/failed | `toolCalls[].result` + `result` in record | Same commit as above | `acpSession.ts:838-941`; `chatRecordingService.ts:775-838` |
| Completion/failure | Loop exit or thrown error | `PromptResponse` (stopReason) or JSON-RPC error | none (no terminal record; rollback `$set` on abort/failure) | On abort/error: in-memory rollback then `updateMessagesFromHistory` appends a `$set messages` snapshot | `geminiChat.ts:791-821`; `acpSession.ts:486-548` |

### Subsequent-prompt reconstruction

Within a live session, no reconstruction occurs: `GeminiChat` keeps `agentHistory` in memory and sends the full turn list with each request (`geminiChat.ts:641`). Across restarts, `loadSession` reconstructs: JSONL replay → `ConversationRecord` → `convertSessionToClientHistory` → `GeminiChat` seeded with env context + converted turns (`client.ts:380-431`, `packages/core/src/utils/environmentContext.ts:84-111`). The conversion is potentially lossy (info/warning records and ignorable user content are dropped) and repairs tool-call/result pairing for legacy records (`sessionUtils.ts:40-79,110-228`). The recording service then continues appending to the same file (`chatRecordingService.ts:424-463`).

### Ordering, cancellation, failure, and backpressure

Ordering to the client is FIFO per process: the SDK chains all outbound writes on one `#writeQueue`, and ACP handlers `await` each notification, so chunk order matches emission order. Backpressure is real but unbounded: a slow client's stream backpressure propagates through the awaited `writer.write`, pausing the prompt loop; there is no queue bound or drop policy. On cancellation, the abort signal unwinds the model stream; the chat's `finally` block rolls in-memory history back to the pre-prompt length and appends a `$set messages` snapshot so the durable record matches — partially streamed text is therefore not persisted (`geminiChat.ts:791-821`). Tools completed before the abort keep their durable records; a cancelled tool surfaces as a failed `tool_call_update` (`acpSession.ts:899-941`). Failures map to JSON-RPC errors (429 specially handled, `acpSession.ts:486-548`); transient stream errors retry with backoff inside the core (`geminiChat.ts:712-786`). One session's stall can delay another's outbound notifications, but not its computation.

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | Map of independent sessions | `acpSessionManager.ts:34-49` |
| Concurrent work across sessions | `yes` | SDK dispatches messages concurrently; independent configs/chats | SDK `#processMessage`; `acpSessionManager.ts:286-342` |
| Same-session prompt exclusion | `partial` | Not excluded — the new prompt cancels the old one and both responses are returned | `acpSession.ts:311-314` |
| Durable sessions | `yes` | JSONL transcripts, synchronous appends | `chatRecordingService.ts:559-573` |
| Session list | `no` | `session_list` not implemented; SDK raises method-not-found | `acpRpcDispatcher.ts` (no `listSessions`); SDK dispatch |
| Session load/resume | `yes` | Advertised and implemented; CLI-resumable files reused | `acpRpcDispatcher.ts:92`; `acpSessionManager.ts:164-229` |
| History replay to ACP client | `yes` | `streamHistory` replays user/thought/message/tool records | `acpSession.ts:241-309` |
| Prior history reused by model | `yes` | Converted history seeds `GeminiChat`; in-memory history used thereafter | `acpSessionManager.ts:180-187`; `geminiChat.ts:641` |
| Prompt cancellation | `yes` | AbortController per pending prompt; rollback on abort | `acpSession.ts:198-205`; `geminiChat.ts:792-821` |
| Tool-call progress updates | `yes` | pending (via permission), in_progress, completed, failed | `acpSession.ts:753-847,902-910` |
| Partial-output persistence | `no` | Model text persisted only as consolidated turn; aborts roll back | `geminiChat.ts:1634-1668,791-821` |
| Recovery after process restart | `yes` | `loadSession` reconstructs from JSONL; same file continues | `acpSessionManager.ts:164-206` |

## Design assessment

### Strengths

- Session isolation is structural, not conventional: each session owns its `Config`, `GeminiChat`, and transcript file, so cross-session concurrency needs no locks (`acpSessionManager.ts:58-162`).
- Durability is simple and crash-consistent by construction: synchronous per-event appends mean the transcript is never more than one event behind the runtime, and the JSONL `$set`/`$rewindTo` log makes rollback itself append-only (`chatRecordingService.ts:559-646`).
- The same recording layer serves the TUI and ACP identically, so resume and replay behavior are exercised by the much larger interactive-CLI code path (`geminiChat.ts:424-438`).

### Tradeoffs and limitations

- Cancel-previous means a second same-session prompt silently discards the first turn's context (rolled back from history and record), which a client issuing overlapping prompts may not expect (`acpSession.ts:311-314`; `geminiChat.ts:799-818`).
- `loadSession` over an active session leaves the old prompt running and creates two writers to one transcript file (`acpSessionManager.ts:197-202`).
- Every session re-runs config initialization and starts its own MCP servers, so N sessions duplicate N subprocess sets and N policy/registry instances (`acpSessionManager.ts:286-342`).
- Per-event synchronous `appendFileSync` on the shared event loop puts disk latency on the critical path of every session (`chatRecordingService.ts:559-573`).
- Replay fidelity differs between model context and client replay: `streamHistory` does not apply the filters used for `convertSessionToClientHistory`, so internal/expanded content can be replayed as user chunks (`acpSession.ts:241-251` vs `sessionUtils.ts:110-134`).

### Ideas relevant to Ox

- Append-only event log with `$set` snapshots for rollback (fact: gemini-cli's scheme) makes cancellation rollback trivially correct but pushes reconstruction complexity to the reader; Ox's row-per-event SQLite model front-loads that structure instead (judgment: tradeoff is write-time ordering discipline vs read-time compaction).
- Deriving one identity chain (ACP ID → config → storage key → filename) from a single UUID (`config.ts:994-995`, `sessionUtils.ts:415-440`) avoids ID-translation layers but couples an editor-supplied protocol ID to on-disk layout; Ox's indirection through its own session IDs is more robust to client behavior (judgment).
- Deferring the model-turn durable write until stream consolidation (`geminiChat.ts:1634-1668`) trades partial-output crash recovery for write simplicity; Ox's incremental persistence must handle truncation on load but survives mid-stream crashes (judgment: neither is free).

## Unknowns and conflicts

- Not found: ACP session close/delete/list. Searched `packages/cli/src/acp/**` and `packages/cli/src/utils/sessionUtils.ts` for `closeSession`, `session_list`, `listSessions`; the only `listSessions` is CLI-local and not exposed as an ACP method. The SDK raises method-not-found.
- Not exercised: cancellation of an active prompt has no test in `packages/cli/src/acp/acpSession.test.ts` (searches for `cancel|abort|aborted` return no matches); rollback behavior is tested only in core (`chatRecordingService.test.ts`, `geminiChat` suites).
- Unverified boundary: `Config` is passed where `AgentLoopContext` is declared (`acpSessionManager.ts:131-137`); the interface (`packages/core/src/config/agent-loop-context.ts:19-46`) implies `Config` satisfies it, but the concrete conformance is in core wiring not traced line-by-line.
- Quirk: `pendingPrompt` is never cleared at the end of a completed prompt, so a `cancel` for an idle session aborts a stale controller rather than erroring (`acpSession.ts:198-205`, `:311-314`).
- External dependency: connection-level claims (concurrent dispatch, serialized write queue, no `dispose` call) are pinned to the installed `@agentclientprotocol/sdk` 0.16.1 `dist/acp.js`; it is not vendored in the repository, so citations reference the published artifact.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `packages/cli/src/gemini.tsx:874-876`; `packages/cli/src/acp/acpStdioTransport.ts:15-35`; `packages/cli/src/acp/acpRpcDispatcher.ts:21-104` (`GeminiAgent`) | Mode gating, transport, capability negotiation |
| Session management | `packages/cli/src/acp/acpSessionManager.ts:33-229`; `packages/cli/src/acp/acpSession.ts:65-96` (`Session`); `packages/cli/src/utils/sessionUtils.ts:409-568` (`SessionSelector`) | Session map, create/load, durable lookup |
| Concurrency | `packages/cli/src/acp/acpSession.ts:311-314`; `packages/core/src/core/geminiChat.ts:489,505-509`; SDK `dist/acp.js` (`Connection`) | Cancel-previous, per-chat send serialization, SDK dispatch |
| Persistence | `packages/core/src/services/chatRecordingService.ts:402-646,686-838`; `packages/core/src/config/storage.ts:230-233` | JSONL format, append/rewrite, naming |
| Event translation | `packages/cli/src/acp/acpSession.ts:412-485,650-657,659-943` | Stream-event → ACP notification mapping, tools, permissions |
| Replay/reconstruction | `packages/cli/src/acp/acpSession.ts:241-309` (`streamHistory`); `packages/core/src/utils/sessionUtils.ts:110-228`; `packages/core/src/core/client.ts:339-431` | Client replay vs model-context reconstruction |
| Cancellation/errors | `packages/cli/src/acp/acpSession.ts:486-548,899-941`; `packages/core/src/core/geminiChat.ts:791-821` | Abort handling, rollback, error mapping |
| LLM abstraction | `packages/core/src/core/contentGenerator.ts:39-61,210-421` (`ContentGenerator`, `createContentGenerator`); `packages/core/src/code_assist/server.ts:77-87`; `packages/core/src/core/baseLlmClient.ts:123-128` | Bespoke abstraction, provider SDK, Code Assist client, utility client |
| Model call path | `packages/core/src/core/client.ts:614-807`; `packages/core/src/core/turn.ts:284-414`; `packages/core/src/core/geminiChat.ts:480-675,870-1090,1354-1669` | Model selection, request assembly, streaming consumption, tool-call events |
| Provider boundary | `packages/core/src/code_assist/converter.ts:129-161`; `packages/core/src/code_assist/server.ts:470-522`; `packages/core/src/core/contentGenerator.ts:80-100,141-208`; `packages/cli/src/acp/acpSessionManager.ts:71-85` | Wire normalization, SSE transport, auth/credentials, ACP auth selection |
| Tool schemas | `packages/core/src/tools/tools.ts:423-432`; `packages/core/src/tools/tool-registry.ts:663-714`; `packages/core/src/tools/mcp-tool.ts:505-535`; `packages/core/src/core/geminiChat.ts:955-962` | FunctionDeclaration declarations, registry aggregation, request attachment |

## Research notes

- **Revision inspected:** `cfbcaa8df13ea4610bb379b377b56d62980c0032`
- **Primary evidence:** `packages/cli/src/acp/*` (all non-test modules; `acpResume.test.ts`, `acpSessionManager.test.ts` as corroboration), `packages/core/src/core/geminiChat.ts`, `packages/core/src/core/client.ts`, `packages/core/src/core/contentGenerator.ts`, `packages/core/src/core/turn.ts`, `packages/core/src/core/baseLlmClient.ts`, `packages/core/src/core/loggingContentGenerator.ts`, `packages/core/src/core/modelMappingContentGenerator.ts`, `packages/core/src/core/localLiteRtLmClient.ts`, `packages/core/src/code_assist/server.ts`, `packages/core/src/code_assist/converter.ts`, `packages/core/src/code_assist/oauth2.ts`, `packages/core/src/tools/tools.ts`, `packages/core/src/tools/tool-registry.ts`, `packages/core/src/tools/mcp-tool.ts`, `packages/core/src/services/chatRecordingService.ts`, `packages/core/src/config/config.ts`, `packages/core/src/config/storage.ts`, `packages/core/src/config/agent-loop-context.ts`, `packages/core/src/config/models.ts`, `packages/core/src/utils/sessionUtils.ts`, `packages/cli/src/config/config.ts`, `packages/cli/src/gemini.tsx`, `packages/cli/src/utils/sessionUtils.ts`
- **Relevant docs:** `packages/cli/src/acp/README.md` (module map, consistent with code)
- **Commands/tests run:** Read-only inspection; `@agentclientprotocol/sdk@0.16.1` fetched from npm CDN to pin SDK behavior (not vendored in the checkout)
- **Report confidence:** `high` — all protocol, session, and persistence paths were read at the pinned revision; SDK behavior is pinned to the exact dependency version, with the minor caveats listed under Unknowns.
