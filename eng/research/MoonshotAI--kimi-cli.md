---
project: "MoonshotAI/kimi-cli"
repository: "https://github.com/MoonshotAI/kimi-cli"
revision: "86f136422a0aae6b217ea49e7ea1d2e8a1defcd2"
researched_at: "2026-09-19"
primary_language: "python"
implementation_form: "native-agent"
process_model: "single-process"
session_owner: "agent-runtime"
durability: "local-files"
cross_session_concurrency: "concurrent"
same_session_concurrency: "concurrent"
event_delivery: "direct"
resume_strategy: "reconstruct"
overall_confidence: "high"
---

# MoonshotAI/kimi-cli ACP architecture

## Executive summary

- **ACP boundary:** The `kimi acp` command runs a native multi-session ACP server in-process over stdio using the pinned `agent-client-protocol==0.8.0` SDK (`src/kimi_cli/acp/__init__.py:1`, `pyproject.toml:8`). A deprecated single-session variant raises on every method (`src/kimi_cli/ui/acp/__init__.py:24`). `ACPServer` implements initialize/new/load/resume/list/prompt/cancel and advertises `load_session`, session list/resume, images, embedded context, and HTTP MCP (`src/kimi_cli/acp/server.py:98`).
- **Session model:** One ACP session ID is one `Session` (a `uuid4`) mapped to one `KimiCLI` runtime (agent loop + LLM + tools) and one `ACPSession` translator wrapper, registered in a plain dict (`src/kimi_cli/acp/server.py:160`, `:172`). Durable state lives under `<share_dir>/sessions/<workdir>/<session-id>/` as `context.jsonl` (model conversation) and `wire.jsonl` (UI transcript).
- **Concurrency:** The SDK dispatcher spawns one asyncio task per incoming request, so prompts for different sessions genuinely run concurrently (`agent-client-protocol` `acp/task/dispatcher.py:88`). There is no per-session exclusion: a second prompt to the same session overwrites the turn state and would interleave shared runtime state — concurrent but unguarded.
- **Durability and replay:** Model context is appended per message to `context.jsonl` and restored on load; the UI transcript is appended (merged) to `wire.jsonl` by an async recorder task and replayed to the client on `session/load` (`src/kimi_cli/acp/session.py:250`). Resume without replay and fork are separate; fork raises `NotImplementedError` (`src/kimi_cli/acp/server.py:301`).
- **Event flow:** Live updates are direct awaited `session_update` calls from the translator to the SDK connection, driven by an unbounded wire queue between the agent loop and the translator; a slow client therefore cannot stall the model loop but can grow memory without bound.
- **Notable uncertainty:** On a fatal step error the server emits `StepInterrupted`, which the ACP handler treats as end-of-turn (`src/kimi_cli/acp/session.py:172`); whether the client observes `end_turn` or `internal_error` depends on a race between the wire message and the exception propagating out of the generator (**Inference** from code order).

## Classification

| Dimension | Value | Meaning in this project | Evidence |
| --- | --- | --- | --- |
| Implementation form | `native-agent` | The ACP server lives inside the same Python process and codebase as the agent loop; no wrapped executable or translating adapter process. | `src/kimi_cli/acp/__init__.py:1`; `src/kimi_cli/acp/server.py:30` |
| Process model | `single-process` | One asyncio process serves all sessions over one stdio connection; MCP tool servers are child processes, not session hosts. | `src/kimi_cli/acp/__init__.py:13`; `src/kimi_cli/acp/server.py:34` |
| Session owner | `agent-runtime` | The durable `Session` and the `KimiCLI`/`KimiSoul` runtime own context, loop, and tools; the ACP layer holds only a registry of thin translator wrappers. | `src/kimi_cli/session.py:22`; `src/kimi_cli/acp/session.py:123` |
| Durability | `local-files` | JSONL files (context, wire transcript, session state, subagents) under the share dir (`~/.kimi` by default); no database. | `src/kimi_cli/session.py:49`; `src/kimi_cli/share.py:7`; `src/kimi_cli/wire/file.py:124` |
| Cross-session concurrency | `concurrent` | Each request runs as its own asyncio task; sessions share no locks, only the config file and share-dir metadata. | `acp/task/dispatcher.py:88`; `src/kimi_cli/acp/server.py:34` |
| Same-session concurrency | `concurrent` | No rejection, serialization, or cancel-previous; a second prompt replaces `_TurnState` and shares the runtime — racy, effectively undefined. | `src/kimi_cli/acp/session.py:157`; `:307` |
| Event delivery | `direct` | Live updates are awaited `conn.session_update(...)` calls inside the prompt handler; an internal unbounded queue feeds the translator. | `src/kimi_cli/acp/session.py:358`; `src/kimi_cli/wire/__init__.py:76` |
| Resume strategy | `reconstruct` | Load re-finds the session, restores `Context` from `context.jsonl`, and replays `wire.jsonl` as ACP updates. | `src/kimi_cli/acp/server.py:256`; `src/kimi_cli/app.py:305` |

## System architecture

```text
┌──────────────────────── ACP client (editor) ─────────────────────────┐
│  requests: initialize / new_session / load / prompt / cancel          │
│  updates ◄────────────────────────────────────────────────────────┐   │
└──────────────┬────────────────────────────────────────────────────┼───┘
               │ newline-delimited JSON-RPC 2.0 over stdio          │
┌──────────────▼────────────────────────────────────────────────────┴───┐
│ `kimi acp` process — one Python asyncio process, all sessions         │
│                                                                       │
│  acp SDK: Connection + DefaultMessageDispatcher                       │
│    one asyncio task per incoming request/notification                 │
│                │                                                      │
│  ACPServer  sessions: {id → (ACPSession, _ModelIDConv)}               │
│                │                                                      │
│  ACPSession (translator, no durable state)                            │
│    prompt(): async-for over Wire  ──► session_update (direct await)   │
│    replay_history(): read wire.jsonl ──► session_update               │
│        ▲ raw queue (live deltas)  │ merged queue                      │
│  KimiCLI.run ── cancel_event mirror ──┤  _WireRecorder task           │
│                │ run_soul(soul_task)   ▼                              │
│  KimiSoul: turn → step loop → kosong.step (LLM stream)                │
│            → KimiToolset → tool asyncio tasks → MCP subprocesses      │
│                │                                                      │
│  Context              WireFile            SessionState                │
└───────┬──────────────────┬──────────────────────┬─────────────────────┘
        ▼                  ▼                      ▼
 <share>/sessions/<wd>/<id>/context.jsonl  wire.jsonl  state, subagents/
```

| Component | Responsibility | Lifetime | State owned | Evidence |
| --- | --- | --- | --- | --- |
| `acp` SDK Connection/Dispatcher | JSON-RPC framing, per-request tasks, response send | Process | In-flight request state | `acp/connection.py:61`; `acp/task/dispatcher.py:31` |
| `ACPServer` | Method handlers, session registry, auth, model switching | Process | `sessions` dict, negotiated version, auth methods | `src/kimi_cli/acp/server.py:30` |
| `ACPSession` | Translate Wire ⇄ ACP updates; approval bridging; replay | Session | `_TurnState` (turn ID, cancel event, tool-call map) | `src/kimi_cli/acp/session.py:113` |
| `KimiCLI` / `KimiSoul` | Agent loop: turns, steps, LLM, tools, compaction, hooks | Session | Soul, context, toolset, wire hub | `src/kimi_cli/app.py:379`; `src/kimi_cli/soul/kimisoul.py:228` |
| `Context` | Model-conversation history, checkpoints, token usage | Session | In-memory `Message` list + `context.jsonl` | `src/kimi_cli/soul/context.py:20` |
| `Wire` + `_WireRecorder` | Broadcast agent events to UI and transcript file | Per prompt run | Raw/merged queues, recorder task | `src/kimi_cli/wire/__init__.py:18`; `:130` |
| `Session` | Durable identity, paths, metadata, titles | Session | Session dir paths, `SessionState` | `src/kimi_cli/session.py:22` |

### ACP surface

Transport is newline-delimited JSON-RPC 2.0 over stdio, provided by the pinned `agent-client-protocol` SDK; `acp_main` runs `acp.run_agent(ACPServer(), use_unstable_protocol=True)` (`src/kimi_cli/acp/__init__.py:13`). `ACPServer.initialize` negotiates version 1 (the only supported version, `src/kimi_cli/acp/version.py:21`), stores client capabilities, builds a terminal-auth method, and returns capabilities: `load_session=True`, `prompt_capabilities(embedded_context=True, image=True, audio=False)`, `mcp_capabilities(http=True, sse=False)`, and session list/resume capabilities (`src/kimi_cli/acp/server.py:98`). `new_session` and `load_session`/`resume_session` check OAuth token presence first and raise `AUTH_REQUIRED` with terminal-auth data if missing (`src/kimi_cli/acp/server.py:128`). Handlers: `prompt` → `ACPSession.prompt` (`server.py:400`), `cancel` → `ACPSession.cancel` (`server.py:410`), `set_session_mode` only accepts `"default"` (`server.py:324`), `set_session_model` rebuilds the LLM and persists the choice into the global config file (`server.py:327`), `fork_session`/`ext_method`/`ext_notification` raise `NotImplementedError` (`server.py:298`, `:418`). The ACP layer is native to the agent: no subprocess or protocol adapter sits between `ACPSession` and `KimiSoul`.

A second, deprecated surface exists: `KimiCLI.run_acp` wraps the already-constructed `Soul` in `ACPServerSingleSession`, whose every handler raises `invalid_params` with a "deprecated" message (`src/kimi_cli/ui/acp/__init__.py:11`, `:24`). It is dead weight kept only for the `--acp` flag (`src/kimi_cli/cli/__init__.py:237`).

### Runtime and process boundaries

Everything runs on one asyncio event loop. The SDK receive loop pushes messages onto a queue and a dispatcher loop spawns one supervised task per request/notification (`acp/task/dispatcher.py:56`, `:88`); nothing serializes requests. Each `session/prompt` call runs `KimiCLI.run`, which spawns a `run_soul` task plus a cancel-mirror task, and `run_soul` spawns the soul task, a notification pump, and the UI loop task (`src/kimi_cli/app.py:649`; `src/kimi_cli/soul/__init__.py:206`). Within a step, `kosong.step` streams message parts synchronously to `wire_send` and hands tool calls to `KimiToolset.handle`, which returns `asyncio.create_task(_call())` — tool calls execute concurrently as tasks (`src/kimi_cli/soul/toolset.py:595`). MCP tool servers are separate child processes connected by fastmcp. The only cross-session shared mutable state is the global config file (written by `set_session_model`) and the share-dir session metadata (`kimi.json`), which is loaded/saved without locks (`src/kimi_cli/session.py:139`).

## LLM abstraction and integration

### Abstraction

The project uses a **bespoke internal abstraction library, `kosong`**, which is developed in this repository as a workspace package (`packages/kosong/`) and pinned by the CLI as `kosong[contrib]==0.56.0` (`pyproject.toml:12`; workspace member list at `pyproject.toml:60`, `:62`). `kosong` is not a third-party agent framework: it defines its own provider protocol, stream protocol, and orchestration loop, and wraps the official provider SDKs directly.

- Provider interface: `ChatProvider` is a `runtime_checkable` `Protocol` exposing `generate(system_prompt, tools, history) -> StreamedMessage`, `model_name`, `thinking_effort`, and `with_thinking` (`packages/kosong/src/kosong/chat_provider/__init__.py:15`). `StreamedMessage` is a protocol yielding `StreamedMessagePart` (`ContentPart | ToolCall | ToolCallPart`) with `id` and `usage` (`packages/kosong/src/kosong/chat_provider/__init__.py:80`).
- Orchestration: `kosong.step` layers tool dispatch over `kosong.generate` (`packages/kosong/src/kosong/__init__.py:104`); `generate` is the actual model-call consumer that iterates the provider stream and merges parts (`packages/kosong/src/kosong/_generate.py:17`, `:56`).
- Backends are real SDK clients, not manifest-only deps: the Kimi provider constructs an `AsyncOpenAI` (`packages/kosong/src/kosong/chat_provider/kimi.py:10`, `:128`), and the contrib providers import `anthropic` (`packages/kosong/src/kosong/contrib/chat_provider/anthropic.py:16`), `google.genai` (`packages/kosong/src/kosong/contrib/chat_provider/google_genai.py:17`), and `openai` (`packages/kosong/src/kosong/contrib/chat_provider/openai_legacy.py:7`; `packages/kosong/src/kosong/contrib/chat_provider/openai_responses.py:7`). The corresponding SDKs are declared in `packages/kosong/pyproject.toml:8` (anthropic), `:9` (google-genai), `:12` (openai).

`kimi-cli` itself contains no model loop or HTTP client for chat; its `src/kimi_cli/llm.py` only maps configuration onto kosong constructors, and the agent loop calls into `kosong.step`.

### Integration path

Normal prompt flow from history to runtime events:

1. **Model/provider selection.** `KimiCLI.create` picks an `LLMModel` (explicit `--model`, else `config.default_model`) and its named `LLMProvider`, applies environment overrides via `augment_provider_with_env_vars`, and calls `create_llm` (`src/kimi_cli/app.py:215`, `:231`, `:243`; `src/kimi_cli/llm.py:276`).
2. **Provider construction.** `create_llm` resolves the API key (OAuth-aware) and dispatches on `provider.type` in a `match`, constructing the kosong provider; for `kimi` it installs generation kwargs (`prompt_cache_key=session_id`, optional temperature/top_p/max tokens) and returns an `LLM` dataclass wrapping the provider with context size and capabilities (`src/kimi_cli/llm.py:343`, `:349`, `:353`, `:495`; `LLM` at `src/kimi_cli/llm.py:53`).
3. **Per-step request setup.** `KimiSoul._step` reads `runtime.llm.chat_provider`, normalizes history (adjacent user messages merged), computes request-scoped completion overrides, and wraps the provider with `with_kimi_generation_overrides` and a trace callback (`src/kimi_cli/soul/kimisoul.py:1130`, `:1182`, `:1190`).
4. **Model call.** It invokes `kosong.step(request_chat_provider, system_prompt, toolset, effective_history, on_message_part=wire_send, on_tool_result=wire_send)` inside a tenacity retry (`src/kimi_cli/soul/kimisoul.py:1209`, `:1224`). `kosong.step` calls `kosong.generate`, which awaits `chat_provider.generate(...)`, then iterates the returned stream and merges parts in place (`packages/kosong/src/kosong/__init__.py:159`; `packages/kosong/src/kosong/_generate.py:56`, `:61`).
5. **Request construction.** In the Kimi backend, `generate` builds OpenAI-shaped `messages` (system prompt plus `_convert_message` per history item), normalizes kwargs, and calls `self.client.chat.completions.create(..., stream=True, stream_options={"include_usage": True})`, returning a `KimiStreamedMessage` (`packages/kosong/src/kosong/chat_provider/kimi.py:154`, `:174`, `:196`). `_convert_message` extracts `ThinkPart` into `reasoning_content` and drops empty content alongside tool calls (`:326`).
6. **Streaming consumption.** `KimiStreamedMessage._convert_stream_response` translates chunks into `ThinkPart`, `TextPart`, `ToolCall`, and `ToolCallPart` (`packages/kosong/src/kosong/chat_provider/kimi.py:469`); `_generate` forwards each raw part to `on_message_part` and each completed `ToolCall` to `toolset.handle` (`packages/kosong/src/kosong/_generate.py:63`, `:71`). In kimi-cli the callback is `wire_send`, so model deltas become Wire/runtime events directly (`src/kimi_cli/soul/kimisoul.py:1214`).
7. **Tool-call handling and context growth.** Complete tool calls are dispatched to `KimiToolset`, awaited via `result.tool_results()`, then `_grow_context` appends the assistant message and tool-result messages to `Context` (`src/kimi_cli/soul/kimisoul.py:1279`, `:1389`).
8. **Compaction** reuses the same path with `EmptyToolset` and a fixed system prompt (`src/kimi_cli/soul/compaction.py:126`). ACP `set_session_model` rebuilds the provider through `create_llm` and swaps `runtime.llm` (`src/kimi_cli/acp/server.py:357`).

### Provider and tool boundary

- **Provider-specific code** lives entirely in kosong: `packages/kosong/src/kosong/chat_provider/` (`kimi.py`, `echo/`, `chaos.py`, `mock.py`) and `packages/kosong/src/kosong/contrib/chat_provider/` (`anthropic.py`, `google_genai.py`, `openai_legacy.py`, `openai_responses.py`). The CLI-side factory is only the `match provider.type` in `create_llm` (`src/kimi_cli/llm.py:349`).
- **Credentials/configuration** are typed on the CLI side: `LLMProvider` carries `type`, `base_url`, `api_key` (`SecretStr`), `env`, `custom_headers`, `reasoning_key`, and an OAuth reference; `LLMModel` carries `provider`, `model`, `max_context_size`, `capabilities`, and `display_name` (`src/kimi_cli/config.py:35`, `:60`). Secrets are resolved in `create_llm` (`src/kimi_cli/llm.py:343`), with env-var overrides in `augment_provider_with_env_vars` (`src/kimi_cli/llm.py:276`). The Kimi provider independently falls back to `KIMI_API_KEY`/`KIMI_BASE_URL` (`packages/kosong/src/kosong/chat_provider/kimi.py:112`).
- **Request/response normalization** is per provider. Kimi builds OpenAI params and normalizes tools in `_convert_message`/`_convert_tool` (`packages/kosong/src/kosong/chat_provider/kimi.py:326`, `:365`), sharing `create_openai_client`, `convert_error`, and `tool_to_openai` (`packages/kosong/src/kosong/chat_provider/openai_common.py:24`, `:75`, `:155`); responses and usage are normalized in `KimiStreamedMessage` (`packages/kosong/src/kosong/chat_provider/kimi.py:423`, `:445`, `:469`). Each contrib provider performs its own analogous conversion.
- **Tool schemas** are kosong's `Tool` pydantic model (`name`, `description`, `parameters` JSON Schema) validated against the JSON Schema metaschema (`packages/kosong/src/kosong/tooling/__init__.py:18`). kimi-cli tools subclass `CallableTool`/`CallableTool2`, whose `params` Pydantic model is turned into JSON Schema at construction (`packages/kosong/src/kosong/tooling/__init__.py:232`, `:279`); `KimiToolset.tools` exposes `tool.base` (`src/kimi_cli/soul/toolset.py:281`). MCP tools pass the server's `inputSchema` through verbatim (`src/kimi_cli/soul/toolset.py:907`, `:923`), and MCP results are converted by `convert_mcp_content` (`packages/kosong/src/kosong/tooling/mcp.py:1`). Provider wire-format translation happens per provider (e.g. `_convert_tool`/`tool_to_openai`).
- **Multi-provider:** yes. `ProviderType` enumerates `kimi`, `openai_legacy`, `openai_responses`, `anthropic`, `google_genai`/`gemini`, `vertexai`, plus internal `_echo`/`_scripted_echo`/`_chaos` (`src/kimi_cli/llm.py:32`). Selection is by `provider.type` in the `create_llm` `match` (`src/kimi_cli/llm.py:349`); the user selects a model alias whose `LLMModel.provider` names an `LLMProvider` (`src/kimi_cli/config.py:60`, `:228`). There is no plugin registry or dynamic entry-point dispatch — the `match` is the sole factory, and `vertexai` reuses the Google GenAI provider with a `vertexai=True` flag (`src/kimi_cli/llm.py:431`).

### Limits

- The traced request/response path is the Kimi provider (the default). The Anthropic, Google GenAI, OpenAI-legacy, and OpenAI-responses contrib providers were inspected for imports and class structure but not traced line by line; their exact normalization behavior is not asserted here. Files checked: `packages/kosong/src/kosong/contrib/chat_provider/{anthropic,google_genai,openai_legacy,openai_responses}.py`.
- The concrete set of providers/models available at runtime is not fixed in code: it is read from the user's `~/.kimi/config.toml` and from the managed-model OAuth/auth flows (`src/kimi_cli/auth/oauth.py`, `src/kimi_cli/auth/platforms.py`), which were not traced.
- Server-side provider behavior (model routing, tokenization, real token accounting) is external to the checkout. `estimate_request_tokens` is an explicit local heuristic, not a provider tokenizer (`src/kimi_cli/llm.py:198`).
- `kosong` is both an in-repo workspace package and a pinned PyPI dependency (`kosong[contrib]==0.56.0`); the checkout's workspace source is authoritative here, and an externally resolved published build could differ (**Inference**).

## Session model

### Identity and ownership

A "session" is a `Session` dataclass keyed by a `uuid4` string, scoped to a canonicalized work directory, and materialized as a directory `<share_dir>/sessions/<workdir-basename>/<session-id>/` holding `context.jsonl`, `wire.jsonl`, session state, and `subagents/` (`src/kimi_cli/session.py:22`, `:49`; `src/kimi_cli/metadata.py:34`). The ACP session ID is exactly this `Session.id`: `new_session` returns `session.id` (`server.py:196`) and `ACPServer.sessions` maps it to `(ACPSession, _ModelIDConv)`. The mapping is one-to-one: one ID → one durable directory → one `KimiCLI` runtime → one `ACPSession`. The `ACPSession` owns no durable state, only per-turn state (turn UUID, cancel event, live tool-call map, streaming-args lexer state, `src/kimi_cli/acp/session.py:79`, `:113`). The model conversation is owned by `Context` (restored from `context.jsonl`), and the display transcript is owned by `WireFile` — two separate records of the same turns.

### Lifecycle

| Operation | What happens | Durable effect | Failure/cleanup behavior | Evidence |
| --- | --- | --- | --- | --- |
| Create | Auth check; `Session.create` (uuid4, mkdir, truncate/create `context.jsonl`, save metadata); `KimiCLI.create` (config, LLM, Runtime, MCP, agent, restore empty Context, write system prompt); register `ACPSession`; replace Shell with ACP Terminal if client supports it; fire-and-forget `AvailableCommandsUpdate` | Session dir with empty `context.jsonl`; metadata saved | Exceptions propagate to the SDK and become `internal_error`; auth failure raises `AUTH_REQUIRED` | `src/kimi_cli/acp/server.py:150`; `src/kimi_cli/session.py:129`; `src/kimi_cli/app.py:305` |
| Load/resume | `_setup_session`: `Session.find` (requires session dir + `context.jsonl`, migrates legacy layout), `KimiCLI.create(resumed=True)` restores `Context`; `load_session` additionally calls `replay_history(wire.jsonl)`; `resume_session` returns modes/models only; already-loaded sessions short-circuit (`load` no-ops with a warning, `resume` reuses the registry entry) | None beyond reads | Unknown ID → `invalid_params`; replay errors are logged and swallowed, load still succeeds | `src/kimi_cli/acp/server.py:214`, `:256`, `:271`; `src/kimi_cli/session.py:183` |
| Prompt | See event-flow trace below; returns `PromptResponse(stop_reason)` | User message + per-step messages to `context.jsonl`; merged wire records; usage records; title saved after first turn | Mapped exceptions: `LLMNotSet`→`auth_required`, provider errors→`internal_error`, `MaxStepsReached`→`max_turn_requests`, `RunCancelled`→`cancelled` | `src/kimi_cli/acp/session.py:155`, `:218`; `src/kimi_cli/soul/kimisoul.py:765` |
| Cancel | Sets `_TurnState.cancel_event`; the mirror task in `KimiCLI.run` propagates it; `run_soul` cancels the soul task, converting `CancelledError` into `RunCancelled`; pending approvals from this turn are cancelled by source | No special record; partial wire records remain | Cancel with no active prompt logs a warning and no-ops | `src/kimi_cli/acp/session.py:307`; `src/kimi_cli/app.py:649`; `src/kimi_cli/soul/__init__.py:221`; `src/kimi_cli/soul/kimisoul.py:832` |
| Close/delete | Not found in the ACP surface — no `session/close` or delete handler (`fork_session` raises `NotImplementedError`). `Session.delete` (rmtree) exists for web/CLI callers only | n/a via ACP | n/a | `src/kimi_cli/acp/server.py:298`; `src/kimi_cli/session.py:99` |

### Durable representation

Two append-only JSONL files per session plus a small state file:

- `context.jsonl` — the authoritative model conversation. First record `_system_prompt`, then interleaved `_checkpoint` records (written before each step and turn, `src/kimi_cli/soul/context.py:123`), `_usage` token-count records, and `Message` records (user, assistant with tool calls, tool results). Every message is appended synchronously-in-sequence via `append_message` (`context.py:232`). `revert_to`/`clear` rotate the file and rewrite a prefix (`context.py:135`).
- `wire.jsonl` — the display transcript. First record is a protocol-version header; each subsequent record is `{timestamp, envelope{type, payload}}` (`src/kimi_cli/wire/file.py:18`). A per-run `_WireRecorder` task drains the *merged* wire queue (consecutive `TextPart`/`ThinkPart`/`ToolCallPart` deltas coalesced in place, `packages/kosong/src/kosong/message.py:84`) and appends each message (`src/kimi_cli/wire/__init__.py:130`). This file is authoritative for UI replay only; it is never fed to the model.
- Session state (approval settings, plan mode, custom title, archive flags) is a separate persisted file with read-modify-write refresh to coexist with the web API (`src/kimi_cli/session.py:84`).

Not persisted: raw streaming deltas (only merged messages), approval request/response records (they are wire requests, excluded from replay), and in-flight partial assistant output in `context.jsonl` (assistant messages are appended only after the step's tool phase completes). Stored history is authoritative, not a cache — there is no other owner.

## Concurrency and isolation

| Scenario | Result | Mechanism and scope | Evidence |
| --- | --- | --- | --- |
| Two prompts in different sessions | `concurrent` | SDK spawns one task per request; per-session `KimiCLI`/`Context`/`Wire` objects are independent; only the event loop serializes execution steps | `acp/task/dispatcher.py:88`; `src/kimi_cli/acp/server.py:34` |
| Two prompts in the same session | `concurrent` | No guard at any layer: `ACPSession.prompt` unconditionally replaces `_TurnState` (`session.py:157`), both runs share one `KimiSoul`, one `Context`, one wire file → interleaved appends and lost cancel targeting | `src/kimi_cli/acp/session.py:157`, `:307` |
| Load/resume during an active prompt | No-op if registered (`load_session` warns and returns; `resume_session` reuses the entry); loading an unregistered-but-active session is only reachable from a second connection, and `ACPServer` keeps a single `conn` | Registry membership check | `src/kimi_cli/acp/server.py:261`, `:276`, `:33` |
| Delete/close during an active prompt | `n/a` via ACP (no delete handler); `Session.delete` from web/CLI would rmtree under a live run with no coordination | None | `src/kimi_cli/session.py:99` |
| Cancellation isolation | Per session, per turn | Cancel looks up the session and sets its current `_TurnState.cancel_event`; with two racing prompts only the latest turn state is cancellable | `src/kimi_cli/acp/session.py:120`, `:307` |

Shared-registries coupling: the sessions dict is unsynchronized but only mutated by request tasks on one loop; `Session.create`/`find`/`list` perform unlocked read-modify-write of the global `kimi.json` metadata, so two concurrent `new_session` calls can race (`src/kimi_cli/session.py:139`); the wire queue and approval hub are per-run and per-session respectively. The precise same-session critical section is nonexistent — nothing is released because nothing is held.

## Event and data flow

### New prompt: ACP client to live response

1. Client sends `session/prompt`; `ACPServer.prompt` validates the session ID and delegates (`src/kimi_cli/acp/server.py:400`).
2. `ACPSession.prompt` converts ACP content blocks to `ContentPart`s — text kept, image → data URL, embedded resources wrapped as text, unknown types logged and dropped (`src/kimi_cli/acp/convert.py:17`).
3. A fresh `_TurnState` (turn UUID, `asyncio.Event`) is installed and the ACP `Kaos` (client-backed filesystem) becomes the context-local KAOS backend (`src/kimi_cli/acp/session.py:157`; `src/kimi_cli/acp/kaos.py`).
4. `KimiCLI.run` spawns a cancel-mirror task and a `run_soul` task wired to a `Wire` with `wire.jsonl` as file backend; the UI side subscribes to the raw (unmerged) queue and to the root wire hub for approval forwarding (`src/kimi_cli/app.py:649`).
5. `KimiSoul.run` fires `TurnBegin`, then `_turn` checkpoints and appends the user `Message` to `context.jsonl` **before** the first LLM call (`src/kimi_cli/soul/kimisoul.py:713`, `:850`).
6. `_agent_loop` iterates steps: auto-compaction when over the trigger ratio, per-step checkpoint, then `kosong.step` streams `TextPart`/`ThinkPart`/`ToolCallPart` and `ToolCall`s; each tool call is executed in a concurrent asyncio task and may bridge to ACP `request_permission` via the approval runtime (`src/kimi_cli/soul/kimisoul.py:1000`, `:1209`; `src/kimi_cli/soul/toolset.py:595`).
7. Each `wire_send` lands in the raw queue (consumed by the ACP translator) and in the merged queue (consumed by the recorder, appended to `wire.jsonl`).
8. `ACPSession.prompt` matches each message and awaits a direct `session_update`: text → `AgentMessageChunk`, thinking → `AgentThoughtChunk`, tool call → `ToolCallStart`/`ToolCallProgress` with turn-prefixed IDs, tool result → `ToolCallProgress` (`completed`/`failed`) plus plan updates, approval requests → awaited `conn.request_permission` (`src/kimi_cli/acp/session.py:162`, `:374`, `:469`).
9. After tool results, `_grow_context` — wrapped in `asyncio.shield` — appends the assistant message and tool messages to `context.jsonl` (`src/kimi_cli/soul/kimisoul.py:1295`, `:1389`).
10. On turn end, the handler returns `PromptResponse(stop_reason="end_turn" | "max_turn_requests" | "cancelled")`; the SDK serializes it as the JSON-RPC response after the last awaited update (`acp/connection.py:205`).

### Durable history to ACP client

1. `session/load` → `_setup_session` reconstructs runtime state from `context.jsonl` (`Context.restore`, `src/kimi_cli/soul/context.py:30`), then `replay_history(session.wire_file)` iterates `wire.jsonl` records in file order (`src/kimi_cli/acp/session.py:250`).
2. Each persisted envelope is converted back to a `WireMessage` and dispatched: `TurnBegin`/`SteerInput` start a synthetic replay turn and emit `UserMessageChunk` with the stored input; `TextPart`/`ThinkPart` → message/thought chunks; `ToolCall` → `ToolCallStart`; `ToolCallPart` → `ToolCallProgress`; `ToolResult` → completion update (reusing the live `_send_tool_result`, so `TodoDisplayBlock`s replay as plan updates); `Notification` → text chunk. Turn boundaries reset the synthetic turn state (`session.py:258`).
3. Skipped on replay: `StatusUpdate`, compaction and MCP-loading markers, `PlanDisplay`, and all request records (`ApprovalRequest`, `ToolCallRequest`, `QuestionRequest`) (`session.py:265`, `:290`, `:294`).
4. Replay errors are logged per-record/per-file and swallowed; load still succeeds (`session.py:298`). Content that cannot be reconstructed includes terminal-tool output — the live path streams it via `TerminalToolCallContent` and suppresses the stored result with `HideOutputDisplayBlock` (`src/kimi_cli/acp/tools.py:69`, `:100`; `src/kimi_cli/acp/convert.py:104`), so replays show the call but not its output (**Inference** from the display conversion). Streaming granularity is also lost: replay sends merged text, not original deltas.

### Live events to durable history

| Source event/input | Runtime representation | ACP output | Durable representation | Commit/ordering point | Evidence |
| --- | --- | --- | --- | --- | --- |
| User prompt | `ContentPart` list → `Message(role=user)` | none (client rendered it); replayed later as `UserMessageChunk` | `TurnBegin` wire record; message line in `context.jsonl` | Context committed before first LLM call; wire record written asynchronously by the recorder | `src/kimi_cli/soul/kimisoul.py:713`, `:850` |
| Assistant text | Streamed `TextPart` deltas via `on_message_part` | `AgentMessageChunk` per delta | Merged `TextPart` wire record; full assistant `Message` in `context.jsonl` | Wire: as merged, near-real-time; context: after the step's tool phase | `src/kimi_cli/soul/kimisoul.py:1214`; `src/kimi_cli/acp/session.py:353` |
| Reasoning/thought | Streamed `ThinkPart` | `AgentThoughtChunk` | Merged wire record; part of the assistant `Message` in context | Same as text | `src/kimi_cli/acp/session.py:340` |
| Tool call | `ToolCall` + streaming `ToolCallPart` arg deltas | `ToolCallStart` then `ToolCallProgress` (ID `"<turn-uuid>/<llm-id>"`) | `ToolCall`/merged `ToolCallPart` wire records; assistant message with tool calls in context | Wire: immediate; context: end of step | `src/kimi_cli/acp/session.py:374`, `:402`, `:89` |
| Tool result | `ToolResult` (concurrent tool tasks gathered) | `ToolCallProgress` completed/failed (+`AgentPlanUpdate` for todo blocks; terminal output streamed separately) | `ToolResult` wire record; tool `Message`s in context (shielded append) | Wire: immediate; context: after all results of the step | `src/kimi_cli/acp/session.py:435`; `src/kimi_cli/soul/kimisoul.py:1295` |
| Completion/failure | `TurnEnd` / `StepInterrupted` / exception | `PromptResponse(stop_reason)` response; `StepInterrupted` breaks the handler loop | `TurnEnd` wire record; `_usage` token records during steps; title saved after first turn | Response sent after the last awaited update; wire/context writes async | `src/kimi_cli/soul/kimisoul.py:762`, `:1057`; `src/kimi_cli/acp/session.py:248` |

Persistence ordering versus notification is not coordinated: the client update is awaited inline by the translator while the recorder task persists the merged stream independently, and the context append for a step happens only after its tools finish. There is no commit that atomically covers both files.

### Subsequent-prompt reconstruction

On `load_session`/`resume_session`, `Context.restore` replays `context.jsonl` line by line: `_system_prompt` sets the prompt (replacing the freshly loaded agent's if present, `src/kimi_cli/app.py:308`), `_usage` records reset the token accounting and drop the trailing unaccounted messages from the estimate, `_checkpoint` records restore checkpoint IDs, and every valid `Message` re-enters `history` (`src/kimi_cli/soul/context.py:276`). At each new step the history is normalized (adjacent user messages merged) before the LLM call (`src/kimi_cli/soul/kimisoul.py:1182`). Compaction rewrites history via file rotation and a summarizing LLM call. The wire transcript never feeds the model; lossy conversion relative to a live run is limited to compaction and the display-only blocks.

### Ordering, cancellation, failure, and backpressure

Ordering within a session is guaranteed by construction: one raw queue per run consumed by one translator task, and `wire_send` publishes in program order. Cross-session ordering is unspecified and unneeded. Cancellation mid-LLM-stream loses the partial assistant output from the model context (never appended) while keeping it in the wire transcript (merged and flushed at `wire.shutdown()`, `src/kimi_cli/soul/__init__.py:244`); cancellation mid-tool cancels the in-flight tool futures (`packages/kosong/src/kosong/__init__.py:218`); the shielded `_grow_context` prevents cancel-interrupted partial context writes. Provider errors retry with exponential jitter up to `max_retries_per_step`, emitting `StepRetry`, then surface as `StepInterrupted` + exception (`src/kimi_cli/soul/kimisoul.py:1224`, `:1057`). All queues are unbounded (`src/kimi_cli/utils/aioqueue.py:9`; `publish_nowait` in `src/kimi_cli/utils/broadcast.py:28`), so a slow or stalled client does not stall the agent loop — memory grows instead; the one client-coupled stall is an unanswered `request_permission`, which blocks the turn by design.

## Capability matrix

| Capability | Support | Notes | Evidence |
| --- | --- | --- | --- |
| Multiple sessions in one server process | yes | Registry dict keyed by session ID | `src/kimi_cli/acp/server.py:34` |
| Concurrent work across sessions | yes | Per-request SDK tasks; independent per-session objects | `acp/task/dispatcher.py:88` |
| Same-session prompt exclusion | no | No lock, rejection, or cancel-previous; second prompt overwrites turn state | `src/kimi_cli/acp/session.py:157` |
| Durable sessions | yes | `context.jsonl` + `wire.jsonl` + state file per session dir | `src/kimi_cli/session.py:49` |
| Session list | yes | `list_sessions` per cwd; empty sessions skipped; `next_cursor` always `None` (no pagination) | `src/kimi_cli/acp/server.py:303`; `src/kimi_cli/session.py:267` |
| Session load/resume | yes | `load` (with replay) and `resume` (without) via `Session.find`; fork unsupported | `src/kimi_cli/acp/server.py:256`, `:271`, `:301` |
| History replay to ACP client | yes | Wire transcript replayed as user/agent chunks and tool-call updates; some event kinds skipped | `src/kimi_cli/acp/session.py:250` |
| Prior history reused by model | yes | `Context.restore` from `context.jsonl` at session setup | `src/kimi_cli/app.py:305` |
| Prompt cancellation | yes | Cancel event → task cancellation → `RunCancelled` → `stop_reason="cancelled"` | `src/kimi_cli/soul/__init__.py:221` |
| Tool-call progress updates | yes | Start/args-stream/result mapping, turn-unique IDs; terminal output streaming | `src/kimi_cli/acp/session.py:374`; `src/kimi_cli/acp/tools.py:100` |
| Partial-output persistence | partial | Wire transcript keeps merged partial text; model context only records completed step messages | `src/kimi_cli/wire/__init__.py:87`; `src/kimi_cli/soul/kimisoul.py:1295` |
| Recovery after process restart | yes | E2E test spawns a second server process and loads the session with replay | `tests/acp/test_protocol_v1.py:153` |

## Design assessment

### Strengths

- One `Wire` stream serves three consumers with one serializer: live UI deltas (raw), durable transcript (merged), and replay translation reuses the same sender methods as the live path, keeping live and replay formatting consistent by construction (`src/kimi_cli/wire/__init__.py:18`; `src/kimi_cli/acp/session.py:250`).
- The durable split between `context.jsonl` (model truth, with checkpoints and usage records) and `wire.jsonl` (presentation truth) means display-only churn never pollutes model context and vice versa (`src/kimi_cli/soul/context.py:91`).
- Turn-ID-prefixed ACP tool-call IDs sidestep client-side ID collisions when the LLM reuses IDs after a rejected call — a cheap, local fix (`src/kimi_cli/acp/session.py:89`).
- Cancellation is layered cleanly: a client-facing event, a mirror task, a soul-task cancel, and per-source approval cleanup, without leaking asyncio cancellation into the protocol layer (`src/kimi_cli/app.py:649`; `src/kimi_cli/soul/kimisoul.py:832`).

### Tradeoffs and limitations

- No same-session exclusion: a duplicated `session/prompt` silently replaces the turn state and interleaves appends to shared files; the outcome is undefined and untested (`src/kimi_cli/acp/session.py:157`).
- Failure masking: a fatal step error typically reaches the client as `stop_reason="end_turn"` because the translator breaks on `StepInterrupted` before the generator's exception propagates; the error surfaces only as a log (`src/kimi_cli/acp/session.py:172`, `:248`) (**Inference** from message ordering).
- Global side effects from session-scoped calls: `set_session_model` rewrites the shared config file, affecting other sessions and CLI runs (`src/kimi_cli/acp/server.py:366`); `new_session`/`find` race on the unlocked global metadata file (`src/kimi_cli/session.py:139`).
- Replay is lossy for terminal tool calls (output hidden by design is not recoverable) and for approval interactions, and the fire-and-forget `AvailableCommandsUpdate` task has no error handling (`src/kimi_cli/acp/server.py:187`).
- Unbounded in-memory queues between agent and translator mean a stalled client converts directly into unbounded memory growth during long turns (`src/kimi_cli/utils/aioqueue.py:9`).

### Ideas relevant to Ox

- The context/transcript split is worth considering in Ox: separating "what the model must see next run" from "what the client may want replayed" removes a whole class of replay-filtering problems. Tradeoff: two files that must stay mutually consistent at load, since kimi-cli's replay silently tolerates divergence between them.
- Reusing one translator for live streaming and replay (kimi-cli's `ACPSession`) kept the two paths visibly in sync. Tradeoff: replay inherits live-path assumptions (e.g., "tool results carry display blocks") that silently drop content when a live-only channel like terminal streaming exists outside the durable stream.
- Avoid kimi-cli's gap: define the same-session re-prompt policy explicitly (reject or cancel-previous) while the session model is still small; retrofitting exclusion around shared append-only files is harder than adding a one-slot guard up front.

## Unknowns and conflicts

- **Docs conflict with code:** the repo's `src/kimi_cli/acp/AGENTS.md` claims `embedded_context=False`, `auth_methods=[]`, and "no history replay yet (TODO)". The pinned code contradicts all three (`src/kimi_cli/acp/server.py:102`, `:77`, `:269`); the report follows the code.
- **StepInterrupted race:** whether a step failure reaches the client as `end_turn` (break path) or `internal_error` (exception path) depends on task scheduling; not exercised by tests. Searched `src/kimi_cli/acp/session.py`, `src/kimi_cli/soul/kimisoul.py`, and `tests/acp/` — no test covers the failure path.
- **Same-session concurrent prompts:** no implementation guard and no test; behavior described above is inferred from shared-state analysis, not observed. Searched for locks/semaphores in `src/kimi_cli/acp/` and `src/kimi_cli/soul/` — none found.
- **Not found:** ACP session close/delete, session pagination (`next_cursor` never set), fork (`NotImplementedError`), and any use of `HookRequest`/`QuestionRequest` in the ACP surface (they are logged-or-resolved-empty, `src/kimi_cli/acp/session.py:211`). Searches covered `src/kimi_cli/acp/`, the SDK's registered routes, and `tests/acp/`.
- **Approval runtime internals** (`src/kimi_cli/approval_runtime/`) were skimmed only to the boundary where ACP resolves requests; duplicate-suppression and allow-always persistence inside that module were not traced.

## Evidence index

| Area | Primary locations | Why they matter |
| --- | --- | --- |
| ACP entry point | `src/kimi_cli/acp/__init__.py:1` (`acp_main`); `src/kimi_cli/acp/server.py:42` (`ACPServer.initialize`); `acp/task/dispatcher.py:88` | Server startup, capabilities, per-request task model |
| Session management | `src/kimi_cli/acp/server.py:150` (`new_session`), `:214` (`_setup_session`), `:256` (`load_session`), `:271` (`resume_session`); `src/kimi_cli/session.py:129` (`Session.create`), `:183` (`Session.find`) | ID mapping, registration, durable layout |
| Concurrency | `acp/task/dispatcher.py:56`; `src/kimi_cli/acp/session.py:155` (`prompt`), `:307` (`cancel`); `src/kimi_cli/app.py:649`; `src/kimi_cli/soul/__init__.py:179` (`run_soul`) | Task spawning, absence of session guards, cancel chain |
| Persistence | `src/kimi_cli/wire/file.py:124` (`append_record`); `src/kimi_cli/wire/__init__.py:130` (`_WireRecorder`); `src/kimi_cli/soul/context.py:232` (`append_message`), `:30` (`restore`) | The two durable streams and their commit points |
| Event translation | `src/kimi_cli/acp/session.py:162` (`prompt` match), `:374` (`_send_tool_call`), `:469` (`_handle_approval_request`); `src/kimi_cli/acp/convert.py:17` (`acp_blocks_to_content_parts`) | Wire ⇄ ACP mapping, permission bridging, input validation |
| Replay/reconstruction | `src/kimi_cli/acp/session.py:250` (`replay_history`); `src/kimi_cli/soul/context.py:276` (`_apply_context_record`); `src/kimi_cli/app.py:305` | Load-time reconstruction for client and model |
| Cancellation/errors | `src/kimi_cli/soul/kimisoul.py:1046` (fatal step), `:1224` (retry); `src/kimi_cli/soul/__init__.py:221` (cancel → `RunCancelled`); `src/kimi_cli/acp/session.py:218` (error mapping) | Failure surfaces and stop reasons |
| LLM abstraction | `packages/kosong/src/kosong/chat_provider/__init__.py:15` (`ChatProvider`), `:80` (`StreamedMessage`); `packages/kosong/src/kosong/__init__.py:104` (`kosong.step`); `packages/kosong/src/kosong/_generate.py:17` (`generate`); `packages/kosong/pyproject.toml:8` | The bespoke provider protocol and stream-merge loop |
| Model integration path | `src/kimi_cli/llm.py:326` (`create_llm`), `:32` (`ProviderType`), `:53` (`LLM`); `src/kimi_cli/app.py:215`, `:243` (model/provider selection); `src/kimi_cli/soul/kimisoul.py:1209` (`kosong.step` call), `:1130`, `:1182` | Config → provider construction → per-step request setup |
| Provider backends | `packages/kosong/src/kosong/chat_provider/kimi.py:128` (`AsyncOpenAI`), `:146` (`generate`), `:174` (SDK create), `:326`/`:365` (message/tool conversion); `packages/kosong/src/kosong/contrib/chat_provider/{anthropic,google_genai,openai_legacy,openai_responses}.py` | SDK-backed provider implementations and normalization |
| Tool schemas | `packages/kosong/src/kosong/tooling/__init__.py:18` (`Tool`), `:232`/`:279` (`CallableTool2` → JSON Schema); `src/kimi_cli/soul/toolset.py:281` (`.tools`), `:907` (`MCPTool`); `packages/kosong/src/kosong/tooling/mcp.py:1` | Tool definition and provider-format conversion |

## Research notes

- **Revision inspected:** `86f136422a0aae6b217ea49e7ea1d2e8a1defcd2`
- **Primary evidence:** `src/kimi_cli/acp/` (server, session, convert, tools, kaos, version), `src/kimi_cli/app.py`, `src/kimi_cli/session.py`, `src/kimi_cli/wire/` (`__init__`, `file`, `types`), `src/kimi_cli/soul/` (`__init__`, `kimisoul.py`, `context.py`, `toolset.py`), `packages/kosong/src/kosong/__init__.py`, the pinned SDK at `.venv/lib/python3.14/site-packages/acp/` (`connection.py`, `task/dispatcher.py`, `router.py`), and `tests/acp/`.
- **Relevant docs:** repo `AGENTS.md` and `src/kimi_cli/acp/AGENTS.md` (used for orientation only; several claims outdated and flagged above).
- **Commands/tests run:** `uv run pytest tests/acp -q` — 22 passed (initialize, new_session, prompt, list, resume, load-with-replay after process restart, cancel), corroborating the traced protocol behavior.
- **Report confidence:** `high` — all central paths were read end to end in the pinned revision, the SDK dispatch semantics were verified in source, and the protocol behaviors were exercised by the passing test suite; residual uncertainty is limited to untested failure races, each labeled above.
