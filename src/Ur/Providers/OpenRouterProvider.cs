using System.ClientModel;
using Microsoft.Extensions.AI;
using OpenAI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

/// <summary>
/// Provider for OpenRouter — routes requests through OpenRouter's OpenAI-compatible
/// API endpoint. Model IDs are OpenRouter's own slash-delimited namespacing
/// (e.g. "anthropic/claude-3.5-sonnet"), so the full model ID from our side is
/// "openrouter/anthropic/claude-3.5-sonnet".
///
/// Also owns the <see cref="ModelCatalog"/>, since OpenRouter is the only provider
/// with a browsable remote model catalog.
/// </summary>
internal sealed class OpenRouterProvider : IProvider
{
    private static readonly Uri OpenRouterEndpoint = new("https://openrouter.ai/api/v1");

    private const string SecretService = "ur";
    private const string KeyringAccount = "openrouter";

    private readonly IKeyring _keyring;
    private readonly ModelCatalog _modelCatalog;

    public OpenRouterProvider(IKeyring keyring, ModelCatalog modelCatalog)
    {
        _keyring = keyring;
        _modelCatalog = modelCatalog;
    }

    public string Name => "openrouter";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                "No OpenRouter API key configured. Run: ur config set-api-key <key> --provider openrouter");

        var options = new OpenAIClientOptions { Endpoint = OpenRouterEndpoint };
        return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
            .AsIChatClient();
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? "No API key for 'openrouter'. Run: ur config set-api-key <key> --provider openrouter"
            : null;
    }

    /// <summary>
    /// Reads context window from the locally cached OpenRouter model catalog.
    /// No network call needed — the catalog is populated at startup and periodically refreshed.
    /// </summary>
    public Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default)
    {
        var info = _modelCatalog.GetModel(model);
        int? result = info is { ContextLength: > 0 } ? info.ContextLength : null;
        return Task.FromResult(result);
    }
}
