# Ur

## Vision

Ur is an AI coding agent — a terminal tool (and eventually GUI/IDE-embeddable library) that pairs an LLM with a rich, Lua-scriptable extension system. The core is deliberately minimal: agent loop, extension machinery, session storage, configuration, and secrets. Everything else — tools, workflows, UI behaviors — lives in extensions.

Ur is for developers who want an AI coding assistant they can deeply customize without forking the tool itself.

## Goals

- Provide a fast, lightweight AI coding agent that starts instantly and stays out of the way.
- Make the extension system the primary mechanism for adding capabilities. Tools, middleware, prompt augmentation — all via Lua extensions.
- Keep the core small enough that one person can understand all of it.
- Ship as a single AoT-compiled binary with no runtime dependencies.
- Ur will support macOS and Linux.

## Non-Goals

- Ur is not an IDE. It does not provide editing, syntax highlighting, or project management UI.
- Ur is not a framework for building arbitrary AI applications. It is specifically a coding agent.
- Ur does not aim to support non-LLM AI backends (e.g. local models via ONNX). It targets cloud LLM APIs via Microsoft.Extensions.AI.
- Ur will not implement its own model routing or load balancing. That belongs to the provider or to infrastructure.
- Ur will not support Microsoft Windows.

## Constraints

- **.NET 10, AoT compatible.** No reflection-heavy patterns, no dynamic assembly loading. This rules out MEF, most plugin frameworks, and many serialization libraries.
- **Lua-CSharp for extensions.** The extension runtime is Lua, not .NET plugins. This is a hard constraint — extensions must be expressible in Lua (with C# APIs exposed to them).
- **Single developer.** Simplicity is not just a preference, it is a survival constraint. Every abstraction must earn its keep.
- **Microsoft.Extensions.AI for LLM interaction.** This provides the chat completion, tool calling, and embedding abstractions.

## System Context

```
                    ┌─────────────┐
                    │  LLM APIs   │
                    │ (OpenAI,    │
                    │  Anthropic, │
                    │  etc.)      │
                    └──────┬──────┘
                           │ Microsoft.Extensions.AI
                           │
┌──────────┐       ┌───────┴────────┐       ┌────────────┐
│  User    │◄─────►│      Ur        │◄─────►│ Filesystem │
│ (CLI,    │  UI   │   (library)    │       │ (workspace)│
│  GUI,    │ layer │                │       └────────────┘
│  IDE)    │       │  ┌──────────┐  │
└──────────┘       │  │Extensions│  │       ┌────────────┐
                   │  │  (Lua)   │  │◄─────►│  Network   │
                   │  └──────────┘  │       │ (via ext)  │
                   └────────────────┘       └────────────┘
```

Users interact with Ur through a UI layer (CLI REPL initially, GUI/IDE later). Ur is a class library; the CLI is a separate thin project that consumes it. Extensions run in sandboxed Lua and interact with the outside world through Ur's APIs, subject to the permission system.

## Solution Strategy

- **Library-first architecture.** Ur is a class library. The CLI/REPL is a separate project that depends on it. This ensures UI is fully decoupled and other frontends (GUI, IDE extensions) can embed Ur without pulling in terminal dependencies.
- **Lua extension runtime via Lua-CSharp.** Extensions are Lua scripts loaded from well-known directories. The Lua sandbox (via `LuaPlatform`) controls what system resources extensions can access. Extensions can provide tools, hook into the agent loop as middleware, and modify behavior.
- **Middleware-based agent loop.** The agent loop is a pipeline that extensions can intercept, similar to ASP.NET Core's middleware pattern. This gives extensions deep control without requiring changes to the core.
- **Unified settings with schema validation.** One settings file (VS Code-style flat dot-namespaced keys) for core, extension, and model settings. Validated against JSON schemas declared by each component.
- **JSONL session storage.** Sessions are append-only JSONL files, one per session. Simple, human-readable, and trivially parseable.
- **AoT compilation.** The binary is published as a self-contained, ahead-of-time compiled executable. This constrains the design (no runtime reflection, no dynamic loading) but delivers fast startup and zero dependencies.
- **Library owns chat client and keyring creation.** The library carries the provider SDK (OpenAI), creates `IChatClient` instances internally, and handles platform detection and keyring creation. Frontends provide nothing — `UrHost.Start(cwd)` is the entire API. An optional `IKeyring` parameter exists for testing only. `CreateChatClient` is internal; only the agent loop consumes it. See [ADR-0011](decisions/adr-0011-library-owns-chat-client-and-keyring.md).
- **Public runtime surface via `UrHost` and `UrSession`.** `UrHost` is the workspace-scoped root for listing, creating, and opening sessions. All conversation activity happens through `UrSession`, which owns read-only history, turn execution, and auto-persistence. Startup is non-interactive: the host can exist in a "not ready to chat yet" state until configuration is completed.
- **OpenRouter-only with API-based model discovery.** Model metadata (context length, pricing, capabilities) is fetched from the OpenRouter `GET /api/v1/models` endpoint and cached to `~/.ur/cache/models.json`. No static model catalog to maintain.
- **Linear startup via `UrHost.Start`.** Workspace → load model cache (or fetch) → schema registration → settings load/validate → return `UrHost`. Missing API key or model selection does not fail startup; those surface as chat-readiness blockers on the configuration/runtime API. Extensions are not yet wired into startup. When they are, a two-phase approach is likely: load extension metadata/schemas first (no config access), then validate settings, then initialize extensions with resolved settings.

## Building Blocks

| Block             | Responsibility                                                                                                | Document                                     |
| ----------------- | ------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| Host & Session API | Public runtime surface for UIs. `UrHost` owns workspace-scoped operations; `UrSession` owns conversation turns, history, and chat readiness integration. | [host-session-api.md](host-session-api.md)   |
| Agent Loop        | Drives the conversation cycle: user input → middleware → LLM → tool calls → repeat.                           | [agent-loop.md](agent-loop.md)               |
| Extension System  | Discovers, loads, and manages Lua extensions. Exposes C# APIs to Lua. Manages lifecycle (enable/disable).     | [extension-system.md](extension-system.md)   |
| Permission System | Gates sensitive operations (writes, network, out-of-workspace reads). Prompts user with scoped approvals.     | [permission-system.md](permission-system.md) |
| Provider Registry | Manages provider config and model discovery. Currently OpenRouter-only; fetches model catalog from API, caches to disk. | [provider-registry.md](provider-registry.md) |
| Configuration     | Unified settings file, schema validation, two-level merging, keyring-based secret storage.                    | [configuration.md](configuration.md)         |
| Session Storage   | Persists conversation history as JSONL. Manages session lifecycle (create, resume, list) scoped to workspace. | [session-storage.md](session-storage.md)     |
| Tool Registry     | Maintains available tools. Core provides the registry; extensions register tools into it.                     | [tool-registry.md](tool-registry.md)         |
| Workspace         | Represents the launch directory. Scopes sessions, config, and workspace extensions.                           | [workspace.md](workspace.md)                 |

## Data Architecture

### Core Entities

- **Workspace** — A directory on disk. Has a `.ur/` subdirectory for workspace-scoped state (sessions, config, extensions). Identified by its absolute path.
- **Session** — A conversation within a workspace. Stored as a JSONL file where each line is an envelope wrapping a serialized `ChatMessage` with provenance (provider, model, settings). Models can change mid-session. Identified by a timestamp-based ID.
- **UrHost** — Workspace-scoped public root returned by `UrHost.Start`. Exposes session lifecycle and configuration/readiness.
- **UrSession** — Conversation-scoped public object. Exposes read-only history and is the only entry point for running agent turns.
- **Extension** — A Lua script (or directory with a main script) loaded from one of the three extension directories. Has metadata (name, description, source tier) and a lifecycle (loaded, enabled, disabled).
- **Permission Grant** — User approval for a sensitive operation, scoped to once/session/workspace/always. Some scopes are restricted for dangerous operations.
- **Provider** — Currently OpenRouter only. Has an API key in the keyring and a models discovery endpoint.
- **Model** — A specific LLM available on OpenRouter. Metadata (context length, cost, supported parameters) fetched from the OpenRouter API and cached to disk.
- **Settings** — Unified JSON file with flat dot-namespaced keys. Core, extension, and model settings in one file, validated against declared schemas.

### Key Invariants

- A session belongs to exactly one workspace.
- Extension loading order: system → user → workspace. Within a tier, order is unspecified.
- Workspace extensions are disabled by default; system and user extensions are enabled by default.
- Permission grants for out-of-workspace writes and network access: "once" scope only.
- Every setting has a JSON schema. Unknown keys warn; type mismatches error.
- The model catalog (API-fetched) is separate from user settings. The catalog is what's available; settings record what the user chose.
- API keys live in the system keyring, never in settings files.

## Quality Attributes

| Attribute     | Target                                                                              | Rationale                                                                                                            |
| ------------- | ----------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| Startup speed | < 200ms to first prompt                                                             | AoT compilation. An agent that takes seconds to start will not be used habitually.                                   |
| Extensibility | Extensions can provide tools, middleware, and hook into any stage of the agent loop | The extension system is the primary differentiator. If something can't be done via extension, the API is incomplete. |
| Security      | No extension can perform a sensitive operation without explicit user approval       | Users must trust that installing a workspace extension won't exfiltrate their code.                                  |
| Simplicity    | Core fits in one person's head                                                      | Single-developer constraint. Every abstraction must earn its keep.                                                   |

## Key Scenarios

### Scenario: User starts a new session

1. User runs `ur` in a project directory.
2. CLI creates/identifies the workspace (`$PWD`).
3. Extensions are discovered and loaded (system → user → workspace).
4. A new session is created (JSONL file in `$WORKSPACE/.ur/sessions/`).
5. User types a message → agent loop runs a turn → response is displayed.

### Scenario: First run with no API key and no model

1. User runs `ur` in a project directory.
2. `UrHost.Start` succeeds and returns a host even though chat is not yet ready.
3. The UI inspects the configuration/readiness surface and sees blockers: missing API key and missing model selection.
4. The UI prompts for an API key, stores it in the system keyring, then shows a model picker backed by the cached/fetched model catalog.
5. Once readiness becomes "ready," the user can create/open a session and run turns normally.

### Scenario: Extension provides a custom tool

1. A Lua extension registers a "git_status" tool at load time.
2. When the LLM requests that tool, the agent loop dispatches to the Lua handler (subject to permissions).

### Scenario: Extension hooks into agent loop as middleware

1. A Lua extension registers middleware that injects a system prompt fragment before each LLM call.
2. The middleware pipeline modifies the request, then passes it to the next middleware (or the LLM).

### Scenario: Extension attempts unauthorized file write

1. An extension attempts to write outside the workspace.
2. The permission system prompts: "Extension 'X' wants to write to /etc/foo. Allow? [Yes (once)] [No]".
3. If denied, the error is returned to the LLM as a tool result.

## Decisions

| Decision                                                    | Rationale                                                                                                       | ADR                                                              |
| ----------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| Lua for extensions (not .NET plugins)                       | AoT rules out dynamic assembly loading. Lua is lightweight, sandboxable, and Lua-CSharp has good .NET interop.  | [ADR-0001](decisions/adr-0001-lua-for-extensions.md)             |
| Library-first (Ur is a class lib, CLI is separate)          | Enables future GUI/IDE frontends without coupling to terminal I/O.                                              | [ADR-0002](decisions/adr-0002-library-first.md)                  |
| JSONL for session storage                                   | Append-only, human-readable, no database dependency. Fits the simplicity constraint.                            | [ADR-0003](decisions/adr-0003-jsonl-session-storage.md)          |
| Three-tier extension loading with trust defaults            | System/user enabled by default, workspace disabled. Prevents supply-chain-style attacks via repo contributions. | [ADR-0004](decisions/adr-0004-three-tier-extension-loading.md)   |
| Scoped permission grants with restrictions on dangerous ops | "Always allow network" is a footgun. Restricting dangerous ops to "once" forces deliberate approval.            | [ADR-0005](decisions/adr-0005-scoped-permission-restrictions.md) |
| Unified settings file (VS Code-style flat dot keys)         | One file for core, extension, and model settings. Simpler than multiple config files. Familiar to developers.   | [ADR-0006](decisions/adr-0006-unified-settings-file.md)          |
| Separate model catalog from user settings                   | Catalog is API-fetched truth (what's available). Settings are user choices (what's selected).                   | [ADR-0009](decisions/adr-0009-separate-provider-registry.md)     |
| API keys in system keyring, never in config files           | Secrets must not end up in version control or be readable by extensions scanning config.                        | [ADR-0008](decisions/adr-0008-libraryimport-keyring.md)          |
| Mid-session model switching with per-message envelope       | Empirically verified: messages are interchangeable across OpenAI-compatible providers. Runtime stripping for edge cases. | [ADR-0010](decisions/adr-0010-mid-session-model-switch.md)       |
| OpenRouter-only for v1                                      | One provider, one API key, one auth flow. OpenRouter covers the broadest model set.                             | —                                                                |
| API-based model discovery (not static catalog)              | 345+ models that change frequently. OpenRouter API is the source of truth, cached to disk.                     | —                                                                |
| Library owns chat client and keyring creation               | Provider SDKs, keyring creation, and platform detection are library concerns. Frontends provide nothing.         | [ADR-0011](decisions/adr-0011-library-owns-chat-client-and-keyring.md) |
| Microsoft.Extensions.AI for LLM abstraction                 | Official .NET AI abstraction. Provider-agnostic.                                                                | —                                                                |

## Risks and Technical Debt

- **Lua-CSharp maturity.** Young library. Mitigation: open-source, actively maintained; contribute fixes if needed.
- **AoT compatibility surface area.** M.E.AI and Lua-CSharp must both work under AoT. Mitigation: test AoT publishing early and continuously.
- **Extension API stability.** Once extensions exist, the Lua API surface is a compatibility commitment. Mitigation: keep it small, version explicitly, mark experimental APIs.
- **Permission UX.** Too many prompts trains users to click "yes" blindly. Finding the right granularity is a design challenge.
- **Provider registry staleness.** LLM providers add/deprecate models frequently. Need an update strategy (user override file, auto-fetch, extension-provided models).

## Crosscutting Concerns

| Concern     | Document                                                                                       |
| ----------- | ---------------------------------------------------------------------------------------------- |
| UI Contract | [ui-contract.md](ui-contract.md) — Event streams + callbacks between the library and UI layers |

## Open Questions

System-level questions that span multiple components. Component-specific questions live in their own docs.


## Glossary

| Term                | Definition                                                                                      |
| ------------------- | ----------------------------------------------------------------------------------------------- |
| Workspace           | The directory Ur was launched in. Scopes sessions, config, and workspace extensions.            |
| Session             | A single conversation within a workspace, persisted as JSONL.                                   |
| Extension           | A Lua script or directory that adds capabilities to Ur (tools, middleware, etc.).               |
| System extension    | Extension in `~/.ur/extensions/system/`. Enabled by default. Shipped with or blessed by Ur.     |
| User extension      | Extension in `~/.ur/extensions/user/`. Enabled by default. Installed by the user.               |
| Workspace extension | Extension in `$WORKSPACE/.ur/extensions/`. Disabled by default. Comes from the project repo.    |
| Agent loop          | The core cycle: user input → middleware → LLM → tool calls → repeat.                            |
| Middleware          | Extension hook that intercepts the agent loop pipeline (before LLM call, after response, etc.). |
| Permission grant    | User approval for a sensitive operation, scoped to once/session/workspace/always.               |
| Provider            | An LLM API backend. Currently OpenRouter only.                                                  |
| Model catalog       | Model metadata fetched from the OpenRouter API and cached to disk.                              |
| Model properties    | Read-only model attributes (context length, cost). Fetched from provider API, not user-configurable. |
| AoT                 | Ahead-of-Time compilation. Native binary, no JIT or runtime dependency.                         |
