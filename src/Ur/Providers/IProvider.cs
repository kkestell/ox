using Microsoft.Extensions.AI;

namespace Ur.Providers;

/// <summary>
/// Abstracts a model provider (OpenRouter, OpenAI, Google, Ollama, etc.).
///
/// Each provider encapsulates its own client construction, API key resolution,
/// and settings. The rest of the system only sees IProvider → IChatClient —
/// provider-specific details (endpoint URIs, SDK types, key resolution) stay
/// inside the implementation.
///
/// Each built-in provider lives in its own project (e.g. Ur.Providers.Google,
/// Ur.Providers.OpenAI) so provider-specific NuGet dependencies are isolated.
/// Ox references all provider projects and registers them via key-based
/// dispatch from providers.json.
/// </summary>
public interface IProvider
{
    /// <summary>
    /// The provider prefix used in model IDs (e.g. "openrouter", "ollama", "openai", "google").
    /// </summary>
    string Name { get; }

    /// <summary>
    /// Human-readable display name shown in the connect wizard and provider list
    /// (e.g. "OpenAI", "Z.AI Coding Plan"). Providers own their display name so
    /// it doesn't need to live in providers.json.
    /// </summary>
    string DisplayName { get; }

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
}
