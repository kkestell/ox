using System.Diagnostics;
using System.Text;
using dotenv.net;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Permissions;

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
internal static class Program
{
    // ANSI escape sequences for tool-status lines. Tool activity is intentionally
    // rendered in dark gray so it visually recedes — the assistant's response is
    // the primary signal, and the bracket lines are housekeeping noise.
    private const string DarkGray = "\e[90m";
    private const string Reset    = "\e[0m";

    // Shared flag that pauses the Escape key monitor while the permission
    // callback is reading from stdin. Without this, the key monitor would
    // race with Console.ReadLine and swallow input characters.
    private static volatile bool _pauseKeyReader;

    private static async Task<int> Main(string[] _)
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
        var appCts = new CancellationTokenSource();
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
        Console.WriteLine("Type a message to chat. Escape cancels a turn. Ctrl+C exits.");

        while (!appCts.Token.IsCancellationRequested)
        {
            Console.Write("> ");
            var input = CancellableReadLine(appCts.Token);

            // EOF (Ctrl+D on Unix, Ctrl+Z on Windows) exits the REPL.
            if (input is null)
                break;

            // Empty input — just re-prompt, don't send a blank message.
            if (string.IsNullOrWhiteSpace(input))
                continue;

            // Per-turn CTS linked to the app-level token so Ctrl+C also cancels
            // the turn immediately rather than waiting for the next loop iteration.
            // ReSharper disable once AccessToDisposedClosure — monitor is awaited before disposal
            var turnCts = CancellationTokenSource.CreateLinkedTokenSource(appCts.Token);

            // Start a background task that watches for Escape and cancels the turn.
            var keyMonitor = MonitorEscapeKeyAsync(turnCts);

            try
            {
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
                catch (OperationCanceledException) when (appCts.Token.IsCancellationRequested)
                {
                    // Ctrl+C during a turn — print a newline so the cursor
                    // is clean, then fall through to cancel the turn CTS and
                    // await the key monitor before the while-loop exits.
                    Console.WriteLine();
                }

                // Ensure the key monitor exits before we loop back to Console.ReadLine,
                // otherwise it could steal keystrokes from the next prompt.
                await turnCts.CancelAsync();
                await keyMonitor;
            }
            finally
            {
                turnCts.Dispose();
            }
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
                        var key = CancellableReadLine(ct)?.Trim();
                        if (string.IsNullOrEmpty(key))
                            return false;
                        await host.Configuration.SetApiKeyAsync(key, ct);
                        break;

                    case ChatBlockingIssue.MissingModelSelection:
                        Console.Write("No model selected. Enter a model ID (or blank to exit): ");
                        var model = CancellableReadLine(ct)?.Trim();
                        if (string.IsNullOrEmpty(model))
                            return false;
                        await host.Configuration.SetSelectedModelAsync(model, ct: ct);
                        break;

                    default:
                        throw new UnreachableException($"Unexpected {nameof(ChatBlockingIssue)}: {issue}");
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
            // Relay sub-agent events straight into RenderEvent so the user sees
            // what the sub-agent is doing while it runs.
            SubagentEventEmitted = evt =>
            {
                RenderEvent(evt);
                return ValueTask.CompletedTask;
            },

            RequestPermissionAsync = (req, ct) =>
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

                    var input = CancellableReadLine(ct)?.Trim().ToLowerInvariant();

                    // Parse the user's response: "y"/"yes" grants once, a scope
                    // name grants durably, anything else denies.
                    var candidate = input switch
                    {
                        "y" or "yes" => new PermissionResponse(true, PermissionScope.Once),
                        "session"    => new PermissionResponse(true, PermissionScope.Session),
                        "workspace"  => new PermissionResponse(true, PermissionScope.Workspace),
                        "always"     => new PermissionResponse(true, PermissionScope.Always),
                        _            => new PermissionResponse(false, null)
                    };

                    // If the user chose a scope that this operation doesn't allow,
                    // deny rather than silently granting more than permitted.
                    var response = candidate is { Granted: true, Scope: not null }
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
    /// SubagentEvent envelopes are unwrapped via a loop rather than recursion so the
    /// call stack stays flat regardless of nesting depth.
    /// </summary>
    private static void RenderEvent(AgentLoopEvent evt)
    {
        // Unwrap SubagentEvent envelopes first, emitting the >>>> prefix for each layer.
        // In practice the inner event is always a base type (never another SubagentEvent),
        // but the loop guards against unbounded stack growth if that ever changes.
        while (evt is SubagentEvent subagentEvt)
        {
            Console.Write(">>>> ");
            evt = subagentEvt.Inner;
        }

        switch (evt)
        {
            // Stream text as it arrives — no newline between chunks so the
            // response reads as a single flowing paragraph.
            case ResponseChunk chunk:
                Console.Write(chunk.Text);
                break;

            // Tool status lines are rendered in dark gray so they recede visually;
            // the assistant's response text is the primary content. No leading blank
            // line — the previous write (prompt echo or prior status line) already
            // ends with a newline.
            case ToolCallStarted started:
                Console.WriteLine($"{DarkGray}{started.FormatCall()}{Reset}");
                break;

            case ToolCallCompleted completed:
                var status = completed.IsError ? "error" : "ok";
                Console.WriteLine($"{DarkGray}{completed.ToolName} \u2192 {status}{Reset}");
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
    /// Reads a line from stdin, returning <c>null</c> if
    /// <paramref name="ct"/> is cancelled or EOF is received (Ctrl+D).
    /// Unlike <see cref="Console.ReadLine()"/>, this method is responsive to
    /// cancellation because it polls <see cref="Console.KeyAvailable"/> instead
    /// of issuing a single blocking read.
    /// </summary>
    private static string? CancellableReadLine(CancellationToken ct)
    {
        var buffer = new StringBuilder();
        while (!ct.IsCancellationRequested)
        {
            if (!Console.KeyAvailable)
            {
                Thread.Sleep(50);
                continue;
            }

            var key = Console.ReadKey(intercept: true);

            if (key.Key == ConsoleKey.Enter)
            {
                Console.WriteLine();
                return buffer.ToString();
            }

            if (key.Key == ConsoleKey.Backspace)
            {
                if (buffer.Length > 0)
                {
                    buffer.Remove(buffer.Length - 1, 1);
                    Console.Write("\b \b");
                }
                continue;
            }

            // Ctrl+D on an empty buffer signals EOF (matching standard
            // Unix terminal behavior).
            if (key.Key == ConsoleKey.D
                && key.Modifiers.HasFlag(ConsoleModifiers.Control)
                && buffer.Length == 0)
            {
                return null;
            }

            // Skip other control characters — Ctrl+C is handled by the
            // CancelKeyPress event, and filtering keeps noise out of
            // the buffer.
            if (char.IsControl(key.KeyChar))
                continue;

            buffer.Append(key.KeyChar);
            Console.Write(key.KeyChar);
        }

        // Cancellation — print a newline so the cursor doesn't sit on
        // the prompt line.
        Console.WriteLine();
        return null;
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
                if (key.Key != ConsoleKey.Escape)
                    continue;

                await turnCts.CancelAsync();
                return;
            }
        });
    }
}
