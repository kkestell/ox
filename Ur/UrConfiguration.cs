using System.Text.Json;
using Ur.Configuration;
using Ur.Configuration.Keyring;
using Ur.Providers;

namespace Ur;

public sealed class UrConfiguration
{
    private const string ModelSettingKey = "ur.model";
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

    public UrChatReadiness Readiness => new(GetBlockingIssues());

    public string? SelectedModelId => _settings.Get<string>(ModelSettingKey);

    public IReadOnlyList<ModelInfo> AvailableModels => _modelCatalog.Models
        .Where(m => m.Modality is { } mod && mod.Split("->") is [var input, var output] && input.Split('+').Contains("text") && output.Split('+').Contains("text"))
        .Where(m => m.SupportedParameters.Contains("tools", StringComparer.OrdinalIgnoreCase))
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
        _settings.SetAsync(ModelSettingKey, JsonSerializer.SerializeToElement(modelId), scope, ct);

    public Task ClearSelectedModelAsync(
        ConfigurationScope scope = ConfigurationScope.User,
        CancellationToken ct = default) =>
        _settings.ClearAsync(ModelSettingKey, scope, ct);

    internal bool HasApiKey() =>
        !string.IsNullOrWhiteSpace(_keyring.GetSecret(SecretService, SecretAccount));

    private List<UrChatBlockingIssue> GetBlockingIssues()
    {
        var issues = new List<UrChatBlockingIssue>();

        if (!HasApiKey())
            issues.Add(UrChatBlockingIssue.MissingApiKey);

        if (string.IsNullOrWhiteSpace(SelectedModelId))
            issues.Add(UrChatBlockingIssue.MissingModelSelection);

        return issues;
    }
}
