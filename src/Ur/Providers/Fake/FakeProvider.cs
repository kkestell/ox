using System.Collections.Concurrent;
using Microsoft.Extensions.AI;

namespace Ur.Providers.Fake;

/// <summary>
/// A deterministic provider that replays scripted scenarios instead of calling
/// a live model API.
///
/// Implements <see cref="IProvider"/> so it participates in the real provider
/// registry and exercises the same composition path as production providers.
/// Model IDs are in the form "fake/scenario-name" — the model portion is
/// resolved to a built-in or file-backed <see cref="FakeScenario"/>.
///
/// The provider does not require an API key and is always ready, so it bypasses
/// the readiness prompts in the configuration phase.
/// </summary>
public sealed class FakeProvider : IProvider
{
    /// <summary>
    /// Shared turn counters keyed by model/scenario name. When a scenario declares
    /// a <see cref="FakeScenario.ContextWindow"/>, all chat clients created for that
    /// scenario share a single turn counter so compaction's non-streaming call and
    /// the agent loop's streaming call advance through turns in order rather than
    /// each starting at turn 0. Scenarios without ContextWindow get per-client
    /// counters (the default) so existing behavior is unchanged.
    /// </summary>
    private readonly ConcurrentDictionary<string, SharedTurnCounter> _sharedCounters = new();

    public string Name => "fake";

    public bool RequiresApiKey => false;

    public IChatClient CreateChatClient(string model)
    {
        var scenario = FakeScenarioLoader.Load(model);

        // Scenarios with a context window need shared turn ordering across client
        // instances (compaction creates separate clients for summarization and
        // the main agent loop within the same session).
        SharedTurnCounter? sharedCounter = null;
        if (scenario.ContextWindow is not null)
            sharedCounter = _sharedCounters.GetOrAdd(model, _ => new SharedTurnCounter());

        return new FakeChatClient(scenario, sharedCounter);
    }

    /// <summary>
    /// Returns the simulated context window for a scenario, or null if the
    /// scenario doesn't declare one. Called by the context window resolver registered via
    /// <c>OxConfiguration.ResolveContextWindow</c>
    /// as a fallback when the static providers.json has no entry for fake models.
    /// </summary>
    public static int? GetContextWindow(string model)
    {
        try
        {
            return FakeScenarioLoader.Load(model).ContextWindow;
        }
        catch
        {
            return null;
        }
    }

    /// <summary>
    /// The fake provider is always ready — no API key, no endpoint to check.
    /// </summary>
    public string? GetBlockingIssue() => null;
}

/// <summary>
/// Thread-safe shared turn counter for scenarios that need turn coordination
/// across multiple <see cref="FakeChatClient"/> instances (e.g. compaction
/// creates a separate client for the summarization call).
/// </summary>
internal sealed class SharedTurnCounter
{
    private int _index = -1;
    public int Next() => Interlocked.Increment(ref _index);
}
