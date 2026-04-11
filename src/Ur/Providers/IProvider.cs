using Microsoft.Extensions.AI;

namespace Ur.Providers;

/// <summary>
/// Abstracts a model provider (OpenRouter, OpenAI, Google, Ollama, etc.).
///
/// Each provider encapsulates its own client construction, API key resolution,
/// and settings. The rest of the system only sees IProvider → IChatClient —
/// provider-specific details (endpoint URIs, SDK types, key resolution) stay
/// inside the implementation.
/// </summary>
internal interface IProvider
{
    /// <summary>
    /// The provider prefix used in model IDs (e.g. "openrouter", "ollama", "openai", "google").
    /// </summary>
    string Name { get; }

    /// <summary>
    /// Creates an <see cref="IChatClient"/> for the given model portion of the ID.
    /// The <paramref name="model"/> parameter is everything after the provider prefix
    /// (e.g. for "openrouter/anthropic/claude-3.5-sonnet", model is "anthropic/claude-3.5-sonnet").
    /// </summary>
    IChatClient CreateChatClient(string model);

    /// <summary>
    /// Whether this provider requires an API key to function. Ollama does not;
    /// OpenRouter, OpenAI, and Google do.
    /// </summary>
    bool RequiresApiKey { get; }

    /// <summary>
    /// Checks whether the provider is ready to create a chat client.
    /// Returns null if ready, or a human-readable issue string describing
    /// what the user needs to fix (e.g. "No API key for 'openai'").
    /// </summary>
    string? GetBlockingIssue();

    /// <summary>
    /// Resolves the context window size (max input tokens) for the given model.
    /// Returns null if the provider cannot determine the context window (e.g. unknown
    /// model, API unreachable). Each provider uses its own authoritative source —
    /// Google queries the Gemini API, OpenRouter reads from the model catalog,
    /// Ollama calls its local /api/show endpoint, etc.
    /// </summary>
    Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default);
}
