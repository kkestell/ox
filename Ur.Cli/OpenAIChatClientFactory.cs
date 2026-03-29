using System.ClientModel;
using Microsoft.Extensions.AI;
using OpenAI;
using Ur.Providers;

namespace Ur.Cli;

/// <summary>
/// Creates IChatClient instances using the OpenAI SDK.
/// Works with any OpenAI-compatible API (OpenAI, OpenRouter, Ollama, etc.).
/// </summary>
public sealed class OpenAIChatClientFactory : IChatClientFactory
{
    private readonly Dictionary<string, Uri> _providerEndpoints;

    public OpenAIChatClientFactory(Dictionary<string, Uri> providerEndpoints)
    {
        _providerEndpoints = providerEndpoints;
    }

    public IChatClient Create(string providerId, string modelId, string apiKey)
    {
        var options = new OpenAIClientOptions();
        if (_providerEndpoints.TryGetValue(providerId, out var endpoint))
            options.Endpoint = endpoint;

        return new OpenAI.Chat.ChatClient(
                modelId,
                new ApiKeyCredential(apiKey),
                options)
            .AsIChatClient();
    }
}
