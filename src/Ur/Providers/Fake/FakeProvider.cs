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
internal sealed class FakeProvider : IProvider
{
    public string Name => "fake";

    public bool RequiresApiKey => false;

    public IChatClient CreateChatClient(string model)
    {
        // The model portion is the scenario name-or-path.
        var scenario = FakeScenarioLoader.Load(model);
        return new FakeChatClient(scenario);
    }

    /// <summary>
    /// The fake provider is always ready — no API key, no endpoint to check.
    /// </summary>
    public string? GetBlockingIssue() => null;
}
