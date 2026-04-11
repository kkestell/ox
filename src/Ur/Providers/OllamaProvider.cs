using Microsoft.Extensions.AI;
using OllamaSharp;

namespace Ur.Providers;

/// <summary>
/// Provider for local Ollama models. No API key is needed — Ollama runs locally.
/// The endpoint URI comes from providers.json (the "url" field on the ollama entry).
///
/// <see cref="OllamaApiClient"/> directly implements <see cref="IChatClient"/>,
/// so no adapter is needed.
/// </summary>
internal sealed class OllamaProvider : IProvider
{
    private readonly string _name;
    private readonly Uri _endpoint;

    public OllamaProvider(string name, Uri endpoint)
    {
        _name = name;
        _endpoint = endpoint;
    }

    public string Name => _name;
    public bool RequiresApiKey => false;

    public IChatClient CreateChatClient(string model) =>
        new OllamaApiClient(_endpoint, model);

    /// <summary>
    /// Ollama needs no API key, so it's always ready. A future enhancement could
    /// ping the Ollama endpoint to verify it's reachable, but that would add
    /// latency to every readiness check.
    /// </summary>
    public string? GetBlockingIssue() => null;
}
