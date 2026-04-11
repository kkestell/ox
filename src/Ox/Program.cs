using Microsoft.Extensions.DependencyInjection;
using Te.Input;
using Ur;
using Ur.Configuration;
using Ur.Hosting;
using Ur.Providers.Fake;

namespace Ox;

/// <summary>
/// Entry point for the Ox TUI.
///
/// Handles three phases:
///   1. CLI argument parsing (--fake-provider, etc.)
///   2. Configuration phase — plain console I/O to set up model and API key.
///   3. TUI phase — alternate screen, main loop, clean exit.
/// </summary>
public static class Program
{
    private const string Esc = "\u001b[";

    public static async Task<int> Main(string[] args)
    {
        var bootOptions = OxBootOptions.Parse(args);

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

        services.AddUr(startupOptions);
        using var sp = services.BuildServiceProvider();
        var host = sp.GetRequiredService<UrHost>();

        // ── Configuration phase ─────────────────────────────────────────
        // Plain console I/O before the TUI starts. Prompts for model and
        // API key if not already configured.
        if (!await RunConfigurationPhaseAsync(host.Configuration))
            return 0; // User chose to exit during configuration.

        // ── TUI phase ───────────────────────────────────────────────────
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
            var app = new OxApp(host, coordinator, width, height, startupOptions.WorkspacePath);
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

    /// <summary>
    /// Run the pre-TUI configuration phase. Returns false if the user chose
    /// to exit (entered a blank line when prompted).
    /// </summary>
    private static async Task<bool> RunConfigurationPhaseAsync(UrConfiguration config)
    {
        // Loop until all blocking issues are resolved.
        while (!config.Readiness.CanRunTurns)
        {
            foreach (var issue in config.Readiness.BlockingIssues)
            {
                switch (issue)
                {
                    case ChatBlockingIssue.MissingModelSelection:
                        Console.Write("Enter model (provider/model): ");
                        var modelInput = Console.ReadLine()?.Trim();
                        if (string.IsNullOrEmpty(modelInput))
                            return false;

                        await config.SetSelectedModelAsync(modelInput);

                        // Check if the provider is recognized.
                        if (!config.IsSelectedProviderKnown())
                        {
                            Console.WriteLine(config.GetProviderBlockingMessage());
                            await config.ClearSelectedModelAsync();
                        }
                        break;

                    case ChatBlockingIssue.ProviderNotReady:
                        var provider = config.GetSelectedProviderName() ?? "unknown";
                        Console.Write($"Enter API key for {provider}: ");
                        var keyInput = Console.ReadLine()?.Trim();
                        if (string.IsNullOrEmpty(keyInput))
                            return false;

                        await config.SetApiKeyAsync(keyInput, provider);
                        break;
                }
            }
        }

        return true;
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
