using System.Text.Json;

namespace Ur.Configuration;

/// <summary>
/// Merged, validated settings. Flat dot-namespaced keys.
/// Workspace values override user values.
/// </summary>
public sealed class Settings
{
    private static readonly JsonSerializerOptions WriteOptions = new()
    {
        WriteIndented = true,
    };

    private readonly SettingsSchemaRegistry _schemaRegistry;
    private readonly string? _userSettingsPath;
    private readonly string? _workspaceSettingsPath;

    private Dictionary<string, JsonElement> _userValues;
    private Dictionary<string, JsonElement> _workspaceValues;
    private Dictionary<string, JsonElement> _mergedValues;

    internal Settings(
        SettingsSchemaRegistry schemaRegistry,
        string? userSettingsPath,
        string? workspaceSettingsPath,
        Dictionary<string, JsonElement> userValues,
        Dictionary<string, JsonElement> workspaceValues,
        Dictionary<string, JsonElement> mergedValues)
    {
        _schemaRegistry = schemaRegistry;
        _userSettingsPath = userSettingsPath;
        _workspaceSettingsPath = workspaceSettingsPath;
        _userValues = userValues;
        _workspaceValues = workspaceValues;
        _mergedValues = mergedValues;
    }

    public JsonElement? Get(string key)
    {
        return _mergedValues.TryGetValue(key, out var value) ? value : null;
    }

    public T? Get<T>(string key)
    {
        if (!_mergedValues.TryGetValue(key, out var value))
            return default;
        return value.Deserialize<T>();
    }

    public IEnumerable<string> Keys => _mergedValues.Keys;

    internal async Task SetAsync(
        string key,
        JsonElement value,
        ConfigurationScope scope,
        CancellationToken ct = default)
    {
        var originalUserValues = CloneValues(_userValues);
        var originalWorkspaceValues = CloneValues(_workspaceValues);

        try
        {
            GetValues(scope)[key] = value.Clone();
            RebuildMergedValues();
            await PersistScopeAsync(scope, ct);
        }
        catch
        {
            _userValues = originalUserValues;
            _workspaceValues = originalWorkspaceValues;
            _mergedValues = SettingsLoader.Merge(_userValues, _workspaceValues);
            throw;
        }
    }

    internal async Task ClearAsync(
        string key,
        ConfigurationScope scope,
        CancellationToken ct = default)
    {
        var originalUserValues = CloneValues(_userValues);
        var originalWorkspaceValues = CloneValues(_workspaceValues);

        try
        {
            GetValues(scope).Remove(key);
            RebuildMergedValues();
            await PersistScopeAsync(scope, ct);
        }
        catch
        {
            _userValues = originalUserValues;
            _workspaceValues = originalWorkspaceValues;
            _mergedValues = SettingsLoader.Merge(_userValues, _workspaceValues);
            throw;
        }
    }

    private Dictionary<string, JsonElement> GetValues(ConfigurationScope scope) => scope switch
    {
        ConfigurationScope.User => _userValues,
        ConfigurationScope.Workspace => _workspaceValues,
        _ => throw new ArgumentOutOfRangeException(nameof(scope)),
    };

    private string? GetPath(ConfigurationScope scope) => scope switch
    {
        ConfigurationScope.User => _userSettingsPath,
        ConfigurationScope.Workspace => _workspaceSettingsPath,
        _ => throw new ArgumentOutOfRangeException(nameof(scope)),
    };

    private void RebuildMergedValues()
    {
        var mergedValues = SettingsLoader.Merge(_userValues, _workspaceValues);
        SettingsLoader.Validate(_schemaRegistry, mergedValues);
        _mergedValues = mergedValues;
    }

    private async Task PersistScopeAsync(ConfigurationScope scope, CancellationToken ct)
    {
        var path = GetPath(scope);
        if (path is null)
            return;

        var directory = Path.GetDirectoryName(path);
        if (!string.IsNullOrEmpty(directory))
            Directory.CreateDirectory(directory);

        var json = JsonSerializer.Serialize(GetValues(scope), WriteOptions);
        await File.WriteAllTextAsync(path, json, ct);
    }

    private static Dictionary<string, JsonElement> CloneValues(Dictionary<string, JsonElement> values)
    {
        var clone = new Dictionary<string, JsonElement>(values.Count, StringComparer.Ordinal);
        foreach (var (key, value) in values)
            clone[key] = value.Clone();

        return clone;
    }
}
