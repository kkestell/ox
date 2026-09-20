---
project: "OpenHands/OpenHands"
repository: "https://github.com/OpenHands/OpenHands"
revision: "a07364828c8f202e7745c6bce3dcef3915ae7ac1"
researched_at: "2026-09-19"
primary_language: "TypeScript"
implementation_form: "gateway-orchestrator"
process_model: "hybrid"
session_owner: "agent-runtime"
durability: "mixed"
cross_session_concurrency: "concurrent"
same_session_concurrency: "unknown"
event_delivery: "mixed"
resume_strategy: "reconstruct"
overall_confidence: "medium"
---

# OpenHands/OpenHands ACP architecture

## Executive summary

- **ACP boundary:** This revision of `OpenHands/OpenHands` is the **Agent Canvas frontend** (React/TypeScript); it contains no ACP server. ACP terminates in the external **agent-server** (Python, `OpenHands/software-agent-sdk`, pinned at 1.49.2 by `config/defaults.json:4`), the ACP *client*, which spawns third-party ACP server CLIs (Claude Code, Codex, Gemini CLI, or custom) via `acp_command` (`docs/ACP_AGENTS.md:11-27`).
- **Session model:** The unit of work is the **conversation**. One conversation id maps to one WebSocket stream (`/sockets/events/{id}`) and one client-side event store; the ACP session id never reaches the frontend (`websocket-url.ts:109-137`).
- **Concurrency:** Different conversations run concurrently — an active `/goal` loop emits while the user views another (`use-conversation-history.ts:75-81`). Within one session the client imposes no exclusion (input not disabled while running, `chat-interface.tsx:663-666`); server-side serialization is not observable (`unknown`).
- **Durability and replay:** Events persist in the agent-server (or cloud App API) and are reconstructed client-side: REST tail of 50 events, then a WebSocket subscribed with `resend_mode='since'` (`use-conversation-history.ts:21-29`; `conversation-websocket-context.tsx:1000-1002`).
- **Event flow:** Live updates arrive as JSON `OpenHandsEvent` frames; deltas are batched per frame and flushed before any non-delta event (`conversation-websocket-context.tsx:562-567`). ACP tool calls arrive as two persisted events per `tool_call_id`, merged into one card (`acp-tool-call-event.ts:11-15`; `handle-event-for-ui.ts:404-416`).
- **Notable uncertainty:** Everything server-side — ACP session lifecycle, persistence engine, `/interrupt` effects on an in-flight ACP prompt — lives in `software-agent-sdk` and cannot be verified at this revision.

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `gateway-orchestrator` | Orchestrating UI of a multi-repo system: drives a local or cloud agent-server, selects the agent kind (`openhands` vs `acp`), renders one event stream. External CLIs play the ACP server role; the external agent-server the client. | `AGENTS.md`; `docs/ACP_AGENTS.md:17-27` |
| Process model | `hybrid` | Browser SPA; one agent-server process per backend; one ACP subprocess per ACP conversation; cloud conversations in separate sandboxes. | `bin/agent-canvas.mjs:5-12`; `docs/ACP_AGENTS.md:153-158`; `config/defaults.json:4,9` |
| Session owner | `agent-runtime` | The agent-server owns conversations, events, settings, secrets, and the ACP subprocess; the frontend owns view state; the wrapped CLI owns the model context. | `docs/ACP_AGENTS.md:29-31`; `agent-server-adapter.ts:97-115` |
| Durability | `mixed` | Events/settings/secrets in the agent-server (engine not visible); cloud history in the App API; frontend localStorage UI metadata; ACP CLIs' credential files. | `event-service.api.ts:18-38`; `conversation-metadata-store.ts` |
| Cross-session concurrency | `concurrent` | Conversations are independent; one keeps running while another is displayed. Coupling: same-provider ACP subprocesses race on a shared HOME. | `use-conversation-history.ts:75-81`; `docs/ACP_AGENTS.md:205-213` |
| Same-session concurrency | `unknown` | Client permits sending while a run is active (WS `run: true`); server-side serialization is outside this checkout. | `chat-interface.tsx:663-666`; `conversation-websocket-context.tsx:1169-1173` |
| Event delivery | `mixed` | Live WebSocket (`resend_mode=since/all`); history/back-pagination via REST; cloud mode via `/api/cloud-proxy`. | `websocket-url.ts:109-137`; `event-service.api.ts:18-38,102-181` |
| Resume strategy | `reconstruct` | Reopening replays the REST tail and re-anchors the socket with `after_timestamp`; replay dedupes by id. Wrapped-ACP context recovery is not visible here. | `conversation-websocket-context.tsx:371-403,569-581` |

## System architecture

```text
+---------------------------+  REST /api/*, /server_info        +--------------------------------+
| Agent Canvas (this repo)  | --------------------------------> | openhands-agent-server         |
| React SPA, src/           |  WS /sockets/events/{convId}      | (Python, software-agent-sdk,   |
|                           | <-------------------------------- |  external to this checkout)    |
| - settings + onboarding   |       live OpenHandsEvents        | - conversations, event log     |
|   (agent_kind, acp_*)     |       (JSON frames)               | - settings, secrets, profiles  |
| - event store (dedup)     |                                   | - ACPAgent = ACP client        |
| - REST history pagination |                                   +---------------+----------------+
+------------+--------------+                                                   | spawn argv (acp_command)
             |                                                                  | JSON-RPC over stdio
             | cloud backend: REST via /api/cloud-proxy;                                            v
             | runtime WS straight to sandbox                            +--------------------------------+
             v                                                           | ACP server subprocess          |
+---------------------------+                                            | claude-agent-acp / codex-acp / |
| Cloud App API             |                                            | gemini --acp / custom / mock   |
| durable event history     |                                            | owns ACP session id +          |
+---------------------------+                                            | model context                  |
                                                                         +--------------------------------+
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| Agent Canvas SPA | Settings/onboarding, conversation CRUD, event rendering, optimistic chat | Browser tab | Zustand event store (active conversation), localStorage metadata | `use-event-store.ts`; `conversation-metadata-store.ts` |
| agent-server | Conversation registry, agent loop or ACP client, event persistence, settings/secrets | Process per backend (uvx/Docker) | Conversations, events, profiles, secrets, spawned ACP subprocesses | `bin/agent-canvas.mjs:5-12`; `config/defaults.json:4` |
| ACP subprocess (wrapped CLI) | Own LLM, tools, and ACP session | Per ACP conversation, owned by agent-server | ACP session id, model context, provider credentials | `docs/ACP_AGENTS.md:11-31`; `mock-acp-server.py:61-96` |
| Cloud App API | Durable event history for cloud conversations | External service | History surviving sandbox lifecycle | `event-service.api.ts:19-25` |

### ACP surface

There is **no ACP server or ACP client implementation in this repository**. The ACP layer is native to the external agent-server (`software-agent-sdk`), version-pinned rather than vendored: `config/defaults.json:4` pins `agentServer: 1.49.2`, floor `1.28.0` (`config/defaults.json:9`, `agent-server-compatibility.ts:20`). The frontend's ACP surface is:

- **Configuration:** Settings → Agent writes `agent_settings_diff` (`agent_kind: "acp"`, `acp_server`, `acp_command`, `acp_args`, `acp_model`) via `PATCH /api/settings` (`acp-providers.ts:500-541`; `agent-settings.tsx:146-152,236-244`). Presets come from the SDK registry mirrored into `@openhands/typescript-client` (`acp-providers.ts:163-179`); free-text commands are tokenized client-side into the argv the server passes to `subprocess.create_subprocess_exec` (`acp-command.ts:12-20`).
- **Conversation start:** `POST /api/conversations` carries inline `agent_settings` or `agent_profile_id`; ACP conversations are tagged `tags.acpserver` (`agent-server-adapter.ts:1186-1279`).
- **Rendering:** ACP tool calls arrive as `ACPToolCallEvent` in the event union (`openhands-event.ts:33-34`), rendered by `get-acp-tool-call-content.ts` and included in transcript export (`transcript-export/index.ts:444-450`).
- **Error/auth handling:** SDK error codes — `ACPAuthRequired`, `ACPSpawnError`, `ACPInitError`, `ACPPromptError`, `UsagePolicyRefusal` — map to UI headers (`acp-error-codes.ts:8-18`); login probes run provider status commands through the server's bash endpoint (`acp-service.api.ts:29-77`).
- **Model switching:** `switchAcpModel` → `POST /switch_acp_model`, forwarded to the ACP wrapper's `session/set_model` on the live session (`agent-server-conversation-service.api.ts:1133-1146`).

The spawned side of the boundary is characterized by the e2e mock: a stdio JSON-RPC agent implementing `initialize`, `new_session` (returns a fixed `session_id`), and `prompt` (one `session/update` text notification, then `stop_reason: "end_turn"`) (`mock-acp-server.py:41-96`) — confirming the agent-server initiates the ACP handshake and consumes `session/update` notifications.

### Runtime and process boundaries

The browser never touches the ACP subprocess: "The Agent Server owns the subprocess and the credentials; Agent Canvas only records *which* agent to run" (`docs/ACP_AGENTS.md:29-31`). Credentials travel as `LookupSecret` references the server resolves from its own store at spawn time — off the event loop per software-agent-sdk#3510 (`agent-server-adapter.ts:1154-1184`); file-shaped secrets are materialized to disk by the SDK's `acp_file_secrets` (`docs/ACP_AGENTS.md:169-181`). Concurrent same-provider ACP subprocesses in one container share a HOME and can race on auth/lock files — the SDK's `acp_isolate_data_dir` fix is known but not yet sent because the pinned TypeScript client does not expose it (`agent-server-adapter.ts:940-945`; `docs/ACP_AGENTS.md:205-213`). Local mode also runs an automation backend and ingress proxy, outside the conversation path.

## LLM abstraction and integration

### Abstraction

This repository contains no LLM abstraction library and no model-call site. Its only LLM-adjacent dependency is `@openhands/typescript-client` 1.49.2 (`package.json:27`), a generated HTTP client for the agent-server API. That client exposes metadata classes (`LLMMetadataClient`, `ProfilesClient`, `SettingsClient`) but no completion or streaming API (`src/api/config-service/config-service.api.ts:1`; `src/api/profiles-service/profiles-service.api.ts:21`); no provider SDK (OpenAI, Anthropic, Google) appears in `package.json`.

The model loop is a **wrapped external runtime**, split by agent kind:

- **Built-in OpenHands agent** — the external **agent-server** (Python, `OpenHands/software-agent-sdk`, pinned at 1.49.2 by `config/defaults.json:4`) runs an LLM-driven agent the adapter describes as "direct litellm" (`src/api/agent-server-adapter.ts:100-101`). The third-party abstraction is therefore **LiteLLM, server-side and outside this repository**: the e2e mock states "The agent-server's litellm layer talks to this instead of a real LLM provider" and serves OpenAI `/v1/chat/completions` responses (`tests/e2e/mock-llm/scripts/mock-llm-server.py:1-9`); the provider catalogue the UI lists is "~150 entries from litellm" (`src/hooks/query/use-search-providers.ts:13`).
- **ACP agent** — the model loop lives in the spawned provider CLI subprocess (Claude Code, Codex, Gemini CLI, or custom): "Instead of Agent Canvas calling an LLM directly, the Agent Server spawns the agent's own CLI as a subprocess and relays each turn to it. The external agent manages its own LLM, tools, and execution" (`docs/ACP_AGENTS.md:10-27`).

What the frontend does own is a **bespoke configuration layer** that resolves, normalizes, and forwards LLM settings and provider credentials to the agent-server; it never constructs a provider request. `buildNormalizedLlmSettings` (`src/api/agent-server-adapter.ts:621-652`) and the ACP provider registry (`src/constants/acp-providers.ts`) are that layer.

### Integration path

For a normal prompt on the built-in OpenHands agent:

1. **Provider/model selection.** `ModelSelector` splits the current `provider/model` string with `extractModelAndProvider` and rebuilds it on change (`openai` keeps the bare model id) (`src/components/shared/modals/settings/model-selector.tsx:88-111`; `src/utils/extract-model-and-provider.ts:1-22`). Provider/model lists come from `useSearchProviders` → `ConfigService.searchProviders`/`searchModels` → `LLMMetadataClient.getProviders()`/`getModels()`/`getVerifiedModels()` against the agent-server (`src/api/config-service/config-service.api.ts:65-195`; `src/hooks/query/use-search-providers.ts:60-91`; `src/api/option-service/option-service.api.ts:6-27`).
2. **Persistence.** The choice is stored server-side under `agent_settings.llm.model` (with `api_key`/`base_url`) via `PATCH /api/settings` diffs (`src/api/settings-service/settings-service.api.ts:443-583`; `src/services/settings.ts:5-9`). Shared per-provider credentials live in provider connections (`api_key` + optional `base_url`, `src/api/provider-connections-service/provider-connections-service.api.ts:29-124`).
3. **Request construction.** `buildConfiguredOpenHandsAgentSettings` normalizes `agent_settings.llm` (via `buildNormalizedLlmSettings`), forces `llm.stream = true` so the server emits deltas, and attaches the tool list (`src/api/agent-server-adapter.ts:992-1029`). `getAgentTools` emits SDK tool *names* (`terminal`, `file_editor`, `task_tracker`, `browser_tool_set`, `task_tool_set`) with opaque `params` — no schemas (`src/api/agent-server-adapter.ts:144-146,733-764`). The payload rides `POST /api/conversations` (`src/api/agent-server-adapter.ts:1186-1293`).
4. **Model call and streaming.** Not visible here: the agent-server's litellm layer performs the provider request and consumes its stream (`tests/e2e/mock-llm/scripts/mock-llm-server.py:1-9`). The frontend's only lever on streaming is the `llm.stream` flag above; it consumes the result as `StreamingDeltaEvent`/`MessageEvent` frames (`src/utils/handle-event-for-ui.ts:348-448`).
5. **Tool-call handling and conversion.** Tool execution and tool-result production are server-side; the frontend renders tool activity from persisted events, e.g. `ACPToolCallEvent` for ACP (`src/types/agent-server/core/events/acp-tool-call-event.ts`). `handleEventForUI` folds model deltas and tool events into UI state (`src/utils/handle-event-for-ui.ts:348-448`).

For an ACP conversation the flow terminates at a different delegated boundary: `buildConfiguredAcpAgentSettings` resolves `acp_server` → `acp_command` from the registry and `acp_model` from the preferred default, then sends them in `agent_settings` (`src/api/agent-server-adapter.ts:924-990`); credentials travel as `LookupSecret` references the server resolves at spawn time (`src/api/agent-server-adapter.ts:1155-1183`). The model is switched on the live session through `/switch_acp_model` (`src/api/conversation-service/agent-server-conversation-service.api.ts:1133-1161`). Everything past the spawn — provider request construction, streaming, tool calls — is inside the wrapped CLI and **not visible**.

### Provider and tool boundary

- **Provider-specific code.** For ACP it is Canvas's registry, `src/constants/acp-providers.ts`, which layers brand icons and descriptions over `@openhands/typescript-client`'s mirror of the Python registry `openhands.sdk.settings.acp_providers` (`src/constants/acp-providers.ts:1,72-181`; `docs/ACP_AGENTS.md:33-39`). Per-provider credential env-var names and conflict pairs (e.g. `CLAUDE_CODE_OAUTH_TOKEN` vs `ANTHROPIC_API_KEY`) live there too (`src/constants/acp-providers.ts:193-330`). For the built-in agent there is no provider-specific code in this repo: provider and model names are opaque server data (`src/api/option-service/option-service.api.ts:6-27`).
- **Credentials and configuration.** LLM credentials are stored server-side: `agent_settings.llm.api_key`/`base_url` for the built-in agent (`src/api/agent-server-adapter.ts:621-652`), provider connections for shared keys (`src/api/provider-connections-service/provider-connections-service.api.ts:29-124`), and `LookupSecret` references to `/api/settings/secrets/{name}` for conversation start (`src/api/agent-server-adapter.ts:1155-1183`). A ChatGPT-subscription auth mode (`llm.auth_type: "subscription"`, `llm.subscription_vendor: "openai"`) is toggled in the same normalized settings (`src/constants/llm-subscription.ts:3-7,71-77`). Balance/subscription reads are normalized from server responses (`src/api/llm-balance-service.ts:6-49`; `src/api/llm-subscription-service.ts:10-107`).
- **Request/response normalization.** The frontend normalizes only *settings*, not model traffic: `buildNormalizedLlmSettings` (`src/api/agent-server-adapter.ts:621-652`) and the small response shims in the balance/subscription services. Provider request/response normalization is done by LiteLLM in the agent-server and is **Not found** in this checkout (searched `src/api`, `src/services`, `src/types/agent-server` for any completion call — none exists).
- **Tool schemas.** Only tool *names* and opaque params cross the wire (`src/api/agent-server-adapter.ts:733-764`); schemas, dispatch, and execution are server-side. MCP server config is forwarded to the ACP subprocess at session creation (`src/api/agent-server-adapter.ts:962-969`).
- **Multi-provider and selection.** Yes, multi-provider, selected two ways. Built-in: the provider is encoded in the `provider/model` string (`openai` bare) chosen in `ModelSelector` and sent as `agent_settings.llm.model` (`src/components/shared/modals/settings/model-selector.tsx:95-111`). ACP: `agent_settings.acp_server` selects the registry entry, which determines `acp_command` and the credential fields (`src/api/agent-server-adapter.ts:908-990`; `src/constants/acp-providers.ts:330-430`).

### Limits

- The provider abstraction itself (LiteLLM), the model-call site, the streaming consumer, tool-schema construction, and tool execution are in `OpenHands/software-agent-sdk`, outside this checkout. They are evidenced only indirectly by the adapter comments and the e2e mock (`src/api/agent-server-adapter.ts:100-101`; `tests/e2e/mock-llm/scripts/mock-llm-server.py:1-9`).
- `@openhands/typescript-client` 1.49.2 is not vendored or installed in the checkout (no `node_modules/` directory exists), so the internals of `LLMMetadataClient`/`ProfilesClient`/`SettingsClient` and the generated request types are **Not found**; only their call sites and argument shapes are inspectable.
- Whether the agent-server persists `StreamingDeltaEvent`s, serializes prompts per conversation, or replays prior history into a fresh provider/ACP session remains outside the checkout (see also Unknowns).
- Real provider endpoints and credential formats are not in the repository; only mock and proxy hosts appear — the live harness defaults to `https://llm-proxy.app.all-hands.dev` (`tests/e2e/live/scripts/run-live-e2e.mjs:15`), and the mock LLM is a local OpenAI-compatible server (`tests/e2e/mock-llm/scripts/mock-llm-server.py:1-9`).

## Session model

### Identity and ownership

The project calls a session a **conversation**. The frontend obtains a server-generated `conversation.id` from `POST /api/conversations` (`agent-server-conversation-service.api.ts:560-565`) and uses it as the key for REST history, the WebSocket path, the client event store (`loadedConversationId`, `use-event-store.ts:66-76`), and per-conversation localStorage metadata. The conversation → ACP session mapping is **one-to-one but invisible client-side**: the frontend never sees an ACP session id, identifying ACP conversations by the persisted `tags.acpserver` provider key or the `agent.kind === "ACPAgent"` discriminator with `acp_server`/`acp_model` fields (`agent-server-adapter.ts:97-115,359-388,487`). Planner sub-conversations are server-derived via `sub_conversation_ids`/`parent_conversation_id` (`agent-server-adapter.ts:134-142`). **Inference:** the agent-server spawns one ACP subprocess per ACP conversation and maps its ACP session to the conversation internally — following from per-agent `acp_command`, the subprocess-isolation TODO, and the mock's `new_session` — but the mapping code is outside the checkout.

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | Client POSTs `/api/conversations` with the start payload (ACP settings or profile id, workspace, `LookupSecret` refs, tags); optional `initial_message` runs the first turn | Conversation + settings persisted server-side; selection metadata in localStorage | Toasts; `NoBackendAvailableError` without a backend | `agent-server-adapter.ts:1186-1293`; `conversation-service.api.ts:540-600` |
| Load/resume | REST tail (50 events, `TIMESTAMP_DESC`, reversed) seeds the store → socket opens with `resend_mode='since'`, `after_timestamp` = latest loaded event | Read-only; `refetchOnMount` re-pulls the tail so events made while away arrive in one REST page | Failed initial load falls back to `resend_mode='all'`; retry capped at 1 | `use-conversation-history.ts:21-29,73-96`; `conversation-websocket-context.tsx:281-294,395-403,1000-1002` |
| Prompt | WS `{role:"user", content:[...], run:true}`; optimistic bubble consumed by the echoed user `MessageEvent`; REST `sendEvent(run:true)` fallback when closed | User message persists server-side (echo authoritative) | Un-echoed pending messages time out to "error" after 150 s | `use-send-message.ts:22-64`; `conversation-websocket-context.tsx:623-637,1123-1173`; `optimistic-user-message-store.ts:16-19` |
| Cancel | Local: `POST /interrupt` ("in-flight LLM requests are cancelled immediately"); cloud: pause sandbox, socket gated on `sandbox_status === "PAUSED"` | `PauseEvent`/status transitions arrive as events | Toast on failure | `conversation-mutation-utils.ts:98-104`; `use-unified-stop-conversation.ts:52-70` |
| Close/delete | `deleteConversation` (e2e cleanup path); no teardown handshake visible client-side | Conversation removed from server list | Best-effort try/catch in tests | `mock-llm-acp-agent.spec.ts:63-71` |

### Durable representation

Durable conversation state is the **append-only event log owned by the agent-server**; the storage engine is not part of this checkout (**Inference** from the REST events API and the SDK boundary). The frontend observes it as pages: `GET /events/search` returns `{items, next_page_id}` with `limit`, `sort_order`, `timestamp__gte/lt` keyset pagination, capped at 100 per page (`event-service.api.ts:102-181`). Cloud mode splits history (App API, persists across sandbox lifecycle) from live runtime endpoints (`event-service.api.ts:19-34`). Settings, agent profiles (`/api/agent-profiles`, storing `acp_command` as a shell string), and secrets persist server-side (`mock-llm-acp-agent.spec.ts:144-167`). Deliberately **not** durable client-side: streaming deltas (not id-tracked — `use-event-store.ts:95-107`), drafts (cleared on echo), and UI-only state. Whether the server persists `StreamingDeltaEvent`s is not observable; the client folds them into the final message at render (`handle-event-for-ui.ts:374-385`).

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | Separate sockets, store slices, REST pages; a running `/goal` loop emits into one conversation while the UI shows another | `use-conversation-history.ts:75-81`; `conversation-websocket-context.tsx:306-321` |
| Two prompts in the same session | `unknown` | No client exclusion: `InteractiveChatBox` disabled only for pending-creation/LLM-blocked states, so a second send rides the same socket during a run; server-side queueing outside this checkout | `chat-interface.tsx:663-666`; `conversation-websocket-context.tsx:1169-1173` |
| Load/resume during an active prompt | Works; overlap deduped | `refetchOnMount: "always"` re-pulls the tail on return; socket stays open during refetches; `since`-anchor overlap and WS replay dedupe by event id, duplicate side-effects skipped | `use-conversation-history.ts:73-96`; `conversation-websocket-context.tsx:383-394,569-581` |
| Delete/close during an active prompt | Not found | No client path handles deleting a running conversation; e2e deletes only after completion | `mock-llm-acp-agent.spec.ts:63-71` |
| Cancellation isolation | Per conversation | `interruptConversation(conversationId)` targets one conversation; cloud pause one sandbox; per-socket reconnect backoff with jitter keeps main/planning sockets out of lockstep | `conversation-mutation-utils.ts:98-104`; `use-websocket.ts:110-134` |

Cross-session coupling that does exist: all conversations of a backend share the agent-server process and its provider rate limits (unobservable here), and concurrent same-provider ACP subprocesses share a HOME in one container — a race the docs acknowledge via the pending `acp_isolate_data_dir` opt-in (`docs/ACP_AGENTS.md:205-213`). The browser's critical sections are Zustand updates; the event store dedupes by id but skips delta ids, so delta merges rely on arrival order.

## Event and data flow

### New prompt: ACP client to live response

1. User submits; `useSendMessage` converts the payload to `{role:"user", content:[text, images...]}` (`use-send-message.ts:22-64`).
2. `sendMessage` sends `{...message, run: true}` over the WebSocket; if closed it falls back to REST `sendEvent(..., {run:true})`, which queues server-side (`conversation-websocket-context.tsx:1123-1173`). An optimistic bubble is consumed by the echoed user `MessageEvent` (`conversation-websocket-context.tsx:623-637`).
3. The agent-server runs the turn — for `agent_kind: "acp"`, relaying to the spawned ACP subprocess over stdio JSON-RPC (server-side; boundary evidenced by `mock-acp-server.py:70-96`, `docs/ACP_AGENTS.md:17-27`).
4. Live events return as JSON frames: `StreamingDeltaEvent`, `ACPToolCallEvent` or `ActionEvent`/`ObservationEvent`, `MessageEvent`, `ConversationStateUpdateEvent` (`openhands-event.ts:25-46`).
5. The WS handler validates with type guards, buffers deltas in a per-frame batcher, and flushes before any non-delta event so a terminal event cannot overtake buffered text (`conversation-websocket-context.tsx:551-581`).
6. `handleEventForUI` renders: deltas merge per sender; an observation replaces its action; an ACP tool call's terminal event replaces its started event by `tool_call_id`; status/stats drive execution-state and metrics stores (`handle-event-for-ui.ts:341-346,374-385,404-416`; `conversation-websocket-context.tsx:650-755`).

### Durable history to ACP client

1. On conversation open, `useConversationHistory` fetches the latest 50 events (`TIMESTAMP_DESC`) and reverses them; the store seeds via `addEvents` with a final re-sort (`use-conversation-history.ts:43-71`; `conversation-websocket-context.tsx:323-360`).
2. The WebSocket is gated until the first REST page lands, then subscribes with `resend_mode='since'` + `after_timestamp` of the latest preloaded event; empty or failed loads fall back to `resend_mode='all'` (`conversation-websocket-context.tsx:371-403,993-1002`).
3. Replays and reconnects dedupe by event id; idempotence-sensitive side effects (optimistic consumption, cache invalidation) are skipped for duplicates (`conversation-websocket-context.tsx:569-581`; `use-event-store.ts:95-107`).
4. Scrolling up paginates older pages via `useLoadOlderEvents` (`use-load-older-events.ts:146,181`); the e2e ACP spec verifies a returned conversation still renders the ACP reply after resume (`mock-llm-acp-agent.spec.ts:333-376`). The planning sub-conversation still replays fully (`resend_all: true`) with count-based completion (`conversation-websocket-context.tsx:1034-1086`).

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | WS `{role:"user", content, run:true}`; optimistic bubble | Relayed to ACP `session/prompt` (server-side) | Echoed user `MessageEvent` persisted server-side | Server persists; echo authoritative | `conversation-websocket-context.tsx:623-637` |
| Assistant text | `StreamingDeltaEvent` batches, then `MessageEvent` | `session/update` text blocks (per mock) | `MessageEvent` persisted; deltas client-transient | Final event supersedes deltas at render | `handle-event-for-ui.ts:354-385`; `use-event-store.ts:95-107` |
| Reasoning/thought | `StreamingDeltaEvent.reasoning_content`; `ThinkAction` | None observed; SDK-dependent | Rides `ActionEvent.thought`/reasoning fields | Render-time extraction | `collapsible-thinking.tsx` |
| Tool call (ACP) | `ACPToolCallEvent` `pending`/`in_progress` | ACP tool_call update → SDK event | Persisted as its own event | Merged started→terminal by `tool_call_id` | `acp-tool-call-event.ts:11-15`; `handle-event-for-ui.ts:404-416` |
| Tool result (ACP) | `ACPToolCallEvent` `completed`/`failed` with `raw_output`/`is_error` | Terminal tool_call update | Terminal event persisted | Started card replaced in place | `acp-tool-call-event.ts:11-15,66-95` |
| Completion/failure | `end_turn` → `MessageEvent`; `ConversationErrorEvent` with ACP codes | ACP prompt response | Error events persisted and replayed | Rendered on arrival; codes drive recovery | `acp-error-codes.ts:8-18`; `conversation-websocket-context.tsx:583-621` |

### Subsequent-prompt reconstruction

On the next prompt the frontend does not rebuild model context — it only re-anchors the view (REST tail + `since` socket). Reconstruction is delegated: for the built-in OpenHands agent the server holds the event history; for ACP conversations the wrapped CLI owns its session context, and whether the agent-server replays prior history into a fresh ACP session after a restart is **Not found** here (searched `docs/ACP_AGENTS.md`, start-payload builders, e2e specs). One related observable: `/model` is a no-op for ACP parents — the planner launcher calls an ACP conversation's `active_profile` "a stale launch-time snapshot," evidence that ACP model state lives with the subprocess, refreshed only via `/switch_acp_model` (`agent-server-conversation-service.api.ts:617-629,1133-1146`).

### Ordering, cancellation, failure, and backpressure

- **Ordering:** per-frame delta batching plus flush-before-non-delta guarantees buffered text cannot be overtaken within one socket (`conversation-websocket-context.tsx:562-567,796-801`). Cross-source ordering (REST refetch vs WS replay) reconciles via id dedup and a final re-sort in `addEvents` (`use-event-store.ts:159-178`).
- **Cancellation:** `POST /interrupt` per conversation; cloud pause flips `sandbox_status` and blocks the socket until resume (`conversation-mutation-utils.ts:98-104`). Effects on the in-flight ACP subprocess prompt are server-side and not visible.
- **Failure:** reconnect uses exponential backoff with jitter, unbounded attempts by default, and a `since`→`all` fallback after a failed initial load (`use-websocket.ts:110-134`; `use-conversation-history.ts:83-96`). ACP failures surface as coded error events (`acp-error-codes.ts:8-18`).
- **Backpressure:** none observable. The event store is unbounded; deltas coalesce per frame rather than queue; push delivery means a slow client cannot stall the agent. A mid-stream disconnect loses only transient deltas — final messages persist server-side.

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `yes` | Conversation list, batch get, per-conversation sockets | `agent-server-conversation-service.api.ts:447-565` |
| Concurrent work across sessions | `yes` | `/goal` loop emits while another conversation is displayed | `use-conversation-history.ts:75-81` |
| Same-session prompt exclusion | `unknown` | Client permits sends during a run; server behavior outside checkout | `chat-interface.tsx:663-666` |
| Durable sessions | `yes` | Events/settings persist server-side; cloud history survives sandbox | `event-service.api.ts:19-34` |
| Session list | `yes` | `GET /api/conversations` search + sidebar | `AGENTS.md` |
| Session load/resume | `yes` | REST tail + `since`-anchored WS; verified e2e for ACP | `mock-llm-acp-agent.spec.ts:333-376` |
| History replay to ACP client | `yes` | Persisted ACP tool-call events and messages replay via REST/WS | `acp-tool-call-event.ts:11-15`; `use-conversation-history.ts:21-29` |
| Prior history reused by model | `unknown` | ACP context owned by the subprocess; replay not observable | Not found (see Unknowns) |
| Prompt cancellation | `yes` | `POST /interrupt` local; sandbox pause cloud | `conversation-mutation-utils.ts:98-104` |
| Tool-call progress updates | `yes` | Two-phase tool-call events merged into one card | `handle-event-for-ui.ts:404-416` |
| Partial-output persistence | `partial` | Final turns persist; `StreamingDeltaEvent`s are client-transient | `use-event-store.ts:95-107` |
| Recovery after process restart | `partial` | Event history reloads; ACP subprocess context/`can_resume` server-side | `agent-server-adapter.ts:74-78` |

## Design assessment

### Strengths

- **Two-phase replay is clean:** REST for the tail, WebSocket `since` for the delta, id-based dedup to reconcile overlap — no double render, no double-applied side effects (`use-conversation-history.ts:21-29`; `conversation-websocket-context.tsx:383-394,569-581`).
- **The durable stream is a closed union:** one typed `OpenHandsEvent` union covers native and ACP activity, so ACP integration is rendering plus configuration, not a second protocol surface (`openhands-event.ts:25-46`).
- **Credential boundary is explicit:** the browser sends only secret *references* (`LookupSecret` URLs); the server resolves values at spawn time (`agent-server-adapter.ts:1154-1184`).

### Tradeoffs and limitations

- **Fragile echo matching:** optimistic user bubbles are consumed by matching echoed text (FIFO fallback), not a client-generated id, with a 150 s timeout backstop (`conversation-websocket-context.tsx:623-637`; `optimistic-user-message-store.ts:16-19`).
- **Split durability:** ACP model context lives in the wrapped CLI while events live in the agent-server, so post-restart model-context recovery is undefined from the frontend's perspective (`agent-server-conversation-service.api.ts:617-629`).
- **Known cross-session race deferred:** concurrent same-provider ACP conversations share a HOME; the fix exists server-side but cannot be sent until the pinned client exposes it (`agent-server-adapter.ts:940-945`).

### Ideas relevant to Ox

- REST-tail + `since`-anchored replay with idempotent merge rules separates "what the user needs now" from "what changed since" and makes reconnects cheap. Tradeoff: requires a stable event id/timestamp on every durable event, plus consumer-side dedup.
- Persisting tool calls as two events (started/terminal) keeps the log append-only and lets any consumer reconstruct progress, at the cost of a merge step in every renderer (`handle-event-for-ui.ts:404-416`). Ox's single-session-file model could get the same append-only discipline via a per-call status field.
- Delegating session context to a wrapped agent turns cancellation and resume into server-side promises the client cannot verify. Keeping model context in the same durable store as events avoids this class of unknown.

## Unknowns and conflicts

- **Agent-server internals:** ACP session lifecycle, persistence engine, per-conversation prompt serialization, and `/interrupt` propagation to ACP subprocesses live in `OpenHands/software-agent-sdk` and were not inspectable here. Looked for: vendored server code, protocol schemas, or ACP client logic in `src/`, `tools/`, `scripts/`, `docs/` — only configuration, types, and the e2e mock exist.
- **Server-side persistence of `StreamingDeltaEvent`:** the client treats deltas as transient; whether the SDK stores them is not determinable from this checkout.
- **Same-session prompt behavior:** no client-side rejection exists; server-side queue/reject semantics are **Not found** (searched `chat-interface.tsx`, `use-send-message.ts`, `conversation-websocket-context.tsx`, types).
- **Docs vs code:** `docs/ACP_AGENTS.md:151-155` recommends agent-server ≥ 1.28.0, matching `config/defaults.json:9`; no docs/tests/code disagreements observed, but docs describe server behavior unverifiable from this repo.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `docs/ACP_AGENTS.md:11-31`; `src/utils/acp-command.ts:12-20`; `tests/e2e/mock-llm/scripts/mock-acp-server.py:31-96`; `src/api/agent-server-adapter.ts:924-990` (`buildConfiguredAcpAgentSettings`) | ACP terminates in the external agent-server; what the frontend contributes |
| Session management | `src/utils/websocket-url.ts:109-137` (`buildWebSocketUrl`); `src/contexts/conversation-websocket-context.tsx:124-138`; `src/api/agent-server-adapter.ts:97-115,359-388,487` | Conversation identity, per-conversation socket, ACP identity tags |
| Concurrency | `src/components/features/chat/chat-interface.tsx:663-666`; `src/hooks/use-websocket.ts:110-134`; `src/hooks/query/use-conversation-history.ts:75-81` | No same-session exclusion; per-socket reconnect; cross-conversation concurrency |
| Persistence | `src/api/event-service/event-service.api.ts:102-181` (`searchEvents`); `src/hooks/query/use-conversation-history.ts:30-97`; `src/hooks/use-load-older-events.ts:146,181` | REST pagination, tail-first loading, keyset back-pagination |
| Event translation | `src/types/agent-server/core/events/acp-tool-call-event.ts`; `src/utils/handle-event-for-ui.ts:348-448` (`handleEventForUI`); `get-acp-tool-call-content.ts` | Durable events → render state |
| Replay/reconstruction | `src/contexts/conversation-websocket-context.tsx:281-403,993-1002`; `src/stores/use-event-store.ts:95-178` | REST seed, `since` anchoring, dedup semantics |
| Cancellation/errors | `src/hooks/mutation/conversation-mutation-utils.ts:98-104` (`pauseConversation`); `use-unified-stop-conversation.ts:52-70`; `src/utils/acp-error-codes.ts:8-18` | Interrupt vs pause; ACP-coded failure recovery |
| LLM integration | `src/api/agent-server-adapter.ts:621-652` (`buildNormalizedLlmSettings`), `:924-1029` (`buildConfiguredAcpAgentSettings`/`buildConfiguredOpenHandsAgentSettings`), `:1155-1183` (`buildCustomSecrets`); `src/api/config-service/config-service.api.ts:65-195`; `src/api/provider-connections-service/provider-connections-service.api.ts:29-124`; `src/constants/acp-providers.ts`; `src/constants/llm-subscription.ts`; `tests/e2e/mock-llm/scripts/mock-llm-server.py:1-9` | Model loop is the wrapped agent-server (LiteLLM) or ACP CLI; frontend only resolves/normalizes settings, credentials, and provider registry |

## Research notes

- **Revision inspected:** `a07364828c8f202e7745c6bce3dcef3915ae7ac1` (committed 2026-09-18)
- **Primary evidence:** `src/api/agent-server-adapter.ts`, `src/contexts/conversation-websocket-context.tsx`, `src/api/event-service/`, `src/hooks/query/use-conversation-history.ts`, `src/utils/handle-event-for-ui.ts`, `src/constants/acp-providers.ts`, `src/types/agent-server/core/**`, `tests/e2e/mock-llm/**`, `bin/agent-canvas.mjs`, `config/defaults.json`
- **Relevant docs:** `docs/ACP_AGENTS.md`; `AGENTS.md` (multi-repo boundary map)
- **Commands/tests run:** read-only inspection only (`git rev-parse`, file reads, greps); no tests executed
- **Report confidence:** `medium` — frontend data paths are fully grounded at the pinned SHA, but the ACP server side of every boundary claim lives in `software-agent-sdk` and is evidenced only indirectly.
