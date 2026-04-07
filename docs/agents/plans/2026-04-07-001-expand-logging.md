# Replace UrLogger with Microsoft.Extensions.Logging and instrument the codebase

## Goal

Replace the custom `UrLogger` with `Microsoft.Extensions.Logging`, then add logging across the codebase — both error paths (catch blocks without logging) and key lifecycle events (turns, tool invocations, sessions, extensions). The result: when something goes wrong, the log file tells the full story.

## Desired outcome

- Every catch block that swallows or re-throws logs the exception at an appropriate level.
- Key lifecycle events (turn start/end, tool invocations, session operations, extension activation) are logged at Info/Debug.
- All `Console.Error.WriteLine` diagnostic messages route through ILogger instead.
- Log output goes to the same daily rolling file (`~/.ur/logs/ur-{date}.log`) via a custom file sink.
- `UrLogger` is deleted.

## How we got here

Audit found 4 logging calls in the entire codebase, 32 catch blocks (28 with zero logging), and 9 `Console.Error.WriteLine` calls used as ad-hoc logging. The custom `UrLogger` had only INFO and ERROR levels, no structured context, and no way to filter. `Microsoft.Extensions.Logging.Abstractions` was already a transitive dependency via `Microsoft.Extensions.AI`, so the migration cost was low.

## Current state

Phase 1 (infrastructure) is complete. The DI-hosted architecture was adopted in a separate effort (see `2026-04-07-002-hosting-dependency-injection.md`), so the approach is now DI-based rather than the manual wiring originally proposed.

What's done:

- `UrLogger` has been deleted.
- `Microsoft.Extensions.Logging` is integrated via the Generic Host (`Host.CreateApplicationBuilder`) in both TUI and CLI entry points.
- `UrFileLoggerProvider` / `UrFileLogger` implement `ILoggerProvider` / `ILogger`, writing to `~/.ur/logs/ur-{date}.log` with daily rolling, thread-safe, fire-and-forget semantics.
- The provider is registered in DI via `services.AddLogging(builder => builder.AddProvider(new UrFileLoggerProvider()))` in `ServiceCollectionExtensions.AddUr()`.
- `UrHost` receives `ILoggerFactory` from DI and distributes loggers to procedurally-constructed components (`AgentLoop`, `SubagentRunner`).
- Tests use `NullLoggerFactory.Instance` (e.g. `SubagentRunnerTests`).

What has logging today (5 call sites):

- `TuiService` (`Ur.Tui/Program.cs`): application starting, unhandled exceptions, fatal agent errors, unexpected turn exceptions (4 calls).
- `AgentLoop` (`AgentLoop.cs`): LLM streaming error (1 call).

What remains:

- 20+ catch blocks across the codebase still have no logging.
- 10+ `Console.Error.WriteLine` calls in `Ur/` remain (extension/skill loaders, settings schema registration, override store).
- CLI `Console.Error.WriteLine` calls in `Ur.Cli/Commands/` are intentional user-facing stderr output, not diagnostic logging — these should stay as-is.
- No lifecycle logging yet (turn start/end, tool invocations, session operations, extension activation).

## Related code

- `src/Ur/Logging/UrFileLoggerProvider.cs` — Custom `ILoggerProvider` (daily rolling file sink)
- `src/Ur/Logging/UrFileLogger.cs` — Custom `ILogger` implementation
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — DI registration including logging provider
- `src/Ur/UrHost.cs` — Holds `ILoggerFactory`, distributes loggers to procedural components
- `src/Ur/AgentLoop/AgentLoop.cs` — Turn loop; has `ILogger<AgentLoop>`, one call site
- `src/Ur/AgentLoop/SubagentRunner.cs` — Receives `ILoggerFactory`, creates loggers for sub-agent AgentLoops
- `src/Ur.Tui/Program.cs` — TUI entry point; `TuiService` has `ILogger<TuiService>` with 4 call sites

### Files needing `Console.Error.WriteLine` → ILogger migration

- `src/Ur/Extensions/ExtensionLoader.cs` — 3 calls (extension discovery errors)
- `src/Ur/Extensions/ExtensionOverrideStore.cs` — 4 calls (override persistence errors)
- `src/Ur/Skills/SkillLoader.cs` — 1 call (skill parse error)
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — 1 call (schema registration conflict)

### Files with unlogged catch blocks

- `src/Ur/AgentLoop/ToolInvoker.cs` — tool failure
- `src/Ur/Sessions/UrSession.cs` — persistence failures (2 catches)
- `src/Ur/Sessions/SessionStore.cs` — malformed session data
- `src/Ur/Tools/BashTool.cs` — process timeout, exit code retrieval (2 catches)
- `src/Ur/Tools/GlobTool.cs` — search timeout
- `src/Ur/Tools/GrepTool.cs` — search timeout, unreadable files (2 catches)
- `src/Ur/Tools/ToolArgHelpers.cs` — ripgrep detection failure
- `src/Ur/Tools/ReadFileTool.cs` — file not found
- `src/Ur/Tools/UpdateFileTool.cs` — file not found
- `src/Ur/Permissions/PermissionGrantStore.cs` — malformed grants, I/O failure (2 catches)
- `src/Ur/Providers/ModelCatalog.cs` — cache corruption
- `src/Ur/Configuration/SettingsWriter.cs` — settings persistence (2 catches)
- `src/Ur/Configuration/UrSettingsConfigurationProvider.cs` — config load (2 catches)

## Structural considerations

**Hierarchy**: `ILoggerFactory` is created by the Generic Host and injected into `UrHost` via DI. `UrHost` distributes `ILogger<T>` instances downward to procedurally-constructed components. This respects the existing top-down dependency flow.

**Encapsulation**: Components receive `ILogger<T>` (not `ILoggerFactory`), so they can't create loggers for other components. The exception is `UrHost` (which holds the factory because it constructs other components) and `SubagentRunner` (which needs to create loggers for dynamically-spawned AgentLoops).

**Modularization**: The file sink is two self-contained classes in `Ur/Logging/`. They replace the deleted `UrLogger` in the same namespace.

**DI vs procedural**: Components registered in DI (`ExtensionLoader`, `SkillLoader`, etc.) can receive `ILogger<T>` via constructor injection. Procedural components (`AgentLoop`, tool factories) receive loggers from `UrHost` at construction time.

## Implementation plan

### Phase 1: Infrastructure ✅

- [x] Add `Microsoft.Extensions.Logging` package.
- [x] Create `UrFileLoggerProvider` and `UrFileLogger` (daily rolling file sink).
- [x] Register logging provider in DI via `AddLogging()`.
- [x] `UrHost` receives `ILoggerFactory` from DI.
- [x] Delete `UrLogger.cs`.
- [x] Tests use `NullLoggerFactory.Instance`.

### Phase 2: Migrate remaining Console.Error.WriteLine calls ✅

These are diagnostic messages masquerading as stderr output. They should go through `ILogger` so they appear in the log file and respect log levels.

- [x] **ExtensionLoader.cs**: Add `ILogger` parameter (or inject via DI). Replace 3 `Console.Error.WriteLine` calls with `LogWarning`.
- [x] **ExtensionOverrideStore.cs**: Add `ILogger` parameter (or inject via DI). Replace 4 `Console.Error.WriteLine` calls with `LogWarning`.
- [x] **SkillLoader.cs**: Add `ILogger` parameter (or inject via DI). Replace 1 `Console.Error.WriteLine` call with `LogWarning`.
- [x] **ServiceCollectionExtensions.cs:RegisterExtensionSchemas**: Accept `ILogger` parameter. Replace 1 `Console.Error.WriteLine` call with `LogWarning`.

### Phase 3: Instrument error paths (catch blocks without logging) ✅

Each catch block gets a log call at the appropriate level:

- [x] **ToolInvoker.cs** — `LogError(ex, "Tool {ToolName} failed", call.Name)`. Highest-value addition: tool failures currently vanish.
- [x] **UrSession.cs** — `LogError` for persistence failures (2 catches).
- [x] **SessionStore.cs** — `LogWarning` for malformed session data.
- [x] **BashTool.cs** — `LogWarning` for process timeout; `LogDebug` for exit code retrieval (2 catches).
- [x] **GlobTool.cs** — `LogWarning` for search timeout.
- [x] **GrepTool.cs** — `LogWarning` for search timeout; `LogDebug` for unreadable files (2 catches).
- [x] **ToolArgHelpers.cs** — `LogDebug` for ripgrep detection failure.
- [x] **ReadFileTool.cs** — `LogDebug` for file not found (re-throws).
- [x] **UpdateFileTool.cs** — `LogDebug` for file not found (re-throws).
- [x] **PermissionGrantStore.cs** — `LogWarning` for malformed grants and I/O failures (2 catches).
- [x] **ModelCatalog.cs** — `LogWarning` for cache corruption.
- [x] **SettingsWriter.cs** — `LogError` for settings persistence failures (2 catches).
- [x] **UrSettingsConfigurationProvider.cs** — `LogWarning` for config load failures (2 catches).

### Phase 4: Add lifecycle logging ✅

- [x] **UrHost**: Log startup summary (workspace path, extension/skill counts).
- [x] **UrSession.RunTurnAsync**: Log turn start (session ID, model) and turn completion.
- [x] **ToolInvoker**: Log tool invocation start and completion with elapsed time.
- [x] **ToolInvoker.IsPermissionDeniedAsync**: `LogDebug` for permission decisions.
- [x] **ExtensionCatalog**: Log extension activation and failures.
- [x] **SessionStore**: `LogDebug` for session create and load.
- [x] **SubagentRunner**: Log subagent start and completion.
- [x] **CLI HostRunner.cs**: Log CLI command invocation.

## Validation

- Tests: All existing tests pass after migration (`NullLoggerFactory` for test paths).
- Manual verification: Run TUI, execute a turn, check `~/.ur/logs/ur-{date}.log` contains lifecycle events (startup, turn start, tool invocations, turn complete). Trigger a tool error (e.g. read a nonexistent file) and verify it appears in the log.
- Manual verification: Run a CLI command and verify it also produces log output.

## Open questions

- Should log level be configurable via settings.json? Could add a `"logLevel"` setting that maps to `LogLevel` enum. Not critical for the first pass — default to `Debug` and revisit if logs are too noisy.

We'll add that later.