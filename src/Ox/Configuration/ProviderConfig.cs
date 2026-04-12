using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ox.Configuration;

/// <summary>
/// Loads, deserializes, and queries the providers.json configuration file.
///
/// This is the single source of truth for which providers exist, what models
/// they offer, and what their context windows are. The application never writes
/// this file — it is purely user-authored configuration.
///
/// Lives in the Ox (application) layer because providers.json is an application
/// concern — Ur (library) doesn't know about model catalogs. Ox loads the config,
/// constructs providers, and registers them via standard DI.
/// </summary>
public sealed class ProviderConfig
{
    private readonly Dictionary<string, ProviderConfigEntry> _entries;

    // Cached sorted list of all "provider/model" IDs. Built once on construction
    // because the config is immutable — avoids re-allocating and re-sorting on
    // every call to ListAllModelIds().
    private readonly IReadOnlyList<string> _allModelIds;

    private ProviderConfig(Dictionary<string, ProviderConfigEntry> entries)
    {
        _entries = entries;

        var all = new List<string>();
        foreach (var (name, entry) in _entries)
        {
            foreach (var model in entry.Models)
                all.Add($"{name}/{model.Id}");
        }
        all.Sort(StringComparer.OrdinalIgnoreCase);
        _allModelIds = all;
    }

    /// <summary>
    /// All configured provider names (e.g. "openai", "google", "ollama").
    /// </summary>
    public IReadOnlyCollection<string> ProviderNames => _entries.Keys;

    /// <summary>
    /// Returns the config entry for a provider, or null if not configured.
    /// </summary>
    public ProviderConfigEntry? GetEntry(string providerName) =>
        _entries.GetValueOrDefault(providerName);

    /// <summary>
    /// Resolves the context window (max input tokens) for a specific provider + model.
    /// Throws if the provider or model is not found — every model in the system must
    /// have an explicitly configured context window.
    /// </summary>
    public int GetContextWindow(string providerName, string modelId)
    {
        if (!_entries.TryGetValue(providerName, out var entry))
            throw new InvalidOperationException(
                $"Provider '{providerName}' not found in providers.json.");

        var model = entry.Models.FirstOrDefault(m =>
            string.Equals(m.Id, modelId, StringComparison.OrdinalIgnoreCase));

        if (model is null)
            throw new InvalidOperationException(
                $"Model '{modelId}' not found under provider '{providerName}' in providers.json.");

        return model.ContextIn;
    }

    /// <summary>
    /// Returns model IDs for a specific provider, or null if the provider is not configured.
    /// </summary>
    public IReadOnlyList<string>? ListModelIds(string providerName)
    {
        if (!_entries.TryGetValue(providerName, out var entry))
            return null;

        return entry.Models.Select(m => m.Id).ToList();
    }

    /// <summary>
    /// Returns all model IDs across all providers, prefixed with the provider name
    /// (e.g. "openai/gpt-4o"), sorted alphabetically for stable display order.
    /// The list is built once at construction time — no allocation on each call.
    /// </summary>
    public IReadOnlyList<string> ListAllModelIds() => _allModelIds;

    /// <summary>
    /// Loads and validates providers.json from the given path.
    ///
    /// Throws <see cref="FileNotFoundException"/> if the file doesn't exist.
    /// Throws <see cref="InvalidOperationException"/> if the JSON is malformed,
    /// any model entry is missing a valid <c>context_in</c>, or a URL is invalid.
    /// </summary>
    public static ProviderConfig Load(string path)
    {
        string json;
        try
        {
            json = File.ReadAllText(path);
        }
        catch (FileNotFoundException)
        {
            throw new FileNotFoundException(
                $"providers.json not found at '{path}'. " +
                "Create it to configure your model providers.",
                path);
        }

        var wrapper = JsonSerializer.Deserialize(json, ProviderConfigJsonContext.Default.ProviderConfigRoot);

        if (wrapper?.Providers is not { } raw || raw.Count == 0)
            throw new InvalidOperationException(
                $"Failed to parse providers.json at '{path}' — the file is empty or contains invalid JSON.");

        foreach (var (providerName, entry) in raw)
        {
            if (entry.Models is null || entry.Models.Count == 0)
                throw new InvalidOperationException(
                    $"Provider '{providerName}' in providers.json has no models defined.");

            // Parse and cache the endpoint URI so consumers don't repeat the work.
            if (entry.Url is not null)
            {
                if (!Uri.TryCreate(entry.Url, UriKind.Absolute, out var uri))
                    throw new InvalidOperationException(
                        $"Provider '{providerName}' in providers.json has invalid URL: '{entry.Url}'.");
                entry.Endpoint = uri;
            }

            foreach (var model in entry.Models)
            {
                if (model.ContextIn <= 0)
                    throw new InvalidOperationException(
                        $"Model '{model.Id}' under provider '{providerName}' in providers.json " +
                        $"has invalid context_in: {model.ContextIn}. A positive value is required.");
            }
        }

        return new ProviderConfig(raw);
    }
}

/// <summary>
/// Root shape of providers.json: <c>{ "providers": { ... } }</c>.
/// </summary>
internal sealed class ProviderConfigRoot
{
    [JsonPropertyName("providers")]
    public Dictionary<string, ProviderConfigEntry>? Providers { get; set; }
}

/// <summary>
/// A single provider entry from providers.json. Declares the provider type,
/// optional custom endpoint URL, and the list of available models.
/// </summary>
public sealed class ProviderConfigEntry
{
    /// <summary>
    /// Human-readable display name for the provider (e.g. "OpenAI", "Z.AI Coding Plan").
    /// Used by the connect wizard's provider selection step. Falls back to the provider
    /// key when absent so old providers.json files continue to work.
    /// </summary>
    [JsonPropertyName("name")]
    public string? Name { get; set; }

    [JsonPropertyName("type")]
    public string Type { get; set; } = "";

    [JsonPropertyName("url")]
    public string? Url { get; set; }

    [JsonPropertyName("models")]
    public List<ProviderModelEntry> Models { get; set; } = [];

    /// <summary>
    /// Pre-parsed URI from <see cref="Url"/>. Set during <see cref="ProviderConfig.Load"/>
    /// validation so consumers don't repeat URI parsing. Null when no URL is configured.
    /// </summary>
    [JsonIgnore]
    public Uri? Endpoint { get; internal set; }
}

/// <summary>
/// A single model entry within a provider. The <see cref="ContextIn"/> field
/// is mandatory — there are no fallback paths for unknown context windows.
/// </summary>
public sealed class ProviderModelEntry
{
    [JsonPropertyName("name")]
    public string Name { get; set; } = "";

    [JsonPropertyName("id")]
    public string Id { get; set; } = "";

    [JsonPropertyName("context_in")]
    public int ContextIn { get; set; }
}

/// <summary>
/// Source-generated JSON serialization context for AoT compatibility.
/// </summary>
[JsonSerializable(typeof(ProviderConfigRoot))]
internal partial class ProviderConfigJsonContext : JsonSerializerContext;
