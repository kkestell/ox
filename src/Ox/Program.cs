using System.Text;
using dotenv.net;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Terminal.Gui.App;
using Terminal.Gui.Drawing;
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
///   2. TUI phase: Terminal.Gui runs the full UI with conversation, input, sidebar.
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
            var todoStore = new Ur.Todo.TodoStore();

            // Initialize Terminal.Gui. This takes over the terminal (alternate buffer,
            // raw mode, etc.) — everything before this point uses plain console I/O.
            var app = Application.Create();
            app.Init();

            try
            {
                var oxApp = new OxApp(app, todoStore);
                oxApp.SetAutocomplete(autocomplete);

                var conversationView = oxApp.ConversationView;
                var inputAreaView = oxApp.InputAreaView;
                var sidebarView = oxApp.SidebarView;

                var router = new EventRouter(conversationView);
                var callbacks = PermissionHandler.Build(router, oxApp, host.WorkspacePath);
                var session = host.CreateSession(callbacks, todoStore);

                inputAreaView.SetModelId(session.ActiveModelId);

                // If resuming a session, show context fill immediately from persisted usage.
                UpdateContextUsage(sidebarView, host, session);

                // The REPL loop runs as a background task, marshalling UI updates via
                // Application.Invoke(). Application.Run() blocks the main thread and
                // drives the Terminal.Gui event loop.
                _ = Task.Run(async () =>
                {
                    try
                    {
                        await RunReplLoop(oxApp, router, session, host, sidebarView, stoppingToken);
                    }
                    catch (OperationCanceledException) { }
                    catch (Exception ex)
                    {
                        logger.LogError(ex, "REPL loop crashed");
                    }
                    finally
                    {
                        app.Invoke(() => app.RequestStop());
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
    /// the UI event loop on the main thread. All UI mutations go through
    /// app.Invoke() for thread safety.
    /// </summary>
    private async Task RunReplLoop(
        OxApp oxApp,
        EventRouter router,
        UrSession session,
        UrHost urHost,
        SidebarView sidebarView,
        CancellationToken stoppingToken)
    {
        while (!stoppingToken.IsCancellationRequested)
        {
            // Read user input. This awaits a TCS that's completed on the UI thread
            // when the user presses Enter, Ctrl+C, or Ctrl+D.
            string? input = null;
            var inputTcs = new TaskCompletionSource<string?>();
            oxApp.App.Invoke(async () =>
            {
                try
                {
                    var result = await oxApp.ReadLineAsync("",
                        suffix => oxApp.InputAreaView.SetCompletion(suffix),
                        stoppingToken);
                    inputTcs.TrySetResult(result);
                }
                catch (Exception)
                {
                    inputTcs.TrySetResult(null); // Treat errors as EOF
                }
            });
            input = await inputTcs.Task;

            // null = EOF (Ctrl+C, Ctrl+D) or cancellation.
            if (input is null)
                break;

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

                oxApp.InputAreaView.SetPrompt("");
                oxApp.InputAreaView.SetTurnRunning(true);
            });

            // Per-turn CTS linked to stopping token.
            var turnCts = CancellationTokenSource.CreateLinkedTokenSource(stoppingToken);

            // Monitor Escape key during the turn for cancellation.
            EventHandler<Key> escapeHandler = (_, key) =>
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
                                UpdateContextUsage(sidebarView, urHost, session);

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

            oxApp.App.Invoke(() => oxApp.InputAreaView.SetTurnRunning(false));
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
    /// Computes the context fill display string and pushes it to the sidebar.
    /// </summary>
    private static void UpdateContextUsage(SidebarView sidebar, UrHost host, UrSession session)
    {
        if (session.LastInputTokens is not { } inputTokens)
            return;

        var contextLength = host.Configuration
            .GetModel(session.ActiveModelId ?? "")?.ContextLength ?? 0;

        if (contextLength <= 0)
            return;

        var pct = (int)Math.Round(inputTokens / (double)contextLength * 100);
        sidebar.SetContextUsage($"{inputTokens:N0} / {contextLength:N0} - {pct}%");
    }
}
