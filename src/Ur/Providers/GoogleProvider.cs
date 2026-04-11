using GeminiDotnet;
using GeminiDotnet.Extensions.AI;
using Microsoft.Extensions.AI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

/// <summary>
/// Provider for Google's Generative AI (Gemini) models. Uses the AoT-compatible
/// <see cref="GeminiChatClient"/> from the GeminiDotnet.Extensions.AI package,
/// which directly implements <see cref="IChatClient"/>.
///
/// API key is stored in the OS keyring under account "google".
/// </summary>
internal sealed class GoogleProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "google";

    private readonly IKeyring _keyring;

    // Cache context window sizes to avoid repeated API calls for the same model.
    // The Gemini models.get endpoint is lightweight, but there's no reason to hit
    // it more than once per model per session.
    private readonly Dictionary<string, int?> _contextWindowCache = new(StringComparer.OrdinalIgnoreCase);

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

        return new GeminiChatClient(new GeminiClientOptions
        {
            ApiKey = apiKey,
            ModelId = model
        });
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? "No API key for 'google'. Run: ur config set-api-key <key> --provider google"
            : null;
    }

    /// <summary>
    /// Queries the Gemini models.get API for the model's input token limit.
    /// Creates a lightweight <see cref="GeminiClient"/> (separate from the chat client)
    /// since we only need the models endpoint, not a full chat session.
    /// </summary>
    public async Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default)
    {
        if (_contextWindowCache.TryGetValue(model, out var cached))
            return cached;

        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount);
        if (string.IsNullOrWhiteSpace(apiKey))
            return null;

        try
        {
            var client = new GeminiClient(new GeminiClientOptions { ApiKey = apiKey });
            var modelInfo = await client.V1Beta.Models.GetModelAsync(model, ct);
            var result = modelInfo.InputTokenLimit;
            _contextWindowCache[model] = result;
            return result;
        }
        catch
        {
            // API failure (network error, invalid model name, etc.) — return null
            // so the caller can omit the percentage rather than crash.
            _contextWindowCache[model] = null;
            return null;
        }
    }
}
