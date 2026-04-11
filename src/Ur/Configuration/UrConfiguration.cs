using System.Text.Json;
using Microsoft.Extensions.Options;
using Ur.Configuration.Keyring;
using Ur.Providers;
using Ur.Settings;

namespace Ur.Configuration;

/// <summary>
/// The public API surface for reading and writing Ur configuration.
///
/// Configuration is split across three backing stores:
///   • <see cref="IOptionsMonitor{UrOptions}"/> — strongly-typed core settings
///     (model selection) backed by <see cref="Microsoft.Extensions.Configuration.IConfiguration"/>.
///   • <see cref="SettingsWriter"/> — reads and writes arbitrary dot-namespaced
///     settings to nested JSON files (user-level and workspace-level).
///     Workspace values override user values via IConfiguration's "last source wins" rule.
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
    // The dot-namespaced key used by the CLI/TUI and SettingsWriter for model selection.
    // Maps to IConfiguration path "ur:model" via the nested JSON format.
    internal const string ModelSettingKey = "ur.model";

    // Keyring service name shared by all providers. Each provider stores its
    // API key under service="ur", account=<provider-name> (e.g. "openrouter",
    // "openai", "google"). Ollama needs no key.
    private const string SecretService = "ur";

    private readonly ModelCatalog _modelCatalog;
    private readonly IOptionsMonitor<UrOptions> _optionsMonitor;
    private readonly SettingsWriter _settingsWriter;
    private readonly IKeyring _keyring;
    private readonly ProviderRegistry _providerRegistry;

    // Ephemeral model override from startup options. When set, takes priority
    // over persisted settings so test mode (--fake-provider) can select a model
    // without rewriting settings files or keyring state.
    private readonly string? _selectedModelOverride;

    internal UrConfiguration(
        ModelCatalog modelCatalog,
        IOptionsMonitor<UrOptions> optionsMonitor,
        SettingsWriter settingsWriter,
        IKeyring keyring,
        ProviderRegistry providerRegistry,
        string? selectedModelOverride = null)
    {
        _modelCatalog = modelCatalog;
        _optionsMonitor = optionsMonitor;
        _settingsWriter = settingsWriter;
        _keyring = keyring;
        _providerRegistry = providerRegistry;
        _selectedModelOverride = selectedModelOverride;
    }

    /// <summary>
    /// Checks whether the system has everything needed to run a chat turn
    /// (API key + model selection). The UI should inspect this on startup and
    /// before each turn to provide actionable guidance.
    /// </summary>
    public ChatReadiness Readiness => new(GetBlockingIssues());

    /// <summary>
    /// The currently selected model ID. When a startup override is set (e.g.
    /// --fake-provider mode), that takes priority. Otherwise reads from
    /// IOptionsMonitor which tracks the "ur:model" configuration key across
    /// both user and workspace settings files, with workspace taking priority.
    /// </summary>
    public string? SelectedModelId =>
        _selectedModelOverride ?? _optionsMonitor.CurrentValue.Model;

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

    /// <summary>
    /// Stores an API key in the OS keyring for the given provider.
    /// The keyring account is the provider name (e.g. "openai", "google", "openrouter").
    /// </summary>
    public Task SetApiKeyAsync(string apiKey, string provider = "openrouter", CancellationToken ct = default)
    {
        ct.ThrowIfCancellationRequested();
        _keyring.SetSecret(SecretService, provider, apiKey);
        return Task.CompletedTask;
    }

    /// <summary>
    /// Removes the API key from the OS keyring for the given provider.
    /// </summary>
    public Task ClearApiKeyAsync(string provider = "openrouter", CancellationToken ct = default)
    {
        ct.ThrowIfCancellationRequested();
        _keyring.DeleteSecret(SecretService, provider);
        return Task.CompletedTask;
    }

    public Task SetSelectedModelAsync(
        string modelId,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settingsWriter.SetAsync(ModelSettingKey, JsonSerializer.SerializeToElement(modelId, SettingsJsonContext.Default.String), scope, ct);

    public Task ClearSelectedModelAsync(
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settingsWriter.ClearAsync(ModelSettingKey, scope, ct);

    public JsonElement? GetSetting(string key) => _settingsWriter.Get(key);

    /// <summary>
    /// Typed accessor for string settings. Returns null if the key is absent or the
    /// stored value is not a JSON string. Prefer this over <see cref="GetSetting"/>
    /// so callers don't need to handle <see cref="JsonElement"/> directly.
    /// </summary>
    public string? GetStringSetting(string key)
    {
        var element = _settingsWriter.Get(key);
        return element is { ValueKind: JsonValueKind.String } je ? je.GetString() : null;
    }

    /// <summary>
    /// Typed accessor for boolean settings. Returns null if the key is absent or the
    /// stored value is not a JSON boolean.
    /// </summary>
    public bool? GetBoolSetting(string key)
    {
        var element = _settingsWriter.Get(key);
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
        _settingsWriter.SetAsync(key, value, scope, ct);

    /// <summary>
    /// Typed setter for string settings. Serializes the string to a
    /// <see cref="JsonElement"/> internally so callers don't need to handle JSON.
    /// </summary>
    public Task SetStringSettingAsync(
        string key,
        string value,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settingsWriter.SetAsync(key, JsonSerializer.SerializeToElement(value, SettingsJsonContext.Default.String), scope, ct);

    /// <summary>
    /// Typed setter for boolean settings.
    /// </summary>
    public Task SetBoolSettingAsync(
        string key,
        bool value,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settingsWriter.SetAsync(key, JsonSerializer.SerializeToElement(value, SettingsJsonContext.Default.Boolean), scope, ct);

    public Task ClearSettingAsync(
        string key,
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settingsWriter.ClearAsync(key, scope, ct);

    /// <summary>
    /// Retrieves the API key for the given provider from the OS keyring.
    /// Returns null if not configured. Each provider stores its key under
    /// the provider name as the keyring account (e.g. "openai", "google").
    /// </summary>
    internal string? GetApiKey(string provider = "openrouter") =>
        _keyring.GetSecret(SecretService, provider);

    /// <summary>
    /// Exposes the provider registry so the UI layer can look up a provider
    /// by name for readiness checks and error messages.
    /// </summary>
    internal ProviderRegistry ProviderRegistry => _providerRegistry;

    /// <summary>
    /// Extracts the provider name from the currently selected model ID.
    /// Returns null if no model is selected or the model ID doesn't contain a slash.
    /// Used by the TUI to determine which provider needs an API key without
    /// duplicating the ModelId parsing logic.
    /// </summary>
    public string? GetSelectedProviderName()
    {
        var modelId = SelectedModelId;
        if (string.IsNullOrWhiteSpace(modelId))
            return null;

        try
        {
            return ModelId.Parse(modelId).Provider;
        }
        catch (ArgumentException)
        {
            return null;
        }
    }

    /// <summary>
    /// Checks whether the selected model's provider is known and registered.
    /// Returns false if no model is selected, the model ID can't be parsed,
    /// or the provider prefix doesn't match any registered provider.
    /// </summary>
    public bool IsSelectedProviderKnown()
    {
        var providerName = GetSelectedProviderName();
        return providerName is not null && _providerRegistry.Get(providerName) is not null;
    }

    /// <summary>
    /// Returns a human-readable description of why the selected provider is not ready.
    /// Used by CLI/TUI to show actionable error messages. Returns a generic message
    /// if the model ID can't be parsed or the provider is unknown.
    /// </summary>
    public string GetProviderBlockingMessage()
    {
        var modelId = SelectedModelId;
        if (string.IsNullOrWhiteSpace(modelId))
            return "No model selected.";

        try
        {
            var parsed = ModelId.Parse(modelId);
            var provider = _providerRegistry.Get(parsed.Provider);
            if (provider is null)
                return $"Unknown provider '{parsed.Provider}'. Known providers: {string.Join(", ", _providerRegistry.ProviderNames)}";

            return provider.GetBlockingIssue()
                ?? "Provider is ready (no blocking issue).";
        }
        catch (ArgumentException)
        {
            return $"Model ID '{modelId}' is not in 'provider/model' format. Example: openai/gpt-5-nano";
        }
    }

    private List<ChatBlockingIssue> GetBlockingIssues()
    {
        var issues = new List<ChatBlockingIssue>();

        if (string.IsNullOrWhiteSpace(SelectedModelId))
        {
            // No model selected — can't determine a provider, so report just this issue.
            issues.Add(ChatBlockingIssue.MissingModelSelection);
            return issues;
        }

        // A model is selected — check whether its provider is ready.
        // If the model ID doesn't parse or the provider is unknown, report as
        // a provider issue so the user gets actionable feedback.
        try
        {
            var parsed = ModelId.Parse(SelectedModelId);
            var provider = _providerRegistry.Get(parsed.Provider);
            if (provider is null)
            {
                issues.Add(ChatBlockingIssue.ProviderNotReady);
            }
            else
            {
                var issue = provider.GetBlockingIssue();
                if (issue is not null)
                    issues.Add(ChatBlockingIssue.ProviderNotReady);
            }
        }
        catch (ArgumentException)
        {
            // Model ID doesn't follow provider/model format — legacy model or typo.
            issues.Add(ChatBlockingIssue.ProviderNotReady);
        }

        return issues;
    }
}
