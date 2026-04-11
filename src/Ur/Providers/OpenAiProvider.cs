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

    /// <summary>
    /// Static context window sizes for known OpenAI models. OpenAI doesn't expose a
    /// public model-metadata API, so we maintain this table manually. Unknown models
    /// return null — the UI omits the percentage rather than displaying a wrong one.
    /// </summary>
    private static readonly Dictionary<string, int> KnownContextWindows = new(StringComparer.OrdinalIgnoreCase)
    {
        ["gpt-4o"] = 128_000,
        ["gpt-4o-mini"] = 128_000,
        ["gpt-4.1"] = 1_047_576,
        ["gpt-4.1-mini"] = 1_047_576,
        ["gpt-4.1-nano"] = 1_047_576,
        ["o3"] = 200_000,
        ["o3-mini"] = 200_000,
        ["o4-mini"] = 200_000,
    };

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

    /// <summary>
    /// Returns context window from a static table of known models. OpenAI has no
    /// public metadata API, so unknown models return null.
    /// </summary>
    public Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default)
    {
        int? result = KnownContextWindows.TryGetValue(model, out var size) ? size : null;
        return Task.FromResult(result);
    }

    /// <summary>
    /// Returns the static set of known OpenAI model IDs. These are the same models
    /// tracked in <see cref="KnownContextWindows"/>.
    /// </summary>
    public Task<IReadOnlyList<string>?> ListModelIdsAsync(CancellationToken ct = default) =>
        Task.FromResult<IReadOnlyList<string>?>(KnownContextWindows.Keys.ToList());
}
