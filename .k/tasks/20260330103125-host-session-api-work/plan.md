# Implement Host And Session Runtime API

## Goal

Turn the architecture in `docs/host-session-api.md` into the actual public runtime API, so UIs can work through `UrHost`, `UrSession`, and `UrConfiguration` instead of reaching into workspace, settings, model catalog, and session-store internals.

## Desired outcome

- `UrHost.Start(...)` remains the only startup entry point and stays non-interactive.
- Frontends can list, create, and open sessions through stable public types.
- Chat readiness, API-key management, model selection, and model browsing move behind a configuration API.
- Turn execution moves behind `UrSession.RunTurnAsync(...)`, with readiness preflight and automatic persistence. Per-turn permission callbacks remain reserved for the follow-up tool-approval task.

## Related documents

- `docs/host-session-api.md` - Target API and behavior contract for this task.
- `docs/index.md` - System-level constraints, especially library-first startup and public runtime surface direction.
- `docs/configuration.md` - Current and planned configuration behavior, including runtime mutation and keyring ownership.
- `docs/session-storage.md` - Session lifecycle and persistence expectations that the runtime API must wrap.
- `docs/provider-registry.md` - Model catalog and selected-model behavior that should surface through configuration.
- `docs/agent-loop.md` - Current turn execution contract and where `UrSession` will need to adapt it.
- `docs/ui-contract.md` - Event/callback boundary that `UrSession.RunTurnAsync(...)` needs to preserve.
- `docs/permission-system.md` - Permission request/response contract for `UrTurnCallbacks`.
- `docs/decisions/adr-0010-mid-session-model-switch.md` - Provenance and model-switch expectations that affect session/message shape.
- `docs/decisions/adr-0011-library-owns-chat-client-and-keyring.md` - Encapsulation boundary for host startup and internal chat-client creation.

## Related code

- `Ur/UrHost.cs` - Current public root exposes internal implementation objects instead of the desired UI-facing surface.
- `Ur/Sessions/Session.cs` - Current session handle is a storage object, not the public runtime session described in the architecture doc.
- `Ur/Sessions/SessionStore.cs` - Current persistence API creates IDs, lists files, and appends bare `ChatMessage` objects; it will need to support the new runtime semantics.
- `Ur/AgentLoop/AgentLoop.cs` - Current turn runner mutates caller-owned message lists and has no readiness/persistence wrapper.
- `Ur/AgentLoop/AgentLoopEvent.cs` - Existing public event stream that the new `UrSession` API should continue to expose.
- `Ur/Configuration/Settings.cs` - Current configuration is read-only after load, which blocks the planned runtime write APIs.
- `Ur/Configuration/SettingsLoader.cs` - Current loader defines merge and validation rules that write-through config should preserve.
- `Ur/Providers/ModelCatalog.cs` - Existing model lookup/refresh API that should be surfaced via `UrConfiguration`.
- `Ur/Providers/ChatClientFactory.cs` - Internal client creation path that must remain library-owned and be consumed through `UrSession`.
- `Ur/Workspace.cs` - Current workspace path and directory layout rules that still underpin the new public API.
- `Ur.Cli/Program.cs` - Thin frontend consumer that should move to the new host/config/session surface.

## Current state

- Relevant existing behavior:
  - `UrHost` publicly exposes `Workspace`, `ModelCatalog`, `Settings`, and `SessionStore`.
  - `SessionStore.Create()` returns a session handle with an ID and file path, but no runtime wrapper and no persisted file until append.
  - `AgentLoop.RunTurnAsync(...)` accepts a mutable `List<ChatMessage>` and leaves persistence to the caller.
  - Settings load is implemented, but runtime mutation and write-through persistence are not.
- Existing patterns to follow:
  - Startup is synchronous and library-owned.
  - Model catalog loading and refresh already exist as a distinct concern.
  - Permission interaction already uses typed request/response records rather than UI-specific APIs.
- Constraints from the current implementation:
  - There is no runtime abstraction layer between public callers and storage/config/provider internals yet.
  - Session files still serialize bare `ChatMessage` values, while the docs now describe provenance envelopes.
  - The CLI is currently only exercising startup and model-catalog loading, not conversational flows.

## Constraints

- Preserve non-interactive startup: missing API key or model selection must not make `UrHost.Start(...)` fail.
- Keep provider SDKs, keyring creation, and chat-client creation internal to the library.
- Do not leak `Workspace`, `SessionStore`, `Settings`, or `ModelCatalog` as the public UI contract.
- Default model selection writes to user scope; workspace override must stay explicit.
- New sessions should not appear in persisted session lists until the first user message is durably written.
- The event stream and permission callback model should remain UI-agnostic.

## Research

No saved research notes or brainstorm docs existed under `.k/tasks/20260330103125-host-session-api-work/`.

### Repo findings

- The repo already contains the minimum building blocks for the planned surface, but they are exposed one layer too low.
- `docs/host-session-api.md` is ahead of the current codebase and should be treated as the contract for this implementation pass.
- `docs/configuration.md` explicitly calls out runtime mutation as planned but unimplemented, making `UrConfiguration` the biggest missing subsystem.
- `docs/session-storage.md` and ADR-0010 imply session-envelope/provenance work that is not reflected in `SessionStore` yet.
- `docs/agent-loop.md` intentionally keeps the loop storage-agnostic, so `UrSession` should wrap rather than collapse that boundary.

## Scope decisions

- This task implements the runtime API described in `docs/host-session-api.md`. Doc edits are limited to correcting inconsistencies discovered while aligning code to that contract.
- `UrConfiguration` owns readiness calculation and all frontend-facing write APIs for API-key and model-selection changes.
- `RunTurnAsync(...)` throws `UrChatNotReadyException` before yielding any events when readiness blockers remain.
- `ActiveModelId` is the session's currently selected model in v1. Full per-message provenance and session-envelope persistence are explicitly out of scope for this pass.
- `CreateSession()` remains synchronous. The stray `CreateSessionAsync()` mention in the doc is treated as a documentation typo and should be corrected as part of this work.
- `UrTurnCallbacks` is kept as the future per-turn callback surface, but actual permission-prompt consumption is deferred until gated tool execution is wired into the runtime path.

## Out of scope

- JSONL session-envelope/provenance changes described in `docs/session-storage.md` and ADR-0010.
- Mid-session historical model tracking beyond the runtime session's current active model.
- Broader CLI conversation UX beyond adopting the new public API surface.

## Approach

- Proposed shape of the change:
  - Add dedicated public runtime types for host, configuration, session, session-info, readiness, blocking issues, turn callbacks, and readiness exception.
  - Refactor `UrHost` into a thin public facade over internal workspace/config/provider/session dependencies.
  - Introduce a runtime session object that owns in-memory messages, lazy persistence, and turn orchestration.
  - Extend configuration with explicit read/write methods for selected model and API key, plus model catalog exposure and readiness computation.
- Key tradeoffs:
  - A strict implementation of `ActiveModelId` likely pulls in session-envelope work now, but deferring that keeps the first pass smaller.
  - Preserving agent-loop independence means `UrSession` becomes the orchestration layer rather than pushing persistence into `AgentLoop`.
  - Replacing public low-level properties on `UrHost` may require some internal-only seams for tests and the CLI.
- Areas likely to change:
  - `Ur/UrHost.cs`
  - `Ur/Sessions/*`
  - `Ur/Configuration/*`
  - `Ur/Providers/*` surface wiring
  - `Ur.Cli/Program.cs`
  - New tests in `Ur.Tests/`

## Implementation plan

- [x] Correct `docs/host-session-api.md` so it matches the intended v1 scope: `CreateSession()` is synchronous, `RunTurnAsync(...)` uses exception-based readiness preflight, and `ActiveModelId` reflects the session's current model rather than persisted per-message provenance.
- [x] Refactor `UrHost` into the documented facade: expose `WorkspacePath`, `Configuration`, `ListSessions()`, `CreateSession()`, and `OpenSessionAsync(...)`, while moving existing low-level collaborators behind internal state.
- [x] Implement `UrConfiguration` with `Readiness`, `SelectedModelId`, `AvailableModels`, `GetModel(...)`, `RefreshModelsAsync(...)`, `SetApiKeyAsync(...)`, `ClearApiKeyAsync(...)`, `SetSelectedModelAsync(...)`, and `ClearSelectedModelAsync(...)`.
- [x] Add settings write-through support for user/workspace scopes so configuration changes update in-memory state and persist to the correct `settings.json` file without bypassing existing validation rules.
- [x] Add runtime session types: lightweight `UrSessionInfo` for persisted session lists and `UrSession` for in-memory conversation state, eager ID assignment, read-only messages, and `IsPersisted` tracking.
- [x] Keep `SessionStore` as the persistence layer but adapt it to the runtime API: list/open persisted sessions, create in-memory session identity without eagerly creating files, and persist on first appended message.
- [x] Extend `AgentLoop` integration so `UrSession.RunTurnAsync(...)` owns user-message creation, readiness preflight, chat-client acquisition, and automatic persistence of user/assistant/tool messages. Keep `UrTurnCallbacks` as the reserved callback surface for the follow-up tool-approval task.
- [x] Update the CLI to consume only the new public API surface and remove reliance on exposed internals.
- [x] Add and pass tests covering host startup without configuration, readiness blockers, configuration writes, session create/list/open semantics, first-message persistence, and `UrChatNotReadyException` preflight behavior.

## Task outcome

- Delivered in this task:
  - Public host/configuration/session runtime types and readiness primitives.
  - Write-through configuration APIs for API key management and selected-model persistence.
  - Session create/list/open behavior with lazy first-message persistence.
  - `UrSession.RunTurnAsync(...)` readiness preflight, chat-client acquisition, and automatic message persistence.
  - CLI adoption of the new public startup/configuration surface.
- Deferred to the next task:
  - TUI setup UX for entering the API key and selecting a model based on `UrConfiguration.Readiness`.
  - Conversational CLI/TUI flows that create or open a session and drive `UrSession.RunTurnAsync(...)`.
  - Permission-prompt integration through `UrTurnCallbacks` once gated tool execution actually needs approval.

## Impact assessment

- Code paths affected:
  - Host startup, session lifecycle, configuration mutation, and turn execution paths.
- Data or schema impact:
  - Settings files will gain library-owned write paths for model selection.
- Dependency or API impact:
  - Public API changes substantially toward the documented runtime surface.
  - CLI and future frontends become less coupled to implementation details.
- Ops, rollout, or migration concerns:
  - Tests should cover both empty/new-session behavior and persisted-session reopening.

## Validation

- Tests:
  - Add unit tests for readiness blockers, API-key/model selection writes, session list/open/create semantics, and `UrChatNotReadyException`.
  - Add persistence tests for first-message save behavior and reopening persisted sessions.
- Lint/format/typecheck:
  - `dotnet build Ur.slnx`
  - `dotnet test Ur.slnx`
- Manual verification:
  - Start the CLI in a temp workspace and verify host startup succeeds before setup.
  - Verify a brand-new session is not listed until the first user message is persisted.
  - Verify configuration changes survive restart in the intended scope.

## Risks and follow-up

- Risk: Retrofitting runtime writes into the current `Settings` shape may expose the need for a dedicated settings-writer abstraction.
- Follow-up: Session-envelope/provenance work remains a separate task once the public runtime API is in place.
- Follow-up: Build the first real frontend flow on top of this API: readiness/setup UI first, then conversational session UX, then tool-approval prompting when the runtime actually needs it.
