using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ur.Providers;

/// <summary>
/// Communicates whether the model catalog has been successfully populated.
/// Replaces the sentinel pattern of checking <c>Models.Count &gt; 0</c>.
/// </summary>
public enum CatalogState
{
    /// <summary>No load has been attempted, or the cache was missing.</summary>
    Empty,
    /// <summary>Models were successfully loaded from cache or API.</summary>
    Loaded,
    /// <summary>A load was attempted but failed (corrupt cache, network error).</summary>
    LoadFailed
}

/// <summary>
/// Fetches and caches model metadata from the OpenRouter API.
/// </summary>
public sealed class ModelCatalog
{
    private static readonly Uri ModelsEndpoint = new("https://openrouter.ai/api/v1/models");

    // Reuse a single HttpClient to avoid socket exhaustion and DNS caching issues.
    private static readonly HttpClient SharedHttpClient = new();

    private readonly string _cachePath;
    private Dictionary<string, ModelInfo> _models = new(StringComparer.OrdinalIgnoreCase);

    public ModelCatalog(string cacheDirectory)
    {
        Directory.CreateDirectory(cacheDirectory);
        _cachePath = Path.Combine(cacheDirectory, "models.json");
    }

    /// <summary>
    /// All cached models. Empty until LoadCacheAsync or RefreshAsync is called.
    /// </summary>
    public IReadOnlyCollection<ModelInfo> Models => _models.Values;

    /// <summary>
    /// Explicit load state so callers don't need to infer readiness from
    /// <c>Models.Count &gt; 0</c>. Set on every load/refresh attempt.
    /// </summary>
    public CatalogState State { get; private set; } = CatalogState.Empty;

    /// <summary>
    /// Convenience property: true when models have been successfully loaded.
    /// </summary>
    public bool IsReady => State == CatalogState.Loaded;

    /// <summary>
    /// Look up a model by ID. Returns null if not in catalog.
    /// </summary>
    public ModelInfo? GetModel(string id) =>
        _models.GetValueOrDefault(id);

    /// <summary>
    /// Loads models from disk cache. Returns true if cache was loaded, false if missing or corrupt.
    /// Sets <see cref="State"/> to reflect the outcome.
    /// </summary>
    public bool LoadCache()
    {
        if (!File.Exists(_cachePath))
            return false;

        try
        {
            var json = File.ReadAllText(_cachePath);
            var entries = JsonSerializer.Deserialize(json, ModelCatalogJsonContext.Default.ListOpenRouterModel);
            if (entries is null)
            {
                State = CatalogState.LoadFailed;
                return false;
            }

            // If no entries have modality data, cache is from before that field was added.
            if (entries.All(e => e.Architecture is null))
            {
                State = CatalogState.LoadFailed;
                return false;
            }

            _models = BuildIndex(entries.Select(ToModelInfo));
            State = CatalogState.Loaded;
            return true;
        }
        catch (JsonException)
        {
            // Corrupt cache — caller should re-fetch.
            State = CatalogState.LoadFailed;
            return false;
        }
    }

    /// <summary>
    /// Fetches the model list from the OpenRouter API and updates the disk cache.
    /// Sets <see cref="State"/> to <see cref="CatalogState.Loaded"/> on success.
    /// </summary>
    public async Task RefreshAsync(CancellationToken ct = default)
    {
        var response = await SharedHttpClient.GetFromJsonAsync(
            ModelsEndpoint,
            ModelCatalogJsonContext.Default.OpenRouterModelsResponse,
            ct);

        if (response?.Data is null)
            return;

        // Write raw API response entries to cache.
        var cacheJson = JsonSerializer.Serialize(response.Data, ModelCatalogJsonContext.Default.ListOpenRouterModel);
        await File.WriteAllTextAsync(_cachePath, cacheJson, ct);

        _models = BuildIndex(response.Data.Select(ToModelInfo));
        State = CatalogState.Loaded;
    }

    private static ModelInfo ToModelInfo(OpenRouterModel m) => new(
        Id: m.Id,
        Name: m.Name ?? m.Id,
        ContextLength: m.ContextLength,
        MaxOutputTokens: m.TopProvider?.MaxCompletionTokens ?? 0,
        InputCostPerToken: ParseDecimal(m.Pricing?.Prompt),
        OutputCostPerToken: ParseDecimal(m.Pricing?.Completion),
        SupportedParameters: m.SupportedParameters ?? [],
        Modality: m.Architecture?.Modality);

    private static decimal ParseDecimal(string? s) =>
        decimal.TryParse(s, System.Globalization.NumberStyles.Any, System.Globalization.CultureInfo.InvariantCulture, out var v)
            ? v
            : 0m;

    private static Dictionary<string, ModelInfo> BuildIndex(IEnumerable<ModelInfo> models)
    {
        var dict = new Dictionary<string, ModelInfo>(StringComparer.OrdinalIgnoreCase);
        foreach (var m in models)
            dict[m.Id] = m;
        return dict;
    }

    // --- JSON shapes matching the OpenRouter API response ---
    // A single type is used for both API deserialization and disk cache
    // (the cache stores raw API entries, so the shape is identical).

    internal sealed class OpenRouterModelsResponse
    {
        [JsonPropertyName("data")]
        public List<OpenRouterModel>? Data { get; set; }
    }

    internal sealed class OpenRouterModel
    {
        [JsonPropertyName("id")]
        public string Id { get; set; } = "";

        [JsonPropertyName("name")]
        public string? Name { get; set; }

        [JsonPropertyName("context_length")]
        public int ContextLength { get; set; }

        [JsonPropertyName("pricing")]
        public PricingInfo? Pricing { get; set; }

        [JsonPropertyName("top_provider")]
        public TopProviderInfo? TopProvider { get; set; }

        [JsonPropertyName("supported_parameters")]
        public List<string>? SupportedParameters { get; set; }

        [JsonPropertyName("architecture")]
        public ArchitectureInfo? Architecture { get; set; }
    }

    internal sealed class ArchitectureInfo
    {
        [JsonPropertyName("modality")]
        public string? Modality { get; set; }
    }

    internal sealed class PricingInfo
    {
        [JsonPropertyName("prompt")]
        public string? Prompt { get; set; }

        [JsonPropertyName("completion")]
        public string? Completion { get; set; }
    }

    internal sealed class TopProviderInfo
    {
        [JsonPropertyName("max_completion_tokens")]
        public int? MaxCompletionTokens { get; set; }
    }
}

/// <summary>
/// Source-generated JSON serialization context for AoT compatibility.
/// </summary>
[JsonSerializable(typeof(ModelCatalog.OpenRouterModelsResponse))]
[JsonSerializable(typeof(List<ModelCatalog.OpenRouterModel>))]
internal partial class ModelCatalogJsonContext : JsonSerializerContext;
