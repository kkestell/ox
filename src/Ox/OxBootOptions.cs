namespace Ox;

/// <summary>
/// Parses Ox-specific CLI arguments (like --fake-provider) out of the raw
/// argv and passes everything else through to the host builder. This keeps
/// Ox's custom flags from colliding with ASP.NET / generic-host conventions.
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
        var remaining = new List<string>();

        for (var i = 0; i < args.Length; i++)
        {
            if (args[i] == "--fake-provider" && i + 1 < args.Length)
            {
                fakeProviderScenario = args[++i];
            }
            else
            {
                remaining.Add(args[i]);
            }
        }

        return new OxBootOptions
        {
            FakeProviderScenario = fakeProviderScenario,
            RemainingArgs = remaining,
        };
    }
}
