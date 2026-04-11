using System.Threading.Channels;
using dotenv.net;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Terminal.Gui.App;
using Terminal.Gui.Drivers;
using Terminal.Gui.Input;
using Ur;
using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Hosting;
using Ur.Sessions;
using Ur.Skills;
using Ox;
using Ox.Views;

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
///
/// Two-phase lifecycle:
///   1. Configuration phase: plain Console I/O for API key/model setup (no Terminal.Gui).
///   2. TUI phase: Terminal.Gui runs the full UI with conversation and input.
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

        // Log any unhandled exception that escapes to the CLR before the process dies.
        AppDomain.CurrentDomain.UnhandledException += (_, e) =>
        {
            var ex = e.ExceptionObject as Exception
                ?? new InvalidOperationException(e.ExceptionObject.ToString() ?? "Unknown error");
            logger.LogError(ex, "Unhandled exception (process terminating)");
        };

        try
        {
            // --- Configuration check (pre-TUI, plain console I/O) ---
            // Terminal.Gui is not initialized yet so we use plain Console.ReadLine/Write.
            if (!await EnsureReadyAsync(host, stoppingToken))
            {
                lifetime.StopApplication();
                return;
            }

            // --- TUI phase ---
            var autocomplete = new AutocompleteEngine(commands);

            // Initialize Terminal.Gui. This takes over the terminal (alternate buffer,
            // raw mode, etc.) — everything before this point uses plain console I/O.
            var app = Application.Create();
#pragma warning disable IL2026, IL3050 // Terminal.Gui requires dynamic code; AOT is not a target for Ox.
            app.Init();
#pragma warning restore IL2026, IL3050

            try
            {
                var oxApp = new OxApp(app, host.WorkspacePath);
                oxApp.InputAreaView.SetAutocomplete(autocomplete);

                var conversationView = oxApp.ConversationView;
                var inputAreaView = oxApp.InputAreaView;

                var router = new EventRouter(conversationView);
                var callbacks = PermissionHandler.Build(router, oxApp, host.WorkspacePath);
                var session = host.CreateSession(callbacks);

                inputAreaView.SetModelId(session.ActiveModelId);

                // If the session already knows its last usage, surface the percentage immediately.
                UpdateContextUsage(inputAreaView, host, session);

                // The REPL loop runs as a background task, consuming chat submissions
                // directly from the ComposerController's channel. App.Invoke is used
                // only for synchronous UI mutations; no async work runs inside Invoke.
                _ = Task.Run(async () =>
                {
                    try
                    {
                        await RunReplLoop(oxApp, router, session, host, stoppingToken);
                    }
                    catch (OperationCanceledException) { }
                    catch (Exception ex)
                    {
                        logger.LogError(ex, "REPL loop crashed");
                    }
                    finally
                    {
                        // Safe: RequestStop unblocks app.Run, which returns before
                        // the outer finally disposes app.
                        // ReSharper disable AccessToDisposedClosure
                        app.Invoke(() => app.RequestStop());
                        // ReSharper restore AccessToDisposedClosure
                    }
                }, stoppingToken);

                // Application.Run blocks until RequestStop is called.
                app.Run(oxApp);
            }
            finally
            {
                app.Dispose();
            }
        }
        catch (OperationCanceledException) { }
        catch (Exception ex)
        {
            logger.LogError(ex, "TuiService crashed");
        }
        finally
        {
            lifetime.StopApplication();
        }
    }

    /// <summary>
    /// The main REPL loop. Runs on a background task while app.Run drives
    /// the UI event loop on the main thread.
    ///
    /// Input is consumed from the ComposerController's chat channel, which the
    /// view feeds on every Enter press. This replaces the former TCS + App.Invoke
    /// arm/disarm pattern: the channel buffers typed-ahead input naturally, and
    /// App.Invoke is used only for synchronous UI mutations (never for async work).
    /// </summary>
    private async Task RunReplLoop(
        OxApp oxApp,
        EventRouter router,
        UrSession session,
        UrHost urHost,
        CancellationToken stoppingToken)
    {
        var controller = oxApp.ComposerController;

        while (!stoppingToken.IsCancellationRequested)
        {
            // Signal that no turn is running. Fire-and-forget on the UI thread;
            // the throbber clears asynchronously, which is fine since the visual
            // update has no ordering dependency with the channel read below.
            oxApp.App.Invoke(() => oxApp.InputAreaView.SetTurnRunning(false));

            // Wait for the next user submission from the chat channel.
            // - OperationCanceledException: host stopping token fired (Ctrl+C via host).
            // - ChannelClosedException: user signalled EOF via in-app Ctrl+C/Ctrl+D.
            string input;
            try
            {
                input = await controller.ChatSubmissions.ReadAsync(stoppingToken);
            }
            catch (ChannelClosedException)
            {
                break;
            }

            if (string.IsNullOrWhiteSpace(input))
                continue;

            // /quit: exit immediately without sending to session.
            if (input.Equals("/quit", StringComparison.OrdinalIgnoreCase))
                break;

            // Show the user's message in the conversation.
            oxApp.App.Invoke(() =>
            {
                var userEntry = new ConversationEntry(EntryStyle.User);
                userEntry.SetSegment(input, new Terminal.Gui.Drawing.Color(Terminal.Gui.Drawing.ColorName16.White));
                oxApp.ConversationView.AddEntry(userEntry);

                oxApp.InputAreaView.SetTurnRunning(true);
            });

            // Per-turn CTS linked to stopping token.
            var turnCts = CancellationTokenSource.CreateLinkedTokenSource(stoppingToken);

            // Monitor Escape key during the turn for cancellation.
            // ReSharper disable once AccessToDisposedClosure — safe: handler is
            // unregistered before turnCts is disposed in the finally block.
            EventHandler<Key> escapeHandler = (sender, key) =>
            {
                if (key.KeyCode == KeyCode.Esc)
                    _ = turnCts.CancelAsync();
            };
            oxApp.App.Invoke(() => oxApp.KeyDown += escapeHandler);

            try
            {
                try
                {
                    await foreach (var evt in session.RunTurnAsync(input, turnCts.Token))
                    {
                        // Marshal event routing to the UI thread.
                        var capturedEvt = evt;
                        oxApp.App.Invoke(() =>
                        {
                            router.RouteMainEvent(capturedEvt);

                            if (capturedEvt is TurnCompleted)
                                UpdateContextUsage(oxApp.InputAreaView, urHost, session);

                            if (capturedEvt is TurnError { IsFatal: true } fatal)
                            {
                                logger.LogError("Fatal agent error: {Message}", fatal.Message);
                                oxApp.App.RequestStop();
                            }
                        });
                    }
                }
                catch (OperationCanceledException) when (!stoppingToken.IsCancellationRequested)
                {
                    // Escape cancelled this turn.
                    oxApp.App.Invoke(() =>
                    {
                        var cancelled = new ConversationEntry(EntryStyle.Circle,
                            () => new Terminal.Gui.Drawing.Color(Terminal.Gui.Drawing.ColorName16.DarkGray));
                        cancelled.SetSegment("[cancelled]",
                            new Terminal.Gui.Drawing.Color(Terminal.Gui.Drawing.ColorName16.DarkGray));
                        oxApp.ConversationView.AddEntry(cancelled);
                        router.ResetTurnState();
                    });
                }
                catch (OperationCanceledException) when (stoppingToken.IsCancellationRequested)
                {
                    // Ctrl+C — fall through; outer loop exits.
                }
                catch (Exception ex)
                {
                    logger.LogError(ex, "Unexpected exception during turn");
                    oxApp.App.Invoke(() =>
                    {
                        var crashEntry = new ConversationEntry(EntryStyle.Circle,
                            () => new Terminal.Gui.Drawing.Color(Terminal.Gui.Drawing.ColorName16.Red));
                        crashEntry.SetSegment(
                            $"[error] {ex.Message} — see ~/.ur/logs/ for details",
                            new Terminal.Gui.Drawing.Color(Terminal.Gui.Drawing.ColorName16.Red));
                        oxApp.ConversationView.AddEntry(crashEntry);
                        router.ResetTurnState();
                    });
                }

                await turnCts.CancelAsync();
            }
            finally
            {
                oxApp.App.Invoke(() => oxApp.KeyDown -= escapeHandler);
                turnCts.Dispose();
            }

        }
    }

    /// <summary>
    /// Checks configuration readiness and prompts for any missing values.
    /// Runs before Terminal.Gui starts, using plain console I/O.
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
                    case ChatBlockingIssue.ProviderNotReady:
                    {
                        var message = host.Configuration.GetProviderBlockingMessage();

                        if (!host.Configuration.IsSelectedProviderKnown())
                        {
                            Console.WriteLine(message);
                            Console.Write("Enter a model ID in provider/model format, e.g. openai/gpt-5-nano (or blank to exit): ");
                            var newModel = Console.ReadLine()?.Trim();
                            if (string.IsNullOrEmpty(newModel))
                                return false;
                            await host.Configuration.SetSelectedModelAsync(newModel, ct: ct);
                            break;
                        }

                        var providerName = host.Configuration.GetSelectedProviderName()!;
                        Console.Write($"{message} Enter the API key (or blank to exit): ");
                        var key = Console.ReadLine()?.Trim();
                        if (string.IsNullOrEmpty(key))
                            return false;
                        await host.Configuration.SetApiKeyAsync(key, providerName, ct);
                        break;
                    }

                    case ChatBlockingIssue.MissingModelSelection:
                        Console.Write("No model selected. Enter a model ID, e.g. openai/gpt-5-nano (or blank to exit): ");
                        var model = Console.ReadLine()?.Trim();
                        if (string.IsNullOrEmpty(model))
                            return false;
                        await host.Configuration.SetSelectedModelAsync(model, ct: ct);
                        break;

                    default:
                        throw new System.Diagnostics.UnreachableException(
                            $"Unexpected {nameof(ChatBlockingIssue)}: {issue}");
                }
            }
        }
    }

    /// <summary>
    /// Computes the context fill percentage and pushes it to the composer status line.
    /// </summary>
    private static void UpdateContextUsage(InputAreaView inputArea, UrHost host, UrSession session)
    {
        if (session.LastInputTokens is not { } inputTokens)
            return;

        var contextLength = host.Configuration
            .GetModel(session.ActiveModelId ?? "")?.ContextLength ?? 0;

        if (contextLength <= 0)
            return;

        var pct = (int)Math.Round(inputTokens / (double)contextLength * 100);
        inputArea.SetContextUsagePercent(Math.Max(0, pct));
    }
}
