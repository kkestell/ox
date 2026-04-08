using GenerativeAI.Microsoft;
using Microsoft.Extensions.AI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

/// <summary>
/// Provider for Google's Generative AI (Gemini) models. Uses the
/// <see cref="GenerativeAIChatClient"/> from the Google_GenerativeAI.Microsoft
/// package, which directly implements <see cref="IChatClient"/>.
///
/// API key is stored in the OS keyring under account "google".
/// </summary>
internal sealed class GoogleProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "google";

    private readonly IKeyring _keyring;

    public GoogleProvider(IKeyring keyring)
    {
        _keyring = keyring;
    }

    public string Name => "google";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                "No Google API key configured. Run: ur config set-api-key <key> --provider google");

        return new GenerativeAIChatClient(apiKey, model);
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? "No API key for 'google'. Run: ur config set-api-key <key> --provider google"
            : null;
    }
}
