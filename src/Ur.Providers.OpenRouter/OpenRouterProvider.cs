using System.ClientModel;
using System.ClientModel.Primitives;
using Microsoft.Extensions.AI;
using OpenAI;
using Ur.Configuration.Keyring;
using Ur.Providers;

namespace Ur.Providers.OpenRouter;

/// <summary>
/// Provider for the OpenRouter API. Default endpoint is https://openrouter.ai/api/v1,
/// but accepts an optional override for custom deployments.
///
/// The HTTP pipeline includes <see cref="OpenRouterReasoningHandler"/> to rename
/// the <c>"reasoning":</c> field in responses to <c>"reasoning_content":</c>
/// before the MEAI adapter parses them. This keeps the provider-quirk fix inside
/// the provider layer where it belongs, rather than leaking it into AgentLoop.
///
/// API key is stored in the OS keyring under service="ur", account="openrouter".
/// </summary>
public sealed class OpenRouterProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "openrouter";

    private static readonly Uri DefaultEndpoint = new("https://openrouter.ai/api/v1");

    private readonly IKeyring _keyring;
    private readonly Uri _endpoint;

    /// <param name="keyring">OS keyring for API key resolution.</param>
    /// <param name="endpoint">
    /// Optional endpoint override. Defaults to https://openrouter.ai/api/v1.
    /// </param>
    public OpenRouterProvider(IKeyring keyring, Uri? endpoint = null)
    {
        _keyring = keyring;
        _endpoint = endpoint ?? DefaultEndpoint;
    }

    public string Name => "openrouter";
    public string DisplayName => "OpenRouter";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                $"No API key configured for 'openrouter'. Run: ur config set-api-key <key> --provider openrouter");

        var options = BuildOptions(_endpoint);
        return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
            .AsIChatClient();
    }

    public void ConfigureChatOptions(string model, ChatOptions options)
    {
        // OpenRouter forwards reasoning_effort to reasoning-capable models. The
        // response-side handler below already makes returned reasoning traces visible.
        options.Reasoning ??= new ReasoningOptions { Effort = ReasoningEffort.Low };
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? $"No API key for 'openrouter'. Run: ur config set-api-key <key> --provider openrouter"
            : null;
    }

    /// <summary>
    /// Builds <see cref="OpenAIClientOptions"/> with the reasoning-field renaming
    /// handler attached. OpenRouter returns reasoning traces in a "reasoning" field;
    /// the MEAI OpenAI adapter probes "reasoning_content". The handler bridges the
    /// gap for all OpenRouter models without any per-model configuration.
    /// </summary>
    private static OpenAIClientOptions BuildOptions(Uri endpoint)
    {
        var options = new OpenAIClientOptions { Endpoint = endpoint };
        var handler = new OpenRouterReasoningHandler(new SocketsHttpHandler());
        var transport = new HttpClientPipelineTransport(new HttpClient(handler));
        options.Transport = transport;
        return options;
    }
}
