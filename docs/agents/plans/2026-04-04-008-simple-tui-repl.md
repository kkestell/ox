# Simple TUI REPL

## Goal

Build a minimal interactive terminal UI in the existing `Ur.Tui` project. The TUI is a REPL: it boots the host, ensures configuration is ready, then loops — prompting for user input, streaming agent loop events to the terminal, and repeating.

## Desired outcome

Running `ur-tui` (or `dotnet run --project src/Ur.Tui`) drops the user into an interactive session:

1. If the API key or model is missing, the TUI prompts for them inline before entering the REPL.
2. A `> ` prompt accepts user input.
3. All `AgentLoopEvent` types are printed as they arrive (streaming response text, tool call start/complete, errors).
4. Tool permission requests show a `y/n` prompt; the user types a response.
5. Pressing Escape during a turn cancels it (the agent loop stops, control returns to the `> ` prompt).
6. The session persists across turns within a single run.

## How we got here

The user explicitly asked for a "VERY simple" TUI — no framework, no widgets, no fancy layout. The architecture already supports this cleanly: `UrHost` boots the system, `UrSession.RunTurnAsync` yields `IAsyncEnumerable<AgentLoopEvent>`, and `TurnCallbacks` hooks permission prompts. The `ChatCommand` in `Ur.Cli` demonstrates the full integration pattern. The TUI is essentially ChatCommand wrapped in a REPL loop with Escape-to-cancel.

## Related code

- `src/Ur.Tui/Program.cs` — Empty stub, will contain the entire TUI
- `src/Ur.Tui/Ur.Tui.csproj` — Already references `Ur.csproj`, needs `dotenv.net` added
- `src/Ur.Cli/Commands/ChatCommand.cs` — Reference implementation: event rendering, permission callback
- `src/Ur.Cli/HostRunner.cs` — Boot sequence (load .env, start UrHost); will be inlined in TUI since it's trivial
- `src/Ur/Sessions/UrSession.cs` — `RunTurnAsync` is the main integration point
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — The 5 event types to handle
- `src/Ur/Configuration/UrConfiguration.cs` — `Readiness`, `SetApiKeyAsync`, `SetSelectedModelAsync`
- `src/Ur/Configuration/ChatReadiness.cs` — `CanRunTurns`, `BlockingIssues`
- `src/Ur/Permissions/PermissionPolicy.cs` — Allowed scopes per operation type

## Current state

- `Ur.Tui` project exists with an empty `Program.cs` and a project reference to `Ur.csproj`.
- `ChatCommand` shows the complete pattern for event streaming, permission prompts, and readiness checking.
- The `HostRunner` boot sequence (load .env, start UrHost) is a 3-line pattern that can be inlined.
- `UrSession.RunTurnAsync` accepts a `CancellationToken` — cancelling it cleanly stops the turn.

## Structural considerations

The TUI is a thin UI layer. All business logic lives in `Ur.csproj` (host, session, agent loop, permissions, configuration). The TUI's only job is:

1. **Boot** — Load .env, call `UrHost.StartAsync`.
2. **Configure** — Check `Readiness`, prompt for missing values.
3. **REPL** — Read input, call `RunTurnAsync`, render events, handle Escape.

This respects the existing hierarchy: `Ur.Tui → Ur` (never the reverse). No changes to the core library are needed.

**Escape key handling**: The main complexity. During a turn, we need to detect Escape presses while the async enumerable is streaming. Approach:

- Start a background `Task.Run` that polls `Console.KeyAvailable` / `Console.ReadKey(true)` and cancels a `CancellationTokenSource` on Escape.
- Pause the key reader during permission prompts (to avoid racing with `Console.ReadLine`). A simple `volatile bool` flag is sufficient.
- The key reader task is short-lived — created per turn, disposed after.

## Implementation plan

All work is in `src/Ur.Tui/`. The entire TUI fits in `Program.cs`.

- [ ] **Add `dotenv.net` dependency** to `Ur.Tui.csproj` (needed for .env loading, matching the CLI pattern).

- [ ] **Write the boot sequence** in `Program.cs`:
  - Load .env files with `DotEnv.Load` (same options as `HostRunner`).
  - Call `UrHost.StartAsync(Environment.CurrentDirectory)`.
  - Wire up `Console.CancelKeyPress` to a top-level `CancellationTokenSource` for graceful shutdown.

- [ ] **Write the setup / configuration check**:
  - Check `host.Configuration.Readiness`.
  - If `MissingApiKey`: print message, prompt with `Console.ReadLine()`, call `SetApiKeyAsync`.
  - If `MissingModelSelection`: print message, prompt with `Console.ReadLine()`, call `SetSelectedModelAsync`.
  - Re-check readiness after each fix. Loop until `CanRunTurns` is true (or the user provides empty input to bail).

- [ ] **Write the REPL loop**:
  - Create a session: `host.CreateSession(callbacks)`.
  - Loop:
    1. Print `> ` prompt.
    2. Read input via `Console.ReadLine()`.
    3. If null (EOF) or empty, skip (or exit on null).
    4. Create a per-turn `CancellationTokenSource` linked to the app-level token.
    5. Start background Escape key monitor (see below).
    6. `await foreach (var evt in session.RunTurnAsync(input, turnCts.Token))` — render each event.
    7. Catch `OperationCanceledException` — print "[cancelled]" and continue the loop.
    8. Dispose the per-turn CTS.

- [ ] **Write the Escape key monitor**:
  - A helper method that returns a `Task` and accepts the `CancellationTokenSource` to cancel plus a `Func<bool>` pause predicate.
  - Polls `Console.KeyAvailable` every 50ms.
  - On Escape, calls `cts.Cancel()` and exits.
  - On turn completion (token already cancelled or turn CTS disposed), exits.
  - Uses `volatile bool` to pause during permission prompts.

- [ ] **Write the event renderer** (a `switch` on `AgentLoopEvent`):
  - `ResponseChunk` → `Console.Write(chunk.Text)` (no newline, streams naturally).
  - `ToolCallStarted` → `Console.WriteLine($"\n[tool: {started.ToolName}]")`.
  - `ToolCallCompleted` → `Console.WriteLine($"[tool: {completed.ToolName} -> {status}] {result}")` (truncate result to 200 chars).
  - `TurnCompleted` → `Console.WriteLine()` (newline after response).
  - `Error` → `Console.WriteLine($"[error] {error.Message}")`. If fatal, exit the REPL.

- [ ] **Write the permission callback** (in `TurnCallbacks`):
  - Set `pauseKeyReader = true`.
  - Print the prompt: `"Allow {opType} on '{target}'? (y/n [scopes]): "`.
  - Read input via `Console.ReadLine()`.
  - Parse: "y"/"yes" → Once, "session"/"workspace"/"always" → that scope, anything else → deny.
  - Validate scope is in `AllowedScopes`.
  - Set `pauseKeyReader = false`.
  - Return `PermissionResponse`.

- [ ] **Verify it builds**: `dotnet build src/Ur.Tui/Ur.Tui.csproj`.

- [ ] **Manual verification**: Run `dotnet run --project src/Ur.Tui`, confirm:
  - Setup prompt appears if API key / model missing.
  - `> ` prompt works, can type a message.
  - Streaming response text appears.
  - Tool calls show start/complete events.
  - Permission prompt works (y/n).
  - Escape cancels a running turn.
  - Multiple turns work within the same session.

## Validation

- **Build**: `dotnet build src/Ur.Tui/Ur.Tui.csproj` succeeds with no warnings.
- **Manual verification**: Run through the scenarios listed above.
- **No unit tests for V1**: The TUI is a thin rendering layer over well-tested core code. Tests would require mocking Console I/O, which is disproportionate effort for this iteration.

## Gaps and follow-up

- **Session resume**: No way to resume a previous session. Could add a `--session` argument or a session picker later.
- **Model switching**: No way to change models mid-session. Could add `/model` slash command later.
- **Rich rendering**: No color, no markdown rendering, no syntax highlighting. Future iterations can add these.
- **Input editing**: `Console.ReadLine()` provides basic line editing (backspace, arrow keys) but no history. Could add readline-style history later.

## Open questions

- Should "exit" / "quit" typed at the `> ` prompt exit the TUI, or should only Ctrl+C / EOF exit? (Plan assumes Ctrl+C / EOF for exit, since user input is passed directly to the agent and "exit" is a valid message.)
