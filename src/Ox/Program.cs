using System.Diagnostics;
using dotenv.net;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Ur;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Hosting;
using Ur.Sessions;
using Ur.Skills;
using Ox;
using Ox.Rendering;

// --- Boot ---
DotEnv.Load(options: new DotEnvOptions(
    probeForEnv: true,
    probeLevelsToSearch: 8));

var builder = Host.CreateApplicationBuilder(args);
builder.Services.AddUr(new UrStartupOptions
{
    WorkspacePath = Environment.CurrentDirectory
});
builder.Services.AddHostedService<TuiService>();

await builder.Build().RunAsync();

/// <summary>
/// The REPL loop as a <see cref="BackgroundService"/>.
///
/// The host's console lifetime wires Ctrl+C to the stopping token via
/// <see cref="IHostApplicationLifetime.StopApplication"/>. Per-turn CTS links to
/// <see cref="BackgroundService.ExecuteAsync"/>'s <c>stoppingToken</c>.
/// Viewport cleanup runs in the <c>finally</c> block and in the <c>ProcessExit</c> handler.
/// </summary>
internal sealed class TuiService(
    UrHost host,
    CommandRegistry commands,
    IHostApplicationLifetime lifetime,
    ILogger<TuiService> logger) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        logger.LogInformation("Application starting");

        // Build the rendering stack before registering signal handlers so
        // cleanup always has a viewport reference to call Stop() on.
        var eventList = new EventList();
        var viewport = new Viewport(eventList);

        // Restore the terminal on both Ctrl+C and normal process exit.
        // We register both because Ctrl+C triggers CancelKeyPress AND ProcessExit
        // on macOS/Unix, while unhandled exceptions trigger only ProcessExit.
        // viewport.Stop() is idempotent, so double-calling is safe.
        Console.CancelKeyPress += (_, e) =>
        {
            e.Cancel = true; // Prevent immediate termination; let the host shut down gracefully.
            viewport.Stop();
            lifetime.StopApplication();
        };
        AppDomain.CurrentDomain.ProcessExit += (_, _) => viewport.Stop();

        // Log any unhandled exception that escapes to the CLR before the process dies.
        // ProcessExit fires after this, which cleans up the viewport. Without this hook
        // the terminal is restored but the crash reason is silently discarded.
        AppDomain.CurrentDomain.UnhandledException += (_, e) =>
        {
            var ex = e.ExceptionObject as Exception
                ?? new InvalidOperationException(e.ExceptionObject.ToString() ?? "Unknown error");
            logger.LogError(ex, "Unhandled exception (process terminating)");
        };

        // --- Configuration check (pre-viewport, plain console I/O) ---
        // AutocompleteEngine is created here so it's available to InputReader
        // for the REPL phase. EnsureReadyAsync uses ReadLine (not viewport-mode),
        // so autocomplete doesn't apply there — it activates in the REPL below.
        var autocomplete = new AutocompleteEngine(commands);
        var inputReader = new InputReader(autocomplete);
        if (!await EnsureReadyAsync(host, inputReader, stoppingToken))
        {
            lifetime.StopApplication();
            return;
        }

        // --- REPL ---
        var router = new EventRouter(eventList);
        var callbacks = PermissionHandler.Build(router, inputReader, viewport);
        var session = host.CreateSession(callbacks);

        viewport.SetSessionId(session.Id);
        viewport.SetModelId(session.ActiveModelId);

        // If resuming a session, show context fill immediately from persisted usage.
        UpdateContextUsage(viewport, host, session);

        viewport.Start();

        try
        {
            while (!stoppingToken.IsCancellationRequested)
            {
                var input = inputReader.ReadLineInViewport(
                    "❯ ", viewport.SetInputPrompt, viewport.SetCompletion, stoppingToken);

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

                // Per-turn CTS linked to stopping token so Ctrl+C also cancels mid-turn.
                // ReSharper disable once AccessToDisposedClosure — monitor awaited before disposal.
                var turnCts = CancellationTokenSource.CreateLinkedTokenSource(stoppingToken);
                var keyMonitor = inputReader.MonitorEscapeKeyAsync(turnCts);

                try
                {
                    try
                    {
                        await foreach (var evt in session.RunTurnAsync(input, turnCts.Token))
                        {
                            router.RouteMainEvent(evt);

                            // Update context fill display when a turn completes with usage data.
                            if (evt is TurnCompleted)
                                UpdateContextUsage(viewport, host, session);

                            // Fatal errors are unrecoverable — log and exit the process.
                            if (evt is not TurnError { IsFatal: true } fatal)
                                continue;

                            logger.LogError("Fatal agent error (exiting): {Message}", fatal.Message);
                            lifetime.StopApplication();
                            return;
                        }
                    }
                    catch (OperationCanceledException) when (!stoppingToken.IsCancellationRequested)
                    {
                        // Escape cancelled this turn; add a visual marker and reset state.
                        // Circle child (not a User root) — belongs to the current turn's tree group.
                        var cancelled = new TextRenderable();
                        cancelled.SetText("[cancelled]");
                        eventList.Add(cancelled, BubbleStyle.Circle, () => Color.BrightBlack);
                        router.ResetTurnState();
                    }
                    catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
                    {
                        // Ctrl+C during a turn — fall through; outer loop exits.
                    }
                    catch (Exception ex)
                    {
                        // Any other exception escaping the turn is unexpected. Log it with
                        // the full stack trace so we can diagnose crashes, then surface a
                        // brief message in the viewport rather than letting the process die.
                        logger.LogError(ex, "Unexpected exception during turn");

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
        }
        finally
        {
            viewport.Stop();
        }
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
                    case ChatBlockingIssue.ProviderNotReady:
                    {
                        var message = host.Configuration.GetProviderBlockingMessage();

                        // If the provider is unknown (typo, old format), prompting for an API key
                        // won't help — the user needs to fix the model ID. Without this check, the
                        // loop would store a key under the unknown provider name and repeat forever.
                        if (!host.Configuration.IsSelectedProviderKnown())
                        {
                            Console.WriteLine(message);
                            Console.Write("Enter a model ID in provider/model format, e.g. openai/gpt-5-nano (or blank to exit): ");
                            var newModel = inputReader.ReadLine(ct)?.Trim();
                            if (string.IsNullOrEmpty(newModel))
                                return false;
                            await host.Configuration.SetSelectedModelAsync(newModel, ct: ct);
                            break;
                        }

                        // Known provider that needs an API key — prompt for it.
                        var providerName = host.Configuration.GetSelectedProviderName()!;
                        Console.Write($"{message} Enter the API key (or blank to exit): ");
                        var key = inputReader.ReadLine(ct)?.Trim();
                        if (string.IsNullOrEmpty(key))
                            return false;
                        await host.Configuration.SetApiKeyAsync(key, providerName, ct);
                        break;
                    }

                    case ChatBlockingIssue.MissingModelSelection:
                        Console.Write("No model selected. Enter a model ID, e.g. openai/gpt-5-nano (or blank to exit): ");
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

    /// <summary>
    /// Computes the context fill display string from the session's last input token
    /// count and the model's context length, then pushes it to the viewport header.
    /// Format: "125,000 / 250,000 - 50%". No-op if usage data is unavailable.
    /// </summary>
    private static void UpdateContextUsage(Viewport viewport, UrHost host, UrSession session)
    {
        if (session.LastInputTokens is not { } inputTokens)
            return;

        var contextLength = host.Configuration
            .GetModel(session.ActiveModelId ?? "")?.ContextLength ?? 0;

        if (contextLength <= 0)
            return;

        var pct = (int)Math.Round(inputTokens / (double)contextLength * 100);
        viewport.SetContextUsage($"{inputTokens:N0} / {contextLength:N0} - {pct}%");
    }
}
