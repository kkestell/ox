# CLI Test Harness with System.CommandLine

## Goal

Replace the current diagnostic stub in `Ur.Cli/Program.cs` with a complete, idiomatic CLI built on System.CommandLine 2.0.5 that exposes every piece of existing Ur library functionality as subcommands. This CLI serves as both the user-facing interface and a manual test harness for validating core library behavior during development.

## Desired outcome

Running `ur --help` shows a clean, grouped command hierarchy. Every public API method on `UrHost`, `UrConfiguration`, `ExtensionCatalog`, and `UrSession` is reachable through a command. Streaming chat output works end-to-end. The CLI is thin — it wires commands to library calls and formats output, nothing more.

## How we got here

The Ur library has a well-defined public API surface (`UrHost`, `UrSession`, `UrConfiguration`, `ExtensionCatalog`) but the CLI is currently a 14-line diagnostic stub. To validate library functionality during development — especially as we build out remaining features like permissions, new extensions, and configuration — we need a proper CLI that exercises every code path. System.CommandLine was chosen because it's the official .NET CLI framework, and the 2.0.0 stable release (Nov 2025) resolved the years of API churn.

The user is separately developing a TUI framework; `Ur.Tui` is dormant and excluded from this work.

## Approaches considered

### Option 1: Minimal hand-rolled arg parsing

- Summary: Parse `args` manually with a switch/pattern-match dispatch table. No framework dependency.
- Pros: Zero dependencies, total control, trivial to understand.
- Cons: No auto-generated help, no completion support, no validation. Reimplements what System.CommandLine gives for free. Becomes maintenance burden as command count grows.
- Failure modes: Inconsistent option parsing across commands. Help text drifts from actual behavior.

### Option 2: System.CommandLine 2.0.5

- Summary: Use the official .NET CLI framework. Define commands, options, and arguments declaratively. Get help, validation, completions, and consistent parsing for free.
- Pros: Idiomatic .NET. Auto-generated `--help` at every level. Tab completion. Type-safe option parsing. Well-tested. Stable API (finally).
- Cons: One more NuGet dependency. Must learn the 2.0.x API (significantly different from beta4 tutorials floating around).
- Failure modes: Using stale beta4 API patterns. Over-engineering command infrastructure beyond what the library needs.

### Option 3: Spectre.Console.Cli

- Summary: Use Spectre.Console's CLI framework for rich terminal output and command routing.
- Pros: Beautiful output formatting. Good CLI framework. Active community.
- Cons: Heavier dependency. Opinionated about output rendering — may conflict when the TUI framework arrives. Two frameworks doing overlapping things.
- Failure modes: Spectre's rendering assumptions clash with future TUI integration.

## Recommended approach

**Option 2: System.CommandLine 2.0.5.**

Why: It's the official .NET answer, the API is finally stable, and it does exactly what we need — command routing, option parsing, help generation — without pulling in rendering opinions that would conflict with the eventual TUI. The dependency is minimal and maintained by the .NET team.

Key tradeoffs: Must reference `.ignored/` source/docs during implementation to avoid stale API patterns. The 2.0.x API is substantially different from pre-release tutorials (no `SetHandler`, no `InvocationContext`, no `CommandLineBuilder`).

## Related code

- `Ur.Cli/Program.cs` — Current 14-line diagnostic stub. Will be replaced entirely.
- `Ur.Cli/Ur.Cli.csproj` — Needs `System.CommandLine` 2.0.5 package reference.
- `Ur/UrHost.cs` — Boot entry point. Every command starts with `UrHost.StartAsync()`.
- `Ur/UrConfiguration.cs` — Config surface: API key, model selection, readiness, model catalog. Will add public getter/setter methods for arbitrary settings keys.
- `Ur/Configuration/Settings.cs` — Needs the implementation details available to the new public UrConfiguration methods.
- `Ur/UrSession.cs` — Session lifecycle and `RunTurnAsync()` for chat.
- `Ur/ExtensionCatalog.cs` — Extension listing, enable/disable/reset.
- `Ur/ExtensionInfo.cs` — Extension snapshot type for display.
- `Ur/Providers/ModelInfo.cs` — Model metadata for display.
- `Ur/AgentLoop/AgentLoopEvent.cs` — Event types emitted during chat turns.
- `Ur/TurnCallbacks.cs` — Permission callback contract (used in chat command).
- `Ur/ConfigurationScope.cs` — User vs Workspace scope enum.
- `.gitignore` — Needs `.ignored/` entry.

## Current state

- **Existing behavior:** `Ur.Cli` boots a `UrHost`, prints workspace path, model count, and readiness. No commands, no interaction.
- **Existing patterns:** The library API is clean and consistent — async methods return `Task`, streaming uses `IAsyncEnumerable<AgentLoopEvent>`, lists return `IReadOnlyList<T>`. Configuration mutations take `ConfigurationScope` and `CancellationToken`.
- **Constraints:** The CLI must stay thin per ADR-0002. No domain logic in the CLI project — only command wiring, option parsing, and output formatting.

## Structural considerations

The CLI is a leaf consumer of the Ur library. Dependencies flow one direction: `Ur.Cli → Ur`. No changes to the Ur library are needed.

**Hierarchy:** CLI sits above the library. It calls public API methods and formats their results. No new abstractions needed in the library.

**Abstraction:** Each command is a thin function: parse options → call library → format output. The `UrHost` boot sequence is shared across all commands and should be factored into a common helper to avoid repetition.

**Modularization:** Commands are organized into files by noun group (`Models`, `Sessions`, `Config`, `Extensions`, `Chat`). Each file defines its `Command` and wires `SetAction`. This keeps individual files small and avoids a monolithic `Program.cs`.

**Encapsulation:** The CLI only touches public API types. It does not reference `internal` members or implementation namespaces (`Ur.Configuration`, `Ur.Sessions`, `Ur.Providers`, etc.).

## Research

### System.CommandLine 2.0.5 API (stable)

The API changed dramatically from beta4. Key patterns for this plan:

**Command definition:**
```csharp
var cmd = new Command("name", "description");
cmd.Options.Add(someOption);
cmd.Arguments.Add(someArgument);
cmd.Subcommands.Add(childCommand);
```

**Action binding (replaces SetHandler):**
```csharp
cmd.SetAction(async (ParseResult result, CancellationToken ct) =>
{
    var value = result.GetValue(someOption);
    // do work
    return 0; // exit code
});
```

**Options and arguments:**
```csharp
var opt = new Option<string>("--name", "-n") { Description = "...", Required = true };
var arg = new Argument<string>("model-id") { Description = "..." };
// DefaultValueFactory replaces SetDefaultValue
var scopeOpt = new Option<string>("--scope", "-s")
{
    Description = "Configuration scope",
    DefaultValueFactory = _ => "user"
};
```

**Invocation:**
```csharp
var root = new RootCommand("ur — AI agent framework");
root.Subcommands.Add(modelsCmd);
return await root.Parse(args).InvokeAsync();
```

**Removed (do NOT use):** `SetHandler`, `InvocationContext`, `CommandLineBuilder`, `IConsole`, `AddOption/AddCommand/AddArgument` methods, `IsRequired`/`IsHidden` properties, `NamingConventionBinder`, `DragonFruit`, `Hosting`.

## Implementation plan

### Phase 0: Reference material

- [ ] Add `.ignored/` to `.gitignore`
- [ ] Clone `dotnet/command-line-api` at the `v2.0.5` tag (or latest `2.0.x` tag available) into `.ignored/command-line-api/`
- [ ] Clone the System.CommandLine docs from `dotnet/docs` (sparse checkout of `docs/standard/commandline/`) into `.ignored/docs-commandline/`
- [ ] Verify the cloned source builds or at minimum that the example code in `src/System.CommandLine.Tests/` and any `samples/` directory is readable and matches the 2.0.x API patterns documented above

### Phase 1: Library surface additions

- [ ] Add three public methods to `UrConfiguration` to expose settings mutation for generic config commands:
  - `public async Task<JsonElement?> GetSettingAsync(string key, CancellationToken ct = default)` — returns the merged setting value or null
  - `public async Task SetSettingAsync(string key, JsonElement value, ConfigurationScope scope = ConfigurationScope.User, CancellationToken ct = default)` — delegates to `Settings.SetAsync`
  - `public async Task ClearSettingAsync(string key, ConfigurationScope scope = ConfigurationScope.User, CancellationToken ct = default)` — delegates to `Settings.ClearAsync`

### Phase 2: Project setup

- [ ] Add `System.CommandLine` 2.0.5 package reference to `Ur.Cli/Ur.Cli.csproj`
- [ ] Keep `dotenv.net` for `.env` loading
- [ ] Create `Ur.Cli/HostRunner.cs` — shared async helper that boots `UrHost.StartAsync(Environment.CurrentDirectory)` with `.env` loaded. All commands delegate to this so boot logic isn't duplicated.

### Phase 3: Root command and status

- [ ] Rewrite `Ur.Cli/Program.cs` to define a `RootCommand` with description `"ur — AI agent framework"`, wire all subcommand groups, and call `root.Parse(args).InvokeAsync()`
- [ ] Create `Ur.Cli/Commands/StatusCommand.cs` — `ur status`: boots host, prints workspace path, selected model (or "none"), model catalog count, readiness status with any blocking issues, extension summary (N enabled / M total)

### Phase 4: Config commands

- [ ] Create `Ur.Cli/Commands/ConfigCommands.cs` — defines the `config` parent command and all subcommands:
  - `ur config set-api-key <key>` — calls `Configuration.SetApiKeyAsync(key)`
  - `ur config clear-api-key` — calls `Configuration.ClearApiKeyAsync()`
  - `ur config set-model <model-id> [--scope user|workspace]` — calls `Configuration.SetSelectedModelAsync(modelId, scope)`
  - `ur config clear-model [--scope user|workspace]` — calls `Configuration.ClearSelectedModelAsync(scope)`
  - `ur config get <key>` — calls `Configuration.GetSettingAsync(key)`, prints JSON value
  - `ur config set <key> <value> [--scope user|workspace]` — parses value as JSON, calls `Configuration.SetSettingAsync()`
  - `ur config clear <key> [--scope user|workspace]` — calls `Configuration.ClearSettingAsync()`

### Phase 5: Model commands

- [ ] Create `Ur.Cli/Commands/ModelCommands.cs` — defines the `models` parent command:
  - `ur models list [--all]` — lists available models (filtered for text+tool by default, `--all` shows unfiltered catalog). Tabular output: ID, name, context length, cost.
  - `ur models refresh` — calls `Configuration.RefreshModelsAsync()`, reports count
  - `ur models show <model-id>` — calls `Configuration.GetModel(modelId)`, prints full details

### Phase 6: Session commands

- [ ] Create `Ur.Cli/Commands/SessionCommands.cs` — defines the `sessions` parent command:
  - `ur sessions list` — calls `ListSessions()`, tabular output: ID, created-at
  - `ur sessions show <session-id>` — calls `OpenSessionAsync(id)`, prints message history (role, content, truncated for tool results)

### Phase 7: Extension commands

- [ ] Create `Ur.Cli/Commands/ExtensionCommands.cs` — defines the `extensions` parent command:
  - `ur extensions list` — calls `Extensions.List()`, tabular output: ID, name, tier, enabled, active, version, description
  - `ur extensions enable <extension-id>` — calls `Extensions.SetEnabledAsync(id, true)`
  - `ur extensions disable <extension-id>` — calls `Extensions.SetEnabledAsync(id, false)`
  - `ur extensions reset <extension-id>` — calls `Extensions.ResetAsync(id)`

### Phase 8: Chat command

- [ ] Create `Ur.Cli/Commands/ChatCommand.cs` — `ur chat <message> [--session <id>] [--model <id>]`:
  - Creates or opens a session
  - If `--model` is provided, calls `SetSelectedModelAsync` before the turn
  - Calls `session.RunTurnAsync(message)` and streams output:
    - `ResponseChunk` → write text to stdout (no newline between chunks)
    - `ToolCallStarted` → write `[tool: {name}]` to stderr
    - `ToolCallCompleted` → write result summary to stderr
    - `TurnCompleted` → write final newline
    - `Error` → write to stderr
  - Supports `Ctrl+C` cancellation via `CancellationToken`

### Phase 9: Cleanup and verification

- [ ] Verify `dotnet build` succeeds
- [ ] Verify `ur --help`, `ur status`, `ur models list`, `ur sessions list`, `ur extensions list` all produce correct output
- [ ] Verify `ur chat "hello"` streams a response end-to-end (requires API key and model to be configured)
- [ ] Verify each subcommand's `--help` shows correct options, arguments, and descriptions

## Impact assessment

- **Code paths affected:** Only `Ur.Cli/` — no changes to the Ur library.
- **Data or schema impact:** None. The CLI reads/writes through the existing library API.
- **Dependency impact:** Adds `System.CommandLine` 2.0.5 to `Ur.Cli`. May remove `dotenv.net` if env loading is handled differently.

## Validation

- **Build:** `dotnet build` for the solution (excluding dormant projects)
- **Tests:** Existing `Ur.Tests` and `Ur.IntegrationTests` continue to pass (CLI changes don't affect them)
- **Manual verification:**
  - `ur --help` shows all command groups
  - `ur status` reports workspace, model, readiness
  - `ur config set-api-key <key>` + `ur config set-model <id>` + `ur chat "hello"` produces streaming output
  - `ur models list` shows tabular model data
  - `ur extensions list` shows extension state
  - `ur sessions list` + `ur sessions show <id>` shows session history
  - Every subcommand's `--help` is accurate and complete

## Gaps and follow-up

- **Interactive REPL mode:** This plan builds single-shot commands only. A `ur repl` or default interactive mode (multi-turn conversation) is a natural follow-up but is out of scope here.
- **Permission prompts in chat:** `TurnCallbacks.RequestPermissionAsync` exists but is not yet consumed by the runtime. When it is, the chat command will need a stdin-based permission prompt. For now, `turnCallbacks` is left null.
- **Tab completion:** System.CommandLine supports shell completion generation. Worth adding later but not in this pass.
- **Output formatting:** This plan uses simple `Console.WriteLine` / tabular text. A future pass could add `--json` output mode for scriptability.

## Open questions

None.