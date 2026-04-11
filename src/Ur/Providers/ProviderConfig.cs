using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ur.Providers;

/// <summary>
/// Loads, deserializes, and queries the providers.json configuration file.
///
/// This is the single source of truth for which providers exist, what models
/// they offer, and what their context windows are. The application never writes
/// this file — it is purely user-authored configuration.
///
/// <see cref="ProviderConfig"/> sits alongside <see cref="ProviderRegistry"/> as
/// an infrastructure singleton. ProviderRegistry maps names to live IProvider
/// instances; ProviderConfig maps names to static metadata (type, URL, models).
/// </summary>
internal sealed class ProviderConfig
{
    private readonly Dictionary<string, ProviderConfigEntry> _entries;

    private ProviderConfig(Dictionary<string, ProviderConfigEntry> entries)
    {
        _entries = entries;
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
    /// </summary>
    public IReadOnlyList<string> ListAllModelIds()
    {
        var all = new List<string>();

        foreach (var (name, entry) in _entries)
        {
            foreach (var model in entry.Models)
                all.Add($"{name}/{model.Id}");
        }

        all.Sort(StringComparer.OrdinalIgnoreCase);
        return all;
    }

    /// <summary>
    /// Loads and validates providers.json from the given path.
    ///
    /// Throws <see cref="FileNotFoundException"/> if the file doesn't exist.
    /// Throws <see cref="InvalidOperationException"/> if the JSON is malformed
    /// or any model entry is missing a valid <c>context_in</c>.
    /// </summary>
    public static ProviderConfig Load(string path)
    {
        if (!File.Exists(path))
            throw new FileNotFoundException(
                $"providers.json not found at '{path}'. " +
                "Create it to configure your model providers.",
                path);

        var json = File.ReadAllText(path);
        var wrapper = JsonSerializer.Deserialize(json, ProviderConfigJsonContext.Default.ProviderConfigRoot);

        if (wrapper?.Providers is not { } raw || raw.Count == 0)
            throw new InvalidOperationException(
                $"Failed to parse providers.json at '{path}' — the file is empty or contains invalid JSON.");

        // Validate every model entry has a positive context_in.
        foreach (var (providerName, entry) in raw)
        {
            if (entry.Models is null || entry.Models.Count == 0)
                throw new InvalidOperationException(
                    $"Provider '{providerName}' in providers.json has no models defined.");

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
internal sealed class ProviderConfigEntry
{
    [JsonPropertyName("type")]
    public string Type { get; set; } = "";

    [JsonPropertyName("url")]
    public string? Url { get; set; }

    [JsonPropertyName("models")]
    public List<ProviderModelEntry> Models { get; set; } = [];
}

/// <summary>
/// A single model entry within a provider. The <see cref="ContextIn"/> field
/// is mandatory — there are no fallback paths for unknown context windows.
/// </summary>
internal sealed class ProviderModelEntry
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
