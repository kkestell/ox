using System.Text.Json;
using System.Text.Json.Serialization;

namespace Ur.Configuration;

/// <summary>
/// Merged, validated settings built from two JSON files: user-level and
/// workspace-level. Keys are flat dot-namespaced strings (e.g. "ur.model",
/// "my-extension.debug") and values are raw <see cref="JsonElement"/>s so
/// that any JSON type can be stored without custom serialization per key.
///
/// Merge semantics: workspace values override user values for the same key.
/// Writes go to a single scope (user or workspace) and trigger an immediate
/// re-merge and re-validation. If the write or validation fails, the in-memory
/// state is rolled back to the snapshot taken before the write attempt, so
/// the process never operates on an invalid configuration.
/// </summary>
public sealed class Settings
{
    private static readonly SettingsJsonContext WriteContext = new(new JsonSerializerOptions
    {
        WriteIndented = true
    });

    private readonly SettingsSchemaRegistry _schemaRegistry;
    private readonly string? _userSettingsPath;
    private readonly string? _workspaceSettingsPath;

    // Three separate dictionaries: raw user values, raw workspace values, and
    // the merged result. The merged view is rebuilt whenever either scope changes.
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

    /// <summary>Gets a value from the merged view, or null if not set.</summary>
    public JsonElement? Get(string key)
    {
        return _mergedValues.TryGetValue(key, out var value) ? value : null;
    }

    /// <summary>
    /// Reads a string value from the merged view, or null if not set.
    /// Uses <see cref="JsonElement.GetString"/> rather than generic deserialization
    /// to stay AoT-safe.
    /// </summary>
    public string? GetString(string key)
    {
        if (!_mergedValues.TryGetValue(key, out var value))
            return null;
        return value.ValueKind == JsonValueKind.String ? value.GetString() : value.GetRawText();
    }

    /// <summary>
    /// Sets a value in the given scope, re-merges, validates, and persists.
    /// On failure (I/O or validation), rolls back to the pre-write state.
    /// </summary>
    internal Task SetAsync(
        string key,
        JsonElement value,
        ConfigurationScope scope,
        CancellationToken ct = default) =>
        MutateAndPersistAsync(scope, dict => dict[key] = value.Clone(), ct);

    /// <summary>
    /// Removes a key from the given scope and persists. Same rollback semantics as SetAsync.
    /// </summary>
    internal Task ClearAsync(
        string key,
        ConfigurationScope scope,
        CancellationToken ct = default) =>
        MutateAndPersistAsync(scope, dict => dict.Remove(key), ct);

    /// <summary>
    /// Applies a mutation to one scope's dictionary, re-merges, validates, and persists.
    /// On failure, restores the pre-mutation snapshot so the process never
    /// operates on an invalid configuration.
    /// </summary>
    private async Task MutateAndPersistAsync(
        ConfigurationScope scope,
        Action<Dictionary<string, JsonElement>> mutation,
        CancellationToken ct)
    {
        // Only snapshot the scope being modified — the other scope is untouched.
        var original = CloneValues(GetValues(scope));

        try
        {
            mutation(GetValues(scope));
            RebuildMergedValues();
            await PersistScopeAsync(scope, ct);
        }
        catch
        {
            RestoreScope(scope, original);
            _mergedValues = SettingsLoader.Merge(_userValues, _workspaceValues);
            throw;
        }
    }

    private void RestoreScope(ConfigurationScope scope, Dictionary<string, JsonElement> snapshot)
    {
        switch (scope)
        {
            case ConfigurationScope.User: _userValues = snapshot; break;
            case ConfigurationScope.Workspace: _workspaceValues = snapshot; break;
            default: throw new ArgumentOutOfRangeException(nameof(scope));
        }
    }

    private Dictionary<string, JsonElement> GetValues(ConfigurationScope scope) => scope switch
    {
        ConfigurationScope.User => _userValues,
        ConfigurationScope.Workspace => _workspaceValues,
        _ => throw new ArgumentOutOfRangeException(nameof(scope))
    };

    private string? GetPath(ConfigurationScope scope) => scope switch
    {
        ConfigurationScope.User => _userSettingsPath,
        ConfigurationScope.Workspace => _workspaceSettingsPath,
        _ => throw new ArgumentOutOfRangeException(nameof(scope))
    };

    /// <summary>
    /// Rebuilds the merged dictionary and validates against the schema registry.
    /// Throws <see cref="SettingsValidationException"/> if any value's type
    /// doesn't match its registered schema — this is what triggers the rollback.
    /// </summary>
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

        var json = JsonSerializer.Serialize(GetValues(scope), WriteContext.DictionaryStringJsonElement);
        await File.WriteAllTextAsync(path, json, ct);
    }

    /// <summary>
    /// Deep-clones a settings dictionary. Required because <see cref="JsonElement"/>
    /// is bound to its parent <see cref="JsonDocument"/> — cloning detaches it so
    /// it survives after the original document is disposed.
    /// </summary>
    private static Dictionary<string, JsonElement> CloneValues(Dictionary<string, JsonElement> values)
    {
        var clone = new Dictionary<string, JsonElement>(values.Count, StringComparer.Ordinal);
        foreach (var (key, value) in values)
            clone[key] = value.Clone();

        return clone;
    }
}

/// <summary>
/// Source-generated JSON serialization context for AoT-safe settings persistence.
/// Also used by <see cref="UrConfiguration"/> for typed setting serialization.
/// </summary>
[JsonSerializable(typeof(Dictionary<string, JsonElement>))]
[JsonSerializable(typeof(string))]
[JsonSerializable(typeof(bool))]
internal partial class SettingsJsonContext : JsonSerializerContext;
