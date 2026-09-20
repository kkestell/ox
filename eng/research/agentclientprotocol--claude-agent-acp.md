---
project: "agentclientprotocol/claude-agent-acp"
repository: "https://github.com/agentclientprotocol/claude-agent-acp"
revision: "d421f56a6c43cde16d9a7531d08a750a5ef2f04a"
researched_at: "2026-09-19"
primary_language: "TypeScript"
implementation_form: "adapter"
process_model: "hybrid"
session_owner: "acp-layer"
durability: "wrapped-agent"
cross_session_concurrency: "concurrent"
same_session_concurrency: "serialized"
event_delivery: "mixed"
resume_strategy: "delegate"
overall_confidence: "high"
---

# agentclientprotocol/claude-agent-acp ACP architecture

## Executive summary

- **ACP boundary:** A single Node process speaks ACP over stdin/stdout NDJSON via `@agentclientprotocol/sdk` (`src/acp-agent.ts:10087` (`runAcp`)); it is an adapter that wraps the Claude Agent SDK, which spawns one native `claude` CLI subprocess per session (`src/acp-agent.ts:8202` (`query`), `src/acp-agent.ts:8078` (`pathToClaudeCodeExecutable`)).
- **Session model:** A "session" is an adapter-owned in-memory `Session` record (query handle, input pushable, turn queue, caches) keyed by the ACP session ID (`src/acp-agent.ts:694` (`Session`), `src/acp-agent.ts:8397`). The durable conversation is owned by the wrapped CLI; the adapter persists nothing.
- **Concurrency:** Sessions are independent (one consumer task + one CLI subprocess each) and run concurrently. Within a session, prompts are accepted and queued FIFO (`turnQueue`) and executed one at a time by the SDK (`src/acp-agent.ts:2707`–`2710`, `src/acp-agent.ts:551`–`555`).
- **Durability and replay:** Transcripts are persisted by the CLI, read back through SDK helpers (`getSessionMessages`, `listSessions`) (`src/resumed-session.ts:49`, `src/acp-agent.ts:2209`). `session/load` replays that transcript to the client as ACP notifications (`src/acp-agent.ts:6580` (`replaySessionHistory`)); `session/resume` resumes the conversation without replay.
- **Event flow:** One long-lived consumer task per session drains the SDK query stream and delivers every live update through a single awaited `sendUpdate` chokepoint (`src/acp-agent.ts:3107` (`runConsumer`), `src/acp-agent.ts:3182`); events originate on the CLI subprocess stream and are sent directly to the ACP client.
- **Notable uncertainty:** Transcript persistence timing, format, and flush points are internal to the wrapped CLI and are not observable in this repository; the adapter neither controls nor verifies them.

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `adapter` | ACP agent implemented over `@anthropic-ai/claude-agent-sdk`; the model loop and tools live in the wrapped CLI subprocess, not here | `package.json:69`, `src/acp-agent.ts:8202` (`query`), `src/acp-agent.ts:8078` (`pathToClaudeCodeExecutable`) |
| Process model | `hybrid` | One ACP server process; each session's runtime is a separate Claude CLI subprocess spawned by the SDK `query()` | `src/index.ts:88` (`runAcp`), `src/acp-agent.ts:8202`, `src/acp-agent.ts:6402` (comment: `query.close()` terminates the subprocess) |
| Session owner | `acp-layer` | The adapter owns the live `Session` object (turn queue, consumer, mode/model state); the wrapped CLI owns the conversation itself | `src/acp-agent.ts:1877` (`sessions` map), `src/acp-agent.ts:8397`–`8456`, `src/acp-agent.ts:6404`–`6409` (husk comment) |
| Durability | `wrapped-agent` | Only transcripts, persisted by the CLI; the adapter's only file write is an optional log | `src/resumed-session.ts:49` (`getSessionMessages`), `src/fork-session.ts:185` (`forkClaudeSession`), `src/index.ts:75` (log `appendFileSync`) |
| Cross-session concurrency | `concurrent` | Independent per-session consumers and subprocesses; no shared lock across sessions | `src/acp-agent.ts:3088` (`ensureConsumer`), `src/acp-agent.ts:6457` (`dispose`) |
| Same-session concurrency | `serialized` | Concurrent prompts are queued FIFO in `turnQueue` and settled in order; `promptQueueing` is advertised | `src/acp-agent.ts:2707`–`2710`, `src/acp-agent.ts:698`–`703`, `src/acp-agent.ts:2097` |
| Event delivery | `mixed` | Events come off the wrapped CLI's stdout stream (SDK async iterator) and are delivered by direct awaited `sessionUpdate` calls from the per-session consumer | `src/acp-agent.ts:3823`–`3825` (`query.next()`), `src/acp-agent.ts:3220` (`await this.client.sessionUpdate(...)`) |
| Resume strategy | `delegate` | Model context is rebuilt by the CLI via the SDK `resume` option; the adapter separately reconstructs the client view on `load` | `src/acp-agent.ts:8181` (`resume` option), `src/acp-agent.ts:2196` (`replaySessionHistory`) |

## System architecture

```text
ACP client (editor/IDE)
   │  NDJSON over stdio (ACP @agentclientprotocol/sdk 1.4.0)
   ▼
┌─────────────────────────────────────────────────────────────┐
│ Node ACP server process (src/index.ts → runAcp)             │
│                                                             │
│  ClaudeAcpAgent                                             │
│   ├─ sessions: { acpSessionId → Session }   (in-memory)     │
│   │     Session.query ────── SDK Query (async iterator)     │
│   │     Session.input ────── Pushable<SDKUserMessage>       │
│   │     Session.consumer ─── runConsumer task (per session) │
│   ▼                                                         │
│  @anthropic-ai/claude-agent-sdk (query())                   │
│   └─ spawns ONE `claude` native CLI subprocess per session  │
│        stdin:  pushed user messages / control requests      │
│        stdout: SDKMessage stream (echoes, deltas, results)  │
│        writes its own durable transcript (~/.claude store)  │
└─────────────────────────────────────────────────────────────┘
   │ live sessionUpdate notifications (direct, awaited)        ▲
   ▼                                                           │ replay (load)
ACP client                       transcript read back via getSessionMessages
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `runAcp` + `ClientConnection` | Bind ACP methods to `ClaudeAcpAgent`; wrap the connection as the `AcpClient` used for all outbound updates | process | none | `src/acp-agent.ts:10101`–`10138`, `src/acp-agent.ts:1657` (`ClientConnection`) |
| `ClaudeAcpAgent` | ACP method implementations; owns the session map | process | `sessions`, auth/provider state, `contextWindowCache` | `src/acp-agent.ts:1876`–`1915`, `src/acp-agent.ts:10193` |
| `Session` record | Per-session runtime state | session | query handle, input pushable, `turnQueue`, `toolUseCache`, `taskState`, modes/models, usage | `src/acp-agent.ts:694`–`1086`, `src/acp-agent.ts:8397`–`8456` |
| `runConsumer` task | Sole reader of the SDK stream; translates every message to ACP updates; settles turns | session (started on first prompt) | consumer-local scratch (streamed blocks, usage snapshots) | `src/acp-agent.ts:3102`–`3107`, `src/acp-agent.ts:3108`–`3152` |
| Claude CLI subprocess | Model loop, tools, permissions enforcement, transcript persistence | session | durable conversation store | `src/acp-agent.ts:8202` (`query`), `src/acp-agent.ts:6402` |
| `SettingsManager` | Watches Claude settings files per session | session | settings snapshot | `src/acp-agent.ts:7845`–`7849`, `src/acp-agent.ts:6417` (disposed on close) |

### ACP surface

The transport is NDJSON over stdio: `runAcp` bridges `process.stdin`/`process.stdout` into a web `ndJsonStream` and connects an ACP agent built with `@agentclientprotocol/sdk` (`src/acp-agent.ts:10087`–`10136`). Every ACP method is registered there: `initialize`, `session/new`, `session/load`, `session/fork` (unstable), `session/list`, `session/delete`, `session/resume`, `session/close`, `session/setMode`, `session/setConfigOption`, `authenticate`, provider methods, `logout`, `session/prompt`, `session/cancel` notification, plus the `_session/steering` and `_session/async_task/stop` extensions (`src/acp-agent.ts:10102`–`10135`). `initialize` advertises `loadSession: true`, `promptCapabilities.image/embeddedContext`, MCP http/sse, and a `sessionCapabilities` bundle (close/delete/fork/list/resume/subagents) (`src/acp-agent.ts:2082`–`2122`). Outbound client traffic goes through `AcpClient` (`src/acp-agent.ts:1629`–`1650`), implemented by `ClientConnection` over the connection's peer handle (`src/acp-agent.ts:1657`–`1697`).

The ACP layer is purely an adapter: no model calls, tool execution, or transcript storage exist in this repo. The SDK `query()` call spawns the CLI with `replay-user-messages` enabled and per-session options (`cwd`, `mcpServers`, `canUseTool`, hooks, `resume`) (`src/acp-agent.ts:8037`–`8205`).

### Runtime and process boundaries

Node's single event loop runs one ACP connection; each `Session` gets (a) a `Pushable` async iterable as prompt input (`src/utils.ts:8`–`46`), (b) an SDK `Query` — an async iterator over the CLI subprocess's stdout (`src/acp-agent.ts:3823`–`3825`), and (c) one long-lived consumer task started lazily on the first prompt and kept alive for the session so between-turn/background output still streams (`src/acp-agent.ts:3088`–`3100`, `src/acp-agent.ts:774`–`777`). The consumer holds exactly one in-flight `query.next()` (`pendingNext`) and races it against a cancel signal (`src/acp-agent.ts:3812`–`3834`). There is no thread pool, daemon, or shared queue; the only shared cross-session state is the module-level `contextWindowCache` (a read-through cache, `src/acp-agent.ts:10193`) and the serialized `providerUpdate` promise (`src/acp-agent.ts:1888`).

## LLM abstraction and integration

### Abstraction

The adapter does not contain an LLM client or a bespoke model abstraction; it wraps a native executable through the official provider SDK. Production code calls `query({prompt, options})` from `@anthropic-ai/claude-agent-sdk` (0.3.274), which spawns one `claude` subprocess per session and returns a `Query` async iterator (`package.json:69`, `src/acp-agent.ts:57`–`86`, `src/acp-agent.ts:8202`–`8205`). The executable is resolved from the SDK's platform-specific optional dependency or `CLAUDE_CODE_EXECUTABLE` and passed as `pathToClaudeCodeExecutable` (`src/acp-agent.ts:1438`–`1476` (`claudeCliPath`), `src/acp-agent.ts:8078`). The model loop, API request construction, tool execution, and transcript storage are therefore outside this repository — the boundary is the SDK `query()`/CLI subprocess. `@anthropic-ai/sdk` (0.126.0) is a devDependency imported only for wire-format types (`package.json:73`, `src/acp-agent.ts:138`–`139`, `src/tools.ts:32`–`64`); no `fetch`, `https.request`, `axios`, or `new Anthropic(...)` call exists in `src/` (searched). The adapter's own modules (`src/session-model.ts`, `src/session-effort.ts`, `src/session-mode.ts`, `src/tools.ts`) are translation/presentation layers over SDK-reported state, not a provider abstraction.

### Integration path

1. **Message conversion.** `promptToClaude` maps ACP prompt chunks (text, `resource_link`, `resource`, `image`) into an `SDKUserMessage` stamped `origin: {kind: "human"}`, then the message is pushed onto the session's `Pushable<SDKUserMessage>` input and a `Turn` is queued (`src/acp-agent.ts:9119`–`9202`, `src/acp-agent.ts:2658`–`2660`, `src/acp-agent.ts:2707`–`2710`). No model-specific content is constructed here.
2. **Provider selection.** At session creation `resolveProviderConfig()` resolves the active provider (or `null` for native routing), `createEnvForProvider()` maps it to Claude Code environment variables, and that env is baked into the `query()` options (`src/acp-agent.ts:7990`–`8032`, `src/acp-agent.ts:2549`–`2577`, `src/acp-agent.ts:8812`–`8862`).
3. **Model selection.** `getAvailableModels` filters `initializationResult.models` through the user allowlist and, when the resolved pin differs, issues `query.setModel(currentModel.value)` over the SDK control channel; priority is `ANTHROPIC_MODEL` env → `settings.model` → resumed transcript model → `models[0]` (`src/session-model.ts:348`–`466`, `src/acp-agent.ts:8270`–`8279`, `src/session-model.ts:360`–`407`).
4. **Request construction.** The adapter builds only `Options` — system-prompt preset `claude_code`, tool preset, MCP servers, hooks, permission mode, `resume`/`forkSession`, `sessionId`, env, `canUseTool` — and hands it to `query()`; the actual API request body is assembled inside the CLI (`src/acp-agent.ts:8037`–`8205`).
5. **Streaming.** The consumer holds one in-flight `session.query.next()` and reads `SDKMessage`s (`src/acp-agent.ts:3823`–`3834`). `stream_event` (`SDKPartialAssistantMessage`) deltas are converted by `streamEventToAcpNotifications` → `toAcpNotifications` into `agent_message_chunk`/`thought_chunk`/`tool_call` updates; consolidated `assistant`/`user` messages are diffed against streamed content (`src/acp-agent.ts:5367`–`5515`, `src/acp-agent.ts:9911`–`10034`, `src/acp-agent.ts:9472`).
6. **Tool-call handling.** Tool definitions and execution remain in the CLI. The adapter supplies `canUseTool` to gate permissions and maps tool names/inputs/results to ACP `tool_call`/`tool_call_update` (`src/acp-agent.ts:8058`, `src/acp-agent.ts:7024`–`7219`; tool-name and input/output types imported from `@anthropic-ai/claude-agent-sdk/sdk-tools.js` in `src/tools.ts:31`).
7. **Back to runtime events.** The `result` message reconciles usage/stop reasons and the trailing idle settles the turn, which resolves the ACP `PromptResponse` (`src/acp-agent.ts:4790`–`4993`, `src/acp-agent.ts:5517`–`5890`).

Because this is an adapter, the trace ends at the delegated boundary: everything from the `query()` call inward (HTTP request construction, provider retries, model loop, tool dispatch, transcript writes) is not visible in this checkout.

### Provider and tool boundary

- **Provider-specific code** is limited to routing: `ProviderConfig`, `SUPPORTED_PROTOCOLS` (`anthropic`/`bedrock`/`vertex`), and `PROVIDER_ID` live in `src/acp-agent.ts:1222`–`1255`; `createEnvForProvider` maps a config to `ANTHROPIC_BASE_URL`/`CLAUDE_CODE_USE_BEDROCK`/`CLAUDE_CODE_USE_VERTEX` plus headers and credential placeholders (`src/acp-agent.ts:8812`–`8862`); `gatewayRequestToProviderConfig` adapts legacy gateway auth (`src/acp-agent.ts:8790`–`8804`).
- **Configuration/credentials:** the adapter never reads credential material. Identity is probed by shelling out to `claude auth status --json` (`src/acp-agent.ts:2381`, `src/auth-status.ts:50`) and `logout` runs `claude auth logout` (`src/acp-agent.ts:2601`); the CLI store (keychain/config dir) is authoritative. Provider env placeholders (`ANTHROPIC_AUTH_TOKEN: "acp-proxy"`, `AWS_BEARER_TOKEN_BEDROCK: "acp-proxy"`) suppress the CLI's own login checks (`src/acp-agent.ts:8837`, `src/acp-agent.ts:8860`). The `--hide-claude-auth` subscription guard lives in `src/hide-claude-auth.ts`.
- **Provider selection** is process-scoped and mutually exclusive: `unstable_setProvider` (`providers/set`) sets `providerConfig`, `unstable_disableProvider` clears it, and the legacy gateway `authenticate` populates `gatewayAuthRequest`; `resolveProviderConfig` falls back to env-derived defaults, and `unstable_listProviders` exposes the current choice (`src/acp-agent.ts:2484`–`2534`, `src/acp-agent.ts:2540`–`2577`, `src/acp-agent.ts:2464`–`2476`). Changing the provider closes and recreates every live query with `resume` so later turns inherit the new env (`src/acp-agent.ts:8475`–`8524`).
- **Multi-provider:** yes at the transport/routing layer (Anthropic, Bedrock, Vertex); the model loop and tool set are Claude-only, so this is provider *routing*, not a multi-vendor model abstraction.
- **Request/response normalization:** only SDK-message → ACP-update normalization exists here (`toAcpNotifications`, `streamEventToAcpNotifications`); normalization of Anthropic/Bedrock/Vertex API payloads is inside the CLI/SDK — **Not found** in this repository.
- **Tool schemas:** defined by the CLI/SDK via `tools: {type: "preset", preset: "claude_code"}` (`src/acp-agent.ts:7946`–`7948`); the adapter imports the SDK's tool input/output types for rendering and permission presentation (`src/tools.ts:31`, `src/acp-agent.ts:8058`).

### Limits

- The actual model-call site — HTTP request construction, provider retries, and response streaming against the Anthropic/Bedrock/Vertex APIs — is inside the bundled `claude` binary and is not observable here. Checked: `src/` for `fetch(`, `https.request`, `axios`, `new Anthropic(` (none outside type imports); `src/acp-agent.ts:8202`–`8205`.
- The model loop, tool dispatch/execution, and tool schemas are delegated; the adapter sees only `SDKMessage`s and permission callbacks. Checked: `src/acp-agent.ts` (message switch), `src/tools.ts` (rendering only).
- How the SDK/CLI resolves a concrete model from the `default` alias or a Bedrock/Vertex model id is inferred from SDK-reported `ModelInfo`; the resolver internals are not visible. Checked: `src/session-model.ts`.
- Credential storage format/location (keychain vs config dir) and any provider fallback/retry policy are CLI-internal. Checked: `src/auth-status.ts`, `src/hide-claude-auth.ts` (status reads only).
- Whether the CLI coalesces/reorders queued pushed inputs is asserted from observed behavior, not a documented contract (see Unknowns).

## Session model

### Identity and ownership

The ACP session ID is the single key everywhere: it indexes `ClaudeAcpAgent.sessions` (`src/acp-agent.ts:1877`), and — except for forks — it is also the Claude CLI session ID passed as the SDK `resume` target (`src/acp-agent.ts:7815`–`7824`, `src/acp-agent.ts:2830`–`2832`). A fresh `session/new` generates a random UUID and hands it to the SDK as `options.sessionId` (`src/acp-agent.ts:8192`–`8195`). Mapping is one-to-one between ACP session ID, adapter `Session` object, and CLI conversation; forks are the exception (`unstable_forkSession` creates a new CLI conversation with a new ID, `src/fork-session.ts:185`–`234`). Subagent child sessions are synthetic ACP IDs of the form `<parent>:replay-subagent:<toolUseId>` announced as child sessions; they have no adapter-side runtime of their own (`src/acp-agent.ts:6670`).

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | `newSession` → `createSession`: validate `cwd`, build options, `query()` spawns the CLI, await `initializationResult`, register the `Session` | None from the adapter; the CLI creates its conversation lazily (a never-prompted session is "not resumable" per integration test) | Errors before registration discard the query, dispose settings, end input | `src/acp-agent.ts:2154`–`2166`, `src/acp-agent.ts:8202`–`8256`, `src/acp-agent.ts:8464`–`8467`, `src/tests/session-load.test.ts:21` |
| Load/resume | `getOrCreateSession` returns the live session if the fingerprint (`cwd`, mcpServers) matches, else tears it down and recreates with `resume: sessionId`; `loadSession` additionally replays history to the client; `resumeSession` does not | CLI resumes its stored transcript | Resume of an unknown conversation maps SDK errors to `resourceNotFound` | `src/acp-agent.ts:2189`–`2206`, `src/acp-agent.ts:2177`–`2187`, `src/acp-agent.ts:7702`–`7751`, `src/acp-agent.ts:8214`–`8225` |
| Prompt | Build `SDKUserMessage`, enqueue a `Turn`, push to the input pushable, ensure the consumer, await the turn's deferred | None directly; CLI appends to its transcript | Dead-stream prompts reject with `SESSION_ENDED_MESSAGE` | `src/acp-agent.ts:2623`–`2713`, `src/acp-agent.ts:2643`–`2645` |
| Cancel | Set `cancelled`, settle queued turns immediately, seed orphan accounting, `query.interrupt()`, arm a 30 s force-cancel backstop; the consumer settles the active turn at the trailing idle or on abort | None from the adapter | Backstop aborts the cancel signal to force a "cancelled" settle when the SDK wedges | `src/acp-agent.ts:6095`–`6374`, `src/acp-agent.ts:6309`–`6322`, `src/acp-agent.ts:340` |
| Close/delete | `teardownSession`: cancel, abort cancel signal, `closeQueryStream` (dispose settings, end input, `query.close()` → subprocess exit), remove from map; `deleteSession` additionally calls SDK `deleteSession` | Delete removes the CLI-side transcript | Idempotent via `queryClosed`; closed sessions become addressable "husks" that reject further prompts | `src/acp-agent.ts:6424`–`6454`, `src/acp-agent.ts:6410`–`6420`, `src/acp-agent.ts:6469`–`6477`, `src/acp-agent.ts:6404`–`6409` |

### Durable representation

All durable state is the wrapped CLI's transcript. The adapter reads it through SDK helpers: `getSessionMessages(sessionId)` returns ordered `SessionMessage` records (user/assistant entries with `uuid`, `parentUuid` chain, `parent_tool_use_id`, tool_use/tool_result blocks, compaction summary messages) (`src/resumed-session.ts:49`, `src/acp-agent.ts:6586`); `listSessions({dir})` returns session summaries and last-modified times (`src/acp-agent.ts:2209`–`2219`); `deleteSession` removes it (`src/acp-agent.ts:6475`); forking reads full history including inactive branches via `importSessionToStore` into a throwaway `SessionStore` (`src/fork-session.ts:68`–`82`). Ordering keys are the transcript's `uuid`/`parentUuid` chain; the adapter additionally maps ACP message ids (Anthropic API `msg_…` ids) to transcript uuids in memory (`src/acp-agent.ts:1049`–`1066`). Commit timing and format are CLI-internal — **Not found** in this repo (searched: `grep` for `writeFile|appendFile|createWriteStream|mkdir` across `src/`; only the optional agent log in `src/index.ts:75` writes files). Stored history is authoritative for the conversation; the adapter's in-memory state (turn queue, tool caches, task lists, mode/model selections, usage tallies) is a per-session cache rebuilt at creation. What is deliberately not persisted by the adapter: everything — including titles it generates via a CLI control request (`src/session-titles.ts:4`–`13`; the durable title visible in `listSessions` comes back from the CLI store).

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | Independent `Session` objects, consumers, and CLI subprocesses; no shared lock (only the awaited `providerUpdate` chain and read-only caches are shared) | `src/acp-agent.ts:3088`–`3100`, `src/acp-agent.ts:8475`–`8481`, `src/acp-agent.ts:10193` |
| Two prompts in the same session | `serialized` | Both are accepted; each becomes a `Turn` in the FIFO `turnQueue`; the SDK processes pushed inputs in order and echoes them back in submission order, and the consumer settles the head first | `src/acp-agent.ts:2707`–`2710`, `src/acp-agent.ts:551`–`555`, `src/acp-agent.ts:5528`–`5534` |
| Load/resume during an active prompt | `unknown` (unguarded) | `getOrCreateSession` returns the live session when the fingerprint matches and `loadSession` then replays history with no coordination against the running consumer — replay notifications can interleave with live turn output | `src/acp-agent.ts:7712`–`7727`, `src/acp-agent.ts:2196` (no guard/lock around replay) |
| Delete/close during an active prompt | settled/cancelled | `teardownSession` calls `cancel()` first, aborts `cancelController` to wake a wedged consumer, closes the stream, then removes the session; queued turns are settled by `cancel()`, the active turn by the abort path | `src/acp-agent.ts:6424`–`6453`, `src/acp-agent.ts:3837`–`3898` |
| Cancellation isolation | per session | `cancel()` looks up only `this.sessions[params.sessionId]`; `cancelController`/`forceCancelTimer` are per-session; connection-close aborts map to `cancel()` for the affected prompt only | `src/acp-agent.ts:6095`–`6099`, `src/acp-agent.ts:839`–`849`, `src/acp-agent.ts:10067`–`10085` |

Additional coupling: all sessions share one event loop, so any awaited `sessionUpdate` send in one consumer delays that consumer only — but `sendUpdate` is awaited inline in the consumer loop (`src/acp-agent.ts:3220`), so a slow ACP client backpressures the session's own message draining; the SDK stream is drained one message at a time (`pendingNext`, `src/acp-agent.ts:3818`–`3825`) and the `Pushable` input queue is unbounded (`src/utils.ts:8`–`46`). Provider changes serialize across sessions: `enqueueProviderUpdate` waits for all submitted turns, closes every query, and resumes each session under the same ID (`src/acp-agent.ts:8475`–`8481`).

## Event and data flow

### New prompt: ACP client to live response

1. ACP `session/prompt` arrives; `runPromptWithCancellation` binds the request's abort signal so a transport-level cancel invokes `agent.cancel` (`src/acp-agent.ts:10067`–`10085`).
2. `prompt()` awaits any provider update, resolves the session, fires a non-blocking CLI auth probe, handles sign-out respawn, rejects dead-stream sessions, and runs the optional subscription guard (`src/acp-agent.ts:2623`–`2652`).
3. `promptToClaude` converts ACP chunks (text, resource links/embedded context, images) into an `SDKUserMessage` stamped with a fresh uuid and `origin: {kind: "human"}` (`src/acp-agent.ts:9119`–`9202`); local-only slash commands (`/context`, `/heapdump`, `/extra-usage`) are flagged (`src/acp-agent.ts:2662`–`2666`).
4. A `Turn` with a deferred `PromptResponse` is appended to `session.turnQueue`, the message is pushed into the SDK input, and the consumer is started if needed; `prompt()` then awaits the deferred (`src/acp-agent.ts:2682`–`2712`).
5. The CLI echoes the pushed user message back on its output stream; the consumer matches the echo uuid and promotes the turn to active, handing off any previous active turn first (`src/acp-agent.ts:5528`–`5605`, `src/acp-agent.ts:3371`–`3401`).
6. Streaming: `stream_event` deltas become `agent_message_chunk`/`thought_chunk`/`tool_call`/`tool_call_update` notifications via `streamEventToAcpNotifications`, plus mid-stream `usage_update` on `message_start`/`message_delta` (`src/acp-agent.ts:5367`–`5515`, `src/acp-agent.ts:9911`). The consolidated `assistant` message later diffs assembled blocks against what already streamed and forwards only the remainder (`src/acp-agent.ts:3131`–`3142`). Tool permission asks go through `canUseTool` → `requestPermissionFromClient`, emitting the referenced `tool_call` eagerly if needed (`src/acp-agent.ts:7024`, `src/acp-agent.ts:6955`–`7013`).
7. The `result` message reconciles fast mode, promotes echo-less turns (`ensureActiveTurn` with orphan accounting), accumulates usage, emits a final `usage_update`, and records the stop reason; a trailing `session_state_changed: idle` settles the turn (`src/acp-agent.ts:4790`–`4993`, `src/acp-agent.ts:4215`–`4334`).
8. `settleActive` resolves the turn's deferred, so `prompt()` returns `PromptResponse {stopReason, usage, _meta}`; turns that spawned live background subagents are held open until the subagents drain (`src/acp-agent.ts:3650`–`3693`, `src/acp-agent.ts:3627`–`3645`).

### Durable history to ACP client

1. `session/load` reads the transcript once via `readResumedSession` (`getSessionMessages`) for both the model hint and replay (`src/resumed-session.ts:40`–`58`, `src/acp-agent.ts:2192`).
2. `getOrCreateSession`/`createSession` recreate the SDK query with `resume: sessionId` so the wrapped CLI restores its own conversation (`src/acp-agent.ts:7733`–`7744`, `src/acp-agent.ts:8181`).
3. `replaySessionHistory` walks the persisted messages in transcript order and, for each, emits ACP notifications through the same `toAcpNotifications` converter used live: user messages are stripped of local-command metadata, synthetic login messages are skipped, synthetic usage-limit messages are restored as typed failures, compaction summaries are materialized as compaction entities, and tool_use/tool_result pairs become tool_call/tool_call_update via a fresh `toolUseCache` (`src/acp-agent.ts:6580`–`6825`).
4. With the subagent capability, child sessions are announced (`subagent_spawned`) in lineage order and their terminal states reconstructed from persisted tool_results (`src/acp-agent.ts:6624`–`6683`, `src/acp-agent.ts:6827`–`6849`).
5. Only after replay does the adapter send `available_commands_update` and start MCP OAuth (deferred by `setTimeout`) (`src/acp-agent.ts:2199`–`2203`). `session/resume` performs steps 1–2 and 5 but **no replay** — the client receives no history (`src/acp-agent.ts:2177`–`2187`). Events that cannot be reconstructed: live `stream_event` granularity (replay sends consolidated content), task_started/task_updated lifecycle frames (inferred from tool_results), and session state transitions.

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `SDKUserMessage` pushed to input; `Turn` in queue | user-message echo suppressed (client already shows it) | CLI transcript user record | CLI-side, not adapter-controlled | `src/acp-agent.ts:2658`–`2709`, `src/acp-agent.ts:5533`–`5534` |
| Assistant text | `stream_event` deltas + consolidated `assistant` message | `agent_message_chunk` (deduped against streamed deltas) | CLI transcript assistant record | CLI-side | `src/acp-agent.ts:5405`–`5437`, `src/acp-agent.ts:3131`–`3142` |
| Reasoning/thought | `thinking_delta` / thinking blocks | `thought_chunk` (per capabilities) | CLI transcript thinking block | CLI-side | `src/acp-agent.ts:5418`–`5422`, `src/acp-agent.ts:9581` |
| Tool call | `tool_use` block; eager emission from `canUseTool` | `tool_call` (+ `tool_call_update` refinements) | CLI transcript tool_use block | CLI-side | `src/acp-agent.ts:895`–`905`, `src/acp-agent.ts:7085`–`7101` |
| Tool result | `tool_result` block in replayed user message | `tool_call_update` (completed/failed) | CLI transcript user record with tool_result | CLI-side | `src/acp-agent.ts:890`–`903`, `src/fork-session.ts:97`–`105` |
| Completion/failure | `result` message; trailing `session_state_changed: idle` | turn settles; `usage_update`; typed failure updates | CLI transcript (result/usage not adapter-persisted) | settle before `prompt()` returns | `src/acp-agent.ts:4790`–`4993`, `src/acp-agent.ts:3650`–`3693` |

Persistence happens in the wrapped CLI process, outside the adapter's control; the adapter never writes durable records itself, so there is no adapter-side ordering between notification and commit. **Inference:** because `getSessionMessages` returns a session's messages only after the CLI has written them (a never-prompted session is neither listable nor resumable, `src/tests/session-load.test.ts:21`), transcript records are committed by the CLI as turns produce them, but the exact point relative to the ACP response is not observable here.

### Subsequent-prompt reconstruction

The adapter does not rebuild model context. On resume/load, the SDK query is created with `resume: sessionId`, and the wrapped CLI loads its own transcript into the model context (`src/acp-agent.ts:8181`, `src/acp-agent.ts:7741`). The adapter's contribution is limited to seeding metadata: the resumed model id read from the transcript (`src/resumed-session.ts:18`–`36`), mode/model/config-option catalogs negotiated with the fresh query (`src/acp-agent.ts:8270`–`8324`), and a `resumedModelHint` to avoid a slow context probe (`src/acp-agent.ts:7834`–`7841`). A later prompt therefore continues the stored conversation without any adapter-side message replay into the model. Lossy aspects: adapter-side state such as task lists, effort pins, and fast-mode intent starts from the CLI's own restored values, not from adapter memory.

### Ordering, cancellation, failure, and backpressure

- **Ordering:** one consumer per session processes the SDK stream sequentially; turn attribution relies on the SDK echoing queued user messages in submission order, with two orphan-accounting lanes (a count and a per-uuid lifecycle map) covering cancelled queued turns whose results still arrive (`src/acp-agent.ts:551`–`555`, `src/acp-agent.ts:720`–`764`).
- **Cancellation:** `cancel()` settles queued turns immediately (no usage), settles held/settling turns inline with their captured outcome, then `await query.interrupt()`; the active turn normally settles at the interrupt's trailing idle. A 30 s force-cancel backstop aborts a wake-up signal so a wedged `query.next()` still yields a "cancelled" response (`src/acp-agent.ts:6145`–`6212`, `src/acp-agent.ts:6324`, `src/acp-agent.ts:6309`–`6322`, `src/acp-agent.ts:3837`–`3881`).
- **Failure:** stream end (done or error) settles the active turn (cancelled if a cancel is pending), rejects still-queued turns with `SESSION_ENDED_MESSAGE`, closes the stream, and marks `queryClosed`; the session becomes a husk that rejects future prompts but remains addressable for close/delete (`src/acp-agent.ts:3926`–`3967`, `src/acp-agent.ts:2640`–`2645`, `src/acp-agent.ts:6404`–`6409`). Unsettleable turns are detected via the owed-trailing-idle ledger ("SDK went idle without emitting a result", `src/acp-agent.ts:4304`–`4330`).
- **Backpressure:** no queues are bounded (`Pushable`, `turnQueue`); every notification is awaited in the consumer loop, so a slow client stalls that session's stream draining (the single in-flight `query.next()` cannot advance), but other sessions are unaffected. Permission requests race the tool's abort signal so a client that ignores `$/cancel_request` still releases the tool (`src/acp-agent.ts:6955`–`6976`).

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | `sessions` map keyed by ACP session ID | `src/acp-agent.ts:1877`, `src/acp-agent.ts:8397` |
| Concurrent work across sessions | `yes` | Independent consumers + CLI subprocesses; provider updates are the one serialized cross-session operation | `src/acp-agent.ts:3088`, `src/acp-agent.ts:8475`–`8481` |
| Same-session prompt exclusion | `yes` | Enforced by FIFO queueing (advertised `promptQueueing`), not rejection; turns settle strictly in order | `src/acp-agent.ts:2097`, `src/acp-agent.ts:2707`–`2710` |
| Durable sessions | `yes` | CLI-owned transcripts; adapter delegates all reads/writes | `src/resumed-session.ts:49`, `src/fork-session.ts:80` |
| Session list | `yes` | `session/list` → SDK `listSessions({dir})`; entries without `cwd` skipped | `src/acp-agent.ts:2208`–`2224` |
| Session load/resume | `yes` | `loadSession` (with replay) and `resumeSession` (without); fingerprint mismatch triggers teardown+recreate | `src/acp-agent.ts:2189`–`2206`, `src/acp-agent.ts:7712`–`7727` |
| History replay to ACP client | `yes` | On `loadSession` only, via `replaySessionHistory`; `resumeSession` sends nothing | `src/acp-agent.ts:6580`, `src/acp-agent.ts:2177`–`2187` |
| Prior history reused by model | `yes` | SDK `resume`/`forkSession` options; reconstruction is CLI-internal | `src/acp-agent.ts:8181`, `src/fork-session.ts:185` |
| Prompt cancellation | `yes` | `session/cancel` + `$/cancel_request` mapping + force-cancel backstop | `src/acp-agent.ts:6095`, `src/acp-agent.ts:10067`–`10085` |
| Tool-call progress updates | `yes` | Streamed `tool_call`/`tool_call_update`, input refinement, eager permission-time emission, `tool_progress` handling | `src/acp-agent.ts:896`–`905`, `src/acp-agent.ts:5898` |
| Partial-output persistence | `unknown` | Persistence is CLI-internal; the adapter neither stores partials nor observes whether the CLI does | searched `src/` for persistence calls; none outside logging |
| Recovery after process restart | `partial` | Conversations survive via CLI transcripts and can be re-attached with load/resume; adapter state (queue, modes catalogs, usage) is rebuilt, and in-flight turns are lost | `src/acp-agent.ts:2177`–`2206`, `src/acp-agent.ts:6404`–`6409` |

## Design assessment

### Strengths

- Single-reader discipline: exactly one consumer task per session drains the SDK stream and owns a single `sendUpdate` chokepoint, which keeps dedupe, delivery tracking, and turn settlement coherent instead of scattering them across prompt handlers (`src/acp-agent.ts:3102`–`3107`, `src/acp-agent.ts:3153`–`3160`).
- Prompt queueing is first-class: concurrent prompts are accepted and serialized with an explicit orphan-accounting design for cancelled queue entries, so a cancel followed by a new prompt cannot misattribute results (`src/acp-agent.ts:2707`–`2710`, `src/acp-agent.ts:3457`–`3520`).
- Cancellation has a bounded worst case: the interrupt is backed by a force-cancel timer that guarantees the ACP prompt response even when the wrapped CLI wedges (`src/acp-agent.ts:6309`–`6322`).
- Replay reuses the exact live conversion path (`toAcpNotifications`), so loaded history and live output render identically (`src/acp-agent.ts:6807`–`6825`).

### Tradeoffs and limitations

- The adapter is deeply coupled to undocumented CLI stream behavior — echo ordering, `command_lifecycle` frames, idle cadence, interrupt receipts — and maintains two parallel orphan-accounting lanes plus an idle-debt ledger to compensate; the code itself flags these as observed, not contractual invariants (`src/acp-agent.ts:3461`–`3476`, `src/acp-agent.ts:6325`–`6339`).
- `loadSession` on a session with queued work is unguarded; replay notifications can interleave with live turn output since both go through awaited sends on one connection (`src/acp-agent.ts:2196`, `src/acp-agent.ts:3220`).
- Unbounded queues plus awaited per-notification sends mean a slow or stalled client can park a session's consumer indefinitely while its CLI subprocess keeps buffering output (`src/acp-agent.ts:3818`–`3834`, `src/utils.ts:8`–`46`).
- A dead query stream permanently bricks the session object ("husk"); recovery requires the client to open a new session, and prompts on the old ID fail even though the durable transcript could be resumed (`src/acp-agent.ts:2640`–`2645`, `src/acp-agent.ts:1489`).

### Ideas relevant to Ox

- The per-session consumer-with-chokepoint pattern maps cleanly onto Ox's `acp.rs` event conversion: one task owning the translation from runtime events to ACP updates (and to persisted events) makes dedupe and turn settlement auditable in one place; the tradeoff is that everything — including failure ledgering — funnels through that task, which here grew into 7000+ lines of compensating state (researcher judgment; fact: `runConsumer` spans `src/acp-agent.ts:3107`–`6094`).
- Delegating durability to the runtime (here, the CLI) removed all storage code from the adapter, but it also made commit timing unobservable and recovery semantics dependent on undocumented CLI behavior; Ox's SQLite-owned events keep that control inside the process.
- The force-cancel backstop (grace timer + separate wake-up signal that does not kill the subprocess) is a cheap, concrete pattern for guaranteeing an ACP prompt response when a wrapped runtime stops yielding (`src/acp-agent.ts:839`–`849`).
- Queue-don't-reject for same-session prompts (with an advertised capability so clients know) is a deliberate UX choice Ox could adopt or explicitly reject; the cost is visible here in the orphan-accounting machinery needed when queued turns are cancelled (`src/acp-agent.ts:2097`, `src/acp-agent.ts:6145`–`6212`).

## Unknowns and conflicts

- Transcript persistence internals (format on disk, flush/fsync points, whether partial streamed output is persisted before the consolidated message) are **Not found** — they live in `@anthropic-ai/claude-agent-sdk` and the CLI, not this repo. Searched: `grep -rn "writeFile|appendFile|createWriteStream|mkdir" src/` (only the log file in `src/index.ts:75`); inspected `src/resumed-session.ts`, `src/fork-session.ts` (read/delegation only).
- Whether the SDK `Query` ever coalesces or reorders queued pushed inputs is asserted from observed CLI behavior in comments, not a documented wire contract (`src/acp-agent.ts:3471`–`3476`); the adapter hedges with tripwire logging.
- Behavior of `loadSession` concurrent with an active prompt is untested and unguarded (see Concurrency table); no test exercises it.
- The integration tests that would corroborate load/resume (`src/tests/session-load.test.ts:20`) are skipped unless `RUN_INTEGRATION_TESTS=true`; they were read, not run.
- Docs vs code: README's feature list matches the implemented surface; no disagreement found.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `src/acp-agent.ts:10087` (`runAcp`), `src/index.ts:88`, `src/acp-agent.ts:1657` (`ClientConnection`) | Transport, method binding, outbound update path |
| Session management | `src/acp-agent.ts:2154` (`newSession`), `src/acp-agent.ts:7782` (`createSession`), `src/acp-agent.ts:7702` (`getOrCreateSession`), `src/acp-agent.ts:694` (`Session`) | Session creation, ID mapping, in-memory state |
| Concurrency | `src/acp-agent.ts:3088` (`ensureConsumer`), `src/acp-agent.ts:2707`–`2710` (turn queue), `src/acp-agent.ts:8475` (`enqueueProviderUpdate`) | Cross- and same-session serialization mechanisms |
| Persistence | `src/resumed-session.ts:49` (`getSessionMessages`), `src/acp-agent.ts:2209` (`listSessions`), `src/fork-session.ts:74`–`82`, `src/acp-agent.ts:6475` (`deleteSession`) | All durable access is delegated to the SDK |
| Event translation | `src/acp-agent.ts:3107` (`runConsumer`), `src/acp-agent.ts:3182` (`sendUpdate`), `src/acp-agent.ts:5367` (`stream_event`), `src/acp-agent.ts:9472` (`toAcpNotifications`), `src/acp-agent.ts:9911` (`streamEventToAcpNotifications`) | Live SDK→ACP conversion and delivery |
| LLM integration | `src/acp-agent.ts:8202` (`query`), `src/acp-agent.ts:8078` (`pathToClaudeCodeExecutable`), `src/acp-agent.ts:8812` (`createEnvForProvider`), `src/session-model.ts:411` (`query.setModel`), `src/tools.ts:31` (SDK tool types) | Model loop delegated to the wrapped CLI; provider routing and model selection are adapter-side |
| Replay/reconstruction | `src/acp-agent.ts:6580` (`replaySessionHistory`), `src/acp-agent.ts:2189` (`loadSession`), `src/resumed-session.ts:40` (`readResumedSession`) | Durable history → client, model-context delegation |
| Cancellation/errors | `src/acp-agent.ts:6095` (`cancel`), `src/acp-agent.ts:6410` (`closeQueryStream`), `src/acp-agent.ts:3837`–`3967` (abort/done paths), `src/acp-agent.ts:10067` (`runPromptWithCancellation`) | Cancel semantics, stream death, backstop |

## Research notes

- **Revision inspected:** `d421f56a6c43cde16d9a7531d08a750a5ef2f04a` (v0.79.0 release commit, 2026-09-17)
- **Primary evidence:** `src/acp-agent.ts` (all major paths), `src/index.ts`, `src/utils.ts`, `src/resumed-session.ts`, `src/fork-session.ts`, `src/session-titles.ts`, `src/session-model.ts`, `src/session-effort.ts`, `src/tools.ts`, `src/auth-status.ts`, `src/hide-claude-auth.ts`, `src/acp-agent.ts` type block (`Turn`/`Session`), `src/tests/session-load.test.ts`, `src/tests/acp-agent.test.ts` (structure only)
- **Relevant docs:** `README.md` (adapter framing, capability negotiation), `package.json` (SDK/ACP versions)
- **Commands/tests run:** read-only inspection (`git rev-parse`, `grep`, file reads); no tests executed (unit suite mocks the SDK; integration suite requires network credentials and `RUN_INTEGRATION_TESTS`)
- **Report confidence:** `high` — every traced path is directly cited from the pinned revision's production code, and the adapter/wrapped-agent boundary is explicit throughout
