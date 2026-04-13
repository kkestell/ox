using System.ClientModel;
using System.ClientModel.Primitives;
using Microsoft.Extensions.AI;
using OpenAI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

/// <summary>
/// Generic provider for any API that speaks the OpenAI protocol. Replaces the
/// three near-identical providers (OpenAI, OpenRouter, ZaiCoding) with a single
/// implementation parameterized by name and optional custom endpoint URI.
///
/// The provider name doubles as the OS keyring account for API key storage,
/// following the existing convention: service="ur", account=provider-name.
///
/// For OpenRouter endpoints (host == openrouter.ai), the <see cref="HttpClient"/>
/// pipeline is wrapped with <see cref="OpenRouterReasoningHandler"/> to rename
/// the <c>"reasoning":</c> field in HTTP responses to <c>"reasoning_content":</c>
/// before the MEAI adapter parses them. This keeps the provider-quirk fix inside
/// the provider layer where it belongs, rather than leaking it into AgentLoop.
/// </summary>
internal sealed class OpenAiCompatibleProvider : IProvider
{
    private const string SecretService = "ur";

    private readonly string _name;
    private readonly Uri? _endpoint;
    private readonly IKeyring _keyring;

    /// <param name="name">
    /// The provider name from providers.json (e.g. "openai", "openrouter", "zai-coding").
    /// Used as the keyring account and the prefix in model IDs.
    /// </param>
    /// <param name="endpoint">
    /// Optional custom endpoint URI. When null, the standard OpenAI endpoint is used.
    /// OpenRouter and ZaiCoding set this to their respective API base URLs.
    /// </param>
    /// <param name="keyring">OS keyring for API key resolution.</param>
    public OpenAiCompatibleProvider(string name, Uri? endpoint, IKeyring keyring)
    {
        _name = name;
        _endpoint = endpoint;
        _keyring = keyring;
    }

    public string Name => _name;
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, _name)
            ?? throw new InvalidOperationException(
                $"No API key configured for '{_name}'. Run: ur config set-api-key <key> --provider {_name}");

        if (_endpoint is not null)
        {
            var options = BuildOptions(_endpoint);
            return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
                .AsIChatClient();
        }

        // Standard OpenAI endpoint — no custom options needed.
        return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey))
            .AsIChatClient();
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, _name);
        return string.IsNullOrWhiteSpace(key)
            ? $"No API key for '{_name}'. Run: ur config set-api-key <key> --provider {_name}"
            : null;
    }

    /// <summary>
    /// Builds <see cref="OpenAIClientOptions"/> for the given endpoint.
    ///
    /// For OpenRouter, attaches <see cref="OpenRouterReasoningHandler"/> to the
    /// HTTP pipeline so the <c>"reasoning"</c> field in responses is renamed to
    /// <c>"reasoning_content"</c> before the MEAI adapter parses it. All other
    /// endpoints receive plain options with only the custom endpoint set.
    /// </summary>
    private static OpenAIClientOptions BuildOptions(Uri endpoint)
    {
        var options = new OpenAIClientOptions { Endpoint = endpoint };

        // OpenRouter returns reasoning traces in a "reasoning" field; the MEAI
        // OpenAI adapter probes "reasoning_content". The handler bridges the gap
        // for all OpenRouter models without any per-model configuration.
        if (endpoint.Host.Equals("openrouter.ai", StringComparison.OrdinalIgnoreCase))
        {
            var handler = new OpenRouterReasoningHandler(new SocketsHttpHandler());
            var transport = new HttpClientPipelineTransport(new HttpClient(handler));
            options.Transport = transport;
        }

        return options;
    }
}
