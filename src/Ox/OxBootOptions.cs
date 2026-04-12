namespace Ox;

/// <summary>
/// Parses Ox-specific CLI arguments (like --fake-provider, --headless, --yolo,
/// --prompt, --model) out of the raw argv and passes everything else through to
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
    /// responses to stdout. Requires <see cref="Prompt"/> to be set.
    /// </summary>
    public bool IsHeadless { get; private init; }

    /// <summary>
    /// Auto-grants all tool permission requests without prompting. Only meaningful
    /// in headless mode — the TUI always uses interactive prompts.
    /// </summary>
    public bool IsYolo { get; private init; }

    /// <summary>
    /// The single user message to send to the agent in headless mode.
    /// Set by <c>--prompt &lt;msg&gt;</c>. Required when <see cref="IsHeadless"/> is true.
    ///
    /// Headless mode is always one task (one prompt) that the agent works on
    /// autonomously. Multi-turn dialogue is a TUI concept — headless mode has
    /// exactly one user turn.
    /// </summary>
    public string? Prompt { get; private init; }

    /// <summary>
    /// Override the configured model for this run. Passed to
    /// <c>UrOptions.SelectedModelOverride</c> so headless/eval runs can
    /// select a model without rewriting settings files.
    /// </summary>
    public string? ModelOverride { get; private init; }

    /// <summary>
    /// Maximum number of AgentLoop iterations (LLM calls) the agent may make
    /// within the single headless turn before being aborted with a fatal error.
    /// Only meaningful in headless mode — the TUI does not bound loop iterations.
    /// Null means no cap: the loop runs until the LLM stops calling tools.
    ///
    /// Use this as a safety rail in eval scenarios to prevent a runaway ReAct
    /// loop from burning through tokens when the agent gets stuck in a tool-call
    /// cycle.
    /// </summary>
    public int? MaxIterations { get; private init; }

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
        string? prompt = null;
        string? modelOverride = null;
        int? maxIterations = null;
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
                case "--prompt" when i + 1 < args.Length:
                    // Last --prompt wins; duplicate args silently overwrite.
                    prompt = args[++i];
                    break;
                case "--model" when i + 1 < args.Length:
                    modelOverride = args[++i];
                    break;
                case "--max-iterations" when i + 1 < args.Length:
                    if (int.TryParse(args[++i], out var parsed) && parsed > 0)
                        maxIterations = parsed;
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
            Prompt = prompt,
            ModelOverride = modelOverride,
            MaxIterations = maxIterations,
            RemainingArgs = remaining,
        };
    }
}
