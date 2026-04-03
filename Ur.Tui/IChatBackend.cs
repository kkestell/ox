using Ur.AgentLoop;
using Ur.Configuration;
using Ur.Extensions;
using Ur.Providers;

namespace Ur.Tui;

/// <summary>
/// The narrow contract <see cref="ChatApp"/> needs from the backend.
/// Extracted as an interface so the TUI can be tested with a mock backend
/// that doesn't require a real <see cref="UrHost"/> or network access.
/// The production implementation is <see cref="ChatBackend"/>.
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
/// A single chat session that can run conversational turns. Wraps
/// <see cref="UrSession.RunTurnAsync"/> behind an interface for testability.
/// </summary>
public interface IChatSession
{
    IAsyncEnumerable<AgentLoopEvent> RunTurnAsync(string input, CancellationToken ct);
}
