using System.ClientModel;
using Microsoft.Extensions.AI;
using OpenAI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

/// <summary>
/// Provider for Z.AI's GLM Coding Plan API. The API follows the OpenAI protocol,
/// so we reuse the standard OpenAI .NET SDK with a custom endpoint pointing at
/// <c>https://api.z.ai/api/coding/paas/v4</c> — the coding-specific base URL
/// (distinct from the general-purpose API at <c>/api/paas/v4</c>).
///
/// API key is stored in the OS keyring under account "zai-coding".
/// </summary>
internal sealed class ZaiCodingProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "zai-coding";

    /// <summary>
    /// The coding-plan endpoint. Z.AI hosts a separate path for coding-optimized
    /// models; all GLM Coding Plan models are served from this single base URL.
    /// </summary>
    private static readonly Uri Endpoint = new("https://api.z.ai/api/coding/paas/v4");

    /// <summary>
    /// Static context window sizes for known GLM Coding Plan models. Z.AI has no
    /// public model-metadata API, so we maintain this table manually from the docs.
    /// Unknown models return null — the UI omits the percentage rather than
    /// displaying a wrong one.
    /// </summary>
    private static readonly Dictionary<string, int> KnownContextWindows = new(StringComparer.OrdinalIgnoreCase)
    {
        ["glm-5.1"] = 200_000,
        ["glm-5-turbo"] = 200_000,
        ["glm-4.7"] = 200_000,
        ["glm-4.5-air"] = 128_000,
    };

    private readonly IKeyring _keyring;

    public ZaiCodingProvider(IKeyring keyring)
    {
        _keyring = keyring;
    }

    public string Name => "zai-coding";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                "No Z.AI API key configured. Run: ur config set-api-key <key> --provider zai-coding");

        // Three-arg constructor: model, credential, options with custom endpoint.
        // This is the only difference from the plain OpenAI provider.
        var options = new OpenAIClientOptions { Endpoint = Endpoint };
        return new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(apiKey), options)
            .AsIChatClient();
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? "No API key for 'zai-coding'. Run: ur config set-api-key <key> --provider zai-coding"
            : null;
    }

    /// <summary>
    /// Returns context window from a static table of known GLM models. Z.AI has no
    /// public metadata API, so unknown models return null.
    /// </summary>
    public Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default)
    {
        int? result = KnownContextWindows.TryGetValue(model, out var size) ? size : null;
        return Task.FromResult(result);
    }
}
