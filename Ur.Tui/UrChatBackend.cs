using Ur.AgentLoop;
using Ur.Providers;

namespace Ur.Tui;

/// <summary>
/// Production adapter wrapping <see cref="UrHost"/> behind <see cref="IChatBackend"/>.
/// </summary>
public sealed class UrChatBackend(UrHost host) : IChatBackend
{
    public UrChatReadiness Readiness => host.Configuration.Readiness;

    public IReadOnlyList<ModelInfo> AvailableModels => host.Configuration.AvailableModels;

    public Task SetApiKeyAsync(string apiKey) =>
        host.Configuration.SetApiKeyAsync(apiKey);

    public Task SetSelectedModelAsync(string modelId) =>
        host.Configuration.SetSelectedModelAsync(modelId);

    public IChatSession CreateSession() =>
        new UrChatSessionAdapter(host.CreateSession());

    private sealed class UrChatSessionAdapter(UrSession session) : IChatSession
    {
        public IAsyncEnumerable<AgentLoopEvent> RunTurnAsync(string input, CancellationToken ct) =>
            session.RunTurnAsync(input, ct: ct);
    }
}
