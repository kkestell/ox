using System.Text.Json;

namespace Ur.Configuration;

/// <summary>
/// Merged, validated settings. Flat dot-namespaced keys.
/// Workspace values override user values.
/// </summary>
public sealed class Settings
{
    private readonly Dictionary<string, JsonElement> _values;

    internal Settings(Dictionary<string, JsonElement> values)
    {
        _values = values;
    }

    public JsonElement? Get(string key)
    {
        return _values.TryGetValue(key, out var value) ? value : null;
    }

    public T? Get<T>(string key)
    {
        if (!_values.TryGetValue(key, out var value))
            return default;
        return value.Deserialize<T>();
    }

    public IEnumerable<string> Keys => _values.Keys;
}
