using Microsoft.Extensions.DependencyInjection;
using Te.Input;
using Ur.Configuration.Keyring;
using Ur.Hosting;
using Ur.Providers.Fake;

namespace Ox;

/// <summary>
/// Entry point for the Ox application.
///
/// Handles three phases:
///   1. CLI argument parsing (--fake-provider, --headless, --yolo, --turn, --model).
///   2. DI container setup with Ur services.
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

        // Build the DI container with Ur services.
        var services = new ServiceCollection();
        var startupOptions = new UrStartupOptions
        {
            WorkspacePath = Directory.GetCurrentDirectory(),
        };

        // If --fake-provider was specified, register the fake provider and
        // select the fake model so the configuration phase is skipped.
        if (bootOptions.FakeProviderScenario is { } scenario)
        {
            startupOptions = new UrStartupOptions
            {
                WorkspacePath = startupOptions.WorkspacePath,
                FakeProvider = new FakeProvider(),
                SelectedModelOverride = $"fake/{scenario}",
            };
        }

        // Headless mode uses EnvironmentKeyring (no OS keyring in containers)
        // and may override the model from the CLI.
        if (bootOptions.IsHeadless)
        {
            startupOptions = new UrStartupOptions
            {
                WorkspacePath = startupOptions.WorkspacePath,
                FakeProvider = startupOptions.FakeProvider,
                KeyringOverride = new EnvironmentKeyring(),
                SelectedModelOverride = bootOptions.ModelOverride ?? startupOptions.SelectedModelOverride,
            };
        }

        // AddUr loads providers.json — if the file is missing or malformed,
        // we catch the error here and exit with a clear message instead of
        // dumping a raw exception trace to the terminal.
        try
        {
            services.AddUr(startupOptions);
        }
        catch (FileNotFoundException ex) when (ex.Message.Contains("providers.json"))
        {
            await Console.Error.WriteLineAsync(
                $"Error: {ex.Message}\nSee docs/settings.md for the expected format.");
            return 1;
        }
        catch (InvalidOperationException ex) when (ex.Message.Contains("providers.json"))
        {
            await Console.Error.WriteLineAsync($"Error: {ex.Message}");
            return 1;
        }

        using var sp = services.BuildServiceProvider();
        var host = sp.GetRequiredService<UrHost>();

        // ── Headless path ───────────────────────────────────────────────
        // Branch before any TUI initialization — no alternate screen, no input
        // source, no OxApp. HeadlessRunner drives the agent loop from CLI args.
        if (bootOptions.IsHeadless)
        {
            using var cts = new CancellationTokenSource();
            Console.CancelKeyPress += (_, e) =>
            {
                e.Cancel = true;
                cts.Cancel();
            };

            var runner = new HeadlessRunner(host, bootOptions.Prompt!, bootOptions.IsYolo, bootOptions.MaxIterations);
            return await runner.RunAsync(cts.Token);
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
            using var app = new OxApp(host, coordinator, width, height, startupOptions.WorkspacePath);
            await app.RunAsync();
        }
        finally
        {
            ShowCursor();
            ExitAlternateScreen();
            Console.CancelKeyPress -= cancelHandler;
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
