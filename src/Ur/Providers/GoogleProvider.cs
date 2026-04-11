using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Serialization;
using GeminiDotnet;
using GeminiDotnet.Extensions.AI;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;
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
    private readonly ILogger? _logger;

    // Cache context window sizes to avoid repeated API calls for the same model.
    // The Gemini models.get endpoint is lightweight, but there's no reason to hit
    // it more than once per model per session.
    private readonly Dictionary<string, int?> _contextWindowCache = new(StringComparer.OrdinalIgnoreCase);

    public GoogleProvider(IKeyring keyring, ILogger? logger = null)
    {
        _keyring = keyring;
        _logger = logger;
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

    // Shared HttpClient for model metadata calls — avoids socket exhaustion.
    private static readonly HttpClient MetadataHttpClient = new()
    {
        BaseAddress = new Uri("https://generativelanguage.googleapis.com")
    };

    /// <summary>
    /// Queries the Gemini models.get API for the model's input token limit.
    ///
    /// Uses a direct HTTP call instead of GeminiDotnet's <c>ModelsClient</c> because
    /// GeminiDotnet (as of 0.23.0) marks <c>baseModelId</c> as <c>required</c> on its
    /// <c>Model</c> type. The Gemini API only returns <c>baseModelId</c> for tuned
    /// models — base models (all the ones we use) never include it, so
    /// <c>GetModelAsync</c> always throws a <see cref="System.Text.Json.JsonException"/>.
    /// A minimal JSON projection that only reads <c>inputTokenLimit</c> sidesteps the
    /// issue without forking the library.
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
            var url = $"/v1beta/models/{model}?key={apiKey}";
            var response = await MetadataHttpClient.GetFromJsonAsync(
                url, GoogleProviderJsonContext.Default.GeminiModelMetadata, ct);
            var result = response?.InputTokenLimit;
            _contextWindowCache[model] = result;
            return result;
        }
        catch (Exception ex)
        {
            // API failure (network error, invalid model name, etc.) — return null
            // so the caller can omit the percentage rather than crash.
            _logger?.LogWarning(ex, "Failed to resolve context window for model '{Model}'", model);
            _contextWindowCache[model] = null;
            return null;
        }
    }

    /// <summary>
    /// Google's model listing API exists but requires additional integration
    /// (pagination, filtering) that isn't worth the complexity for this round.
    /// Returns null to indicate listing is not supported.
    /// </summary>
    public Task<IReadOnlyList<string>?> ListModelIdsAsync(CancellationToken ct = default) =>
        Task.FromResult<IReadOnlyList<string>?>(null);

    /// <summary>
    /// Minimal projection of the Gemini models.get response. We only need
    /// <c>inputTokenLimit</c>, so this avoids GeminiDotnet's <c>Model</c> type
    /// which fails to deserialize base models (see <see cref="GetContextWindowAsync"/>).
    /// </summary>
    internal sealed class GeminiModelMetadata
    {
        [JsonPropertyName("inputTokenLimit")]
        public int? InputTokenLimit { get; set; }
    }
}

/// <summary>
/// Source-generated JSON context for the lightweight Gemini model metadata shape.
/// </summary>
[JsonSerializable(typeof(GoogleProvider.GeminiModelMetadata))]
internal partial class GoogleProviderJsonContext : JsonSerializerContext;
