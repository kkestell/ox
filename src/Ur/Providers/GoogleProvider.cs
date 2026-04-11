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

    /// <summary>
    /// Static context window sizes for known Gemini models. All three current preview
    /// models share the same 1 M-token input limit (verified via the models.get API).
    /// Unknown models return null — the UI omits the percentage rather than displaying
    /// a wrong one.
    /// </summary>
    private static readonly Dictionary<string, int> KnownContextWindows = new(StringComparer.OrdinalIgnoreCase)
    {
        ["gemini-3.1-pro-preview"] = 1_048_576,
        ["gemini-3-flash-preview"] = 1_048_576,
        ["gemini-3.1-flash-lite-preview"] = 1_048_576,
    };

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
    /// Returns context window from a static table of known models. Unknown models
    /// return null so the UI can omit the percentage.
    /// </summary>
    public Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default)
    {
        int? result = KnownContextWindows.TryGetValue(model, out var size) ? size : null;
        return Task.FromResult(result);
    }

    /// <summary>
    /// Returns the static set of known Gemini model IDs. These are the same models
    /// tracked in <see cref="KnownContextWindows"/>.
    /// </summary>
    public Task<IReadOnlyList<string>?> ListModelIdsAsync(CancellationToken ct = default) =>
        Task.FromResult<IReadOnlyList<string>?>(KnownContextWindows.Keys.ToList());
}
