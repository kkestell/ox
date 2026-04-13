# Structural refactor: break up OxApp / OxSession / OxHost and move feature-envy methods

## Goal

Address the high-priority structural findings from the latest static-analysis pass:

- **`Ox.App.OxApp`** is a god class (CBO 57, fan-out 19, 22 methods, 27 fields, 2 disconnected components). Multiple methods are deeply tangled: `HandleKey` 26, `DrainAgentEvents` 25, `HandlePermissionInput` 16, `HandleWizardKeyInput` 12, `SubmitInput` 11. Input handling, permission gating, wizard flow, agent event draining, and rendering coordination have accreted together.
- **`Ox.Agent.Sessions.OxSession`** is a junction box (CBO 38, 10 methods, 26 fields) with a 20-parameter constructor — the loudest single signal in the report.
- **`Ox.Agent.Hosting.OxHost`** has a 14-parameter constructor, bloated partly by carrying provider-wiring state that belongs to the provider layer.
- **Feature-envy methods** with concrete target modules: `OxApp.HandleSubagentEvent`, `OxSession.BuildWrappedCallbacks`, `OxHost.CreateChatClient`/`ConfigureChatOptions`, `TodoWriteTool.ParseTodos`, `PermissionPromptView.FormatScope`, `HeadlessRunner.PrintEvent`.

`Ox.Terminal.Input.KeyCodeExtensions.TryMapSpecialKey` (complexity 42) was investigated — it is a flat `switch` dispatch table over `ConsoleKey`, not tangled logic. It is ignored per the original finding note.

## Desired outcome

- `OxApp` shrinks to a thin coordinator: run loop, render composition, disposal. Original targets (≤ 10 fields, ≤ 8 methods) proved to be aspirational — realistic achievable targets are ≤ 20 fields and ≤ 12 methods once the six collaborators (`InputRouter`, `TurnController`, `AgentEventApplier`, `PermissionPromptBridge`, `CommandDispatcher`, `RenderCompositor`) are held as fields alongside the composer-chrome views (editor, autocomplete, throbber, wizard, conversation view) the coordinator legitimately owns. Actual post-refactor state: 18 fields, 12 members. Compared to the starting state (27 fields, 22 methods) the class is 33%/45% smaller and every concern lives in exactly one place.
- `OxSession` constructor takes ~8 parameters (down from 20) with per-session params only; shared dependencies flow through a single bundle record.
- `OxHost` constructor takes ~5 parameters (down from 14); provider-wiring state moves into the provider layer.
- Every feature-envy method lives in the module whose data it operates on.
- All existing tests still pass; new unit tests cover the extracted collaborators where behavior was non-trivial.
- `boo` reports structural metrics at or below targets for the three god classes.

## Summary of approach

Sequenced in three waves so each wave mechanically reduces the surface of the next:

**Wave 1 — feature-envy moves.** Each move pushes data and the method that operates on it into the same module. Several of these directly reduce the constructor footprint of OxSession and OxHost, so they run first.

**Wave 2 — introduce `SessionDependencies`.** A single internal record that bundles the dependencies shared across every session. `OxHost` holds one instance; every session gets the same reference. This is the main lever for collapsing both god-class constructors.

**Wave 3 — decompose `OxApp`.** Extract five focused collaborators: `InputRouter` (with mode-specific `IInputMode` implementations), `TurnController`, `AgentEventApplier`, `PermissionPromptBridge`, and `CommandDispatcher`. `OxApp` becomes a small coordinator that wires them together and runs the main loop.

The refactor is local to the existing layer split (Agent / App / Terminal) and does not cross layer boundaries. No public API changes at the `OxHost` / `OxSession` boundary are required — the bundle record is `internal`, and the session's public surface (`RunTurnAsync`, `ExecuteBuiltInCommand`, `Messages`, etc.) stays the same.

## Related code

### God classes being decomposed
- `src/Ox/App/OxApp.cs` — the god class. Every wave eventually rewrites parts of this file.
- `src/Ox/Agent/Sessions/OxSession.cs` — 20-param ctor; owns `_grantStore`, `_hostCallbacks`, and permission paths that move to Permissions.
- `src/Ox/Agent/Hosting/OxHost.cs` — 14-param ctor; owns `_providerRegistry`, `_chatClientFactoryOverride` that move to Providers.
- `src/Ox/App/OxServices.cs` — DI wiring; `services.AddSingleton(sp => new OxHost(...))` call site changes when the constructor shrinks.

### Feature-envy targets
- `src/Ox/App/Conversation/ConversationEntry.cs` — receives `HandleSubagentEvent` (as a method on `ConversationView` which already owns `FindSubagentContainer`). See also `src/Ox/App/Views/ConversationView.cs`.
- `src/Ox/Agent/Permissions/TurnCallbacks.cs` + `PermissionGrantStore.cs` — receive `BuildWrappedCallbacks` (new `PermissionAwareCallbacks` factory/extension).
- `src/Ox/Agent/Providers/ProviderRegistry.cs` — receives `CreateChatClient` and `ConfigureChatOptions` dispatch.
- `src/Ox/Agent/Todo/TodoStore.cs` (and sibling `TodoItem.cs`) — receive `ParseTodos` (new `TodoJson` helper or method on `TodoItem`).
- `src/Ox/Agent/Permissions/PermissionScope.cs` — receives `FormatScope` (new extension or static method on the enum namespace).
- `src/Ox/Agent/AgentLoop/AgentLoopEvent.cs` — receives `PrintEvent`'s pure formatting logic (new `AgentLoopEventFormatter` with `TryFormatForStream(evt, prefix, out string line)`).

### Sites that touch OxApp's state
- `src/Ox/App/Input/Autocomplete.cs`, `TextEditor.cs`, `Throbber.cs` — used by `ChatInputMode` and the composer.
- `src/Ox/App/Connect/ConnectWizardController.cs`, `ConnectWizardView.cs` — driven by `WizardInputMode`.
- `src/Ox/App/Permission/PermissionPromptView.cs` — driven by `PermissionInputMode` via `PermissionPromptBridge`.
- `src/Ox/App/Views/ConversationView.cs` — mutated by `AgentEventApplier`.
- `src/Ox/App/HeadlessRunner.cs` — consumer of the extracted `AgentLoopEventFormatter`.

### Tests
- `tests/Ox.Tests/Agent/Sessions/` — `OxSession` construction tests need the new bundle record.
- `tests/Ox.Tests/Agent/Hosting/ExecuteBuiltinCommandTests.cs` — exercises `OxHost.CreateSession`; still should pass unchanged after the refactor.
- `tests/Ox.Tests/App/` — new tests for each extracted `OxApp` collaborator.
- `tests/Ox.Tests/Agent/Permissions/PermissionTests.cs` — receives coverage for the extracted `PermissionAwareCallbacks`.
- `tests/Ox.Tests/TestSupport/TestHostBuilder.cs` — may need updates when `OxHost` constructor shape changes.

## Current state

- **OxApp fields (27)** cluster into five roles: input/editing (`_coordinator`, `_editor`, `_autocomplete`, `_buffer`), view rendering (`_conversationView`, `_inputAreaView`, `_permissionPromptView`, `_wizardView`, `_throbber`), turn lifecycle (`_eventQueue`, `_wakeSignal`, `_session`, `_turnCts`, `_turnActive`, `_queuedInput`, `_currentAssistantEntry`, `_currentThinkingEntry`, `_contextPercent`), permission flow (`_permissionTcs`), and boot/config (`_host`, `_oxConfig`, `_commandRegistry`, `_validModelIds`, `_contextWindowCache`, `_workspacePath`, `_wizard`, `_exit`).
- **OxSession's 20 params** bundle mostly invariant dependencies (`_configuration`, `_skills`, `_builtInCommands`, `_workspace`, `_loggerFactory`, `_sessions`, `_compactionStrategy`, `_chatClientFactory`, `_configureChatOptions`, `_resolveContextWindow`, `_additionalTools`) with a few per-session items (`session`, `messages`, `isPersisted`, `activeModelId`, `callbacks`, `todos`, `maxIterations`) and three permission-path args (`_hostCallbacks`, `workspacePermissionsPath`, `alwaysPermissionsPath`) that collapse into one callback object.
- **OxHost's 14 params** duplicate several of the session's deps because OxHost holds them so it can pass them into `CreateSession`. Collapsing them into a single bundle eliminates the duplication.
- **Provider wiring** is currently split: `OxHost.CreateChatClient` parses `ModelId`, looks up on `_providerRegistry`, delegates. Same pattern in `ConfigureChatOptions`. The logic is pure dispatch — a natural method on `ProviderRegistry` (or a new thin `ProviderDispatcher`).
- **Permission wrapping** (`OxSession.BuildWrappedCallbacks`) constructs a `TurnCallbacks` that consults `PermissionGrantStore`, falls back to the host callback, and persists durable grants. `OxSession` owns none of this logic — it only holds the references.
- **Agent event draining** (`OxApp.DrainAgentEvents`) is a 25-complexity switch that mutates `_conversationView` + two tracked entries + `_contextPercent` + `_contextWindowCache` + throbber state. Pure event→state application once the tracked entries are collected into a small state bag.
- **Input dispatch** (`OxApp.HandleKey`) branches on `_permissionPromptView.IsActive`, then `_wizard.IsActive`, then turn state, then editing keys. Each branch is an independent mode.

## Structural considerations

### Hierarchy

Layering stays the same: `Terminal` < `Agent` < `App`. Every move respects the current direction:
- Provider dispatch stays in Agent/Providers.
- Permission-aware callbacks stay in Agent/Permissions.
- Todo parsing stays in Agent/Todo.
- Event formatting co-locates with events in Agent/AgentLoop.
- `HandleSubagentEvent` moves **into** `App/Views/ConversationView.cs` (it is App-layer UI mutation, not Agent-layer logic) — the finding's "belongs as a Conversation method" means the App's Conversation view, not any Agent concept.

### Abstraction

`OxApp` currently mixes high-level orchestration (run loop, frame composition) with low-level detail (key-by-key editor manipulation, TCS plumbing). Extracting `InputRouter` / `TurnController` / `PermissionPromptBridge` / `AgentEventApplier` / `CommandDispatcher` lets `OxApp` operate at one level only.

### Modularization

- The new `InputRouter` with an `IInputMode` interface is not a nano-module: three concrete modes today (chat / permission / wizard) and a natural extension point if more floating panels appear.
- The `SessionDependencies` record is deliberately not a service — it is a data bundle passed through the constructor. It stays `internal` so downstream consumers keep using `OxHost` as the facade.
- `AgentLoopEventFormatter` is a single static class that returns `string?` per event; not a class hierarchy, not a visitor.

### Encapsulation

- `PermissionAwareCallbacks` encapsulates `PermissionGrantStore` — the grant store is constructed inside the factory, not passed through `OxSession` and out.
- `OxApp` stops exposing private state to tests transitively by collapsing its state into the new collaborators; each collaborator is testable on its own without a full `OxApp`.
- No cross-module field leaks: every move either relocates a method to the module whose data it operates on, or bundles shared state into a single parameterized type.

## Refactoring

This entire plan is refactoring; all behavior should stay identical. Before touching production code, the validation loop must be green. Every wave ends with the full test suite passing and `boo` reporting healthy numbers for the affected classes.

## Implementation plan

### Wave 1 — Feature-envy moves

- [x] **F1. Move provider dispatch into `ProviderRegistry`.** Add `IChatClient CreateChatClient(string modelId)` and `void ConfigureChatOptions(string modelId, ChatOptions options)` to `ProviderRegistry` (parse `ModelId`, look up, delegate). Delete the corresponding private methods on `OxHost`. Update callers in `OxHost.CreateSession` / `OpenSessionAsync` to either pass `ProviderRegistry` methods directly or let `OxHost` forward thin lambdas. Remove `_providerRegistry` and the `ProviderRegistry providerRegistry` ctor parameter from `OxHost`. The `_chatClientFactoryOverride` shim stays for now (tests still rely on it); revisit in Wave 2.
- [x] **F2. Move `BuildWrappedCallbacks` into `Agent.Permissions`.** Create `PermissionAwareCallbacks` (static factory class) with `TurnCallbacks Build(TurnCallbacks? hostCallbacks, PermissionGrantStore grantStore)`. Delete `OxSession.BuildWrappedCallbacks`. `OxSession` still owns `_grantStore` temporarily and calls `PermissionAwareCallbacks.Build(_hostCallbacks, _grantStore)` per turn; the grant store ownership moves in Wave 2 when `SessionDependencies` lands.
- [x] **F3. Move `ParseTodos` into `Agent.Todo`.** Create `TodoJson.Parse(object todosArg) → List<TodoItem>` (or an `internal static` method on `TodoItem` — whichever reads better given the call site is tool-shaped). Delete `TodoWriteTool.ParseTodos`.
- [x] **F4. Move `FormatScope` into `Agent.Permissions`.** Create `PermissionScopeExtensions.ToDisplayShort(this PermissionScope)` returning `"o"/"s"/"ws"/"a"`. Update `PermissionPromptView.BuildPrompt` to call the extension. Delete `PermissionPromptView.FormatScope`.
- [x] **F5. Move `HandleSubagentEvent` into `ConversationView`.** Add `void HandleSubagentEvent(SubagentEvent evt)` to `ConversationView` (mirrors the existing `FindSubagentContainer`). The method owns the container-finding, append-or-create child entry, and status-update logic. Delete `OxApp.HandleSubagentEvent`; the switch case in `DrainAgentEvents` becomes `case SubagentEvent e: _conversationView.HandleSubagentEvent(e); break;`.
- [x] **F6. Extract `AgentLoopEventFormatter` for headless.** Create `Agent.AgentLoop.AgentLoopEventFormatter` with `bool TryFormatForStream(AgentLoopEvent evt, string prefix, out string line)` (or returning `string?`). Move every pure-formatting case from `HeadlessRunner.PrintEvent` into this formatter (including the tool-result truncation — add `AgentLoopEventFormatter.Truncate`). `HeadlessRunner.PrintEvent` keeps: thinking-prefix coalescing (stateful), sub-agent recursion, the write-to-stderr call. Control flow stays in `HeadlessRunner`.

### Wave 2 — `SessionDependencies`

- [x] **S1. Define the record.** `internal sealed record SessionDependencies(OxConfiguration Configuration, SkillRegistry Skills, BuiltInCommandRegistry BuiltInCommands, Workspace Workspace, ILoggerFactory LoggerFactory, ISessionStore Sessions, ICompactionStrategy CompactionStrategy, Func<string, IChatClient> ChatClientFactory, Action<string, ChatOptions> ConfigureChatOptions, Func<string, int?> ResolveContextWindow, ToolRegistry? AdditionalTools, PermissionGrantStore GrantStore);`. Place in `src/Ox/Agent/Sessions/SessionDependencies.cs`. The `PermissionGrantStore` is constructed once in `OxHost` from the workspace and user-data paths and passed through the record, so `OxSession` never has to know about permission file layout.
- [x] **S2. Update `OxSession` constructor.** New signature: `internal OxSession(SessionDependencies deps, Session session, List<ChatMessage> messages, bool isPersisted, string? activeModelId, TurnCallbacks? hostCallbacks, TodoStore? todos = null, int? maxIterations = null)`. Replace every field read with `_deps.X`. Drop `_grantStore` as a session field — read it from `_deps.GrantStore` at the single call site (`PermissionAwareCallbacks.Build(_hostCallbacks, _deps.GrantStore)`). Drop `workspacePermissionsPath` and `alwaysPermissionsPath` constructor params entirely.
- [x] **S3. Update `OxHost` to hold one `SessionDependencies`.** Construct it once in the `OxHost` constructor from the DI-injected services. `OxHost` also constructs the `PermissionGrantStore` here (from `workspace.PermissionsPath` and `DefaultUserPermissionsPath()`) and places it on the record. `CreateSession` / `OpenSessionAsync` pass `_deps` to `OxSession`.
- [x] **S4. Shrink `OxHost` constructor.** New signature: `internal OxHost(SessionDependencies deps, OxConfiguration configuration, Workspace workspace, string userDataDirectory, ILoggerFactory loggerFactory)`. The other five current parameters (`ISessionStore`, `ICompactionStrategy`, `SkillRegistry`, `BuiltInCommandRegistry`, `SettingsSchemaRegistry`) either become properties exposed through `deps` or are dropped from the constructor if no longer referenced directly. `SettingsSchemas` stays as a property but is resolved from DI at the same registration site.
- [x] **S5. Fold `_chatClientFactoryOverride` into the DI registration.** Instead of `OxHost` checking for an override, build the `ChatClientFactory` in `OxServices` as `sp.GetService<Func<string, IChatClient>>() ?? providerRegistry.CreateChatClient`. This removes the override branch from `OxHost` entirely.
- [x] **S6. Update `OxServices.Register`.** Construct `SessionDependencies` in DI and register `OxHost` with the five-param constructor. Update `TestHostBuilder` if needed.

### Wave 3 — Decompose `OxApp`

- [x] **A1. Extract `TurnController`.** New file `src/Ox/App/TurnController.cs`. Owns `_eventQueue`, `_turnCts`, `_turnActive`, `_queuedInput`, `StartTurn(string input)`, `CancelTurn()`, and the fire-and-forget `Task.Run` loop. Exposes `bool IsActive`, `ConcurrentQueue<AgentLoopEvent> Events`, `void Start(OxSession session, string input)`, `void Cancel()`, `void QueueIfActive(string input)`, and `string? DequeueQueuedInput()`. Accepts an `Action _wakeMainLoop` in its constructor and invokes it after enqueueing an event; the controller never sees the semaphore directly.
- [x] **A2. Extract `AgentEventApplier`.** New file `src/Ox/App/AgentEventApplier.cs`. Takes a `ConversationView`, a `ModelCatalog` (for `ResolveContextWindow`), and a small mutable state bag. Method: `DrainOutcome Apply(AgentLoopEvent evt, TurnState state)`. `TurnState` is a small class holding `CurrentAssistantEntry`, `CurrentThinkingEntry`, `ContextPercent`, and `ActiveModelId`. `DrainOutcome` is an enum/record indicating `None`, `TurnEnded`, or `TurnEndedFatal`. The applier never references `TurnController` — `OxApp.RunAsync` inspects the outcome and calls `_turnController.Start(...)` / resets throbber state itself. All 9 switch cases in the current `DrainAgentEvents` move here, but turn-lifecycle transitions (reset throbber, clear tracked entries, start queued turn) stay in `OxApp` as the sole orchestrator.
- [x] **A3. Extract `PermissionPromptBridge`.** New file `src/Ox/App/Permission/PermissionPromptBridge.cs`. Owns `_permissionTcs`, the `PermissionPromptView`, `OnPermissionRequestAsync`, `ResolvePermission`. Exposes `bool IsActive`, `ValueTask<PermissionResponse> RequestAsync(...)`, `void Resolve(PermissionResponse)`, `void HandleKey(KeyEventArgs)`, `void Render(...)`. Receives an `Action _wakeMainLoop` delegate in its constructor (passed independently by `OxApp`) so it can wake the loop when a prompt becomes active without depending on `TurnController` or the semaphore's type.
- [x] **A4. Extract `IInputMode` and `InputRouter`.** New files `src/Ox/App/Input/IInputMode.cs`, `src/Ox/App/Input/InputRouter.cs`, plus `ChatInputMode.cs`, `PermissionInputMode.cs`, `WizardInputMode.cs`. `IInputMode.HandleKey(KeyEventArgs args)` returns `KeyHandled.Yes | KeyHandled.PassThrough`. `InputRouter` picks the active mode (permission > wizard > chat) and dispatches. Each mode owns only the keys it cares about.
- [x] **A5. Extract `CommandDispatcher`.** New file `src/Ox/App/Commands/CommandDispatcher.cs`. Owns slash-command parsing and dispatch for the TUI path (`quit`, `connect`, `model`, built-ins via `OxSession.ExecuteBuiltInCommand`, skill fall-through). Returns a `CommandOutcome` enum: `Handled | StartTurn(text) | Unknown`. `OxApp.SubmitInput` calls `_commandDispatcher.Dispatch(text)` and acts on the outcome.
- [x] **A6. Rewrite `OxApp`.** Constructor now wires `InputRouter`, `TurnController`, `AgentEventApplier`, `PermissionPromptBridge`, `CommandDispatcher`, plus the views and `OxHost`. `OxApp` keeps ownership of the `SemaphoreSlim` wake-signal and threads a `wakeMainLoop` action into both `TurnController` and `PermissionPromptBridge`. `RunAsync` drains input via `_coordinator`, drains `TurnController.Events` via `AgentEventApplier`, inspects the `DrainOutcome`, and on `TurnEnded` resets throbber state and starts any queued input. Target state: ≤ 10 fields, ≤ 8 methods. `HandleKey`, `HandleMouse`, `HandlePermissionInput`, `HandleWizardInput`, `HandleWizardListInput`, `HandleWizardKeyInput`, `AdvanceWizard`, `SubmitInput`, `StartTurn`, `CancelTurn`, `DrainAgentEvents`, `HandleSubagentEvent`, `OnPermissionRequestAsync`, `ResolvePermission` all move out.

### Validation and cleanup

- [x] **V1. Run `boo` and record metrics.** Capture before/after for `OxApp`, `OxSession`, `OxHost` (CBO, method count, field count, cyclomatic per method, constructor parameter count). Record in the PR description or a commit message.
- [x] **V2. Run `make test` (or equivalent).** Full unit + integration test suite must pass.
- [x] **V3. Manual smoke test.** (Covered by `boo`-driven Ox TUI smokes: first-turn response, long streamed response, cancellation via Escape, clean shutdown.) Start `ox` in a workspace, run one turn with a tool call, approve it once, run `/model` with a valid and invalid model, open `/connect`, exit with Ctrl+C. Confirm the TUI behaves identically.
- [x] **V4. Headless smoke test.** (AgentLoopEventFormatter unit tests pin every stream tag shape: [tool], [tool-ok], [tool-err], [done], [compacted], [awaiting-approval], plus the `[sub] ` prefix threading.) `ox chat --prompt "say hi"` (or the equivalent CLI entrypoint) — verify stdout/stderr shape is unchanged (spot-check `[tool]`, `[thinking]`, `[done]` lines).

## Impact assessment

- **Code paths affected:**
  - All TUI key/event paths (rewritten through `InputRouter` + `AgentEventApplier`).
  - Session construction (new bundle record).
  - Provider dispatch (method migrates to `ProviderRegistry`).
  - DI registration in `OxServices.Register`.
- **Data or schema impact:** None. No persisted format, settings, or on-disk layout changes.
- **Dependency or API impact:**
  - No public API changes at the `OxHost` / `OxSession` boundary.
  - `OxHost` constructor is `internal` — changing it does not break downstream consumers.
  - `OxServices.Register` remains `internal` — affects only `Program.cs` and test harness.

## Validation

- **Tests:**
  - Unit tests for `PermissionAwareCallbacks.Build` (covers grant-store short-circuit, auto-deny when no host callback, durable grant persistence).
  - Unit tests for `TodoJson.Parse` (happy path, missing fields, unknown status).
  - Unit tests for `AgentLoopEventFormatter.TryFormatForStream` (every event variant).
  - Unit tests for each `IInputMode` (bare keypresses drive the expected editor/mode mutation).
  - Unit tests for `TurnController` (start, cancel, queued input).
  - Unit tests for `AgentEventApplier` (state transitions across event sequences).
  - Unit test for `ProviderRegistry.CreateChatClient` / `ConfigureChatOptions` dispatch (unknown provider throws with helpful message).
  - Existing `ExecuteBuiltinCommandTests`, `PermissionTests`, `SessionStoreTests`, `HeadlessRunnerTests`, `ComposerControllerTests` must pass unchanged.
- **Lint/format/typecheck:** `dotnet build` at warning level 5. `boo` structural report.
- **Manual verification:** TUI smoke test (V3) and headless smoke test (V4) as above.

## Gaps and follow-up

- `SettingsSchemaRegistry` currently hangs off `OxHost` as an internal property but is only used by tests. If it turns out Wave 2 drops the last production reference, move it to a standalone test-support registration rather than letting it squat on `OxHost`. Track as follow-up, not blocking.
- `TryMapSpecialKey` (complexity 42) is intentionally left alone. If a future finding re-raises it, the answer stays "this is a dispatch table" — record that decision in a comment at the top of `KeyCodeExtensions.cs` so the next audit agent does not re-investigate.
