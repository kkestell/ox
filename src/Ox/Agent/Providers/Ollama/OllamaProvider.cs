using Microsoft.Extensions.AI;
using OllamaSharp;
using Ox.Agent.Providers;

namespace Ox.Agent.Providers.Ollama;

/// <summary>
/// Provider for local Ollama models. No API key is needed — Ollama runs locally.
/// The default endpoint is http://localhost:11434, but can be overridden from
/// providers.json for remote Ollama instances.
///
/// <see cref="OllamaApiClient"/> directly implements <see cref="IChatClient"/>,
/// so no adapter is needed.
/// </summary>
internal sealed class OllamaProvider : IProvider
{
    private static readonly Uri DefaultEndpoint = new("http://localhost:11434");

    private readonly Uri _endpoint;

    /// <param name="endpoint">
    /// Optional endpoint override. Defaults to http://localhost:11434.
    /// </param>
    public OllamaProvider(Uri? endpoint = null)
    {
        _endpoint = endpoint ?? DefaultEndpoint;
    }

    public string Name => "ollama";
    public string DisplayName => "Ollama";
    public bool RequiresApiKey => false;

    public IChatClient CreateChatClient(string model) =>
        new OllamaApiClient(_endpoint, model);

    public void ConfigureChatOptions(string model, ChatOptions options)
    {
        // Ollama reasoning-capable models (for example Qwen3) expose thinking
        // through the provider-native "think" flag. We also set the standard
        // reasoning options so adapters that understand them can participate too.
        options.Reasoning ??= new ReasoningOptions { Effort = ReasoningEffort.Low };
        options.AdditionalProperties ??= new AdditionalPropertiesDictionary();
        options.AdditionalProperties["think"] = true;
    }

    /// <summary>
    /// Ollama needs no API key, so it's always ready. A future enhancement could
    /// ping the Ollama endpoint to verify it's reachable, but that would add
    /// latency to every readiness check.
    /// </summary>
    public string? GetBlockingIssue() => null;
}
