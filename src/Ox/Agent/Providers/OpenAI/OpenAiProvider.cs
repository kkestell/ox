using System.ClientModel;
using Microsoft.Extensions.AI;
using global::OpenAI;
using Ox.Agent.Configuration.Keyring;
using Ox.Agent.Providers;

namespace Ox.Agent.Providers.OpenAI;

/// <summary>
/// Provider for the OpenAI API (GPT models). Uses the standard OpenAI endpoint
/// by default, but accepts an optional endpoint override for custom deployments.
///
/// API key is stored in the OS keyring under service="ur", account="openai".
/// </summary>
internal sealed class OpenAiProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "openai";

    private readonly IKeyring _keyring;
    private readonly Uri? _endpoint;

    /// <param name="keyring">OS keyring for API key resolution.</param>
    /// <param name="endpoint">
    /// Optional endpoint override. When null, the standard OpenAI endpoint is used.
    /// </param>
    public OpenAiProvider(IKeyring keyring, Uri? endpoint = null)
    {
        _keyring = keyring;
        _endpoint = endpoint;
    }

    public string Name => "openai";
    public string DisplayName => "OpenAI";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                $"No API key configured for 'openai'. Run: ur config set-api-key <key> --provider openai");

        if (_endpoint is not null)
        {
            var options = new OpenAIClientOptions { Endpoint = _endpoint };
            return new global::OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
                .AsIChatClient();
        }

        // Standard OpenAI endpoint — no custom options needed.
        return new global::OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey))
            .AsIChatClient();
    }

    public void ConfigureChatOptions(string model, ChatOptions options)
    {
        // The current OpenAI path uses Chat Completions via MEAI's OpenAI adapter.
        // In live headless/TUI runs we always attach the tool schema, and OpenAI
        // rejects reasoning_effort + tools on this endpoint with HTTP 400 for GPT-5.x.
        //
        // Until this provider moves to the Responses API (or a tool-free request path),
        // enabling ReasoningOptions here would break normal turns outright. Leave the
        // provider as a no-op so OpenAI remains usable while the providers that do
        // expose visible thinking continue to opt in.
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? $"No API key for 'openai'. Run: ur config set-api-key <key> --provider openai"
            : null;
    }
}
