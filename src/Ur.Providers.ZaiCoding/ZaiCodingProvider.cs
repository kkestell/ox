using System.ClientModel;
using Microsoft.Extensions.AI;
using OpenAI;
using Ur.Configuration.Keyring;
using Ur.Providers;

namespace Ur.Providers.ZaiCoding;

/// <summary>
/// Provider for the Z.AI Coding Plan API (GLM models). Uses the OpenAI-compatible
/// protocol with Z.AI's coding endpoint.
///
/// API key is stored in the OS keyring under service="ur", account="zai-coding".
/// </summary>
public sealed class ZaiCodingProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "zai-coding";

    private static readonly Uri DefaultEndpoint = new("https://api.z.ai/api/coding/paas/v4");

    private readonly IKeyring _keyring;
    private readonly Uri _endpoint;

    /// <param name="keyring">OS keyring for API key resolution.</param>
    /// <param name="endpoint">
    /// Optional endpoint override. Defaults to https://api.z.ai/api/coding/paas/v4.
    /// </param>
    public ZaiCodingProvider(IKeyring keyring, Uri? endpoint = null)
    {
        _keyring = keyring;
        _endpoint = endpoint ?? DefaultEndpoint;
    }

    public string Name => "zai-coding";
    public string DisplayName => "Z.AI Coding Plan";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                $"No API key configured for 'zai-coding'. Run: ur config set-api-key <key> --provider zai-coding");

        var options = new OpenAIClientOptions { Endpoint = _endpoint };
        return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
            .AsIChatClient();
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? $"No API key for 'zai-coding'. Run: ur config set-api-key <key> --provider zai-coding"
            : null;
    }
}
