# Replace UrLogger with Microsoft.Extensions.Logging and instrument the codebase

## Goal

Replace the custom `UrLogger` with `Microsoft.Extensions.Logging`, then add logging across the codebase — both error paths (28 unlogged catch blocks) and key lifecycle events (turns, tool invocations, sessions, extensions). The result: when something goes wrong, the log file tells the full story.

## Desired outcome

- Every catch block that swallows or re-throws logs the exception at an appropriate level.
- Key lifecycle events (turn start/end, tool invocations, session operations, extension activation) are logged at Info/Debug.
- All `Console.Error.WriteLine` diagnostic messages route through ILogger instead.
- Log output goes to the same daily rolling file (`~/.ur/logs/ur-{date}.log`) via a custom file sink.
- `UrLogger` is deleted.

## How we got here

Audit found 4 logging calls in the entire codebase, 32 catch blocks (28 with zero logging), and 9 `Console.Error.WriteLine` calls used as ad-hoc logging. The custom `UrLogger` has only INFO and ERROR levels, no structured context, and no way to filter. `Microsoft.Extensions.Logging.Abstractions` is already a transitive dependency via `Microsoft.Extensions.AI`, so the migration cost is low.

## Recommended approach

Thread an `ILoggerFactory` through UrHost (created at startup via `LoggerFactory.Create()`), and pass `ILogger<T>` instances to components that need them. Write a small custom file sink that preserves the current daily-rolling-file behavior. No DI container needed — the factory is created once and distributed manually, matching the existing wiring pattern.

- Why this approach: Matches the existing manual-wiring style. No new DI framework. `Microsoft.Extensions.Logging` gives us levels, categories, filtering, and structured logging for free.
- Key tradeoffs: Components that currently have zero constructor parameters (e.g. `SessionStore`, `ExtensionLoader` static methods) will need an `ILogger` parameter. This is a good thing — it makes the dependency explicit — but it's a mechanical chore across many files.

## Related code

- `src/Ur/Logging/UrLogger.cs` — The custom logger to be replaced
- `src/Ur/UrHost.cs` — Boot sequence; will create `ILoggerFactory` and distribute loggers
- `src/Ur/AgentLoop/AgentLoop.cs` — Turn loop; needs turn-level lifecycle logging
- `src/Ur/AgentLoop/ToolInvoker.cs` — Tool dispatch; needs invocation + error logging
- `src/Ur/AgentLoop/SubagentRunner.cs` — Subagent execution; needs lifecycle logging
- `src/Ur/Sessions/UrSession.cs` — Session turn driver; persistence failure logging
- `src/Ur/Sessions/SessionStore.cs` — JSONL persistence; malformed-line logging
- `src/Ur/Extensions/ExtensionLoader.cs` — Extension discovery/activation; currently uses stderr
- `src/Ur/Extensions/ExtensionCatalog.cs` — Extension state changes; currently silent
- `src/Ur/Extensions/ExtensionOverrideStore.cs` — Override persistence; currently uses stderr
- `src/Ur/Skills/SkillLoader.cs` — Skill parsing; currently uses stderr
- `src/Ur/Tools/BashTool.cs` — Process timeout/cancellation; silent
- `src/Ur/Tools/GlobTool.cs` — Ripgrep timeout; silent
- `src/Ur/Tools/GrepTool.cs` — Ripgrep timeout + unreadable files; silent
- `src/Ur/Tools/ToolArgHelpers.cs` — Ripgrep detection failure; silent
- `src/Ur/Permissions/PermissionGrantStore.cs` — Grant file I/O failures; silent
- `src/Ur/Configuration/Settings.cs` — Settings persistence failure; silent
- `src/Ur/Providers/ModelCatalog.cs` — Cache corruption; silent
- `src/Ur.Tui/Program.cs` — TUI entry point; has UrLogger calls to migrate
- `src/Ur.Cli/Program.cs` — CLI entry point; has zero logging today

## Current state

- `UrLogger` is a static class with `Info()`, `Error()`, and `Exception()` methods. Thread-safe via static lock. Fire-and-forget (never throws).
- 4 call sites: TUI startup (1), TUI unhandled exceptions (2), AgentLoop LLM error (1).
- 9 `Console.Error.WriteLine` calls acting as ad-hoc logging in extension/skill loaders and UrHost.
- 28 catch blocks with no logging whatsoever.
- `Microsoft.Extensions.Logging.Abstractions` is already available as a transitive dependency. Need to add `Microsoft.Extensions.Logging` for `LoggerFactory.Create()`.
- No DI container — objects are manually wired in `UrHost.StartAsync()`.

## Structural considerations

**Hierarchy**: The `ILoggerFactory` is created at the entry point (TUI/CLI Program.cs), passed to `UrHost.StartAsync()`, which distributes `ILogger<T>` instances downward. This respects the existing top-down dependency flow.

**Encapsulation**: Components receive `ILogger<T>` (not `ILoggerFactory`), so they can't create loggers for other components. The exception is `UrHost`, which holds the factory because it constructs other components.

**Modularization**: The file sink is a single self-contained class in `Ur.Logging/`. It replaces `UrLogger` in the same namespace.

**Abstraction**: Tools receive their logger via `ToolContext` (which already carries per-session state). This avoids adding logger parameters to every tool factory signature — just extend the existing context record.

## Refactoring

### 1. Extend ToolContext to carry ILoggerFactory

`ToolContext` currently carries `Workspace` and `SessionId`. Adding `ILoggerFactory` lets tool factories create category-specific loggers without changing every factory signature.

### 2. Convert ExtensionLoader static methods to instance methods (or add ILogger parameter)

`ExtensionLoader` uses static methods that write to `Console.Error`. The simplest fix is adding an `ILogger` parameter to `DiscoverAllAsync` and `ActivateAsync`. If the parameter count gets unwieldy, convert to an instance class.

## Implementation plan

### Phase 1: Infrastructure

- [ ] Add `Microsoft.Extensions.Logging` package to `Ur.csproj`. The abstractions are already transitive; this brings in `LoggerFactory.Create()`.
- [ ] Create `src/Ur/Logging/RollingFileLoggerProvider.cs` — an `ILoggerProvider` that writes to `~/.ur/logs/ur-{date}.log` with the same daily-rolling, fire-and-forget, thread-safe semantics as the current `UrLogger`. This is a thin wrapper: one class implementing `ILoggerProvider`, one implementing `ILogger` that delegates to a shared `AppendRaw` method.
- [ ] Add `ILoggerFactory` parameter to `UrHost.StartAsync()` (both overloads) and store it as a field on `UrHost`. Create it in `Program.cs` (TUI) and `HostRunner.cs` (CLI) via `LoggerFactory.Create(builder => builder.AddProvider(new RollingFileLoggerProvider()))`.
- [ ] Extend `ToolContext` record to include `ILoggerFactory`.
- [ ] Delete `src/Ur/Logging/UrLogger.cs`.

### Phase 2: Migrate existing call sites

- [ ] **TUI Program.cs**: Replace 4 `UrLogger.*` calls with `ILogger<Program>` equivalents. Create the logger from the factory before passing it to `UrHost.StartAsync()`.
- [ ] **AgentLoop.cs**: Add `ILogger<AgentLoop>` constructor parameter. Replace `UrLogger.Exception("LLM streaming error", ex)` with `logger.LogError(ex, "LLM streaming error")`.
- [ ] **UrHost.cs**: Replace `Console.Error.WriteLine` in `RegisterExtensionSchemas` with `ILogger<UrHost>`.
- [ ] **ExtensionLoader.cs**: Add `ILogger` parameter to `DiscoverAllAsync` and `ActivateAsync`. Replace 3 `Console.Error.WriteLine` calls.
- [ ] **ExtensionOverrideStore.cs**: Add `ILogger` parameter to constructor. Replace 4 `Console.Error.WriteLine` calls.
- [ ] **SkillLoader.cs**: Add `ILogger` parameter to `LoadAllAsync`. Replace `Console.Error.WriteLine`.

### Phase 3: Instrument error paths (catch blocks without logging)

Each catch block gets a log call at the appropriate level:

- [ ] **ToolInvoker.cs:85** — `logger.LogError(ex, "Tool {ToolName} failed", call.Name)`. This is the single highest-value logging addition: every tool failure currently vanishes.
- [ ] **UrSession.cs:130-138** — `logger.LogError(ex, "Failed to persist user message for session {SessionId}", Id)`.
- [ ] **UrSession.cs:279-283** — `logger.LogError(ex, "Failed to flush pending messages for session {SessionId}", Id)`.
- [ ] **SessionStore.cs:91-100** — `logger.LogWarning("Skipping malformed message in session {Path}: {Error}", path, ex.Message)` on `JsonException`.
- [ ] **BashTool.cs:74-85** — `logger.LogWarning("Bash process timed out, killing")` on `OperationCanceledException`.
- [ ] **BashTool.cs:99-106** — `logger.LogDebug("Could not retrieve exit code after timeout")` in the bare catch.
- [ ] **GlobTool.cs:85-93** — `logger.LogWarning("Glob search timed out")` on `OperationCanceledException`.
- [ ] **GrepTool.cs:122-130** — `logger.LogWarning("Grep search timed out")` on `OperationCanceledException`.
- [ ] **GrepTool.cs:156-169** — `logger.LogDebug("Skipping unreadable file during grep")` in the bare catch.
- [ ] **ToolArgHelpers.cs:39-57** — `logger.LogDebug("Ripgrep not available: {Reason}", ex.Message)` on detection failure.
- [ ] **PermissionGrantStore.cs:145-154** — `logger.LogWarning("Skipping malformed grant line: {Error}", ex.Message)` on `JsonException`.
- [ ] **PermissionGrantStore.cs:137-161** — `logger.LogWarning("Could not load permission grants from {Path}: {Error}", path, ex.Message)` on `IOException`.
- [ ] **Settings.cs:102-113** — `logger.LogError(ex, "Failed to persist settings")`.
- [ ] **ModelCatalog.cs:63-68** — `logger.LogWarning("Model cache corrupted, will refresh: {Error}", ex.Message)` on `JsonException`.
- [ ] **ReadFileTool.cs, UpdateFileTool.cs** — `logger.LogDebug` on `FileNotFoundException` (these re-throw, so debug level is sufficient).

### Phase 4: Add lifecycle logging

- [ ] **UrHost.StartAsync**: `logger.LogInformation("Ur starting: workspace={Path}", workspacePath)` at entry, `logger.LogInformation("Ur ready: {ExtCount} extensions, {SkillCount} skills", ...)` at exit.
- [ ] **UrSession.RunTurnAsync**: `logger.LogInformation("Turn started: session={Id}, model={Model}", ...)` at entry, `logger.LogInformation("Turn completed: session={Id}", Id)` when `TurnCompleted` is yielded.
- [ ] **ToolInvoker.InvokeOneAsync**: `logger.LogInformation("Invoking tool {ToolName}", call.Name)` before invocation, `logger.LogInformation("Tool {ToolName} completed in {Elapsed}ms", ...)` after (add a Stopwatch).
- [ ] **ToolInvoker.IsPermissionDeniedAsync**: `logger.LogDebug("Permission {Decision} for {Tool} on {Target}", ...)` for grant-store hits and denials.
- [ ] **ExtensionCatalog.CreateAsync**: `logger.LogInformation("Activated extension {Name} ({Tier})", ...)` per extension, `logger.LogWarning("Extension {Name} failed to activate: {Error}", ...)` on failure.
- [ ] **SessionStore**: `logger.LogDebug("Session created: {Id}", id)` on create, `logger.LogDebug("Loaded session {Id}: {Count} messages", ...)` on read.
- [ ] **SubagentRunner**: `logger.LogInformation("Subagent started: {Prompt}", ...)` at entry, `logger.LogInformation("Subagent completed")` at exit.
- [ ] **CLI Program.cs / HostRunner.cs**: Add logger creation parity with TUI. Log `"CLI command: {Command}"` at invocation.

### Phase 5: Update tests

- [ ] Update `HostSessionApiTests`, `SkillSessionTests`, `SubagentRunnerTests` to pass `NullLoggerFactory.Instance` (from `Microsoft.Extensions.Logging.Abstractions`) wherever `ILoggerFactory` is now required. This is the zero-config test path — no log output, no test noise.
- [ ] Verify all tests pass with `dotnet test`.

## Validation

- Tests: All existing tests pass after migration (NullLoggerFactory for test paths).
- Manual verification: Run TUI, execute a turn, check `~/.ur/logs/ur-{date}.log` contains lifecycle events (startup, turn start, tool invocations, turn complete). Trigger a tool error (e.g. read a nonexistent file) and verify it appears in the log.
- Manual verification: Run a CLI command and verify it also produces log output.

## Open questions

- Should log level be configurable via settings.json? Could add a `"logLevel"` setting that maps to `LogLevel` enum. Not critical for the first pass — default to `Information` and revisit if logs are too noisy or too quiet.
