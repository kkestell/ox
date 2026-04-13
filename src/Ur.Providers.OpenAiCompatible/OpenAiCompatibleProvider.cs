using System.ClientModel;
using Microsoft.Extensions.AI;
using OpenAI;
using Ur.Configuration.Keyring;
using Ur.Providers;

namespace Ur.Providers.OpenAiCompatible;

/// <summary>
/// Generic fallback provider for any API that speaks the OpenAI protocol.
/// Used for custom/unknown provider entries in providers.json — any key that
/// doesn't match a built-in provider name gets routed here.
///
/// The provider name doubles as the OS keyring account for API key storage,
/// following the existing convention: service="ur", account=provider-name.
/// </summary>
public sealed class OpenAiCompatibleProvider : IProvider
{
    private const string SecretService = "ur";

    private readonly string _name;
    private readonly string _displayName;
    private readonly Uri _endpoint;
    private readonly IKeyring _keyring;

    /// <param name="name">
    /// The provider key from providers.json (used as keyring account and model ID prefix).
    /// </param>
    /// <param name="displayName">
    /// Human-readable display name. Falls back to the key if not specified in JSON.
    /// </param>
    /// <param name="endpoint">
    /// Required endpoint URI — custom providers must specify where to send requests.
    /// </param>
    /// <param name="keyring">OS keyring for API key resolution.</param>
    public OpenAiCompatibleProvider(string name, string displayName, Uri endpoint, IKeyring keyring)
    {
        _name = name;
        _displayName = displayName;
        _endpoint = endpoint;
        _keyring = keyring;
    }

    public string Name => _name;
    public string DisplayName => _displayName;
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, _name)
            ?? throw new InvalidOperationException(
                $"No API key configured for '{_name}'. Run: ur config set-api-key <key> --provider {_name}");

        var options = new OpenAIClientOptions { Endpoint = _endpoint };
        return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
            .AsIChatClient();
    }

    public void ConfigureChatOptions(string model, ChatOptions options)
    {
        // Custom OpenAI-compatible endpoints are part of the same "enable thinking
        // everywhere" contract, so we send the standard reasoning_effort knob by default.
        options.Reasoning ??= new ReasoningOptions { Effort = ReasoningEffort.Low };
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, _name);
        return string.IsNullOrWhiteSpace(key)
            ? $"No API key for '{_name}'. Run: ur config set-api-key <key> --provider {_name}"
            : null;
    }
}
