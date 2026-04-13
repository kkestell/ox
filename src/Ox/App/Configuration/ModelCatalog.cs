using Ox.Agent.Providers;
using Ox.Agent.Providers.Fake;

namespace Ox.App.Configuration;

/// <summary>
/// Application-level configuration facade for model catalog queries.
///
/// Wraps <see cref="ProviderConfig"/> (static metadata from providers.json) and
/// the live <see cref="IProvider"/> instances (for runtime checks like API key
/// readiness and FakeProvider context windows).
///
/// Lives in the Ox layer because model catalogs are an application concern — Ur
/// handles sessions, agent loops, and provider abstraction but doesn't know which
/// models exist. OxApp and HeadlessRunner use this for autocomplete, wizard flows,
/// and context window display.
/// </summary>
public sealed class ModelCatalog
{
    private readonly ProviderConfig _providerConfig;
    private readonly IReadOnlyDictionary<string, IProvider> _providers;

    public ModelCatalog(ProviderConfig providerConfig, IEnumerable<IProvider> providers)
    {
        _providerConfig = providerConfig;
        _providers = providers.ToDictionary(p => p.Name, StringComparer.OrdinalIgnoreCase);
    }

    /// <summary>
    /// All "provider/model" strings across all providers in providers.json, sorted
    /// alphabetically. Used for autocomplete in the TUI.
    /// </summary>
    public IReadOnlyList<string> ListAllModelIds() =>
        _providerConfig.ListAllModelIds();

    /// <summary>
    /// Returns all configured providers as (key, display name) pairs in the order
    /// they appear in providers.json. Used by the connect wizard's provider step.
    /// Display names come from the live <see cref="IProvider.DisplayName"/> property
    /// so providers own their human-readable name instead of duplicating it in JSON.
    /// </summary>
    public IReadOnlyList<(string Key, string DisplayName)> ListProviders()
    {
        return _providerConfig.ProviderNames
            .Select(key =>
            {
                // Prefer the live provider's DisplayName; fall back to the config key
                // for providers that aren't registered (shouldn't normally happen).
                var displayName = _providers.TryGetValue(key, out var p) ? p.DisplayName : key;
                return (key, displayName);
            })
            .ToList();
    }

    /// <summary>
    /// Returns models available from a given provider as (id, display name) pairs.
    /// Used by the connect wizard's model selection step. Returns an empty list
    /// when the provider key is not found.
    /// </summary>
    public IReadOnlyList<(string Id, string Name)> ListModelsForProvider(string providerKey)
    {
        var entry = _providerConfig.GetEntry(providerKey);
        if (entry is null) return [];
        return entry.Models.Select(m => (m.Id, m.Name)).ToList();
    }

    /// <summary>
    /// Returns true when the named provider requires an API key. Delegates to the
    /// matching <see cref="IProvider"/> instance from DI. Defaults to true for
    /// unknown providers so the wizard always prompts.
    /// </summary>
    public bool ProviderRequiresApiKey(string providerKey) =>
        _providers.GetValueOrDefault(providerKey)?.RequiresApiKey ?? true;

    /// <summary>
    /// Resolves the context window size for a "provider/model" ID.
    ///
    /// First checks <see cref="ProviderConfig"/> (covers all providers.json entries),
    /// then falls back to the <see cref="IProvider"/> instance (covers
    /// <see cref="FakeProvider"/> which declares context windows on its scenarios
    /// so compaction can be tested without providers.json entries).
    ///
    /// Returns null if the model ID can't be parsed or the provider/model is not found.
    /// </summary>
    public int? ResolveContextWindow(string modelId)
    {
        try
        {
            var parsed = ModelId.Parse(modelId);

            // First check the static providers.json config (covers all real providers).
            try
            {
                return _providerConfig.GetContextWindow(parsed.Provider, parsed.Model);
            }
            catch (InvalidOperationException) { /* not in static config, fall through */ }

            // Fall back to the provider itself — the fake provider declares context
            // windows on its scenarios so compaction can be tested without a real
            // model entry in providers.json.
            if (_providers.GetValueOrDefault(parsed.Provider) is FakeProvider)
                return FakeProvider.GetContextWindow(parsed.Model);

            return null;
        }
        catch (ArgumentException)
        {
            // Model ID didn't parse (e.g. no "provider/" prefix).
            return null;
        }
    }
}
