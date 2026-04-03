using Ur.AgentLoop;
using Ur.Providers;

namespace Ur.Tui;

/// <summary>
/// Production adapter wrapping <see cref="UrHost"/> behind <see cref="IChatBackend"/>.
/// </summary>
public sealed class ChatBackend(UrHost host) : IChatBackend
{
    public ChatReadiness Readiness => host.Configuration.Readiness;

    public IReadOnlyList<ModelInfo> AvailableModels => host.Configuration.AvailableModels;

    public string? SelectedModelId => host.Configuration.SelectedModelId;

    public IReadOnlyList<ExtensionInfo> ListExtensions() =>
        host.Extensions.List();

    public Task SetApiKeyAsync(string apiKey) =>
        host.Configuration.SetApiKeyAsync(apiKey);

    public Task SetSelectedModelAsync(string modelId) =>
        host.Configuration.SetSelectedModelAsync(modelId);

    public Task<ExtensionInfo> SetExtensionEnabledAsync(
        string extensionId,
        bool enabled,
        CancellationToken ct = default) =>
        host.Extensions.SetEnabledAsync(extensionId, enabled, ct);

    public Task<ExtensionInfo> ResetExtensionAsync(
        string extensionId,
        CancellationToken ct = default) =>
        host.Extensions.ResetAsync(extensionId, ct);

    public IChatSession CreateSession() =>
        new ChatSessionAdapter(host.CreateSession());

    private sealed class ChatSessionAdapter(UrSession session) : IChatSession
    {
        public IAsyncEnumerable<AgentLoopEvent> RunTurnAsync(string input, CancellationToken ct) =>
            session.RunTurnAsync(input, ct: ct);
    }
}
