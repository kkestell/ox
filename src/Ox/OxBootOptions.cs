namespace Ox;

/// <summary>
/// Parses Ox-specific CLI arguments (like --fake-provider, --headless, --yolo,
/// --turn, --model) out of the raw argv and passes everything else through to
/// the host builder. This keeps Ox's custom flags from colliding with ASP.NET /
/// generic-host conventions.
/// </summary>
public sealed class OxBootOptions
{
    /// <summary>
    /// When set, registers a fake LLM provider that replays the named scenario
    /// instead of calling a live model API. Value is a built-in scenario name
    /// (e.g. "hello") or a path to a JSON scenario file.
    /// </summary>
    public string? FakeProviderScenario { get; private init; }

    /// <summary>
    /// Runs Ox without a TUI — drives the agent loop from the CLI, printing
    /// responses to stdout. Requires at least one <see cref="Turns"/> entry.
    /// </summary>
    public bool IsHeadless { get; private init; }

    /// <summary>
    /// Auto-grants all tool permission requests without prompting. Only meaningful
    /// in headless mode — the TUI always uses interactive prompts.
    /// </summary>
    public bool IsYolo { get; private init; }

    /// <summary>
    /// Sequence of user messages to send to the LLM in order. Accumulated from
    /// repeated <c>--turn &lt;msg&gt;</c> args. At least one is required in
    /// headless mode.
    /// </summary>
    public IReadOnlyList<string> Turns { get; private init; } = [];

    /// <summary>
    /// Override the configured model for this run. Passed to
    /// <c>UrStartupOptions.SelectedModelOverride</c> so headless/eval runs can
    /// select a model without rewriting settings files.
    /// </summary>
    public string? ModelOverride { get; private init; }

    /// <summary>
    /// Maximum number of user turns (--turn args) to process before stopping.
    /// Only meaningful in headless mode — the TUI does not bound session length.
    /// Null means no limit: all provided turns are processed.
    ///
    /// Use this as a safety cap in eval scenarios where an agent could otherwise
    /// run indefinitely if a loop or retry pattern consumes extra turns.
    /// </summary>
    public int? MaxTurns { get; private init; }

    /// <summary>
    /// All arguments not consumed by Ox-specific parsing — forwarded to the
    /// host builder so flags like --environment and --urls still work.
    /// </summary>
    public IReadOnlyList<string> RemainingArgs { get; private init; } = [];

    /// <summary>
    /// Extract Ox-specific flags from <paramref name="args"/> and collect the rest
    /// into <see cref="RemainingArgs"/>.
    /// </summary>
    public static OxBootOptions Parse(string[] args)
    {
        string? fakeProviderScenario = null;
        var isHeadless = false;
        var isYolo = false;
        var turns = new List<string>();
        string? modelOverride = null;
        int? maxTurns = null;
        var remaining = new List<string>();

        for (var i = 0; i < args.Length; i++)
        {
            switch (args[i])
            {
                case "--fake-provider" when i + 1 < args.Length:
                    fakeProviderScenario = args[++i];
                    break;
                case "--headless":
                    isHeadless = true;
                    break;
                case "--yolo":
                    isYolo = true;
                    break;
                case "--turn" when i + 1 < args.Length:
                    turns.Add(args[++i]);
                    break;
                case "--model" when i + 1 < args.Length:
                    modelOverride = args[++i];
                    break;
                case "--max-turns" when i + 1 < args.Length:
                    if (int.TryParse(args[++i], out var parsed) && parsed > 0)
                        maxTurns = parsed;
                    break;
                default:
                    remaining.Add(args[i]);
                    break;
            }
        }

        return new OxBootOptions
        {
            FakeProviderScenario = fakeProviderScenario,
            IsHeadless = isHeadless,
            IsYolo = isYolo,
            Turns = turns,
            ModelOverride = modelOverride,
            MaxTurns = maxTurns,
            RemainingArgs = remaining,
        };
    }
}
