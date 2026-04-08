using System.ClientModel;
using Microsoft.Extensions.AI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

/// <summary>
/// Provider for the OpenAI API (direct, not via OpenRouter). Uses the standard
/// OpenAI endpoint — no custom URI needed. API key is stored in the OS keyring
/// under account "openai".
/// </summary>
internal sealed class OpenAiProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "openai";

    private readonly IKeyring _keyring;

    public OpenAiProvider(IKeyring keyring)
    {
        _keyring = keyring;
    }

    public string Name => "openai";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                "No OpenAI API key configured. Run: ur config set-api-key <key> --provider openai");

        return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey))
            .AsIChatClient();
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? "No API key for 'openai'. Run: ur config set-api-key <key> --provider openai"
            : null;
    }
}
