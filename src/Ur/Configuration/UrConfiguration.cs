using System.Text.Json;
using Ur.Configuration.Keyring;
using Ur.Providers;

namespace Ur.Configuration;

/// <summary>
/// The public API surface for reading and writing Ur configuration.
///
/// Configuration is split across two backing stores:
///   • <see cref="Settings"/> — JSON files (user-level and workspace-level) for
///     model selection, extension settings, and other serializable preferences.
///     Workspace values override user values.
///   • <see cref="IKeyring"/> — the OS keyring for secrets (API keys). Secrets
///     are never written to plain-text JSON files.
///
/// This class also exposes <see cref="Readiness"/>, which the UI layer checks
/// before attempting to start a chat turn, and <see cref="AvailableModels"/>,
/// which filters the full model catalog to only those that support text-to-text
/// with tool use — the subset that the agent loop can actually drive.
/// </summary>
public sealed class UrConfiguration
{
    // Settings keys are dot-namespaced to avoid collisions between core and extensions.
    internal const string ModelSettingKey = "ur.model";

    // Keyring service/account identifiers. The OS keyring (macOS Keychain /
    // Linux libsecret) stores the OpenRouter API key under this service+account
    // pair, which is shared across all workspaces.
    private const string SecretService = "ur";
    private const string SecretAccount = "openrouter";

    private readonly ModelCatalog _modelCatalog;
    private readonly Settings _settings;
    private readonly IKeyring _keyring;

    internal UrConfiguration(ModelCatalog modelCatalog, Settings settings, IKeyring keyring)
    {
        _modelCatalog = modelCatalog;
        _settings = settings;
        _keyring = keyring;
    }

    /// <summary>
    /// Checks whether the system has everything needed to run a chat turn
    /// (API key + model selection). The UI should inspect this on startup and
    /// before each turn to provide actionable guidance.
    /// </summary>
    public ChatReadiness Readiness => new(GetBlockingIssues());

    /// <summary>
    /// The currently selected model ID from merged settings, or null if unset.
    /// </summary>
    public string? SelectedModelId => _settings.GetString(ModelSettingKey);

    /// <summary>
    /// Models suitable for the chat interface. Filters the full OpenRouter
    /// catalog to only those that:
    ///   1. Accept text input and produce text output (parsed from the
    ///      "input+input->output+output" modality string).
    ///   2. Support the "tools" parameter (required by the agent loop).
    /// This avoids presenting models that would fail at runtime.
    /// </summary>
    public IReadOnlyList<ModelInfo> AvailableModels => _modelCatalog.Models
        .Where(m => m.Modality is { } mod && mod.Split("->") is [var input, var output] && input.Split('+').Contains("text") && output.Split('+').Contains("text"))
        .Where(m => m.SupportedParameters.Contains("tools", StringComparer.OrdinalIgnoreCase))
        .OrderBy(m => m.Id, StringComparer.OrdinalIgnoreCase)
        .ToList();

    /// <summary>
    /// All models in the raw catalog, sorted by ID. Unlike <see cref="AvailableModels"/>,
    /// this includes models that lack tool support or non-text modalities — useful for
    /// browsing or debugging what OpenRouter offers beyond the chat-capable subset.
    /// </summary>
    public IReadOnlyList<ModelInfo> AllModels => _modelCatalog.Models
        .OrderBy(m => m.Id, StringComparer.OrdinalIgnoreCase)
        .ToList();

    public ModelInfo? GetModel(string modelId) => _modelCatalog.GetModel(modelId);

    public Task RefreshModelsAsync(CancellationToken ct = default) =>
        _modelCatalog.RefreshAsync(ct);

    public Task SetApiKeyAsync(string apiKey, CancellationToken ct = default)
    {
        ct.ThrowIfCancellationRequested();
        _keyring.SetSecret(SecretService, SecretAccount, apiKey);
        return Task.CompletedTask;
    }

    public Task ClearApiKeyAsync(CancellationToken ct = default)
    {
        ct.ThrowIfCancellationRequested();
        _keyring.DeleteSecret(SecretService, SecretAccount);
        return Task.CompletedTask;
    }

    public Task SetSelectedModelAsync(
        string modelId,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settings.SetAsync(ModelSettingKey, JsonSerializer.SerializeToElement(modelId, SettingsJsonContext.Default.String), scope, ct);

    public Task ClearSelectedModelAsync(
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settings.ClearAsync(ModelSettingKey, scope, ct);

    public JsonElement? GetSetting(string key) => _settings.Get(key);

    /// <summary>
    /// Typed accessor for string settings. Returns null if the key is absent or the
    /// stored value is not a JSON string. Prefer this over <see cref="GetSetting"/>
    /// so callers don't need to handle <see cref="JsonElement"/> directly.
    /// </summary>
    public string? GetStringSetting(string key)
    {
        var element = _settings.Get(key);
        return element is { ValueKind: JsonValueKind.String } je ? je.GetString() : null;
    }

    /// <summary>
    /// Typed accessor for boolean settings. Returns null if the key is absent or the
    /// stored value is not a JSON boolean.
    /// </summary>
    public bool? GetBoolSetting(string key)
    {
        var element = _settings.Get(key);
        return element switch
        {
            { ValueKind: JsonValueKind.True } => true,
            { ValueKind: JsonValueKind.False } => false,
            _ => null
        };
    }

    public Task SetSettingAsync(
        string key,
        JsonElement value,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settings.SetAsync(key, value, scope, ct);

    /// <summary>
    /// Typed setter for string settings. Serializes the string to a
    /// <see cref="JsonElement"/> internally so callers don't need to handle JSON.
    /// </summary>
    public Task SetStringSettingAsync(
        string key,
        string value,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settings.SetAsync(key, JsonSerializer.SerializeToElement(value, SettingsJsonContext.Default.String), scope, ct);

    /// <summary>
    /// Typed setter for boolean settings.
    /// </summary>
    public Task SetBoolSettingAsync(
        string key,
        bool value,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settings.SetAsync(key, JsonSerializer.SerializeToElement(value, SettingsJsonContext.Default.Boolean), scope, ct);

    public Task ClearSettingAsync(
        string key,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settings.ClearAsync(key, scope, ct);

    /// <summary>
    /// Retrieves the API key from the OS keyring. Returns null if not configured.
    /// This is the single source of truth for keyring service/account constants.
    /// </summary>
    internal string? GetApiKey() =>
        _keyring.GetSecret(SecretService, SecretAccount);

    private bool HasApiKey() =>
        !string.IsNullOrWhiteSpace(GetApiKey());

    private List<ChatBlockingIssue> GetBlockingIssues()
    {
        var issues = new List<ChatBlockingIssue>();

        if (!HasApiKey())
            issues.Add(ChatBlockingIssue.MissingApiKey);

        if (string.IsNullOrWhiteSpace(SelectedModelId))
            issues.Add(ChatBlockingIssue.MissingModelSelection);

        return issues;
    }
}
