using System.ClientModel;
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
            // Custom endpoint — three-arg constructor with options.
            var options = new OpenAIClientOptions { Endpoint = _endpoint };
            return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
                .AsIChatClient();
        }

        // Standard OpenAI endpoint — two-arg constructor.
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
}
