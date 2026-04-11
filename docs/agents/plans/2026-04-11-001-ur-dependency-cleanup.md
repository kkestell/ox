# Ur dependency graph cleanup

## Goal

Eliminate four architectural smells in the Ur library's internal dependency graph: a namespace cycle between Providers and Configuration, a namespace cycle between the root Ur namespace and Ur.Hosting, a dead-weight interface abstraction (ISubagentRunner), and excessive intimacy between UrSession and UrHost.

## Desired outcome

After this work:

- `Ur.Tools` has no `using Ur.AgentLoop;` (SubagentTool depends only on an interface in its own namespace).
- `Ur.Providers` has no `using Ur.Configuration;` (OllamaProvider depends on `Ur.Settings`, a new lower-level namespace).
- `Ur` (root) has no `using Ur.Hosting;` (UrHost receives a resolved directory instead of calling back to ServiceCollectionExtensions).
- `UrSession` holds individual service references instead of a reference to the UrHost god object.
- All existing tests pass after each refactor step.

## How we got here

Static analysis of the namespace dependency graph surfaced four issues. Each was confirmed by reading the code:

1. **ISubagentRunner** — lives in `Ur.AgentLoop`, used only by `SubagentTool` (`Ur.Tools`). The interface was supposed to break a cycle, but SubagentTool still has `using Ur.AgentLoop;`, so the namespace dependency persists. Single implementation, never mocked outside of one test file.
2. **Providers ↔ Configuration** — `OllamaProvider` (`Ur.Providers`) uses `SettingsWriter` (`Ur.Configuration`), while `UrConfiguration` (`Ur.Configuration`) uses `ProviderRegistry` (`Ur.Providers`). Genuine bi-directional namespace dependency.
3. **UrSession ↔ UrHost intimacy** — UrSession holds a reference to the entire UrHost singleton and reaches through it for 8 distinct capabilities across 22 access sites. Tests must construct a full UrHost to test session behavior.
4. **Ur ↔ Ur.Hosting cycle** — `UrHost.cs:96` calls `ServiceCollectionExtensions.DefaultUserDataDirectory()` (Ur → Ur.Hosting), while ServiceCollectionExtensions constructs UrHost (Ur.Hosting → Ur).

## Related code

### Refactor 1 — ISubagentRunner

- `src/Ur/AgentLoop/ISubagentRunner.cs` — Interface to delete (move contract to Ur.Tools)
- `src/Ur/AgentLoop/SubagentRunner.cs` — Sole implementation; already has `using Ur.Tools;`
- `src/Ur/Tools/SubagentTool.cs` — Consumer; currently has `using Ur.AgentLoop;` solely for this interface
- `src/Ur/Sessions/UrSession.cs:197-200` — Instantiation site; constructs SubagentRunner directly
- `tests/Ur.Tests/SubagentToolTests.cs` — Uses StubRunner/ThrowingRunner mock implementations
- `tests/Ur.Tests/SubagentRunnerTests.cs` — Tests concrete runner

### Refactor 2 — Providers ↔ Configuration

- `src/Ur/Configuration/SettingsWriter.cs` — Core settings I/O class to extract
- `src/Ur/Configuration/SettingsSchemaRegistry.cs` — SettingsWriter dependency; extract together
- `src/Ur/Configuration/SettingsJsonContext.cs` — Source-gen JSON context; extract together
- `src/Ur/Configuration/SettingsValidationException.cs` — Exception type; extract together
- `src/Ur/Configuration/ConfigurationScope.cs` — Enum (User/Workspace); extract together
- `src/Ur/Configuration/UrConfiguration.cs` — Stays; orchestrates SettingsWriter + ProviderRegistry
- `src/Ur/Providers/OllamaProvider.cs` — Consumer of SettingsWriter; will use new namespace
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Wires SettingsWriter; will use new namespace
- `tests/Ur.Tests/SettingsLoaderTests.cs` — 13 tests for SettingsWriter behavior

### Refactor 3 — UrSession thinning

- `src/Ur/UrHost.cs` — God object; CreateSession/OpenSessionAsync will pass individual deps
- `src/Ur/Sessions/UrSession.cs` — Replaces `_host` with individual fields
- `src/Ur/Tools/BuiltinToolFactories.cs` — Static factory list; UrSession can use directly
- `tests/Ur.Tests/HostSessionApiTests.cs` — Integration tests exercising UrHost→UrSession
- `tests/Ur.Tests/TestSupport/TestHostBuilder.cs` — Test infrastructure; verify still works
- `tests/Ur.Tests/Skills/SkillSessionTests.cs` — Session-level slash command tests

### Refactor 4 — Ur ↔ Ur.Hosting

- `src/Ur/UrHost.cs:95-96` — Calls `ServiceCollectionExtensions.DefaultUserDataDirectory()`
- `src/Ur/Hosting/ServiceCollectionExtensions.cs:57,209-212` — Defines and uses `DefaultUserDataDirectory()`

## Current state

- **Dependency direction**: UrHost sits at the top, depends on nearly every Ur.* namespace. UrSession depends on UrHost (and transitively on everything). ServiceCollectionExtensions orchestrates construction.
- **Test infrastructure**: TestHostBuilder creates a full UrHost via `AddUr()`. All integration tests go through this path. No tests construct UrSession directly.
- **Existing patterns**: Constructor injection throughout. No DI for per-turn objects (SubagentRunner, AgentLoop, ToolRegistry) — they're created procedurally in RunTurnAsync.

## Structural considerations

Each refactor targets a specific PHAME violation:

1. **ISubagentRunner** — Modularization: the interface pretends to decouple two namespaces but doesn't. Moving it to Ur.Tools makes the boundary real.
2. **Providers ↔ Configuration** — Hierarchy: a genuine namespace cycle. Extracting the settings I/O layer creates a clean lower-level module that both can depend on.
3. **UrSession intimacy** — Encapsulation: UrSession can reach anything UrHost exposes. Explicit dependencies constrain the contract and make UrSession testable in isolation.
4. **Ur ↔ Ur.Hosting** — Hierarchy: the root namespace shouldn't depend on the hosting namespace. Resolving the path before constructing UrHost eliminates the back-edge.

## Implementation plan

Order: simplest and most isolated first, largest last. The build must pass after each step.

### Step 1 — Break Ur ↔ Ur.Hosting cycle

- [ ] Add `string userDataDirectory` parameter to `UrHost` constructor (after `UrStartupOptions options`).
- [ ] In `UrHost` constructor, replace `options.UserDataDirectory ?? ServiceCollectionExtensions.DefaultUserDataDirectory()` with the new parameter.
- [ ] Remove `using Ur.Hosting;` from `UrHost.cs`.
- [ ] In `ServiceCollectionExtensions.AddUr`, pass the already-resolved `userDataDirectory` local variable as the new constructor argument.
- [ ] Update comments that reference the old fallback pattern.
- [ ] Run `dotnet build src/Ur/` and `dotnet test tests/Ur.Tests/` — verify green.

### Step 2 — Move ISubagentRunner to Ur.Tools

- [ ] Move `src/Ur/AgentLoop/ISubagentRunner.cs` to `src/Ur/Tools/ISubagentRunner.cs`.
- [ ] Change its namespace from `Ur.AgentLoop` to `Ur.Tools`.
- [ ] In `SubagentRunner.cs`: it already has `using Ur.Tools;`, so the interface resolves. Remove any now-unnecessary usings.
- [ ] In `SubagentTool.cs`: remove `using Ur.AgentLoop;`. The interface is now in-namespace.
- [ ] In `UrSession.cs:197`: verify `new AgentLoop.SubagentRunner(...)` still compiles (SubagentRunner's namespace hasn't changed).
- [ ] Update XML doc comments in ISubagentRunner.cs and SubagentTool.cs to reflect the new location.
- [ ] Update test mocks (StubRunner, ThrowingRunner in `SubagentToolTests.cs`) — change `using Ur.AgentLoop;` to `using Ur.Tools;` if they implement ISubagentRunner.
- [ ] Run `dotnet build src/Ur/` and `dotnet test tests/Ur.Tests/` — verify green.

### Step 3 — Extract Ur.Settings from Ur.Configuration

- [ ] Create directory `src/Ur/Settings/`.
- [ ] Move these files from `src/Ur/Configuration/` to `src/Ur/Settings/`, changing namespace to `Ur.Settings`:
  - `SettingsWriter.cs`
  - `SettingsSchemaRegistry.cs`
  - `SettingsJsonContext.cs`
  - `SettingsValidationException.cs`
  - `ConfigurationScope.cs`
- [ ] In each moved file, update the `namespace` declaration from `Ur.Configuration` to `Ur.Settings`.
- [ ] Add `using Ur.Settings;` to files that reference moved types:
  - `src/Ur/Configuration/UrConfiguration.cs`
  - `src/Ur/Providers/OllamaProvider.cs`
  - `src/Ur/Hosting/ServiceCollectionExtensions.cs`
- [ ] In `OllamaProvider.cs`: replace `using Ur.Configuration;` with `using Ur.Settings;`. Verify no other Ur.Configuration types are used. **Result: Ur.Providers no longer depends on Ur.Configuration.**
- [ ] In `UrConfiguration.cs`: add `using Ur.Settings;` alongside existing `using Ur.Providers;`. Verify all references resolve.
- [ ] In `ServiceCollectionExtensions.cs`: add `using Ur.Settings;`. Verify SettingsWriter, SettingsSchemaRegistry, ConfigurationScope references resolve.
- [ ] Update `tests/Ur.Tests/SettingsLoaderTests.cs`: change `using Ur.Configuration;` to `using Ur.Settings;` (or add both if UrConfiguration types are also referenced).
- [ ] Verify internal visibility: SettingsWriter and friends are `internal`. Since they stay in the same assembly (Ur), `internal` access is preserved.
- [ ] Run `dotnet build src/Ur/` and `dotnet test tests/Ur.Tests/` — verify green.

### Step 4 — Replace UrHost reference in UrSession with explicit dependencies

This is the largest step. The goal is to replace `private readonly UrHost _host;` with individual fields for each service UrSession actually uses.

- [ ] Add new constructor parameters to `UrSession`:
  - `UrConfiguration configuration`
  - `SkillRegistry skills`
  - `BuiltInCommandRegistry builtInCommands`
  - `Workspace workspace`
  - `ILoggerFactory loggerFactory`
  - `SessionStore sessions` (replaces `_host.AppendMessageAsync`)
  - `Func<string, IChatClient> chatClientFactory` (replaces `_host.CreateChatClient`)
  - `ToolRegistry? additionalTools = null` (replaces `_host._additionalTools`)
- [ ] Remove the `UrHost host` parameter from the constructor.
- [ ] Replace `private readonly UrHost _host;` with individual private readonly fields for each new parameter.
- [ ] Move `BuildSessionToolRegistry` logic from `UrHost` into `UrSession` (private method). UrSession already has Skills and session ID. `BuiltinToolFactories.All` is static. The additionalTools parameter handles test injection.
- [ ] Update every `_host.X` access in UrSession (22 sites):
  - `_host.Configuration` → `_configuration`
  - `_host.Skills` → `_skills`
  - `_host.BuiltInCommands` → `_builtInCommands`
  - `_host.Workspace` → `_workspace`
  - `_host.LoggerFactory` → `_loggerFactory`
  - `_host.AppendMessageAsync(session, msg, ct)` → `_sessions.AppendAsync(session, msg, ct)`
  - `_host.CreateChatClient(modelId)` → `_chatClientFactory(modelId)`
  - `_host.BuildSessionToolRegistry(id, todos)` → `BuildSessionToolRegistry(id, todos)` (now local)
- [ ] Update `UrHost.CreateSession()` to pass individual dependencies instead of `this`.
- [ ] Update `UrHost.OpenSessionAsync()` similarly.
- [ ] Remove `BuildSessionToolRegistry` from UrHost (or keep it as a convenience that delegates to the same static factory loop, if tests use it independently). Check if tests call `host.BuildSessionToolRegistry` — if so, keep a forwarding method or update the tests.
- [ ] Remove the `internal` property accessors on UrHost that existed solely for UrSession: `Workspace`, `Skills`, `BuiltInCommands`, `LoggerFactory`. Keep `SettingsSchemas` if tests use it directly.
- [ ] Remove `using Ur.Sessions;` from `UrHost.cs` if UrSession is no longer referenced (unlikely — UrHost.CreateSession still returns UrSession).
- [ ] Run `dotnet build src/Ur/` and `dotnet test tests/Ur.Tests/` — verify green.
- [ ] Run `dotnet test tests/Ur.IntegrationTests/` — verify green.

## Validation

- **After each step**: `dotnet build src/Ur/` must succeed with no warnings related to the refactor. `dotnet test tests/Ur.Tests/` and `dotnet test tests/Ur.IntegrationTests/` must pass.
- **Namespace dependency check**: After all steps, grep for the eliminated dependencies to confirm they're gone:
  - `grep -r "using Ur.Hosting;" src/Ur/UrHost.cs` → no matches
  - `grep -r "using Ur.AgentLoop;" src/Ur/Tools/` → no matches
  - `grep -r "using Ur.Configuration;" src/Ur/Providers/` → no matches
  - `grep "private readonly UrHost" src/Ur/Sessions/UrSession.cs` → no matches
- **Final full build**: `dotnet build` and `dotnet test` from the repo root.

## Open questions

- Should `BuildSessionToolRegistry` move entirely into UrSession, or should UrHost keep a version for test convenience? Tests currently call `host.BuildSessionToolRegistry(sessionId)` to verify tool registration. If it moves, those tests need updating. Recommend moving it and updating tests — keeps the contract in one place.

Move it

- When thinning UrSession, should the constructor take the raw services (16 params) or a `SessionServices` record that groups them? Raw params are more explicit but verbose. A record is tidier but is arguably just UrHost-lite. Recommend raw params — greenfield code, clarity over brevity, and the constructor is only called from two sites in UrHost.

Raw params