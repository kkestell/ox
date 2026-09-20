---
project: "AntigmaLabs/ante"
repository: "https://github.com/AntigmaLabs/ante"
revision: "9eca8821e004cd76a1c75e396fb9a78c7e06b67a"
researched_at: "2026-09-19"
primary_language: "rust"
implementation_form: "unknown"
process_model: "hybrid"
session_owner: "agent-runtime"
durability: "local-files"
cross_session_concurrency: "unknown"
same_session_concurrency: "unknown"
event_delivery: "mixed"
resume_strategy: "native"
overall_confidence: "medium"
---

# Ante ACP architecture

## Executive summary

- **ACP boundary:** Not found. The `ante-acp` crate advertised in the README ("provides Agent Client Protocol support", `README.md:226`) is an empty placeholder at this revision: `ante-acp/src/lib.rs:1` is blank and `ante-acp/Cargo.toml:11` declares no dependencies. No Rust code in the repository mentions ACP. The real programmatic surface is Ante's own JSONL protocol ("Op/Evt") served by the closed-source `ante serve` daemon (`crates/protocol-shape`), consumed by the client SDK `crates/ante-sdk` (`crates/ante-sdk/src/lib.rs:8-21`).
- **Session model:** A session is one agent-conversation span owned by the closed daemon. The wire schema shows one active session per connection, replaced (not refused) by a new `StartSession` (`crates/protocol-shape/src/msg.rs:37-40`), addressable session IDs of the form `ses_<ULID>` (`crates/protocol-shape/src/id.rs:33-35`), and no ACP ID anywhere.
- **Concurrency:** Both scopes are `unknown` from implementation evidence: the daemon (agent loop, locks, task scheduling) is not in this repository. Docs claim each `--sock`/`--ws` connection gets its own session on a shared host (`docs-site/docs/usage/serve.mdx:33,45`); the shipped example client serializes prompts itself with a `busy` flag (`examples/mini-tui/src/main.rs:88-93,124-129`).
- **Durability and replay:** `save_session` controls "a transcript and a resumable snapshot" (`crates/protocol-shape/src/msg.rs:556-558`) under `~/.ante/sessions/<session-id>/` (`docs-site/docs/reference/storage-reference.mdx:20`). `ResumeSession` restores the snapshot and replays up to 200 historical events (`docs-site/docs/reference/protocol-reference.mdx:151`). File-format and commit-timing code is closed.
- **Event flow:** Ops and events are newline-delimited JSON (`OpMsg`/`EventMsg`, `crates/protocol-shape/src/msg.rs:8-21`) over stdio, a Unix socket, or a WebSocket, bridged by the SDK into a bounded op channel (256) and an unbounded event channel (`crates/ante-sdk/src/connect.rs:91,110-114`); the stream ends with `Goodbye` (`crates/ante-sdk/src/client.rs:86-93`).
- **Notable uncertainty:** The `ante` binary — the process that actually owns sessions, runs the model loop and tools, and writes session files — is distributed prebuilt and its source is not in this repository (`BINARY-TERMS.md:3-6`). Everything between "op received" and "event emitted" is known only from the shipped wire schema and the repo's documentation.

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `unknown` | No ACP implementation exists; the ACP crate is an empty stub despite the README claim. The observable agent surface is the native Op/Evt daemon protocol, which is neither a native ACP agent nor an ACP adapter | `ante-acp/src/lib.rs:1`, `ante-acp/Cargo.toml:11`, `README.md:226` |
| Process model | `hybrid` | One transport spawns a dedicated `ante serve --stdio` child that lives and dies with the connection; the `--sock` and `--ws` transports dial a long-lived shared host process. SDK code shows the stdio spawn; the shared-host behavior is documented only | `crates/ante-sdk/src/connect.rs:120-156`, `docs-site/docs/usage/serve.mdx:33,45` |
| Session owner | `agent-runtime` | The daemon owns the session and its state; the client only nominates a session via `StartSession`/`ResumeSession` ops. "An `Endpoint` names a host, never a session" | `crates/protocol-shape/src/msg.rs:37-74`, `crates/ante-sdk/src/endpoint.rs:6-8` |
| Durability | `local-files` | Documented per-session directories under `~/.ante/sessions/<session-id>/` (transcript + resumable snapshot); the `save_session` wire flag controls it. No database anywhere in the repo | `crates/protocol-shape/src/msg.rs:556-558`, `docs-site/docs/reference/storage-reference.mdx:20` |
| Cross-session concurrency | `unknown` | Docs say each connection on a shared host drives its own session and a client's `Shutdown` ends only that connection; whether sessions execute concurrently inside the daemon, and what serializes them, is not in this repo | `docs-site/docs/usage/serve.mdx:33,45,62` |
| Same-session concurrency | `unknown` | The protocol brackets turns (`TurnStart`…`TurnEnd`) and offers `Steer` for in-turn guidance, but nothing in the repo shows whether a second `UserInput` mid-turn is queued, rejected, or concurrent. The example client simply refuses to send while busy | `crates/protocol-shape/src/msg.rs:46,194-213`, `examples/mini-tui/src/main.rs:124-129` |
| Event delivery | `mixed` | The daemon emits events as JSONL frames over stdio/socket/WS; the SDK pumps them into tokio channels (ops bounded at 256, events unbounded) that clients consume | `crates/ante-sdk/src/connect.rs:91,110-114,178-230`, `crates/ante-sdk/src/client.rs:41-44` |
| Resume strategy | `native` | `ResumeSession { session_id }` is a first-class op: the daemon restores what it persisted and replays recent events to the client | `crates/protocol-shape/src/msg.rs:65-74`, `docs-site/docs/reference/protocol-reference.mdx:140-151` |

## System architecture

```text
                         (no ACP: ante-acp crate is an empty stub)

  Rust client (mini-tui, harnesses)          other clients (raw JSONL)
        │ ante-sdk Client                                 │
        │  ops: bounded channel (256) → JSONL lines       │
        │  events: JSONL lines → unbounded channel        │
        ▼                                                 ▼
┌─────────────────────────────────────────────────────────────────┐
│  `ante` daemon (CLOSED SOURCE binary; not in this repo)         │
│  transports: stdio (per-connection child) | unix socket | ws    │
│  session:    1 active session per connection; StartSession      │
│              replaces the running one; ResumeSession reloads    │
│  runtime:    model loop + tools + approvals (not visible here)  │
└───────────────┬─────────────────────────────────────────────────┘
                │ EventMsg JSONL: SessionStart, MessageDelta,
                │ ToolStart/Update/End, TurnPause, TurnEnd, UsageUpdate
                ▼
        ~/.ante/sessions/<session-id>/   (transcript + resumable snapshot;
                                          written by the daemon, if save_session)
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `ante-acp` crate | Stated ACP support; actually empty | static | none | `ante-acp/src/lib.rs:1`, `ante-acp/Cargo.toml:11` |
| `ante-protocol-shape` | Wire schema: `Op`, `Evt`, `SessionRequest`, `Id` (ULIDs), serde round-trip tests | static (published crate) | none (types only) | `crates/protocol-shape/src/lib.rs:1-5`, `crates/protocol-shape/src/msg.rs:36-235` |
| `ante-sdk` | Client: spawn/dial host, bridge JSONL to channels, `Shutdown`/`Goodbye` close handshake | per connection | channels only; no session state | `crates/ante-sdk/src/connect.rs:96-230`, `crates/ante-sdk/src/client.rs:49-93` |
| `ante` daemon | Session ownership, agent loop, tools, model providers, persistence | process (child per stdio connection, or shared host for sock/ws) | active session, conversation, snapshot writes | `crates/protocol-shape/src/msg.rs:36-114` (ops it must serve), `docs-site/docs/reference/architecture.mdx:25-30` |
| `ante-exec` / `ante-llm` | Published subprocess-execution and provider-profile helper crates | library | none (helpers) | `crates/exec/src/lib.rs:1-14`, `crates/llm/src/lib.rs:1-6` |
| `ante-harbor` | Python adapter that runs `ante` headless inside Harbor benchmark sandboxes (not a client of the Op/Evt protocol) | per benchmark trial | none | `ante-harbor/ante_agent.py:176-200,412-436` |

### ACP surface

**Not found.** Searches over all `*.rs`/`*.toml` for `acp`, `agent_client_protocol`, `AgentClientProtocol`, `session/new`, `session/prompt`, and `initialize` handlers found no ACP code — only the README sentence (`README.md:226`), the stub crate itself, and false positives (`llamacpp` in the changelog). `ante-acp/Cargo.toml` declares the crate ("Agent Client Protocol support for Ante") but has an empty `[dependencies]` section, and its single source file is blank. There is no capability negotiation, no `initialize`/`newSession`/`prompt`/`cancel` handler, and no `agent-client-protocol` dependency in any `Cargo.lock` or manifest in the repository.

The surface an ACP adapter would wrap does exist and is fully typed in-repo: `ante serve` speaks newline-delimited `OpMsg`/`EventMsg` JSON over stdio (spawned by `crates/ante-sdk/src/connect.rs:120-156`), a Unix domain socket, or a loopback-only WebSocket with bearer auth (`crates/ante-sdk/src/endpoint.rs:14-28`, `crates/ante-sdk/src/connect/ws.rs`). The daemon that implements the server side is closed-source.

### Runtime and process boundaries

The SDK spawns `ante serve --stdio` as a child per connection and reaps it when the stream ends; dropping every `OpSender` closes the child's stdin, which is the child's shutdown signal (`crates/ante-sdk/src/connect.rs:116-156`). Two pump tasks move frames: `pump_ops` serializes ops one-per-line into the writer (`connect.rs:178-193`); `pump_events` parses lines, drops unparseable ones with a warning, and — if the client stops listening — drains the stream into a sink so a host blocked on a full write buffer can notice the peer is gone (`connect.rs:197-230`). Docs add that the host reads input independently of event output and applies a 30-second deadline per event write, disconnecting a stalled peer instead of blocking (`docs-site/docs/usage/serve.mdx:66`). Inside the daemon, the architecture doc describes a client/daemon split with a bounded op channel and unbounded event channel, and states the runtime is moving from a session-centric to an agent-centric ownership model (`docs-site/docs/reference/architecture.mdx:25-30`) — the implementation itself is not inspectable.

## LLM abstraction and integration

### Abstraction

Ante uses a **bespoke internal abstraction**, not a named third-party LLM library and not a provider SDK called directly. No manifest in the checkout depends on an LLM framework or an HTTP client (`rig`, `async-openai`, `genai`, `reqwest`, `hyper`): every `Cargo.toml` in the repository was searched, and the only LLM-adjacent dependency is the in-repo `ante-protocol-shape`. The one inspectable LLM module is the published helper crate `ante-llm` (`crates/llm/`), whose public surface is OpenAI-compatible provider/model profiles and an effort ladder (`crates/llm/src/lib.rs:1-2`). Its README states the boundary directly: "HTTP clients, authentication, catalog storage, and streaming stay in the Ante runtime for now" (`crates/llm/README.md:5-7`). `ante-llm` has no in-repository consumer — searching `ante-llm`/`OpenAiCompatProfile`/`thinking_params` outside `crates/llm/` matches only `CHANGELOG.md:25,50` — so its caller is the runtime.

The model loop is therefore **outside this repository**. The runtime is the prebuilt `ante` binary distributed without source (`BINARY-TERMS.md:3-6`), and the repository-visible boundary is the `ante serve` Op/Evt protocol (`crates/protocol-shape/src/msg.rs:8-21`). The architecture doc asserts the daemon "dispatches to LLM providers" and that "each provider implements a common interface" (`docs-site/docs/reference/architecture.mdx:25,68-70`), but neither that interface nor its implementations is in the checkout. **Inference:** `ante-llm` is the public, testable half of that interface — the effort/thinking/search normalization the runtime links — with the transport half withheld.

### Integration path

No ACP path exists, so the trace below is the native prompt path; every step after the op leaves the repository.

1. **History/message conversion:** not visible. The prompt enters as `Op::UserInput(String)`, a bare string rather than a provider message array (`crates/protocol-shape/src/msg.rs:44`). Converting persisted history plus the new text into provider request messages happens in the daemon.
2. **Provider/model selection:** resolved from the catalog at session start. `SessionRequest` pins `provider`, `model`, and `effort` (`crates/protocol-shape/src/msg.rs:516-539`), which the daemon resolves into the `ProviderSpec { id, display_name, base_url }` and `ModelSpec` it announces in `SessionStart` (`msg.rs:424-451,655-686`). Precedence (CLI flag, settings file, auto-detect of the first authenticated provider) is documented only (`docs-site/docs/usage/providers.mdx:12-16`, `docs-site/docs/reference/catalog-reference.mdx:424-428`).
3. **Request construction:** partially visible. `OpenAiCompatProfile::from_model` maps a `(provider_id, model_id)` pair to a `ThinkingDialect`, system-role policy, and search policy (`crates/llm/src/openai_compatible/profile.rs:104-112,243-347`), and `thinking_params`/`search_params` turn the requested `Effort` into concrete `reasoning_effort`/`thinking`/`enable_thinking`/`search_options` fields (`profile.rs:118-224`). The family table covers DeepSeek, GLM, Kimi, Qwen, MiniMax, Mistral, Muse Spark, and a generic fallback (`profile.rs:302-346`). Assembling those fields into an HTTP body, choosing a wire style (`AnthropicMessage`, `OpenAiCompatible`, `OpenAiResponse`, or `Gemini`, `docs-site/docs/reference/catalog-reference.mdx:257`), and attaching credentials are in the closed runtime.
4. **Streaming:** not visible. The wire exposes the results as `Thinking`/`ThinkingDelta` and `MessageDelta` followed by a final `AgentMessage` (`crates/protocol-shape/src/msg.rs:156-159`), and `Usage` normalization is documented as uniform across OpenAI/OpenRouter/DeepSeek/Anthropic (`msg.rs:772-787`), but the provider SSE/WebSocket reader is not in the checkout. The docs describe transport behavior (idle timeout, WebSocket-vs-HTTP/SSE for OpenAI) without implementation (`docs-site/docs/reference/architecture.mdx:70`, `docs-site/docs/reference/catalog-reference.mdx:73-101`).
5. **Tool-call handling:** partially visible. The wire types are `ToolUse { id, name, args, malformed_args, signature }` (`crates/protocol-shape/src/msg.rs:632-653`) and the `ToolStart`/`ToolUpdate`/`ToolEnd` sequence (`msg.rs:182-184,346-378`); `MalformedToolArgs` and `MISSING_TOOL_NAME` pin how a broken streamed call is represented (`msg.rs:620-646`). The loop that executes tools and feeds results back to the model runs in the daemon.
6. **Conversion back to runtime events:** the daemon emits `EventMsg` JSONL frames (`crates/protocol-shape/src/msg.rs:8-21`); the SDK parses and forwards them (`crates/ante-sdk/src/connect.rs:197-230`). The daemon side of that conversion is closed.

For an adapter or gateway the boundary is the same `ante serve` socket/stdio; everything from "op received" to "event emitted" is invisible.

### Provider and tool boundary

- **Provider-specific code:** the only provider-specific inspectable module is `crates/llm/src/openai_compatible/profile.rs`, which encodes per-family thinking, system-role, and search policy (`profile.rs:243-347`). Anthropic, OpenAI Responses, and Gemini wire code exists only as names in docs (`docs-site/docs/reference/architecture.mdx:72-89`, `docs-site/docs/reference/catalog-reference.mdx:12-30`); no such modules are in the checkout. **Not found** in any `*.rs` or `Cargo.toml`.
- **Credentials/configuration:** credentials and endpoints are catalog data, not code here. `~/.ante/catalog.json` defines `base_url`, `wire_style`, `auth` (`env_key` or `oauth_preset`), and `http_headers` (`docs-site/docs/reference/catalog-reference.mdx:250-375`); the resolved `base_url` is echoed on the wire in `ProviderSpec` (`crates/protocol-shape/src/msg.rs:424-429`). The Harbor adapter injects provider keys through the container environment (`ante-harbor/ante_agent.py:421-427`). Reading the env var or OAuth token and placing the header is closed.
- **Request/response normalization:** the visible normalization is the `ThinkingParams`/`SearchParams`/`SearchOptions` structures and the effort ladders (`crates/llm/src/openai_compatible/profile.rs:43-62,118-224,355-451`), plus the shared `Effort` enum and its `Ord` ordering (`crates/protocol-shape/src/msg.rs:688-738`). Provider response → `Evt` and `Usage` mapping is documented, not coded, in the checkout (`msg.rs:772-787`).
- **Tool schemas:** MCP tool schemas are visible as data on the wire (`McpToolInfo`/`McpToolParam`, `crates/protocol-shape/src/msg.rs:485-507`); the built-in tool contract is only a documented trait (`docs-site/docs/reference/architecture.mdx:100-108`). The concrete JSON schemas Ante sends to a provider, and the MCP client, are closed.
- **Multi-provider and selection:** yes, multi-provider. Built-in providers number seventeen across four wire styles (`docs-site/docs/reference/catalog-reference.mdx:12-30`), and custom providers are added through the catalog. Selection is by `--provider`/`--model`, `~/.ante/settings.json`, or auto-detect (`docs-site/docs/usage/providers.mdx:12-16`, `docs-site/docs/reference/catalog-reference.mdx:424-428`); a running local server is registered out-of-band with `Op::RegisterLocalProvider { port, model }` / `Op::RestoreLocalProvider` (`crates/protocol-shape/src/msg.rs:75-79`, `docs-site/docs/reference/protocol-reference.mdx:802`). The registry/factory that maps a wire style to an implementation is **Not found**; the resolver is closed.

### Limits

- The actual model-call site, HTTP/WebSocket client, request-body assembly, streaming parser, retry/timeout logic, credential storage, and tool-execution loop cannot be inspected: they live in the closed `ante` binary (`BINARY-TERMS.md:3-6`), and `crates/llm/README.md:5-7` states they stay in the runtime. Files checked: every `*.rs` and `Cargo.toml` in the checkout.
- `ante-llm` has no in-repository call path, so its behavior is established by its own unit tests (`crates/llm/src/openai_compatible/profile.rs:533-890`, `crates/llm/src/effort.rs:46-93`), not by a caller. **Inference:** the runtime links it.
- Message/history-to-provider conversion, system-prompt placement (the profile exposes `supports_system_role`/`merges_system_messages` at `profile.rs:230-236`, but the caller is closed), and the exact built-in tool JSON schemas are **Not found**.

## Session model

### Identity and ownership

A session is one conversation span on the daemon, identified by `Id::ses()` — a 4-character prefix plus a ULID (`crates/protocol-shape/src/id.rs:25-39`). The mapping is: **one connection ↔ at most one active session**. `Op::StartSession(SessionRequest)` starts a session "replacing any running one"; there is no separate restart op and `/clear` is implemented by re-sending the request (`crates/protocol-shape/src/msg.rs:37-40`). `Op::ResumeSession { session_id }` targets any previously persisted session by ID (`msg.rs:70-74`). `SessionInfo` (the `SessionStart` payload) carries the resolved identity: `session_id`, model, provider, cwd, permission mode, skills, subagents, and title (`msg.rs:434-451`). There is no ACP session ID: the concept does not apply at this revision. Turn identity is separate (`Id::step()`-prefixed `turn_id` in `TurnStart`/`TurnPause`/`TurnEnd`, `msg.rs:194-213`). Session end reasons are exactly `Replaced` (new or resumed session took over) and `Shutdown` (`msg.rs:313-319`).

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | Client sends `StartSession`; pinned fields win, unset fields resolve from the host's current defaults; daemon answers `SessionStart` with `SessionInfo` | Session dir created if saving enabled (`save_session`) | Not visible (closed daemon); docs describe resolution semantics only | `crates/protocol-shape/src/msg.rs:509-567`, `docs-site/docs/reference/protocol-reference.mdx:88-114` |
| Load/resume | `ResumeSession { session_id, unattended }`: daemon emits `SessionEnd(Replaced)` for the current session, then `SessionStart` + `ExtensionRefreshed` for the resumed one, then up to 200 replayed events; on failure an `Error` event | Snapshot restored; unpinned settings resolve to current defaults | `Error` event on the wire; stale/mismatched `ResumeSession` behavior beyond that not visible | `crates/protocol-shape/src/msg.rs:65-74`, `docs-site/docs/reference/protocol-reference.mdx:140-151` |
| Prompt | `UserInput` runs a turn: `TurnStart` → thinking/message deltas → `AgentMessage`/`ToolStart`/`ToolUpdate`/`ToolEnd` → possible `TurnPause` (approval or structured questions) → `TurnResume`/`TurnEnd` | User input is written to the persisted event log (replay-only variant, not emitted live) | `TurnEnd` carries `Completed`, `Interrupted`, or `Error { kind, headline, details }` | `crates/protocol-shape/src/msg.rs:44,138-235,321-344`, `docs-site/docs/reference/protocol-reference.mdx:769` |
| Cancel | `Interrupt` op aborts the running work; turn ends with `TurnEnd { Interrupted }` | Interrupted-turn persistence behavior not visible | Cancellation is connection-scoped (no session parameter on the op) | `crates/protocol-shape/src/msg.rs:43,322-327`, `docs-site/docs/reference/protocol-reference.mdx:290-296` |
| Close/delete | `Shutdown` closes the connection's session: stdio child exits when stdin closes; sock/ws hosts end only that client's session and keep serving | Session snapshot retained if saving was enabled | Client SDK's `close()` sends `Shutdown` and drains until `Goodbye` | `crates/protocol-shape/src/msg.rs:113-114,234`, `crates/ante-sdk/src/client.rs:86-93`, `docs-site/docs/usage/serve.mdx:33,62` |

**Delete:** Not found. The `Op` enum has no operation to delete or list stored sessions; deletion of `~/.ante/sessions/` entries is only described as a manual filesystem action in docs.

### Durable representation

Storage is local files under the Ante home: `~/.ante/sessions/<session-id>/` holds "persisted sessions (for `/resume` and `--resume`)" (`docs-site/docs/reference/storage-reference.mdx:20`). Whether anything is written is a per-session choice: `SessionRequest::save_session` — "whether the session writes a transcript and a resumable snapshot" (`crates/protocol-shape/src/msg.rs:556-558`) — and the headless/Harbor path disables it with `--no-session-save` (`ante-harbor/ante_agent.py:62`). Ordering keys are visible on the wire: every event carries a UTC timestamp, a fresh `evt_`/`op_`/`step_` ULID, and an optional `parent` op ID for correlation (`crates/protocol-shape/src/msg.rs:8-15`). Some events are deliberately excluded from the log: `Ambient` hints are "never persisted to the event log" (`msg.rs:225-229`), while `UserInput` is the inverse — persisted for replay but not emitted during live sessions (`docs-site/docs/reference/protocol-reference.mdx:769`). The snapshot/transcript file format, commit boundary (per event vs. per turn), and whether stored history or an internal message list is authoritative for future model context are **Not found** — the writing code is in the closed binary. **Inference:** the snapshot is authoritative for resume, because the resume contract says "what the host persisted is restored, and everything the snapshot does not pin resolves like a fresh session" (`crates/protocol-shape/src/msg.rs:65-69`).

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `unknown` | Docs claim per-connection sessions on a shared host (`--sock`/`--ws`); no locking, registry, or task-spawning code for the daemon exists in this repo | `docs-site/docs/usage/serve.mdx:33,45` |
| Two prompts in the same session | `unknown` | The protocol brackets one turn at a time and offers `Steer` for in-turn guidance; the example client enforces exclusion client-side (`busy` flag) and ignores Enter while a turn is in flight | `crates/protocol-shape/src/msg.rs:46,194-213`, `examples/mini-tui/src/main.rs:88-93,124-129` |
| Load/resume during an active prompt | Replacement (documented/inferred) | `StartSession` explicitly "replac[es] any running one" and `ResumeSession` emits `SessionEnd(Replaced)` for the current session before starting the new one; whether an in-flight turn is cancelled, drained, or lost first is not visible | `crates/protocol-shape/src/msg.rs:37-40`, `docs-site/docs/reference/protocol-reference.mdx:151` |
| Delete/close during an active prompt | Close ends the session | `Shutdown` or dropping the op side closes the connection; on stdio the child exits; on sock/ws only that session ends. No delete op exists | `crates/ante-sdk/src/connect.rs:116-119,158-162`, `crates/ante-sdk/src/client.rs:86-93` |
| Cancellation isolation | per connection (observed at the wire); `unknown` inside the daemon | `Interrupt` carries no session ID — it applies to the sender's active session; `Interrupt`/`Shutdown` remain responsive while a peer stops consuming events (documented) | `crates/protocol-shape/src/msg.rs:43`, `docs-site/docs/usage/serve.mdx:66` |

The SDK's own channels introduce one coupling worth naming: ops share a bounded 256-slot channel across the connection, and a caller using the non-blocking `try_send` has overflow *logged and dropped*, not queued (`crates/ante-sdk/src/connect.rs:91`, `crates/ante-sdk/src/client.rs:23-34`). Spawning per-connection pump tasks is not itself a correctness mechanism; nothing in this repository demonstrates that the daemon serializes or isolates concurrent sessions safely.

## Event and data flow

### New prompt: ACP client to live response

There is no ACP client path; the numbered trace below is the native client path (`examples/mini-tui` plus SDK), with the daemon interior marked as closed.

1. The client sends `Op::UserInput(text)`; `Client::send` stamps a fresh `op_` ULID (`crates/protocol-shape/src/msg.rs:24-26`, `crates/ante-sdk/src/client.rs:71-73`).
2. `pump_ops` serializes the op as one JSON line to the transport writer (`crates/ante-sdk/src/connect.rs:178-193`).
3. Inside the closed daemon, a turn runs: the protocol requires `TurnStart` first, streamed `ThinkingDelta`/`MessageDelta`, a final `AgentMessage` (complete text replacing the deltas), `ToolStart`/`ToolUpdate`/`ToolEnd` per tool, optional `TurnPause`/`TurnResume` for approvals or model questions, then `TurnEnd` and `UsageUpdate` (`crates/protocol-shape/src/msg.rs:138-235`).
4. Approval pauses are resolved by `Op::ApprovalResponse { turn_id, responses }`; stale or mismatched `turn_id`s are dropped by contract for questions (`crates/protocol-shape/src/msg.rs:47-60`).
5. `pump_events` parses each line into an `EventMsg` and forwards it to the client's unbounded receiver; the client folds deltas into streaming text and clears its busy state on `TurnEnd` (`crates/ante-sdk/src/connect.rs:197-230`, `examples/mini-tui/src/main.rs:143-178`).
6. Persistence of the prompt and its outputs happens inside the closed daemon; the only wire-visible commitment is that `UserInput` is written to the persisted event log (`docs-site/docs/reference/protocol-reference.mdx:769`).

### Durable history to ACP client

No ACP replay exists. On the native surface, replay is bounded and bundled with resume rather than being an independent operation:

1. The client sends `ResumeSession { session_id }` (`crates/protocol-shape/src/msg.rs:70-74`).
2. The daemon emits `SessionEnd(Replaced)` for the outgoing session, `SessionStart` + `ExtensionRefreshed` for the resumed session, then **up to 200** replayed historical events; on failure a single `Error` event (`docs-site/docs/reference/protocol-reference.mdx:151`).
3. The replay stream reuses the live `Evt` vocabulary, with `UserInput` appearing only in replay (`docs-site/docs/reference/protocol-reference.mdx:769`) and `Ambient` never appearing (`crates/protocol-shape/src/msg.rs:225-229`). Whether tool results, thinking, or usage replay in full, and how the 200-event window is selected (most recent N by log order is **Inference**), is not visible.

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `Op::UserInput` | none (no ACP) | persisted event-log `UserInput` entry | inside closed daemon | `crates/protocol-shape/src/msg.rs:44`, protocol-reference.mdx:769 |
| Assistant text | `MessageDelta` stream then final `AgentMessage` | none | transcript (documented as "transcript", granularity not visible) | inside closed daemon | `crates/protocol-shape/src/msg.rs:156-158` |
| Reasoning/thought | `Thinking` / `ThinkingDelta` | none | unknown (not documented) | unknown | `crates/protocol-shape/src/msg.rs:157-159` |
| Tool call | `ToolStart(ToolUse)` (id, name, args, malformed-args marker, signature) | none | unknown | unknown | `crates/protocol-shape/src/msg.rs:632-653` |
| Tool result | `ToolEnd { tool_use_id, tool_name, status, result_json }` with `seq`-numbered `ToolUpdate` progress | none | unknown | unknown | `crates/protocol-shape/src/msg.rs:346-378` |
| Completion/failure | `TurnEnd { status: Completed/Interrupted/Error }` + `UsageUpdate` | none | `SessionEnd` carries final usage at session close | inside closed daemon | `crates/protocol-shape/src/msg.rs:207-221,322-344,144-148` |

No step in this table can be pinned to a commit boundary or an ordering guarantee relative to the client notification: all persistence is daemon-internal.

### Subsequent-prompt reconstruction

On `ResumeSession`, the daemon restores the saved dialog and the pinned model/provider/effort from the snapshot, re-resolves anything unpinned from current defaults, re-discovers extensions (skills, subagents, MCP), and replays recent events for the client's view (`crates/protocol-shape/src/msg.rs:65-74`, `docs-site/docs/reference/protocol-reference.mdx:140-151`). How the snapshot becomes model context (filtering, compaction state, whether the ≤200-event replay window matches what the model sees) is **Not found**; compaction itself is observable only as the `Compact` op and `CompactStart`/`CompactEnd { summary }` events, where the summary "replaces the compacted history and carries forward as the session's context" (`crates/protocol-shape/src/msg.rs:83-89,185-193`).

### Ordering, cancellation, failure, and backpressure

- **Ordering:** events flow as a single FIFO stream per connection, each stamped with a monotonic ULID, a timestamp, and an optional `parent` op ID (`crates/protocol-shape/src/msg.rs:8-15`). The SDK preserves order into the unbounded channel.
- **Backpressure (client side):** the op channel is bounded at 256; `try_send` on a full channel logs `error!` and **drops** the op (`crates/ante-sdk/src/client.rs:23-34`). The event channel is unbounded — "the host neither waits on nor drops for a slow client, so a client that stops reading builds a backlog rather than losing events" (`client.rs:41-44`); the in-process variant warns at a 4096-event backlog (`docs-site/docs/reference/protocol-reference.mdx`, Transport section).
- **Backpressure (host side, documented only):** input is read independently of output; each event write has a 30-second deadline and a stalled or flooding peer is disconnected (`docs-site/docs/usage/serve.mdx:66`).
- **Cancellation:** `Interrupt` → `TurnEnd { Interrupted { reason } }`. What happens to partially streamed text (the final `AgentMessage` replacing deltas implies re-render, `examples/mini-tui/src/main.rs:152-158`), to in-flight tools, and to the durable log for an interrupted turn is not visible. Clients are warned that a cancelled turn may end without a `TurnResume` (`crates/protocol-shape/src/msg.rs:246-250`).
- **Failure:** `TurnEnd { Error { kind, headline, details } }` with a stable machine-readable `kind` for LLM failures (e.g. `"oauth"`), consumed downstream by the Harbor adapter's error mapping (`crates/protocol-shape/src/msg.rs:328-344`, `ante-harbor/ante_agent.py:64-75`).

## Capability matrix

No ACP layer exists at this revision, so rows assess the native `ante serve` client surface — the layer an ACP adapter would sit on.

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | `unknown` | Documented for `--sock`/`--ws` ("each connection drives its own session"); no daemon code to verify | `docs-site/docs/usage/serve.mdx:33,45` |
| Concurrent work across sessions | `unknown` | Same evidence gap; the shared host is asserted, isolation is not demonstrated | `docs-site/docs/usage/serve.mdx:45` |
| Same-session prompt exclusion | `unknown` | Turn bracketing + `Steer` exist; daemon queueing/rejection not visible; example client self-serializes | `crates/protocol-shape/src/msg.rs:46,194-213` |
| Durable sessions | `yes` | `save_session` flag, `~/.ante/sessions/<id>/`, `ResumeSession` op in the shipped wire schema | `crates/protocol-shape/src/msg.rs:556-558,70-74`, storage-reference.mdx:20 |
| Session list | `partial` | No wire op to list sessions (searched `Op` enum); the in-process TUI resume picker reads `~/.ante/sessions` locally per docs | `crates/protocol-shape/src/msg.rs:36-114`, storage-reference.mdx:20 |
| Session load/resume | `yes` | First-class `ResumeSession` with documented restore semantics | `crates/protocol-shape/src/msg.rs:65-74` |
| History replay to ACP client | `partial` | Replay exists on the native surface (≤200 events after resume); no ACP channel to replay into | protocol-reference.mdx:151 |
| Prior history reused by model | `yes` | Resume "restores the saved dialog and pinned model, provider, and effort" into the session; snapshot is the contract | protocol-reference.mdx:140, `crates/protocol-shape/src/msg.rs:65-69` |
| Prompt cancellation | `yes` | `Interrupt` op and `TurnEndStatus::Interrupted`; wired to Esc in the example client | `crates/protocol-shape/src/msg.rs:43,322-327`, mini-tui main.rs:101 |
| Tool-call progress updates | `yes` | `ToolStart`, seq-numbered `ToolUpdate`, terminal `ToolEnd` with status | `crates/protocol-shape/src/msg.rs:182-184,346-378` |
| Partial-output persistence | `unknown` | Transcript granularity (deltas vs. final messages) not visible | Not found in repo |
| Recovery after process restart | `partial` | Sessions are resumable by explicit `ResumeSession` from the snapshot; no auto-reattach or crash-recovery flow in evidence | `crates/protocol-shape/src/msg.rs:65-74` |

## Design assessment

### Strengths

- The wire schema is a small, published crate with exhaustive enums, versioned-with-defaults serde policies (new fields are `#[serde(default)]` so old payloads still decode), and round-trip tests pinning backward compatibility (`crates/protocol-shape/src/msg.rs:953-1024,1142-1154`).
- The event model cleanly separates the three state classes a harness cares about: live stream (deltas), final records (`AgentMessage` after deltas), and replay-only records (`UserInput`), plus an explicit never-persisted class (`Ambient`) (`crates/protocol-shape/src/msg.rs:156-159,225-229`, protocol-reference.mdx:769).
- Session replacement (`StartSession` supersedes, `SessionEnd(Replaced)`) gives clients a deterministic session-end signal instead of silent swap (`crates/protocol-shape/src/msg.rs:37-40,313-319`).
- The SDK's failure posture is explicit: unparseable host lines are dropped with a warning rather than crashing the stream, and close is a `Shutdown`→`Goodbye` handshake that tolerates a gone host (`crates/ante-sdk/src/connect.rs:219-221`, `crates/ante-sdk/src/client.rs:86-93,102-117`).

### Tradeoffs and limitations

- The published repository cannot substantiate its headline integration claim: `ante-acp` is empty, so any ACP behavior attributed to Ante is unverifiable from source at this revision (`ante-acp/src/lib.rs:1`, `README.md:226`).
- Replay is capped at 200 events (`docs-site/docs/reference/protocol-reference.mdx:151`); long sessions cannot be fully rebuilt by a new client, and the selection rule for the window is unspecified.
- The client-side op channel drops ops on overflow while logging (`crates/ante-sdk/src/client.rs:29-30`); for ops like `ApprovalResponse` a silent drop would stall a paused turn.
- No wire-level session list or delete: clients driving a shared host cannot enumerate or clean up stored sessions through the protocol.
- Concurrency guarantees (cross-session isolation, same-session prompt exclusion, persistence timing) are all opaque — the protocol is a promise whose enforcement is not observable.

### Ideas relevant to Ox

- Ante's protocol-shape crate — wire types shipped separately from the agent, with `#[serde(default)]` versioning and round-trip tests — is a pattern Ox could mirror for its persisted-event schema if it ever needs external clients. (Fact: the crate is standalone; Judgment: worth mirroring.)
- The `UserInput`-persisted-but-not-emitted-live split, plus a never-persisted ephemeral class, is a clean way to make replay symmetric without double-rendering user text — Ox's replay path in `src/acp.rs`/`src/sessions.rs` could adopt the same three-way classification. (Judgment; the tradeoff is that clients must special-case replay variants.)
- Bounded replay (≤200 events) is a deliberate complexity cap, but it pushes state reconstruction onto clients; Ox already persists full transcripts in SQLite and should keep full-fidelity replay rather than a window. (Fact: the cap; Judgment: prefer Ox's approach.)
- The pinned-vs-resolved `SessionRequest` fold (`patched()`, `crates/protocol-shape/src/msg.rs:569-617`) is a tidy way to express "restart with the same request" without a restart op; it relies on compile-time exhaustiveness to force a decision per new field.

## Unknowns and conflicts

- **README vs. code conflict:** `README.md:226` says `ante-acp` "provides Agent Client Protocol support"; the crate contains no code and no dependencies. Reported as the conflict it is; ACP behavior is **Not found**.
- **Closed daemon:** the process that owns sessions, runs the model loop, executes tools, and writes `~/.ante/sessions/` is a prebuilt binary (`BINARY-TERMS.md:3-6`); session persistence format, commit timing, in-daemon locking, and task scheduling are unverifiable. Searches: `grep -rn` for `StartSession|handle_op|fn serve|UnixListener` and ACP terms across `*.rs`/`*.toml` — no daemon implementation matches.
- **Same-turn input semantics:** whether `UserInput` during an active turn queues, rejects, or runs concurrently is not stated in code or docs; only `Steer` (explicit in-turn guidance) is defined (`crates/protocol-shape/src/msg.rs:46`).
- **Replay window selection:** how the ≤200 replayed events are chosen, and whether tool results/thinking replay, is not specified anywhere in the repo.
- **The `claude` module** (`crates/ante-sdk/src/claude/`) drives Claude Code as a child process via its stream-json protocol with `--session-id`/`--resume` fixed at launch (`crates/ante-sdk/src/claude/mod.rs:29-33`); it is unrelated to Ante sessions and was excluded from the session analysis. It does show the project treats wrapped agents as launch-time-pinned subprocesses, not multiplexed ones.
- **Tests:** repository tests cover the SDK bridge, serde round-trips, endpoints, and the exec crate (`crates/ante-sdk/src/connect.rs:232-323`, `crates/protocol-shape/src/msg.rs:908-1318`, `crates/exec/src/tests.rs`); none exercise the daemon.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `ante-acp/src/lib.rs:1`, `ante-acp/Cargo.toml:11`, `README.md:226` | Proves the ACP crate is an empty stub contradicting the README claim |
| Session management | `crates/protocol-shape/src/msg.rs:36-74,434-451,509-567`, `crates/protocol-shape/src/id.rs:25-39` | Session ops, identity, pinned/resolved request semantics, ID scheme |
| Concurrency | `crates/ante-sdk/src/connect.rs:91,110-156`, `examples/mini-tui/src/main.rs:88-93`, `docs-site/docs/usage/serve.mdx:33,45,66` | Channel bounds, per-connection child, client-side serialization, documented shared-host behavior |
| Persistence | `crates/protocol-shape/src/msg.rs:556-558,225-229`, `docs-site/docs/reference/storage-reference.mdx:20`, `ante-harbor/ante_agent.py:62` | `save_session`, never-persisted class, session directories, `--no-session-save` |
| Event translation | `crates/protocol-shape/src/msg.rs:8-15,138-235,321-378`, `crates/ante-sdk/src/connect.rs:178-230` | Event envelope, event vocabulary, tool events, JSONL↔channel pumps |
| LLM integration | `crates/llm/src/lib.rs:1-2`, `crates/llm/README.md:5-7`, `crates/llm/src/openai_compatible/profile.rs:104-224,243-347`, `crates/protocol-shape/src/msg.rs:424-451,516-539,655-686`, `docs-site/docs/reference/catalog-reference.mdx:250-375` | Public `ante-llm` profile/effort surface vs. closed runtime; provider/model selection and wire styles; catalog auth |
| Replay/reconstruction | `crates/protocol-shape/src/msg.rs:65-74,83-89,185-193`, `docs-site/docs/reference/protocol-reference.mdx:140-151,769` | `ResumeSession` contract, compaction events, replay cap, replay-only `UserInput` |
| Cancellation/errors | `crates/protocol-shape/src/msg.rs:43,322-344`, `crates/ante-sdk/src/client.rs:86-93`, `ante-harbor/ante_agent.py:64-75` | `Interrupt`, `TurnEnd` statuses, close handshake, downstream error mapping |

## Research notes

- **Revision inspected:** `9eca8821e004cd76a1c75e396fb9a78c7e06b67a` (repo contains a single squashed commit, "docs: sync with v0.2.1"; no history available).
- **Primary evidence:** `ante-acp/` (stub), `crates/protocol-shape/src/{msg,id}.rs`, `crates/ante-sdk/src/{lib,client,connect,endpoint,stdio}.rs` and `claude/`, `crates/exec/src/lib.rs`, `crates/llm/src/lib.rs`, `examples/mini-tui/src/main.rs`, `ante-harbor/ante_agent.py`.
- **Relevant docs:** `docs-site/docs/usage/serve.mdx`, `docs-site/docs/reference/{protocol-reference,storage-reference,architecture}.mdx`, `README.md`, `BINARY-TERMS.md`. Docs were used only where code is absent, and are labeled as such.
- **Commands/tests run:** read-only inspection (`git log`/`rev-parse`, `grep`, targeted file reads). No tests executed — the repo's tests cover SDK/serde only, not the daemon.
- **Report confidence:** `medium` — the ACP absence, wire schema, and SDK behavior are established from code at the pinned revision, but every daemon-internal property (concurrency, persistence timing, replay internals) rests on the repo's own documentation for a closed binary.
