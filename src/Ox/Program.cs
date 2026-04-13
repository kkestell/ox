using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Ox.Configuration;
using Te.Input;
using Ur.Configuration.Keyring;
using Ur.Hosting;
using Ur.Logging;
using Ur.Providers;
using Ur.Providers.Fake;

namespace Ox;

/// <summary>
/// Entry point for the Ox application.
///
/// Handles three phases:
///   1. CLI argument parsing (--fake-provider, --headless, --yolo, --turn, --model).
///   2. Generic Host setup with Ur services.
///   3. Execution path: headless mode (HeadlessRunner) or TUI mode (OxApp).
///      Headless mode branches before any TUI initialization — no alternate screen,
///      no TerminalInputSource, no OxApp.
/// </summary>
public static class Program
{
    private const string Esc = "\u001b[";

    public static async Task<int> Main(string[] args)
    {
        var bootOptions = OxBootOptions.Parse(args);

        // Validate headless mode requirements early, before DI setup.
        if (bootOptions.IsHeadless && string.IsNullOrWhiteSpace(bootOptions.Prompt))
        {
            await Console.Error.WriteLineAsync(
                "Error: --headless requires a --prompt <message> argument.");
            return 1;
        }

        var workspacePath = Directory.GetCurrentDirectory();
        var userDataDir = ServiceCollectionExtensions.DefaultUserDataDirectory();
        var userSettingsPath = ServiceCollectionExtensions.DefaultUserSettingsPath(userDataDir);
        var workspaceSettingsPath = Path.Combine(workspacePath, ".ox", "settings.json");

        // ── Build the Generic Host ──────────────────────────────────────
        //
        // Host.CreateApplicationBuilder registers IConfigurationRoot, IConfiguration,
        // and the options pipeline. We add Ur settings sources, configure logging
        // (file-only — no console/debug loggers that would corrupt the TUI), and
        // register the Ur service graph.
        var builder = Host.CreateApplicationBuilder(args);

        // ClearProviders removes the console and debug loggers that
        // CreateApplicationBuilder registers by default — without this,
        // those providers write directly to stdout and corrupt the TUI.
        builder.Logging.ClearProviders();
        builder.Logging.AddProvider(new UrFileLoggerProvider());

        builder.Configuration.AddUrSettings(userSettingsPath, workspaceSettingsPath);

        // If --fake-provider was specified, register the fake provider and
        // select the fake model so the configuration phase is skipped.
        string? selectedModelOverride = null;
        if (bootOptions.FakeProviderScenario is { } scenario)
        {
            builder.Services.AddSingleton<IProvider>(new FakeProvider());
            selectedModelOverride = $"fake/{scenario}";
        }

        // Headless mode uses EnvironmentKeyring (no OS keyring in containers)
        // and may override the model from the CLI.
        if (bootOptions.IsHeadless)
        {
            builder.Services.AddSingleton<IKeyring>(new EnvironmentKeyring());
            selectedModelOverride = bootOptions.ModelOverride ?? selectedModelOverride;
        }

        // ── Load providers.json and register providers ────────────────
        //
        // ProviderConfig is Ox's application-level concern — Ur doesn't know about
        // model catalogs. Load the config eagerly so we can report errors with a
        // clear message before the DI container is built.
        var providersJsonPath = Path.Combine(userDataDir, "providers.json");
        ProviderConfig providerConfig;
        try
        {
            providerConfig = ProviderConfig.Load(providersJsonPath);
        }
        catch (FileNotFoundException ex)
        {
            await Console.Error.WriteLineAsync(
                $"Error: {ex.Message}\nSee docs/providers.md for the expected format.");
            return 1;
        }
        catch (InvalidOperationException ex) when (ex.Message.Contains("providers.json"))
        {
            await Console.Error.WriteLineAsync($"Error: {ex.Message}");
            return 1;
        }

        builder.Services.AddSingleton(providerConfig);
        builder.Services.AddProvidersFromConfig(providerConfig);

        // OxConfiguration wraps model catalog queries (model listing, context windows,
        // provider metadata) for the TUI and headless runner.
        builder.Services.AddSingleton<OxConfiguration>();

        // Register a context window resolver for Ur's use — UrHost passes this delegate
        // to UrSession so compaction can check context fill percentage.
        builder.Services.AddSingleton<Func<string, int?>>(sp =>
            sp.GetRequiredService<OxConfiguration>().ResolveContextWindow);

        // ── Register Ur services ────────────────────────────────────────
        builder.Services.AddUr(builder.Configuration, o =>
        {
            o.WorkspacePath = workspacePath;
            o.SelectedModelOverride = selectedModelOverride;
        });

        using var app = builder.Build();
        await app.StartAsync();
        var host = app.Services.GetRequiredService<UrHost>();
        var oxConfig = app.Services.GetRequiredService<OxConfiguration>();

        // ── Headless path ───────────────────────────────────────────────
        // Branch before any TUI initialization — no alternate screen, no input
        // source, no OxApp. HeadlessRunner drives the agent loop from CLI args.
        if (bootOptions.IsHeadless)
        {
            var cts = new CancellationTokenSource();
            Console.CancelKeyPress += (_, e) =>
            {
                e.Cancel = true;
                cts.Cancel();
            };

            var runner = new HeadlessRunner(host, bootOptions.Prompt!, bootOptions.IsYolo, bootOptions.MaxIterations);
            var result = await runner.RunAsync(cts.Token);
            await app.StopAsync();
            return result;
        }

        // ── TUI phase ───────────────────────────────────────────────────
        // First-run configuration is now handled inside the TUI by the connect
        // wizard — OxApp opens it automatically when no model is configured.
        using var inputSource = new TerminalInputSource(new TerminalInputSourceOptions
        {
            EnableMouse = true,
            EnableMouseMove = false,
        });
        using var coordinator = new InputCoordinator(inputSource);

        // Ctrl+C handler — sets exit flag instead of crashing so alternate
        // screen cleanup always happens in the finally block.
        ConsoleCancelEventHandler cancelHandler = (_, e) =>
        {
            e.Cancel = true;
        };
        Console.CancelKeyPress += cancelHandler;

        try
        {
            EnterAlternateScreen();
            HideCursor();

            var (width, height) = GetTerminalSize();
            using var oxApp = new OxApp(host, oxConfig, coordinator, width, height, workspacePath);
            await oxApp.RunAsync();
        }
        finally
        {
            ShowCursor();
            ExitAlternateScreen();
            Console.CancelKeyPress -= cancelHandler;
            await app.StopAsync();
        }

        return 0;
    }

    private static (int Width, int Height) GetTerminalSize()
    {
        var width = Console.WindowWidth;
        var height = Console.WindowHeight;
        return (Math.Max(20, width), Math.Max(10, height));
    }

    private static void EnterAlternateScreen() => Console.Write($"{Esc}?1049h");
    private static void ExitAlternateScreen() => Console.Write($"{Esc}?1049l");
    private static void HideCursor() => Console.Write($"{Esc}?25l");
    private static void ShowCursor() => Console.Write($"{Esc}?25h");
}
