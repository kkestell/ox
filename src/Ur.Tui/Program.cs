using System.Diagnostics;
using dotenv.net;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Logging;
using Ur.Tui.Rendering;

namespace Ur.Tui;

/// <summary>
/// A full-screen TUI for Ur built around a retained-mode rendering model.
///
/// Architecture (bottom to top):
///   Terminal   — raw ANSI escape operations (cursor, alternate buffer, etc.)
///   Viewport   — display engine; owns EventList, redraws at ~30 fps when dirty
///   Renderables — live objects (TextRenderable, ToolRenderable, etc.) whose
///                  content can change between redraws
///   EventRouter — maps AgentLoopEvents to renderables; encapsulates routing state
///   Program     — REPL loop, input reading, signal handlers, orchestration
///
/// The main agent loop runs in the "await foreach" body. Subagent events arrive
/// via TurnCallbacks.SubagentEventEmitted and are routed through the same router.
/// </summary>
internal static class Program
{
    private static async Task<int> Main(string[] _)
    {
        UrLogger.Info("Application starting");

        // --- Boot ---
        DotEnv.Load(options: new DotEnvOptions(
            probeForEnv: true,
            probeLevelsToSearch: 8));

        // App-level CTS wired to Ctrl+C. Per-turn CTSs link to this so that
        // Ctrl+C cancels both the running turn and the outer REPL loop.
        var appCts = new CancellationTokenSource();

        // Build the rendering stack before registering signal handlers so
        // cleanup always has a viewport reference to call Stop() on.
        var eventList = new EventList();
        var viewport   = new Viewport(eventList);

        // Restore the terminal on both Ctrl+C and normal process exit.
        // We register both because Ctrl+C triggers CancelKeyPress AND ProcessExit
        // on macOS/Unix, while unhandled exceptions trigger only ProcessExit.
        // viewport.Stop() is idempotent, so double-calling is safe.
        Console.CancelKeyPress += (_, e) =>
        {
            e.Cancel = true;   // Prevent immediate termination; let the REPL loop exit.
            viewport.Stop();
            appCts.Cancel();
        };
        AppDomain.CurrentDomain.ProcessExit += (_, _) => viewport.Stop();

        // Log any unhandled exception that escapes to the CLR before the process dies.
        // ProcessExit fires after this, which cleans up the viewport. Without this hook
        // the terminal is restored but the crash reason is silently discarded.
        AppDomain.CurrentDomain.UnhandledException += (_, e) =>
        {
            var ex = e.ExceptionObject as Exception
                ?? new Exception(e.ExceptionObject.ToString() ?? "Unknown error");
            UrLogger.Exception("Unhandled exception (process terminating)", ex);
        };

        var host = await UrHost.StartAsync(Environment.CurrentDirectory, ct: appCts.Token);

        // --- Configuration check (pre-viewport, plain console I/O) ---
        var inputReader = new InputReader();
        if (!await EnsureReadyAsync(host, inputReader, appCts.Token))
            return 1;

        // --- REPL ---
        var router    = new EventRouter(eventList);
        var callbacks = PermissionHandler.Build(router, inputReader, viewport);
        var session   = host.CreateSession(callbacks);

        viewport.SetSessionId(session.Id);
        viewport.SetModelId(session.ActiveModelId);
        viewport.Start();

        while (!appCts.Token.IsCancellationRequested)
        {
            var input = inputReader.ReadLineInViewport("❯ ", viewport.SetInputPrompt, appCts.Token);

            // null = EOF (Ctrl+D) or cancellation.
            if (input is null)
                break;

            if (string.IsNullOrWhiteSpace(input))
                continue;

            // Show the user's message in the conversation with white text so it
            // stands out clearly against the black bubble background.
            var userMsg = new TextRenderable(foreground: Color.White);
            userMsg.SetText(input);
            eventList.Add(userMsg);

            // Switch input row to a running indicator and start the throbber.
            viewport.SetInputPrompt("");
            viewport.SetTurnRunning(true);

            // Per-turn CTS linked to app token so Ctrl+C also cancels mid-turn.
            // ReSharper disable once AccessToDisposedClosure — monitor awaited before disposal.
            var turnCts = CancellationTokenSource.CreateLinkedTokenSource(appCts.Token);
            var keyMonitor = inputReader.MonitorEscapeKeyAsync(turnCts);

            try
            {
                try
                {
                    await foreach (var evt in session.RunTurnAsync(input, turnCts.Token))
                    {
                        router.RouteMainEvent(evt);

                        // Fatal errors are unrecoverable — log and exit the process.
                        if (evt is not Error { IsFatal: true } fatal)
                            continue;

                        UrLogger.Error($"Fatal agent error (exiting): {fatal.Message}");
                        return 1;
                    }
                }
                catch (OperationCanceledException) when (!appCts.Token.IsCancellationRequested)
                {
                    // Escape cancelled this turn; add a visual marker and reset state.
                    // Circle child (not a User root) — belongs to the current turn's tree group.
                    var cancelled = new TextRenderable();
                    cancelled.SetText("[cancelled]");
                    eventList.Add(cancelled, BubbleStyle.Circle, () => Color.BrightBlack);
                    router.ResetTurnState();
                }
                catch (OperationCanceledException) when (appCts.Token.IsCancellationRequested)
                {
                    // Ctrl+C during a turn — fall through; outer loop exits.
                }
                catch (Exception ex)
                {
                    // Any other exception escaping the turn is unexpected. Log it with
                    // the full stack trace so we can diagnose crashes, then surface a
                    // brief message in the viewport rather than letting the process die.
                    UrLogger.Exception("Unexpected exception during turn", ex);

                    var crashText = new TextRenderable(foreground: Color.Red);
                    crashText.SetText($"[error] {ex.Message} — see ~/.ur/logs/ for details");
                    eventList.Add(crashText, BubbleStyle.Circle, () => Color.Red);
                    router.ResetTurnState();
                }

                await turnCts.CancelAsync();
                await keyMonitor;
            }
            finally
            {
                turnCts.Dispose();
            }

            viewport.SetTurnRunning(false);
            viewport.SetInputPrompt("❯ ");
        }

        viewport.Stop();
        return 0;
    }

    /// <summary>
    /// Checks <see cref="UrConfiguration.Readiness"/> and prompts the user to
    /// supply any missing values. Runs before the viewport starts, so it uses
    /// direct console I/O rather than the viewport's input area.
    /// </summary>
    private static async Task<bool> EnsureReadyAsync(UrHost host, InputReader inputReader, CancellationToken ct)
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
                        var key = inputReader.ReadLine(ct)?.Trim();
                        if (string.IsNullOrEmpty(key))
                            return false;
                        await host.Configuration.SetApiKeyAsync(key, ct);
                        break;

                    case ChatBlockingIssue.MissingModelSelection:
                        Console.Write("No model selected. Enter a model ID (or blank to exit): ");
                        var model = inputReader.ReadLine(ct)?.Trim();
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

}
