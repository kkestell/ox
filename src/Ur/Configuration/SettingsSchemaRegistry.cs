using System.Text.Json;

namespace Ur.Configuration;

/// <summary>
/// Aggregates JSON schemas from core and provider settings.
/// A key can only be registered once.
/// </summary>
public sealed class SettingsSchemaRegistry
{
    private readonly Dictionary<string, JsonElement> _schemas = new(StringComparer.Ordinal);

    public void Register(string key, JsonElement schema)
    {
        if (!_schemas.TryAdd(key, schema))
            throw new InvalidOperationException($"Settings key '{key}' is already registered.");
    }

    public bool TryGetSchema(string key, out JsonElement schema) => _schemas.TryGetValue(key, out schema);

    public bool IsKnown(string key) => _schemas.ContainsKey(key);
}
