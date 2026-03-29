using System.Text.Json;

namespace Ur.Providers;

/// <summary>
/// The static catalog of providers and models.
/// Loaded early in startup; provides model settings schemas to configuration.
/// </summary>
public sealed class ProviderRegistry
{
    private readonly Dictionary<string, ProviderDefinition> _providers = new(StringComparer.OrdinalIgnoreCase);
    private readonly Dictionary<string, ModelDefinition> _models = new(StringComparer.OrdinalIgnoreCase);

    public void AddProvider(ProviderDefinition provider)
    {
        _providers[provider.Id] = provider;
    }

    public void AddModel(ModelDefinition model)
    {
        _models[model.Id] = model;
    }

    public ProviderDefinition? GetProvider(string id) =>
        _providers.GetValueOrDefault(id);

    public ModelDefinition? GetModel(string id) =>
        _models.GetValueOrDefault(id);

    public IEnumerable<ProviderDefinition> Providers => _providers.Values;
    public IEnumerable<ModelDefinition> Models => _models.Values;

    /// <summary>
    /// Registers all per-model settings schemas with the settings schema registry.
    /// Called during startup before settings are loaded.
    /// </summary>
    public void RegisterSettingsSchemas(Configuration.SettingsSchemaRegistry schemaRegistry)
    {
        foreach (var model in _models.Values)
        {
            if (model.SettingsSchema.ValueKind == JsonValueKind.Object)
            {
                foreach (var prop in model.SettingsSchema.EnumerateObject())
                {
                    var key = $"models.{model.Id}.{prop.Name}";
                    schemaRegistry.Register(key, prop.Value.Clone());
                }
            }
        }
    }
}
