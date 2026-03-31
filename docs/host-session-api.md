# Host & Session API

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Defines the public runtime surface consumed by UIs. `UrHost` is the workspace-scoped root object; `UrSession` is the conversation-scoped object. The goal is to give frontends a stable, high-level API for session lifecycle, chat readiness, turn execution, and configuration without exposing storage, provider, or agent-loop internals.

## Current Status

- Implemented in this pass:
  - `UrHost`, `UrConfiguration`, `UrSession`, `UrSessionInfo`, `UrChatReadiness`, and `UrChatNotReadyException` are now the public runtime surface.
  - `UrSession.RunTurnAsync(...)` now owns readiness preflight, user-message append, chat-client acquisition, and automatic persistence of appended messages.
  - Runtime configuration writes for API key storage and model selection now go through `UrConfiguration`.
- Still outstanding:
  - The UI flow that prompts for an API key and model selection is still frontend work built on top of `UrConfiguration`.
  - `UrTurnCallbacks` is reserved for future gated tool-approval prompts. The callback contract exists now, but runtime consumption is deferred until tool execution actually needs permission prompts.

### Non-Goals

- Does not render UI or own interaction widgets. The TUI/GUI still decides how to present sessions, setup flows, and streamed output.
- Does not expose `SessionStore`, `AgentLoop`, `ToolRegistry`, or `IChatClient` directly. Those are implementation details behind the public surface.
- Does not make startup interactive. `UrHost.Start(...)` returns a host; setup flows happen afterward through the public configuration surface.

## Context

### Dependencies

| Dependency | What it provides | Interface |
|---|---|---|
| Workspace | Scope for sessions and workspace settings | Workspace path |
| Session Storage | Existing persisted sessions and history | List/create/open/read/append |
| Configuration | Selected model, settings mutation, secret storage | Readiness + write APIs |
| Provider Registry | Model catalog and model metadata | Model list + lookup |
| Agent Loop | Turn execution once a session is ready | Streamed turn events |

### Dependents

| Dependent | What it needs | Interface |
|---|---|---|
| CLI/TUI | List existing sessions, create/open sessions, run turns, complete first-run setup | `UrHost`, `UrSession`, `UrConfiguration` |
| GUI/IDE host | Same as above, with different presentation | Same |

## Interface

### Proposed Public Surface (sketch)

```csharp
public sealed class UrHost
{
    public static UrHost Start(
        string workspacePath,
        IKeyring? keyring = null,
        string? userSettingsPath = null);

    public string WorkspacePath { get; }
    public UrConfiguration Configuration { get; }
    public UrExtensionCatalog Extensions { get; }

    public IReadOnlyList<UrSessionInfo> ListSessions();
    public UrSession CreateSession();
    public Task<UrSession?> OpenSessionAsync(
        string sessionId,
        CancellationToken ct = default);
}

public sealed class UrSessionInfo
{
    public string Id { get; }
    public DateTimeOffset CreatedAt { get; }
}

public sealed class UrSession
{
    public string Id { get; }
    public DateTimeOffset CreatedAt { get; }
    public bool IsPersisted { get; }
    public IReadOnlyList<ChatMessage> Messages { get; }
    public string? ActiveModelId { get; }

    public IAsyncEnumerable<AgentLoopEvent> RunTurnAsync(
        string userInput,
        UrTurnCallbacks? turnCallbacks = null,
        CancellationToken ct = default);
}

public sealed class UrConfiguration
{
    public UrChatReadiness Readiness { get; }
    public string? SelectedModelId { get; }
    public IReadOnlyList<ModelInfo> AvailableModels { get; }

    public ModelInfo? GetModel(string modelId);
    public Task RefreshModelsAsync(CancellationToken ct = default);

    public Task SetApiKeyAsync(
        string apiKey,
        CancellationToken ct = default);

    public Task ClearApiKeyAsync(CancellationToken ct = default);

    public Task SetSelectedModelAsync(
        string modelId,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default);

    public Task ClearSelectedModelAsync(
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default);
}

public sealed class UrExtensionCatalog
{
    public IReadOnlyList<UrExtensionInfo> List();

    public Task<UrExtensionInfo> SetEnabledAsync(
        string extensionId,
        bool enabled,
        CancellationToken ct = default);

    public Task<UrExtensionInfo> ResetAsync(
        string extensionId,
        CancellationToken ct = default);
}

public sealed class UrExtensionInfo
{
    public string Id { get; }
    public string Name { get; }
    public ExtensionTier Tier { get; }
    public string Description { get; }
    public string Version { get; }
    public bool DefaultEnabled { get; }
    public bool DesiredEnabled { get; }
    public bool IsActive { get; }
    public bool HasOverride { get; }
    public string? LoadError { get; }
}

public sealed class UrTurnCallbacks
{
    public Func<PermissionRequest, CancellationToken, ValueTask<PermissionResponse>>?
        RequestPermissionAsync { get; init; }
}

public sealed class UrChatReadiness
{
    public bool CanRunTurns { get; }
    public IReadOnlyList<UrChatBlockingIssue> BlockingIssues { get; }
}

public enum UrChatBlockingIssue
{
    MissingApiKey,
    MissingModelSelection,
}

public enum ConfigurationScope
{
    User,
    Workspace,
}

public sealed class UrChatNotReadyException : Exception
{
    public UrChatReadiness Readiness { get; }
}
```

**Notes:**

- `CreateSession()` is synchronous because it creates an in-memory `UrSession` with an eager ID; no file is written yet.
- `OpenSessionAsync(...)` is asynchronous because it may need to load persisted message history from disk.
- `WorkspacePath` is exposed, not the internal `Workspace` object. The public API should not leak internal path-construction helpers.
- `AvailableModels` is part of configuration because model browsing is primarily a setup/configuration concern in v1.
- Extension management is a separate host-scoped service because enabling/disabling mutates runtime capabilities and uses dedicated management state rather than ordinary settings.

### `UrHost`

- **Purpose:** Workspace-scoped entry point. Created once per launched workspace.
- **Responsibilities:** Enumerate sessions, create/open sessions, expose configuration and readiness, expose model catalog for selection UIs, and expose extension management for the current workspace.
- **Key operations:**
  - `ListSessions() -> IReadOnlyList<UrSessionInfo>`
  - `CreateSession() -> UrSession`
  - `OpenSessionAsync(sessionId, ...) -> UrSession?`
  - `Configuration` property for setup and readiness
  - `Extensions` property for listing and toggling extensions

### `UrSession`

- **Purpose:** Conversation-scoped object. All agent interactions happen through this type.
- **Responsibilities:** Load and expose conversation history as read-only, accept user input, run turns, auto-persist appended messages, expose session metadata.
- **Key operations:**
  - `Messages` read-only view
  - `RunTurnAsync(userInput, turnCallbacks?, ct) -> IAsyncEnumerable<AgentLoopEvent>`
  - `ActiveModelId` reflects the model currently selected for the session in v1; richer per-message provenance is deferred

### `UrConfiguration`

- **Purpose:** Public setup/configuration surface used by UI layers.
- **Responsibilities:** Report chat readiness, store/remove credentials, store/remove selected model, expose enough state for first-run UX.
- **Key operations:**
  - Read current chat readiness
  - Read current selected model
  - Browse/refresh available models
  - Save/remove API credential
  - `SetSelectedModelAsync(modelId, scope = ConfigurationScope.User)`
  - `ClearSelectedModelAsync(scope = ConfigurationScope.User)`
  - Persist model selection to user scope by default, with workspace scope as an explicit opt-in

### `UrExtensionCatalog`

- **Purpose:** Public workspace-scoped extension management surface used by UIs.
- **Responsibilities:** List discovered extensions, report desired vs active state, persist overrides, and activate/deactivate extensions through the library rather than through frontend-owned logic.
- **Key operations:**
  - `List() -> IReadOnlyList<UrExtensionInfo>`
  - `SetEnabledAsync(extensionId, enabled, ct)`
  - `ResetAsync(extensionId, ct)`

### `UrTurnCallbacks`

- **Purpose:** UI-implemented decision points needed while a turn is actively running.
- **v1 scope:** Permission prompts only.
- **Implementation status:** Defined as the future per-turn callback surface, but not yet consumed by the runtime because gated tool-approval flows are not in use yet.
- **Shape:**
  - `RequestPermissionAsync(PermissionRequest, CancellationToken) -> ValueTask<PermissionResponse>`
- **Why this shape:** Permission prompts are synchronous from the library's point of view, but may require asynchronous UI work. Keeping callbacks bundled in a per-turn object leaves room for future turn-scoped interactions without polluting the main `RunTurnAsync` signature.

## Data Structures

### `UrSessionInfo`

- **Purpose:** Lightweight entry for session lists.
- **Shape:** `{ Id, CreatedAt }`
- **Why this shape:** The UI needs list metadata without forcing full history loads. The current storage `Session` type includes `FilePath`, which is an internal detail and should not be part of the public API.

### `UrSession`

- **Purpose:** Live conversation object loaded into memory.
- **Shape:** `{ Id, CreatedAt, IsPersisted, Messages, ActiveModelId? }`
- **Invariants:**
  - `Id` is assigned eagerly when the session is created and remains stable for the lifetime of the session object, whether or not the session has been persisted yet.
  - `IsPersisted` is `false` until the first user message is durably written.
  - `Messages` is read-only to callers.
  - Message mutation happens only through `RunTurnAsync` and internal persistence logic.
  - The session can exist even when chat is not ready; readiness is separate from session existence.
  - `ActiveModelId` tracks the session's current model selection in memory. It is not yet reconstructed from per-message persisted provenance.

### `UrChatReadiness`

- **Purpose:** Describe whether the host/session is ready to execute turns.
- **Shape:** A small structured value with:
  - `CanRunTurns`
  - `BlockingIssues`
- **Blocking issues (v1):**
  - Missing API key
  - Missing selected model
- **Why this shape:** First-run setup is not a turn-execution concern. The UI should be able to detect and resolve blockers before attempting a turn.

### `UrChatNotReadyException`

- **Purpose:** Structured failure for callers who invoke `RunTurnAsync(...)` while readiness blockers remain.
- **Shape:** `{ Readiness }`
- **Why this shape:** The exception carries actionable state rather than a stringly-typed message, so the UI can redirect into setup without reparsing text.

## Internal Design

### Startup and First Run

`UrHost.Start(...)` should succeed as long as the workspace, settings files, and cache can be initialized. Missing API keys or model selection are **not** startup failures. Instead:

1. Host starts and loads workspace/config/cache state.
2. `UrConfiguration.Readiness` reports whether chat can run.
3. UI resolves blockers by storing the API key and selecting a model.
4. Once readiness reports no blockers, any `UrSession` can run turns.

This keeps startup non-interactive and preserves the library-first boundary.

Setup flows are driven explicitly by the UI through the configuration API. They are **not** callbacks. The library reports readiness state; the UI decides when and how to prompt.

### Session Lifecycle

- `ListSessions()` returns lightweight `UrSessionInfo` values.
- `CreateSession()` returns a new in-memory `UrSession` for the workspace.
- `OpenSessionAsync()` loads history into a `UrSession`.
- A newly created session is in-memory only until the first user message is persisted.
- New or existing sessions remain usable for viewing history even if the host is not yet ready to chat.

### Turn Execution

`UrSession.RunTurnAsync(...)` is the only public path for agent interaction:

1. Validate readiness.
2. Append the user message internally.
3. Execute the agent loop.
4. Append/persist assistant/tool messages as they are finalized.
5. Expose streamed events to the UI.

The current implementation delivers readiness preflight, chat-client creation, event streaming, and persistence. Permission callbacks remain a follow-up step for the first gated tool-execution path that actually needs user approval.

If the caller attempts a turn while setup blockers remain, the operation should fail with a **structured setup-required failure**, not a generic `InvalidOperationException` and not an event-stream-only signal.

## Error Handling and Failure Modes

| Failure Mode | Detection | Recovery | Impact on UI |
|---|---|---|---|
| Missing API key | `UrChatReadiness` contains blocker | Prompt and save credential | Setup flow before first turn |
| Missing model selection | `UrChatReadiness` contains blocker | Show model picker and persist selection | Setup flow before first turn |
| Caller ignores readiness and calls `RunTurnAsync` anyway | Preflight in `UrSession` | Return structured setup-required failure | UI can redirect to setup |
| Session not found | `OpenSessionAsync` returns `null` | UI offers create-new path | No crash |

## Design Decisions

### Host startup is non-interactive

- **Context:** Missing API key/model are common first-run conditions.
- **Choice:** `UrHost.Start(...)` still succeeds.
- **Rationale:** Startup should be deterministic and UI-agnostic. Interactive setup belongs in the public configuration surface, not hidden inside host creation.
- **Consequences:** The host can exist in a "not ready to chat yet" state.

### Session lifecycle is independent of chat readiness

- **Context:** A user may want to list sessions or inspect history before setup is complete.
- **Choice:** Creating/opening/listing sessions does not require API key or selected model.
- **Rationale:** Sessions are workspace data; provider configuration is a separate concern.
- **Consequences:** The UI can browse history offline or pre-setup.

### Readiness lives outside the event stream

- **Context:** Missing API key/model is knowable before a turn starts.
- **Choice:** Expose a readiness/configuration surface, and treat premature turn attempts as structured preflight failures.
- **Rationale:** Event streams are for active turn execution. Setup blockers are configuration state, not streamed conversation events.
- **Consequences:** UIs can build first-run flows cleanly without probing by "trying a turn."

### `UrSession` auto-persists and exposes read-only history

- **Context:** Letting callers mutate messages or decide when to save would collapse the abstraction and push orchestration back into the UI.
- **Choice:** History is read-only; every turn persists automatically.
- **Rationale:** Encapsulation. `UrSession` should own conversation state and durability end to end.
- **Consequences:** The public API stays high-level and hard to misuse.

### New sessions persist on first user message

- **Context:** Creating an empty session file immediately would make abandoned or accidental sessions appear in history with no useful content.
- **Choice:** `CreateSession()` creates an in-memory `UrSession`; the backing JSONL file is created only when the first user message is persisted.
- **Rationale:** Session history should reflect actual conversations, not tentative UI state.
- **Consequences:** Brand-new untouched sessions do not appear in `ListSessions()`. The public API and docs must make this behavior explicit so UIs do not assume "create" implies immediate persistence.

### Session IDs are assigned at birth

- **Context:** An in-memory `UrSession` still needs a stable identity before its first persisted message.
- **Choice:** `CreateSession()` assigns the session ID immediately, not lazily on first persistence.
- **Rationale:** Stable identity simplifies UI state, logging, and internal references. The runtime object should not change identity based on whether the user has sent the first message yet.
- **Consequences:** Some session IDs will never correspond to persisted files if the user abandons the session before the first message. This is acceptable because only persisted sessions appear in `ListSessions()`.

### Model selection defaults to user scope

- **Context:** The selected model is usually a user preference, but some workspaces may need an explicit override.
- **Choice:** Persist selected model to user scope by default. Workspace-scoped selection is supported, but only when the caller asks for it explicitly.
- **Rationale:** This matches typical user expectations while still allowing project-specific overrides. Making workspace scope the default would create surprising repo-local behavior for a fundamentally personal preference.
- **Consequences:** The configuration API uses a single write method with an explicit scope parameter, which keeps the common case terse without multiplying methods.

### Configuration uses explicit methods, not callbacks

- **Context:** First-run setup requires collecting an API key and model selection, but these are not in-band turn execution decisions.
- **Choice:** `UrConfiguration` exposes explicit read/write methods and readiness state. No host- or configuration-level callbacks are defined.
- **Rationale:** This keeps startup deterministic and easy to reason about. The library reports state; the UI decides when to prompt and which flow to show.
- **Consequences:** The callback surface stays minimal and focused on true mid-turn decision points.

### Extension management is host-scoped, not configuration-scoped

- **Context:** Listing and toggling extensions affects the active tool/middleware surface and uses dedicated management state that is intentionally separate from settings.
- **Choice:** Expose extension management as `UrHost.Extensions`, not as methods on `UrConfiguration`.
- **Rationale:** This keeps configuration focused on settings and secrets while giving frontends a single workspace root for mutable runtime capabilities.
- **Consequences:** Frontends use one host-scoped root object for sessions, configuration, and extensions, but the lifecycle semantics stay separate and clearer.

### Turn callbacks are permission-only in v1

- **Context:** The UI contract needs some synchronous-from-the-library decision point during turn execution.
- **Choice:** The only v1 turn callback is permission prompting.
- **Rationale:** This is the only known blocker where the library must pause an active turn and wait for the UI's answer. Other interactions should not be speculated into existence yet.
- **Consequences:** `UrTurnCallbacks` remains small and future additions are opt-in rather than baked into the core too early.

## Open Questions

None currently.
