---
project: "docker/docker-agent"
repository: "https://github.com/docker/docker-agent"
revision: "f18ba0a95cf394e5020758cdbcb2b8b3cd82d199"
researched_at: "2026-09-19"
primary_language: "go"
implementation_form: "native-agent"
process_model: "single-process"
session_owner: "acp-layer"
durability: "embedded-database"
cross_session_concurrency: "concurrent"
same_session_concurrency: "cancel-previous"
event_delivery: "mixed"
resume_strategy: "reconstruct"
overall_confidence: "high"
---

# docker/docker-agent ACP architecture

## Executive summary

- **ACP boundary:** A stdio JSON-RPC server (`docker-agent serve acp`) built on `coder/acp-go-sdk`. `pkg/acp/Agent` implements the ACP agent interface natively in-process: handlers call the project's own `pkg/runtime` agent loop directly; there is no wrapped executable or SDK boundary (`pkg/acp/run.go:16-46`, `pkg/acp/agent.go:30-44`).
- **Session model:** An ACP session ID *is* the runtime session UUID. `Session` (`pkg/acp/agent.go:47-60`) holds the ACP id, the in-memory `session.Session`, a dedicated `LocalRuntime`, working-directory roots, and the turn scheduler. Runtime instances are per session; the agent team and its toolsets are loaded once at `Initialize` and shared by every session (`pkg/acp/agent.go:188-231`, `234-261`).
- **Concurrency:** Different sessions run concurrently (one SDK goroutine per request, one runtime per session, no lock held across a turn). Within one session, a single-slot turn semaphore implements cancel-previous-then-queue-latest: a new prompt immediately cancels the active turn and waits for its teardown before running (`pkg/acp/agent.go:83-159`, tests at `pkg/acp/runagent_test.go:269-343`).
- **Durability and replay:** SQLite (`sessions` metadata rows + position-ordered `session_items`) written synchronously by a `PersistenceObserver` that sees every runtime event before the ACP layer does (`pkg/runtime/persistence_observer.go:57-167`, `pkg/session/store.go:600-653`). Resume reconstructs the in-memory session from the store and rebuilds the runtime, but sends no history to the client — replay to the ACP client is not implemented (`pkg/acp/agent.go:411-466`).
- **Event flow:** Runtime events flow over a buffered (128) Go channel; observers (persistence) run synchronously before the event reaches the ACP layer, which converts each event to one ACP `session/update` notification written directly under the SDK's write mutex (`pkg/runtime/loop.go:249-277`, `pkg/runtime/observer.go:66-81`, `pkg/acp/agent.go:715-811`).
- **Notable uncertainty:** The ACP layer advertises slash commands (`new`, `compact`, `usage`) but never intercepts them — prompt text goes to the model verbatim — and session persistence failures are logged, not surfaced (`pkg/acp/agent.go:909-921`, `pkg/runtime/persistence_observer.go:64-66`).

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `native-agent` | ACP handlers are plain Go methods that call the project's own `LocalRuntime`; same binary, no subprocess | `pkg/acp/agent.go:30-44`, `pkg/acp/run.go:34-38` |
| Process model | `single-process` | One process serves stdio JSON-RPC, the agent loop, tools, and SQLite | `pkg/acp/run.go:16-47`, `cmd/root/acp.go:37-53` |
| Session owner | `acp-layer` | `acp.Session` owns the runtime handle, in-memory session, path roots, and turn state; the runtime drives turns on its behalf | `pkg/acp/agent.go:47-60`, `332-338` |
| Durability | `embedded-database` | File-backed SQLite (default `<data-dir>/session.db`); metadata rows plus position-ordered items | `pkg/acp/run.go:24-32`, `pkg/session/store.go:600-614`, `pkg/session/sqlitestore/sqlitestore.go:24-71` |
| Cross-session concurrency | `concurrent` | Per-session runtimes; SDK spawns one goroutine per inbound request; no global lock across turns | `pkg/acp/agent.go:234-261`, `495-501`; acp-go-sdk `connection.go:412` |
| Same-session concurrency | `cancel-previous` | Single-slot `turns` semaphore; arriving prompt cancels the active turn, waits for its drain, then runs; superseded queued prompts are dropped side-effect-free | `pkg/acp/agent.go:83-159`, `pkg/acp/runagent_test.go:269-314` |
| Event delivery | `mixed` | Runtime→ACP layer is a buffered Go channel; ACP layer→client is a direct synchronous JSON-RPC notification write | `pkg/runtime/loop.go:255-276`, `pkg/acp/agent.go:688-693` |
| Resume strategy | `reconstruct` | Load session row + items from SQLite into a fresh runtime; no client-facing replay; `LoadSession` unsupported | `pkg/acp/agent.go:213`, `356-359`, `411-466`; `pkg/session/store.go:877-896` |

## System architecture

```text
ACP client (e.g. editor)
   │  JSON-RPC over stdio (stdin/stdout)
   ▼
acp-go-sdk AgentSideConnection ── one goroutine per inbound request;
   │                              outbound writes serialized by writeMu
   ▼
pkg/acp.Agent  (Initialize/NewSession/ResumeSession/Prompt/Cancel/Close/List)
   │  sessions map[string]*acp.Session   (guarded by a.mu)
   ▼
acp.Session ── turn scheduler: 1-slot semaphore + cancel-previous (per session)
   │
   ├──────────────► LocalRuntime (one per session)          [pkg/runtime]
   │                    │  RunStream(ctx, sess) → chan Event (cap 128)
   │                    │     ├─ PersistenceObserver ──► SQLite store
   │                    │     └─ events forwarded ──► acp.Agent.runAgent
   │                    │            └─ conn.SessionUpdate ──► ACP client
   │                    └─ tool calls ──► shared Team toolsets (MCP, fs, …)
   │                          (permission ask: conn.RequestPermission →
   │                           rt.Resume via per-runtime resumeChan)
   ▼
SQLite (sessions + session_items)  ◄── also read by ResumeSession/ListSessions
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `acp.Agent` | ACP surface; owns session registry, shared team, provider registry, store | Process | `sessions` map, `clientFS`, `team`, `conn` | `pkg/acp/agent.go:31-42` |
| `acp.Session` | Per-session turn admission, cancellation, path roots | Session | turn slot (`turns` chan), `cancel`, `generation`, `closed`, runtime + session pointers | `pkg/acp/agent.go:47-60`, `83-159` |
| `LocalRuntime` | Agent loop, model calls, tool dispatch, event production | Per session | steer/follow-up queues, resumeChan, observers, live-session registry | `pkg/runtime/runtime.go:236-378`, `701-722` |
| `session.Session` | In-memory conversation items + metadata | Per session (survives reload via store) | `Messages []Item` behind `mu` | `pkg/session/session.go:259-449`, `870-884` |
| `SQLiteSessionStore` | Durable sessions/items | Process | DB handle | `pkg/session/store.go:466-469` |
| Team / toolsets | Agents, MCP clients, filesystem toolsets | Process (loaded at Initialize) | toolset instances shared by all sessions | `pkg/acp/agent.go:194-201`, `pkg/agent/agent.go:613-627` |

### ACP surface

Transport is newline-delimited JSON-RPC over stdin/stdout: `acpsdk.NewAgentSideConnection(acpAgent, stdout, stdin)` (`pkg/acp/run.go:35`). The CLI command is `docker-agent serve acp <agent-file>` (`cmd/root/acp.go:20-35`).

`Initialize` loads the team once (per process) and answers with capabilities: `LoadSession: false`; session capabilities for additional directories, close, list, and resume; prompt capabilities for embedded context and images; client-supplied MCP servers rejected (`pkg/acp/agent.go:188-231`, warning at `289-291`). Handlers implemented: `NewSession`, `Prompt`, `Cancel`, `CloseSession`, `ListSessions`, `ResumeSession`, `Authenticate`. Optional handlers return method-not-found: `LoadSession`, `Logout`, `SetSessionMode`, `SetSessionConfigOption` (`pkg/acp/agent.go:350-359`, `468-485`, `681-685`).

The ACP layer is native: it constructs runtimes and translates runtime events itself. It adds ACP-specific behavior in two places: a toolset registry that swaps the built-in filesystem toolset for one backed by client-side `fs/read_text_file` / `fs/write_text_file` requests (`pkg/acp/registry.go:28-43`, `pkg/acp/filesystem.go:184-307`), and resource-link prompt content fetched from the client (`pkg/acp/agent.go:606-635`).

### Runtime and process boundaries

Everything runs in one OS process. The SDK spawns one goroutine per inbound JSON-RPC request (cancellations handled synchronously; notifications queued with a bounded queue) — verified in acp-go-sdk v0.13.5 `connection.go:380-460`. Each `Prompt` therefore runs on its own goroutine and is serialized only by the per-session turn semaphore.

`LocalRuntime.RunStream` spawns its own loop goroutine and returns a channel of capacity 128 (`pkg/runtime/loop.go:249-277`, `pkg/runtime/defaults.go:17`). An `observe` wrapper fires `OnRunStart`, then for each event runs the observer chain synchronously before forwarding to the consumer channel (`pkg/runtime/observer.go:66-81`). Tool approval blocks the loop goroutine on a per-runtime `resumeChan`, with one pending confirmation at a time (`pkg/runtime/runtime.go:707`, `pkg/runtime/toolexec/dispatcher.go:832-871`); the ACP layer feeds it from the `session/request_permission` round trip (`pkg/acp/agent.go:820-869`).

Cleanup: `Agent.Stop` stops the shared team toolsets on shutdown (`pkg/acp/agent.go:171-180`, deferred at `pkg/acp/run.go:38`); the store is closed when the process exits (`pkg/acp/run.go:29-32`).

## LLM abstraction and integration

### Abstraction

The project uses a **bespoke internal abstraction**. It is neither a named third-party LLM library nor a wrapped executable/service. The contract is the `Provider` interface in `pkg/model/provider/contracts/contracts.go:116-129`: one streaming call `CreateChatCompletionStream(ctx, []chat.Message, []tools.Tool) (chat.MessageStream, error)` plus `ID()` and `BaseConfig()`. `pkg/model/provider/provider.go:1-21` aliases that interface and documents that the package deliberately does not import concrete SDK-backed providers.

Concrete providers are thin adapters over provider SDKs, one package each:

- `anthropic` over `github.com/anthropics/anthropic-sdk-go` (`pkg/model/provider/anthropic/client.go:221-348`; dependency `go.mod:20`)
- `openai` over `github.com/openai/openai-go/v3`, serving `openai`, `openai_chatcompletions`, `openai_responses`, and OpenAI-compatible vendors (`pkg/model/provider/openai/client.go:55`, `154`; dependency `go.mod:62`)
- `gemini` over `google.golang.org/genai` (`pkg/model/provider/gemini/client.go:47`, `817-818`; dependency `go.mod:97`)
- `bedrock` over `github.com/aws/aws-sdk-go-v2/service/bedrockruntime` (`pkg/model/provider/bedrock/client.go:10-12`, `169`)
- `dmr`, a hand-written HTTP client for Docker Model Runner (`pkg/model/provider/dmr/client.go:62`, `199-201`)

The model loop is **in this repository**, not delegated: `fallbackExecutor.execute` builds the attempt chain and calls `CreateChatCompletionStream` (`pkg/runtime/fallback.go:264-355`), and `handleStream` consumes the returned stream (`pkg/runtime/streaming.go:96`). The only external service boundary is the optional Docker AI Gateway, which proxies provider requests but does not run the loop (see Provider and tool boundary).

### Integration path

1. **History conversion.** Each turn assembles a provider-neutral `[]chat.Message` (`pkg/chat/chat.go:52-135`) through `messagesWithDynamicContext` (`pkg/runtime/loop.go:813`) → `Session.getMessages` (`pkg/session/session.go:2355-2462`), which prepends invariant/instruction system messages, renders the latest summary, interleaves instruction updates, trims history, and caps tool-result content.
2. **Model selection.** The loop resolves the agent and calls `r.fallback.execute` with the agent's current provider (`pkg/runtime/loop.go:372`, `852`). `Agent.Model` randomly selects one configured provider (or one from the active override pool, the "alloy" behavior) (`pkg/agent/agent.go:206-222`); configured fallbacks are appended to form the chain (`pkg/runtime/fallback.go:275-277`).
3. **Request construction.** Per attempt, `prepareMessages` rewrites messages for the target provider and `toolsForProvider` filters the tool set (`pkg/runtime/fallback.go:300-305`). The leaf adapter maps neutral types to SDK params: the Anthropic client converts tools and messages, extracts system blocks, builds `anthropic.MessageNewParams`, applies thinking/temperature/top_k, and calls `client.Messages.NewStreaming` (`pkg/model/provider/anthropic/client.go:264-346`).
4. **Streaming.** The adapter wraps the SDK stream as a `chat.MessageStream` (`pkg/model/provider/anthropic/adapter.go:39-45`, `81-118`), normalizing SDK events (content, thinking, tool-use deltas) into `chat.MessageStreamResponse`. `handleStream` reads it on a dedicated goroutine under an idle timeout and accumulates text, reasoning, tool calls, media, and usage (`pkg/runtime/streaming.go:96-200`).
5. **Tool-call handling and conversion back.** `handleStream` returns a `streamResult` carrying `Calls []tools.ToolCall`; `recordAssistantMessage` persists the assistant message (`pkg/runtime/loop.go:960`, `1236`), then `processToolCalls` dispatches through the `toolexec.Dispatcher` (`pkg/runtime/tool_dispatch.go:32-55`), which appends role=tool `chat.Message` results for the next iteration (`pkg/runtime/toolexec/dispatcher.go:1206-1209`, `1267-1270`). Runtime events emitted along the way (`AgentChoiceEvent`, tool-call updates, usage) are converted by the ACP layer as described in Event and data flow.

Every leaf provider is wrapped by `instrumentProvider`, which opens a GenAI semconv `chat {model}` span around each `CreateChatCompletionStream` call (`pkg/model/provider/instrument.go:47-137`).

### Provider and tool boundary

- **Multi-provider: yes.** The built-in registry maps provider types to factories: `openai`/`openai_chatcompletions`/`openai_responses`, `anthropic`, `google` (Gemini, or Vertex AI Model Garden), `dmr`, and `amazon-bedrock` (`pkg/model/provider/providers/providers.go:23-49`). Custom OpenAI-compatible providers from config reuse the `openai` factory.
- **Provider selection.** `resolveProviderType` chooses the registry key by priority `provider_opts.api_type` > built-in alias > provider name (`pkg/model/provider/defaults.go:35-43`), then the registry looks up the factory (`pkg/model/provider/factory.go:102-108`, `pkg/model/provider/defaults.go:83-92`). A model configured with `routing:` rules instead becomes a rule-based router that picks a sub-model by in-memory BM25 similarity and delegates the call (`pkg/model/provider/rulebased/client.go:51-138`; `pkg/model/provider/factory.go:35-59`).
- **Credentials/configuration.** Provider clients resolve tokens through the `environment.Provider` chain using a per-model `token_key` (e.g. `ANTHROPIC_API_KEY`) (`pkg/model/provider/anthropic/client.go:211-217`; `pkg/config/latest/types.go:479-480`). The chain includes OS env, env files, 1Password, Docker Desktop, and credential helpers (`pkg/environment/default.go:22-79`; `pkg/config/runtime.go:150-193`). The ACP process wires the full set through `loaderdefaults.Opts()` → `providers.NewDefaultRegistry()` (`pkg/acp/agent.go:194`, `pkg/teamloader/defaults/defaults.go:29-37`) and passes the registry to the runtime (`pkg/acp/agent.go:247`).
- **Gateway boundary.** When `models_gateway` is set, provider clients dial the Docker AI Gateway instead of the provider's public endpoint, building a gateway HTTP client and injecting Docker Desktop auth (`pkg/model/provider/base/gateway.go:23-93`); the option is threaded from `runConfig.ModelsGateway` (`pkg/teamloader/teamloader.go:678-683`). The upstream gateway's own request normalization, credential exchange, and model catalog are **Not found** in this checkout (checked `pkg/model/provider/base/gateway.go`, `pkg/modelsgateway/`, `pkg/teamloader/teamloader.go`); `pkg/modelsgateway/relay/relay.go:1-15` is an in-repo request relay, not the upstream service.
- **Request/response normalization.** Each adapter converts neutral `chat.Message`/`tools.Tool` to SDK request params and SDK stream events back to `chat.MessageStreamResponse` (`pkg/model/provider/anthropic/client.go:264-348`; `pkg/model/provider/anthropic/adapter.go:81-118`).
- **Tool schemas.** Tools are defined provider-neutrally in `pkg/tools` (`pkg/tools/tools.go:168-192`), filtered per provider by `toolsForProvider` (`pkg/runtime/tool_catalog.go:43-52`), and converted to each SDK's schema inside the adapter (`pkg/model/provider/anthropic/client.go:716-745`; `pkg/model/provider/gemini/client.go:626`).

### Limits

- The Docker AI Gateway is an external service. Its provider-specific request normalization, credential handling, and model catalog are not visible in this checkout. Checked: `pkg/model/provider/base/gateway.go`, `pkg/modelsgateway/`, `pkg/teamloader/teamloader.go`.
- Model metadata (pricing, context window, capabilities) comes from a generated models.dev snapshot (`pkg/modelsdev/snapshot.json`, `pkg/modelsdev/snapshot_date.txt`); the snapshot's upstream generation process is not determined here. Checked: `pkg/modelsdev/`.
- Whether every OpenAI-compatible endpoint behaves identically through the shared `openai` adapter cannot be verified without live provider calls; routing relies on `api_type` and vendor predicates (`pkg/model/provider/defaults.go:120-136`; `pkg/model/provider/contracts/contracts.go:92-98`).
- No named third-party LLM orchestration/abstraction library is used: `go.mod` and `pkg/model/provider/` imports contain only the provider SDKs listed above.

## Session model

### Identity and ownership

A "session" is one `session.Session` — a UUID plus a slice of `Item`s (messages, sub-sessions, summaries, errors, terminations) — in the project's model-conversation sense (`pkg/session/session.go:136-191`, `259-449`). The ACP session ID returned by `NewSession` is `sess.ID`; the same string keys the in-process registry, every ACP notification, the `session_items.session_id` column, and sub-sessions' parent links. The mapping is one-to-one (one ACP id → one in-memory session → one runtime → one set of durable rows); delegated sub-sessions get their own IDs but are stored as items inside the parent, never as ACP sessions (`pkg/session/store.go:1229-1268`).

`acp.Session` (the wrapper) owns the per-session runtime, working dir + additional dirs, and turn state. Sessions default to `Origin: "run"` — the ACP layer does not set a distinct origin (`pkg/session/session.go:2012-2016`; contrast `pkg/a2a/adapter.go:99`), so ACP and CLI sessions share one namespace in the store.

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | Resolve/validate cwd and additional dirs; build runtime; `session.New` with agent limits; title "ACP Session \<uuid\>"; register | `AddSession` commits the session row (empty items) in one tx before returning the ID | Any error aborts before registration; an unrecoverable SQLite open fails server startup (backup+retry recovery in `sqlitestore.New`) | `pkg/acp/agent.go:286-341`; `pkg/session/store.go:629-653`; `pkg/session/sqlitestore/sqlitestore.go:24-71` |
| Load/resume | If already registered, no-op; else `GetSession` (row + items, recursive sub-sessions), optional cwd override, new runtime, register-if-absent (TOCTOU-safe) | Read-only; nothing rewritten | Unknown ID → error to client; losing a registration race drops the duplicate runtime | `pkg/acp/agent.go:411-466`; `pkg/session/store.go:877-896` |
| Prompt | Turn admission (cancel-previous); user message appended to in-memory session; `RunStream` until channel close | User message and all emitted events persisted by the observer as they occur | Turn ctx cancelled → `StopReason: cancelled`; handler errors cancel the runtime stream and drain it before returning | `pkg/acp/agent.go:491-528`, `696-817` |
| Cancel | `cancelTurn()` cancels the session's current `cancel` func | None; already-persisted events stay | No-op if no session/no active cancel | `pkg/acp/agent.go:64-71`, `474-488` |
| Close/delete | Remove from registry; set `closed`; cancel active + queued turns | None — the SQLite row is kept (no ACP delete of durable sessions) | Subsequent prompts/turn admissions fail with "not found" | `pkg/acp/agent.go:362-378`; store `DeleteSession` exists but is not ACP-exposed (`pkg/session/store.go:994-1028`) |

### Durable representation

SQLite via the pure-Go `modernc` driver (`pkg/session/sqlitestore/sqlitestore.go:1-6`). Two tables: `sessions` (id, origin, title, tokens, cost, safety policy, working dir, instruction context, attributes, parent_id, …) and `session_items` (one row per item: `message` / `subsession` / `summary` / `error` / `termination`, positioned at `MAX(position)+1`, with `message_json` holding the full `chat.Message` including tool calls, reasoning content, usage, and provider state) (`pkg/session/store.go:600-614`, `1170-1198`, `1293-1365`). `AddSession` writes row + items in one transaction; `UpdateSession` upserts metadata only; messages go through granular `AddMessage`/`UpdateMessage`/`AddSummary`/`AddError` calls. Reads (`GetSession`) join the row with items ordered by position and recursively load sub-session rows (`pkg/session/store.go:877-896`).

Stored history is authoritative for resume; the in-memory session is authoritative for the running model context. They are written independently (in-memory by `sess.AddMessage`, durable by the observer), so a crash can leave a message in memory that never reached the store — persistence errors are logged warnings, never fatal (`pkg/runtime/persistence_observer.go:64-66`, `96-97`, `108-111`). Not persisted: transient extras (session-start/turn hook context, structured-output reminders), provider request-assembly marks, in-flight steer/follow-up queues, and the ACP wrapper state (path roots, turn slot).

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | SDK request goroutines; separate `acp.Session` + `LocalRuntime` per session; registry lock held only for map lookup/insert | `pkg/acp/agent.go:495-501`, `264-283`; acp-go-sdk `connection.go:412` |
| Two prompts in the same session | `cancel-previous` | 1-slot `turns` semaphore + generation counter: arrival cancels the active turn ctx, queues itself; superseded queued prompts return `cancelled` without side effects (no user message added, no client file reads) | `pkg/acp/agent.go:83-159`, `515-518`; `pkg/acp/runagent_test.go:269-314` |
| Load/resume during an active prompt | Already-registered resume is a no-op that does not disturb the turn; first-time resume would register a second `Session` object for the same ID while a turn runs (no guard against that ordering) | Registry check under `a.mu`, then registration via `registerSessionIfAbsent` | `pkg/acp/agent.go:415-420`, `448-461` |
| Delete/close during an active prompt | Closes cancel the active turn and reject queued prompts ("not found"); durable rows are untouched | `closed` flag checked at admission and after slot acquisition | `pkg/acp/agent.go:73-81`, `90-94`, `362-378`; `pkg/acp/runagent_test.go:345-373` |
| Cancellation isolation | Per session | `cancelTurn` fires only the session's stored cancel func; other sessions unaffected | `pkg/acp/agent.go:64-71`, `474-488` |

Shared state that couples sessions: the single `Team` and its `*agent.Agent` toolset instances (one MCP connection pool serves every session; toolset start/stop is lifecycle-locked, `pkg/agent/agent.go:625-654`, `680-697`); the SQLite store (safe for concurrent use); the one ACP connection, whose outbound writes are serialized by the SDK's `writeMu`. The runtime's plan-change fan-out is deliberately process-global — a plan mutation from one session's tool call reaches every subscribed stream (`pkg/runtime/loop.go:1717-1738`). A canceled request's context cancellation is detected by the loop after tool execution or model streaming returns; the dispatcher then synthesizes error tool responses so the durable transcript stays model-valid (`pkg/runtime/toolexec/dispatcher.go:837-870`, `1254-1274`).

## Event and data flow

### New prompt: ACP client to live response

1. SDK parses the request and calls `Agent.Prompt` on a per-request goroutine with the request context (acp-go-sdk `connection.go:412`).
2. `Prompt` looks up `acp.Session` by ID under `a.mu` (released immediately), then `startTurn` registers a new generation, cancels the previous turn, and blocks on the 1-slot semaphore; cancelled or closed admission returns `StopReason: cancelled` or "not found" (`pkg/acp/agent.go:491-513`).
3. Prompt content blocks are converted to a `session.UserMessage` — text, embedded resources, client-read resource links, and image data URLs become `chat.Message` content/multi-content (`pkg/acp/agent.go:515-518`, `539-604`) — and appended to the in-memory session under its lock (`pkg/session/session.go:870-884`).
4. `runAgent` stamps the session ID into the context, emits `available_commands_update`, and calls `rt.RunStream(runCtx, sess)` (`pkg/acp/agent.go:696-706`).
5. The runtime loop resolves the agent, starts toolsets, seeds model context from `sess.GetMessagesWithoutInstructionContext` (includes the just-added user message), emits `UserMessage` + `StreamStarted`, and iterates `runTurn` (`pkg/runtime/loop.go:372-459`, `741-852`).
6. Streaming deltas become `AgentChoiceEvent`/`AgentChoiceReasoningEvent` (`pkg/runtime/streaming.go:167-183`); completed assistant messages (with tool calls) are appended via `addAgentMessage` and `MessageAddedEvent` (`pkg/runtime/tool_dispatch.go:160-164`, `pkg/runtime/loop.go:960`, `1236-1343`).
7. `runAgent`'s select loop converts each event to ACP updates: agent text/thought chunks, `StartToolCall` / `UpdateToolCall`, plan updates from todo tools, usage, title, warnings/errors (`pkg/acp/agent.go:715-811`, `pkg/acp/toolcall.go:19-85`).
8. Tool confirmation: runtime emits `ToolCallConfirmationEvent` → ACP `RequestPermission` round trip → `rt.Resume(approve/reject)` unblocks the loop goroutine (`pkg/acp/agent.go:819-869`; `pkg/runtime/toolexec/dispatcher.go:852-871`).
9. On channel close without error, `Prompt` returns `StopReason: end_turn`; a cancelled turn context yields `StopReason: cancelled`; `finish()` releases the turn slot only after the runtime channel is fully drained (`pkg/acp/agent.go:503-527`, `705-712`; `pkg/acp/runagent_drain_test.go:44-82`).

### Durable history to ACP client

Not implemented. `LoadSession` is unsupported (`LoadSession: false` capability; handler returns method-not-found — `pkg/acp/agent.go:213`, `356-359`). `ResumeSession` reconstructs the in-memory session (row + all items, recursively including sub-sessions) and registers a fresh runtime, but sends no `session/update` notifications, so the client's transcript stays empty until the next turn (`pkg/acp/agent.go:411-466` — the only store read is `GetSession`; there is no `sendUpdate` call on this path). The durable history does reach the *model*: the reconstructed `Messages` feed `getMessages` on the next prompt (`pkg/runtime/loop.go:429`, `pkg/session/session.go:2355-2462`). `ListSessions` reads summaries from the store without an origin filter, so CLI-created sessions appear too (`pkg/acp/agent.go:381-408`, `pkg/session/store.go:954-991`).

### Live events to durable history

Persistence happens inside the runtime's forwarding goroutine, synchronously, *before* the event reaches the ACP layer (`pkg/runtime/observer.go:73-78`). All store failures are logged, never propagated.

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `session.UserMessage` in `sess.Messages` | none | `store.AddMessage` (role user) | On `UserMessageEvent`, before the event is forwarded | `pkg/acp/agent.go:517`; `pkg/runtime/loop.go:436-438`; `pkg/runtime/persistence_observer.go:93-97` |
| Assistant text | growing `streamingState` in the observer; final `chat.Message` | `AgentMessageChunk` per delta | one message row: created on first delta, `UpdateMessage` per delta, finalized by `MessageAddedEvent` | Per delta, synchronous | `pkg/runtime/persistence_observer.go:83-91`, `169-200`; `pkg/acp/agent.go:721-724` |
| Reasoning/thought | accumulated with text | `AgentThoughtChunk` per delta | `ReasoningContent` on the same streaming row | Per delta | `pkg/runtime/persistence_observer.go:88-91`, `174-199` |
| Tool call | `chat.Message.ToolCalls` on the assistant message | `StartToolCall` (+ pending update on confirmation) | embedded in the finalized assistant message JSON | With `MessageAddedEvent` | `pkg/runtime/loop.go:1236-1343`; `pkg/acp/agent.go:731-740` |
| Tool result | role=tool `chat.Message` appended by the dispatcher | `UpdateToolCall` (completed/failed, diff content for edits) | own `message` item at next position | With its `MessageAddedEvent` | `pkg/runtime/toolexec/dispatcher.go:1263-1281`; `pkg/acp/agent.go:742-759` |
| Completion/failure | `ErrorEvent` / turn end | error text as `AgentMessageChunk`; `PromptResponse` stop reason | `error` item via `AddError`; session row upserted at run start | On the event, before forwarding; response after drain | `pkg/acp/agent.go:520-527`, `761-764`; `pkg/runtime/persistence_observer.go:57-67`, `147-165` |

Token usage (`TokenUsageEvent` → `usage_update` notification + `UpdateSessionTokens`) and titles (`SessionTitleEvent` → `session_info_update` + `UpdateSessionTitle`) follow the same observer pattern (`pkg/acp/agent.go:771-797`; `pkg/runtime/persistence_observer.go:135-145`). Compaction summaries are persisted via `AddSummary` unless the runtime already stored them (`pkg/runtime/persistence_observer.go:121-133`).

### Subsequent-prompt reconstruction

The next `Prompt` in a live session uses the in-memory `sess.Messages`; after `ResumeSession` (or process restart + resume) the same shape is rebuilt from `session_items` ordered by position, including assistant messages with tool calls, tool results, summaries (with `FirstKeptEntry` keep-tail boundaries), errors, and instruction-context state (`pkg/session/store.go:766-868`; `pkg/session/session.go:2164-2194`). Model assembly (`getMessages`) then adds invariant system prompts, renders the latest summary as a synthetic user message followed by kept messages from `FirstKeptEntry`, interleaves instruction updates, and applies history trimming and tool-content truncation caps (`pkg/session/session.go:2355-2462`). Nothing is delegated; the load is a faithful, lossless reconstruction of what the observer wrote — the only lossy boundaries are the deliberate ones (tool-result caps, `NumHistoryItems` trimming, compaction).

### Ordering, cancellation, failure, and backpressure

Ordering is total per session: the runtime emits events from one loop goroutine; observers and the ACP converter run inline; the SDK serializes outbound writes. Cross-session ordering is not coordinated.

Backpressure is real and end-to-end: the events channel is bounded (128) with blocking `Emit` (`pkg/runtime/defaults.go:17`, `pkg/runtime/event_sink.go:23-43`), and `sendUpdate` writes synchronously to stdout — a stalled client blocks the session's runtime loop and therefore model streaming and persistence. There is no per-session output queue or drop policy; only teardown-bound events use bounded delivery (`pkg/runtime/loop.go:204-231`, `pkg/runtime/event_sink.go:75-108`).

Cancellation (client `session/cancel` or a replacing prompt) cancels the turn context. The ACP loop returns `StopReason: cancelled` after draining the runtime channel; the dispatcher converts the cancelled context into synthesized error tool responses so persisted history remains provider-valid; partially streamed text has already been persisted incrementally, and its finalized `MessageAddedEvent` may never fire if the model call is interrupted, leaving the last streaming-row update as the stored text (`pkg/acp/agent.go:705-724`; `pkg/runtime/toolexec/dispatcher.go:864-870`). A permission request left waiting is unblocked by ctx cancellation with a cancelled outcome.

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | Registry map + per-session runtime | `pkg/acp/agent.go:35`, `264-283` |
| Concurrent work across sessions | `yes` | Separate runtimes; shared team/toolsets/store; no global turn lock | `pkg/acp/agent.go:312-315`, `443-446` |
| Same-session prompt exclusion | `yes` | Cancel-previous via single-slot semaphore; strictly one active turn | `pkg/acp/agent.go:100-149` |
| Durable sessions | `yes` | SQLite written synchronously per event | `pkg/session/store.go:1170-1198` |
| Session list | `yes` | `ListSessions` from store summaries (all origins) | `pkg/acp/agent.go:381-408` |
| Session load/resume | `partial` | `ResumeSession` reconstructs runtime + memory; `LoadSession` unsupported | `pkg/acp/agent.go:213`, `356-359`, `411-466` |
| History replay to ACP client | `no` | No updates sent on resume/load | `pkg/acp/agent.go:411-466` |
| Prior history reused by model | `yes` | Reconstructed items feed `getMessages` on the next prompt | `pkg/runtime/loop.go:429`; `pkg/session/session.go:2355-2462` |
| Prompt cancellation | `yes` | Per-session `Cancel` → context cancellation → `StopReason: cancelled` | `pkg/acp/agent.go:474-488`, `520-524` |
| Tool-call progress updates | `yes` | Start/pending/complete updates; permission round trip; todo→plan updates | `pkg/acp/agent.go:731-759`, `820-869`; `pkg/acp/toolcall.go` |
| Partial-output persistence | `yes` | Streaming row updated per delta, finalized on completion | `pkg/runtime/persistence_observer.go:169-200` |
| Recovery after process restart | `partial` | Durable state survives; requires client-initiated `ResumeSession`; nothing is replayed to the client and in-flight turns are lost | `pkg/acp/agent.go:411-466`; `pkg/session/sqlitestore/sqlitestore.go:24-71` |

## Design assessment

### Strengths

- The ACP layer is a thin translator: it owns only ID mapping, path roots, and turn scheduling, while the runtime owns the loop, tools, and persistence. The event→ACP mapping is a single exhaustive switch (`pkg/acp/agent.go:715-811`).
- Persistence is centralized in one observer that sees events before consumers, so durability ordering is defined in one place and cannot drift from what the client is shown (`pkg/runtime/observer.go:60-81`).
- Same-session turn admission is small, explicit, and well tested — including the tricky cases (superseded queued prompts leave no side effects; teardown drains before the slot is released) (`pkg/acp/runagent_test.go:269-343`, `pkg/acp/runagent_drain_test.go:44-82`).

### Tradeoffs and limitations

- The synchronous persistence-plus-forward pipeline means a slow ACP client or SQLite stall back-pressures the model stream; there is no buffering between the ACP write and the runtime loop (`pkg/runtime/observer.go:14-18`, `pkg/acp/agent.go:688-693`).
- Two representations of history (in-memory session vs. store) are written by different actors from the same events; persistence errors are swallowed into warnings, so a crashed process can lose messages the user already saw (`pkg/runtime/persistence_observer.go:64-66`).
- Resume reconstructs model context but gives the client nothing, and `session/request_permission` in-flight approvals are lost across restarts; `CloseSession` never deletes durable rows, so closed sessions keep accumulating (`pkg/acp/agent.go:362-378`).
- Advertised slash commands (`new`, `compact`, `usage`) are never intercepted — `Prompt` forwards the text to the model; the runtime's command evaluation is an embedder opt-in that the ACP path does not wire (`pkg/acp/agent.go:909-921` vs. `pkg/runtime/commands.go:20-40`).
- `ResumeSession` racing with an active turn on the same ID can register a second `Session` wrapper while the first still runs (the register-if-absent guard only covers duplicate resumes, not active turns) (`pkg/acp/agent.go:415-461`).

### Ideas relevant to Ox

- The single-slot generation-counter turn semaphore is a compact, testable answer to same-session concurrency, including the "queued prompt must have no side effects" invariant — Ox's `Session.startTurn` analogue could adopt the pattern, at the cost of every prompt holding a goroutine while queued (fact: implementation and tests; judgment: cheaper to reason about than a queue of pending prompts).
- Emitting persistence from an observer that runs before the consumer keeps "what the client saw" and "what was stored" order-identical by construction; the tradeoff is that persistence latency lands on the model-streaming path (fact: `pkg/runtime/observer.go`; judgment: Ox could decouple them if SQLite latency ever matters).
- Incremental in-place updates of one streaming message row (create-then-update per delta) keep partial output durable without row explosion — a pattern worth copying if Ox persists partial streams (fact: `pkg/runtime/persistence_observer.go:169-200`).
- Avoid the gap where a surface advertises commands it does not implement; Ox should gate ACP capability advertisement on actual handlers (fact: `emitAvailableCommands` vs. no interception; judgment: capability honesty is cheap).

## Unknowns and conflicts

- **Advertised commands vs. behavior:** `available_commands_update` lists `new`/`compact`/`usage` (`pkg/acp/agent.go:910-921`), but no ACP-path code evaluates them; searches across `pkg/acp/` and `pkg/runtime/commands.go` found interception only in embedder-wired `CommandEvaluator` (unused here). Treated as an observed gap rather than a doc claim; README/docs were not relied on.
- **Resume racing an active turn:** the code guards duplicate resumes but has no test or explicit handling for `ResumeSession` while the same ID's first `Session` object has a live turn; behavior is inferred from `registerSessionIfAbsent` (`pkg/acp/agent.go:448-461`), not exercised.
- **Automatic titles in ACP mode:** the ACP layer forwards `SessionTitleEvent` and persists it, but no title generation is wired in `newRuntime` (title generation lives in the App/TUI layer, `pkg/app/app.go:2032-2090`); sessions keep "ACP Session \<uuid\>" unless something else emits the event.
- **Sub-second durability guarantee:** `AddMessage`/`UpdateMessage` run outside explicit transactions against WAL-mode SQLite (`pkg/session/store.go:1183-1198`); atomicity per statement is assumed from SQLite semantics, not tested here.
- Searches performed for replay/update-on-resume: `sendUpdate` callers (`pkg/acp/agent.go`), `LoadSession`/`SessionUpdate` references under `pkg/acp/` and `pkg/app/` — no path sends stored items to the client.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `pkg/acp/run.go:16-47` (`Run`), `cmd/root/acp.go:37-53` (`runACPCommand`), `pkg/acp/agent.go:188-231` (`Initialize`) | Server lifecycle, transport, capability advertisement |
| Session management | `pkg/acp/agent.go:286-341` (`NewSession`), `411-466` (`ResumeSession`), `362-378` (`CloseSession`), `47-60` (`Session`) | ID mapping, ownership, lifecycle |
| Concurrency | `pkg/acp/agent.go:83-159` (`startTurn`/`clearTurn`), `495-501` (`Prompt` lookup); `pkg/acp/runagent_test.go:269-343`; acp-go-sdk `connection.go:380-460` | Turn semaphore, cancel-previous, per-request goroutines |
| Persistence | `pkg/runtime/persistence_observer.go:57-200`; `pkg/session/store.go:600-653`, `1033-1126`, `1170-1198`, `877-896`; `pkg/session/sqlitestore/sqlitestore.go:24-71` | Schema, commit points, durability of every event type |
| Event translation | `pkg/acp/agent.go:696-817` (`runAgent`), `820-907` (permission handling); `pkg/acp/toolcall.go:19-85` | Runtime event → ACP update mapping |
| LLM abstraction/integration | `pkg/model/provider/contracts/contracts.go:116-129` (`Provider`), `pkg/model/provider/factory.go:17-119` (`Registry`), `pkg/model/provider/providers/providers.go:23-49` (built-in factories), `pkg/model/provider/instrument.go:47-137` (chat span); call sites `pkg/runtime/fallback.go:264-355`, `pkg/runtime/streaming.go:96`; adapters `pkg/model/provider/{anthropic,openai,gemini,bedrock,dmr}/client.go`; selection `pkg/agent/agent.go:206-222`, `pkg/model/provider/rulebased/client.go:51-138`; credentials/gateway `pkg/model/provider/base/gateway.go:23-93` | Bespoke multi-provider abstraction over provider SDKs; model loop in-repo |
| Replay/reconstruction | `pkg/acp/agent.go:411-466`; `pkg/session/store.go:766-868`; `pkg/session/session.go:2164-2194`, `2355-2462` | Resume path, absence of client replay, model-context rebuild |
| Cancellation/errors | `pkg/acp/agent.go:64-81`, `474-488`, `503-527`; `pkg/runtime/toolexec/dispatcher.go:820-871`; `pkg/acp/runagent_drain_test.go:44-82` | Cancel semantics, teardown drain, synthesized tool errors |

## Research notes

- **Revision inspected:** `f18ba0a95cf394e5020758cdbcb2b8b3cd82d199`
- **Primary evidence:** `pkg/acp/` (agent.go, run.go, toolcall.go, filesystem.go, registry.go, runagent_test.go, runagent_drain_test.go, filesystem_test.go), `pkg/runtime/` (runtime.go, loop.go, streaming.go, observer.go, event_sink.go, persistence_observer.go, resume.go, tool_dispatch.go, defaults.go), `pkg/runtime/toolexec/dispatcher.go`, `pkg/session/` (session.go, store.go, sqlitestore/sqlitestore.go), `cmd/root/acp.go`, `pkg/team/team.go`, `pkg/agent/agent.go`
- **Relevant docs:** `AGENTS.md` (build/test commands, session-lifetime notes); docs pages not used for behavior claims
- **Commands/tests run:** read-only inspection (`git rev-parse`, `rg`, module-cache read of `coder/acp-go-sdk@v0.13.5` `connection.go`/`agent_gen.go`); no tests executed
- **Report confidence:** `high` — all load-bearing claims are read from production code at the pinned revision, with concurrency semantics corroborated by dedicated unit tests; only explicitly labeled inferences (resume-vs-active-turn race, per-statement SQLite atomicity) rest on implied behavior.
