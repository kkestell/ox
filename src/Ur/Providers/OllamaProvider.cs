using Microsoft.Extensions.AI;
using OllamaSharp;
using Ur.Settings;

namespace Ur.Providers;

/// <summary>
/// Provider for local Ollama models. No API key is needed — Ollama runs locally.
/// The endpoint URI defaults to http://localhost:11434 but can be overridden via
/// the "ollama.uri" setting.
///
/// <see cref="OllamaApiClient"/> directly implements <see cref="IChatClient"/>,
/// so no adapter is needed.
///
/// Takes a <see cref="SettingsWriter"/> rather than <see cref="Configuration.UrConfiguration"/>
/// to avoid a circular dependency: UrConfiguration depends on the ProviderRegistry,
/// and the registry contains this provider.
/// </summary>
internal sealed class OllamaProvider : IProvider
{
    /// <summary>
    /// The settings key for the Ollama endpoint URI. Registered in
    /// <see cref="Hosting.ServiceCollectionExtensions.RegisterCoreSchemas"/>.
    /// </summary>
    internal const string UriSettingKey = "ollama.uri";

    private static readonly Uri DefaultUri = new("http://localhost:11434");

    private readonly SettingsWriter _settingsWriter;

    // Cache context window sizes — Ollama's /api/show is a local call but there's
    // no reason to repeat it for the same model within one session.
    private readonly Dictionary<string, int?> _contextWindowCache = new(StringComparer.OrdinalIgnoreCase);

    // Cache the model list for the session — avoids repeated /api/tags calls.
    private IReadOnlyList<string>? _modelListCache;
    private bool _modelListCached;

    public OllamaProvider(SettingsWriter settingsWriter)
    {
        _settingsWriter = settingsWriter;
    }

    public string Name => "ollama";
    public bool RequiresApiKey => false;

    public IChatClient CreateChatClient(string model)
    {
        var uri = ResolveUri();
        return new OllamaApiClient(uri, model);
    }

    /// <summary>
    /// Ollama needs no API key, so it's always ready. A future enhancement could
    /// ping the Ollama endpoint to verify it's reachable, but that would add
    /// latency to every readiness check.
    /// </summary>
    public string? GetBlockingIssue() => null;

    /// <summary>
    /// Queries the local Ollama instance via OllamaSharp's ShowModelAsync to retrieve
    /// model metadata. The context length lives in <c>Info.ExtraInfo["general.context_length"]</c>
    /// in the /api/show response. Returns null if Ollama is unreachable or the model
    /// doesn't report a context length.
    /// </summary>
    public async Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default)
    {
        if (_contextWindowCache.TryGetValue(model, out var cached))
            return cached;

        try
        {
            var uri = ResolveUri();
            var client = new OllamaApiClient(uri, model);
            var response = await client.ShowModelAsync(model, ct);
            int? result = null;

            // The Ollama API returns model_info.general.context_length in the /api/show
            // response. OllamaSharp maps this to ShowModelResponse.Info.ExtraInfo dictionary.
            if (response?.Info?.ExtraInfo is { } extra
                && extra.TryGetValue("general.context_length", out var val))
            {
                result = Convert.ToInt32(val, System.Globalization.CultureInfo.InvariantCulture);
            }

            _contextWindowCache[model] = result;
            return result;
        }
        catch
        {
            // Ollama unreachable, model not found, or unexpected response shape.
            _contextWindowCache[model] = null;
            return null;
        }
    }

    /// <summary>
    /// Queries the local Ollama instance via OllamaSharp's ListLocalModelsAsync
    /// (hitting /api/tags) and returns installed model names. Returns null if
    /// Ollama is unreachable. Results are cached for the session to avoid
    /// repeated local calls — same pattern as <see cref="_contextWindowCache"/>.
    /// </summary>
    public async Task<IReadOnlyList<string>?> ListModelIdsAsync(CancellationToken ct = default)
    {
        if (_modelListCached)
            return _modelListCache;

        try
        {
            var uri = ResolveUri();
            var client = new OllamaApiClient(uri);
            var models = await client.ListLocalModelsAsync(ct);
            _modelListCache = models.Select(m => m.Name).ToList();
        }
        catch
        {
            // Ollama unreachable — return null, same graceful degradation as
            // GetContextWindowAsync.
            _modelListCache = null;
        }

        _modelListCached = true;
        return _modelListCache;
    }

    private Uri ResolveUri()
    {
        var element = _settingsWriter.Get(UriSettingKey);
        var uriString = element is { ValueKind: System.Text.Json.JsonValueKind.String } je
            ? je.GetString()
            : null;

        if (string.IsNullOrWhiteSpace(uriString))
            return DefaultUri;

        return Uri.TryCreate(uriString, UriKind.Absolute, out var uri)
            ? uri
            : DefaultUri;
    }
}
