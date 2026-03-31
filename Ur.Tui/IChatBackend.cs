using Ur.AgentLoop;
using Ur.Providers;

namespace Ur.Tui;

/// <summary>
/// The narrow contract ChatApp needs from the backend.
/// </summary>
public interface IChatBackend
{
    UrChatReadiness Readiness { get; }
    IReadOnlyList<ModelInfo> AvailableModels { get; }
    Task SetApiKeyAsync(string apiKey);
    Task SetSelectedModelAsync(string modelId);
    IChatSession CreateSession();
}

/// <summary>
/// A single chat session that can run conversational turns.
/// </summary>
public interface IChatSession
{
    IAsyncEnumerable<AgentLoopEvent> RunTurnAsync(string input, CancellationToken ct);
}
