using Ur.AgentLoop;
using Ur.Providers;

namespace Ur.Tui;

/// <summary>
/// The narrow contract ChatApp needs from the backend.
/// </summary>
public interface IChatBackend
{
    ChatReadiness Readiness { get; }
    IReadOnlyList<ModelInfo> AvailableModels { get; }
    string? SelectedModelId { get; }
    IReadOnlyList<ExtensionInfo> ListExtensions();
    Task SetApiKeyAsync(string apiKey);
    Task SetSelectedModelAsync(string modelId);
    Task<ExtensionInfo> SetExtensionEnabledAsync(
        string extensionId,
        bool enabled,
        CancellationToken ct = default);
    Task<ExtensionInfo> ResetExtensionAsync(
        string extensionId,
        CancellationToken ct = default);
    IChatSession CreateSession();
}

/// <summary>
/// A single chat session that can run conversational turns.
/// </summary>
public interface IChatSession
{
    IAsyncEnumerable<AgentLoopEvent> RunTurnAsync(string input, CancellationToken ct);
}
