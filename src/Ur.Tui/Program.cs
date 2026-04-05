using dotenv.net;
using Ur;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Permissions;
using Ur.Sessions;

namespace Ur.Tui;

/// <summary>
/// A minimal interactive TUI for Ur. The entire program is a REPL:
///   1. Boot the host (load .env, start UrHost).
///   2. Ensure configuration is ready (API key, model selection).
///   3. Loop: prompt → run turn → render events → repeat.
///
/// Escape cancels a running turn. Ctrl+C exits the process.
/// There are no dependencies beyond the core Ur library and dotenv.net.
/// </summary>
static class Program
{
    // Shared flag that pauses the Escape key monitor while the permission
    // callback is reading from stdin. Without this, the key monitor would
    // race with Console.ReadLine and swallow input characters.
    private static volatile bool _pauseKeyReader;

    static async Task<int> Main(string[] args)
    {
        // --- Boot ---
        // Load .env files by probing upward from cwd (same strategy as the CLI)
        // so a repo-root .env with OPENROUTER_API_KEY is found automatically.
        DotEnv.Load(options: new DotEnvOptions(
            probeForEnv: true,
            probeLevelsToSearch: 8));

        // Top-level CTS wired to Ctrl+C for graceful shutdown. Every per-turn
        // CTS is linked to this so that Ctrl+C cancels both the current turn
        // and the outer REPL loop.
        using var appCts = new CancellationTokenSource();
        Console.CancelKeyPress += (_, e) =>
        {
            e.Cancel = true;  // Prevent immediate process termination
            appCts.Cancel();
        };

        var host = await UrHost.StartAsync(Environment.CurrentDirectory, ct: appCts.Token);

        // --- Configuration check ---
        // Prompt interactively for any missing configuration so the user doesn't
        // have to bail out and run separate CLI commands.
        if (!await EnsureReadyAsync(host, appCts.Token))
            return 1;

        // --- REPL ---
        var callbacks = BuildCallbacks();
        var session = host.CreateSession(callbacks);

        Console.WriteLine($"Session: {session.Id}");
        Console.WriteLine("Type a message to chat. Escape cancels a turn. Ctrl+C exits.\n");

        while (!appCts.Token.IsCancellationRequested)
        {
            Console.Write("> ");
            var input = Console.ReadLine();

            // EOF (Ctrl+D on Unix, Ctrl+Z on Windows) exits the REPL.
            if (input is null)
                break;

            // Empty input — just re-prompt, don't send a blank message.
            if (string.IsNullOrWhiteSpace(input))
                continue;

            // Per-turn CTS linked to the app-level token so Ctrl+C also cancels
            // the turn immediately rather than waiting for the next loop iteration.
            using var turnCts = CancellationTokenSource.CreateLinkedTokenSource(appCts.Token);

            // Start a background task that watches for Escape and cancels the turn.
            var keyMonitor = MonitorEscapeKeyAsync(turnCts);

            try
            {
                await foreach (var evt in session.RunTurnAsync(input, turnCts.Token))
                {
                    RenderEvent(evt);

                    // Fatal errors end the REPL — no point continuing if the
                    // provider is unreachable or the session is corrupted.
                    if (evt is Error { IsFatal: true })
                        return 1;
                }
            }
            catch (OperationCanceledException) when (!appCts.Token.IsCancellationRequested)
            {
                // Escape was pressed — cancel just this turn, not the whole app.
                Console.WriteLine("\n[cancelled]");
            }

            // Ensure the key monitor exits before we loop back to Console.ReadLine,
            // otherwise it could steal keystrokes from the next prompt.
            turnCts.Cancel();
            await keyMonitor;

            Console.WriteLine();
        }

        return 0;
    }

    /// <summary>
    /// Checks <see cref="UrConfiguration.Readiness"/> and prompts the user to
    /// supply any missing values. Loops until everything is configured or the
    /// user bails (empty input).
    /// </summary>
    private static async Task<bool> EnsureReadyAsync(UrHost host, CancellationToken ct)
    {
        while (true)
        {
            var readiness = host.Configuration.Readiness;
            if (readiness.CanRunTurns)
                return true;

            foreach (var issue in readiness.BlockingIssues)
            {
                switch (issue)
                {
                    case ChatBlockingIssue.MissingApiKey:
                        Console.Write("No API key configured. Enter your OpenRouter API key (or blank to exit): ");
                        var key = Console.ReadLine()?.Trim();
                        if (string.IsNullOrEmpty(key))
                            return false;
                        await host.Configuration.SetApiKeyAsync(key, ct);
                        break;

                    case ChatBlockingIssue.MissingModelSelection:
                        Console.Write("No model selected. Enter a model ID (or blank to exit): ");
                        var model = Console.ReadLine()?.Trim();
                        if (string.IsNullOrEmpty(model))
                            return false;
                        await host.Configuration.SetSelectedModelAsync(model, ct: ct);
                        break;
                }
            }
        }
    }

    /// <summary>
    /// Builds <see cref="TurnCallbacks"/> with a permission prompt that reads
    /// from stdin. Pauses the Escape key monitor while waiting for input so
    /// the two don't race over console reads.
    /// </summary>
    private static TurnCallbacks BuildCallbacks()
    {
        return new TurnCallbacks
        {
            RequestPermissionAsync = (req, _) =>
            {
                // Build a hint showing the available scope options beyond simple y/n.
                var scopeHints = req.AllowedScopes.Count > 1
                    ? $" [{string.Join(", ", req.AllowedScopes.Select(s => s.ToString().ToLowerInvariant()))}]"
                    : "";

                _pauseKeyReader = true;
                try
                {
                    Console.Write(
                        $"\nAllow {req.OperationType} on '{req.Target}' by '{req.RequestingExtension}'?"
                        + $" (y/n{scopeHints}): ");

                    var input = Console.ReadLine()?.Trim().ToLowerInvariant();

                    // Parse the user's response: "y"/"yes" grants once, a scope
                    // name grants durably, anything else denies.
                    var candidate = input switch
                    {
                        "y" or "yes" => new PermissionResponse(true, PermissionScope.Once),
                        "session"    => new PermissionResponse(true, PermissionScope.Session),
                        "workspace"  => new PermissionResponse(true, PermissionScope.Workspace),
                        "always"     => new PermissionResponse(true, PermissionScope.Always),
                        _            => new PermissionResponse(false, null),
                    };

                    // If the user chose a scope that this operation doesn't allow,
                    // deny rather than silently granting more than permitted.
                    var response = candidate.Granted && candidate.Scope is not null
                        && !req.AllowedScopes.Contains(candidate.Scope.Value)
                        ? new PermissionResponse(false, null)
                        : candidate;

                    return ValueTask.FromResult(response);
                }
                finally
                {
                    _pauseKeyReader = false;
                }
            }
        };
    }

    /// <summary>
    /// Renders a single <see cref="AgentLoopEvent"/> to the console.
    /// Response text streams inline; tool status and errors get bracketed markers.
    /// </summary>
    private static void RenderEvent(AgentLoopEvent evt)
    {
        switch (evt)
        {
            // Stream text as it arrives — no newline between chunks so the
            // response reads as a single flowing paragraph.
            case ResponseChunk chunk:
                Console.Write(chunk.Text);
                break;

            case ToolCallStarted started:
                Console.WriteLine($"\n[tool: {started.ToolName}]");
                break;

            case ToolCallCompleted completed:
                var result = completed.Result.Length > 200
                    ? completed.Result[..200] + "…"
                    : completed.Result;
                var status = completed.IsError ? "error" : "ok";
                Console.WriteLine($"[tool: {completed.ToolName} → {status}] {result}");
                break;

            // Newline after the streamed response so the next prompt starts clean.
            case TurnCompleted:
                Console.WriteLine();
                break;

            case Error error:
                Console.WriteLine($"\n[error] {error.Message}");
                break;
        }
    }

    /// <summary>
    /// Polls for Escape key presses in the background and cancels
    /// <paramref name="turnCts"/> when detected. The monitor pauses while
    /// <see cref="_pauseKeyReader"/> is true (during permission prompts) to
    /// avoid racing with Console.ReadLine.
    ///
    /// The task is short-lived — created per turn and exits when the turn
    /// completes (token cancelled) or Escape is pressed.
    /// </summary>
    private static Task MonitorEscapeKeyAsync(CancellationTokenSource turnCts)
    {
        return Task.Run(async () =>
        {
            while (!turnCts.Token.IsCancellationRequested)
            {
                // Yield to avoid busy-spinning. 50ms is responsive enough
                // that Escape feels instant but doesn't burn CPU.
                await Task.Delay(50, CancellationToken.None);

                // Don't touch Console.ReadKey while a permission prompt is
                // active — the two would race over stdin.
                if (_pauseKeyReader)
                    continue;

                if (!Console.KeyAvailable)
                    continue;

                var key = Console.ReadKey(intercept: true);
                if (key.Key == ConsoleKey.Escape)
                {
                    turnCts.Cancel();
                    return;
                }
            }
        });
    }
}
