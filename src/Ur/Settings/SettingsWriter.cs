using System.Text.Json;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Logging;

namespace Ur.Settings;

/// <summary>
/// Writes settings to the appropriate JSON file (user or workspace scope),
/// then triggers an <see cref="IConfigurationRoot.Reload"/> so IConfiguration
/// and IOptionsMonitor pick up the change immediately.
///
/// Writes are validated against <see cref="SettingsSchemaRegistry"/> before
/// persisting. If validation fails, a <see cref="SettingsValidationException"/>
/// is thrown and the file is not modified.
///
/// The file format is nested JSON: top-level keys are namespaces (e.g. "ur"),
/// and their values are objects containing the actual settings. This aligns
/// with IConfiguration's native section model (e.g. <c>{"ur": {"model": "value"}}</c>).
/// </summary>
internal sealed class SettingsWriter
{
    private static readonly SettingsJsonContext WriteContext = new(new JsonSerializerOptions
    {
        WriteIndented = true
    });

    private readonly SettingsSchemaRegistry _schemaRegistry;
    private readonly IConfigurationRoot _configurationRoot;
    private readonly string? _userSettingsPath;
    private readonly string? _workspaceSettingsPath;
    private readonly ILogger? _logger;

    public SettingsWriter(
        SettingsSchemaRegistry schemaRegistry,
        IConfigurationRoot configurationRoot,
        string? userSettingsPath,
        string? workspaceSettingsPath,
        ILogger? logger = null)
    {
        _schemaRegistry = schemaRegistry;
        _configurationRoot = configurationRoot;
        _userSettingsPath = userSettingsPath;
        _workspaceSettingsPath = workspaceSettingsPath;
        _logger = logger;
    }

    /// <summary>
    /// Sets a value for the given dot-namespaced key in the specified scope.
    /// The key is split on the first dot to determine the namespace and leaf key,
    /// then validated against the schema registry, persisted to the JSON file,
    /// and the configuration root is reloaded.
    /// </summary>
    public Task SetAsync(
        string key,
        JsonElement value,
        ConfigurationScope scope,
        CancellationToken ct = default)
    {
        ct.ThrowIfCancellationRequested();

        // Validate the new value against the schema before persisting.
        ValidateSingleKey(key, value);

        var path = GetPath(scope);
        var data = ReadNestedJson(path);
        SetNestedValue(data, key, value);
        PersistAndReload(path, data);

        return Task.CompletedTask;
    }

    /// <summary>
    /// Removes a key from the specified scope's settings file and reloads.
    /// </summary>
    public Task ClearAsync(
        string key,
        ConfigurationScope scope,
        CancellationToken ct = default)
    {
        ct.ThrowIfCancellationRequested();

        var path = GetPath(scope);
        var data = ReadNestedJson(path);
        RemoveNestedValue(data, key);
        PersistAndReload(path, data);

        return Task.CompletedTask;
    }

    /// <summary>
    /// Reads a single setting from the merged configuration. Keys use dot-namespaced
    /// format (e.g. "ur.model"). Reads directly from the JSON files rather than from
    /// IConfiguration because IConfiguration loses type fidelity (everything becomes a
    /// string — booleans become "True"/"False", numbers lose their JSON representation).
    ///
    /// Merge semantics: workspace values override user values for the same key.
    /// </summary>
    public JsonElement? Get(string key)
    {
        // Check workspace first (higher priority), then user.
        var workspaceValue = ReadValueFromFile(_workspaceSettingsPath, key);
        if (workspaceValue is not null)
            return workspaceValue;

        return ReadValueFromFile(_userSettingsPath, key);
    }

    /// <summary>
    /// Reads a string setting from the merged view. Returns null if not set.
    /// Uses <see cref="Get"/> and extracts the string from the JsonElement
    /// to preserve consistent merge and type semantics.
    /// </summary>
    public string? GetString(string key)
    {
        var element = Get(key);
        if (element is null)
            return null;

        return element.Value.ValueKind == JsonValueKind.String
            ? element.Value.GetString()
            : element.Value.GetRawText();
    }

    private string? GetPath(ConfigurationScope scope) => scope switch
    {
        ConfigurationScope.User => _userSettingsPath,
        ConfigurationScope.Workspace => _workspaceSettingsPath,
        _ => throw new ArgumentOutOfRangeException(nameof(scope))
    };

    /// <summary>
    /// Validates a single key-value pair against its registered schema.
    /// Throws <see cref="SettingsValidationException"/> if the type doesn't match.
    /// Unknown keys are tolerated for forward compatibility.
    /// </summary>
    private void ValidateSingleKey(string key, JsonElement value)
    {
        if (!_schemaRegistry.TryGetSchema(key, out var schema))
            return;

        var expectedType = GetExpectedType(schema);
        if (expectedType is not null && !MatchesType(value, expectedType))
        {
            throw new SettingsValidationException([
                $"Setting '{key}': expected type '{expectedType}', got '{value.ValueKind}'."
            ]);
        }
    }

    /// <summary>
    /// Sets a value in the nested JSON dictionary. The dot-namespaced key is split
    /// on the first dot: everything before is the namespace (top-level object key),
    /// everything after is the leaf key within that object.
    /// Keys without a dot are stored directly at the top level.
    /// </summary>
    private static void SetNestedValue(
        Dictionary<string, JsonElement> data,
        string key,
        JsonElement value)
    {
        var dotIndex = key.IndexOf('.');
        if (dotIndex <= 0)
        {
            // No namespace — store directly at top level.
            data[key] = value.Clone();
            return;
        }

        var ns = key[..dotIndex];
        var leafKey = key[(dotIndex + 1)..];

        // Get or create the namespace sub-object.
        var subDict = GetOrCreateSubObject(data, ns);
        subDict[leafKey] = value.Clone();

        // Re-serialize the sub-dictionary back as a JsonElement.
        var json = JsonSerializer.Serialize(subDict, WriteContext.DictionaryStringJsonElement);
        using var doc = JsonDocument.Parse(json);
        data[ns] = doc.RootElement.Clone();
    }

    /// <summary>
    /// Removes a key from the nested JSON dictionary. If the namespace sub-object
    /// becomes empty after removal, the namespace key itself is removed.
    /// </summary>
    private static void RemoveNestedValue(
        Dictionary<string, JsonElement> data,
        string key)
    {
        var dotIndex = key.IndexOf('.');
        if (dotIndex <= 0)
        {
            data.Remove(key);
            return;
        }

        var ns = key[..dotIndex];
        var leafKey = key[(dotIndex + 1)..];

        if (!data.TryGetValue(ns, out var nsElement) ||
            nsElement.ValueKind != JsonValueKind.Object)
            return;

        var subDict = DeserializeSubObject(nsElement);
        subDict.Remove(leafKey);

        if (subDict.Count == 0)
        {
            // Namespace is empty — remove the whole key for a clean file.
            data.Remove(ns);
        }
        else
        {
            var json = JsonSerializer.Serialize(subDict, WriteContext.DictionaryStringJsonElement);
            using var doc = JsonDocument.Parse(json);
            data[ns] = doc.RootElement.Clone();
        }
    }

    /// <summary>
    /// Extracts or creates a sub-object dictionary for a namespace key.
    /// </summary>
    private static Dictionary<string, JsonElement> GetOrCreateSubObject(
        Dictionary<string, JsonElement> data,
        string ns)
    {
        if (data.TryGetValue(ns, out var existing) && existing.ValueKind == JsonValueKind.Object)
            return DeserializeSubObject(existing);

        return new Dictionary<string, JsonElement>(StringComparer.Ordinal);
    }

    private static Dictionary<string, JsonElement> DeserializeSubObject(JsonElement element)
    {
        var dict = new Dictionary<string, JsonElement>(StringComparer.Ordinal);
        foreach (var property in element.EnumerateObject())
            dict[property.Name] = property.Value.Clone();
        return dict;
    }

    /// <summary>
    /// Reads the nested JSON settings file into a dictionary, or returns an empty
    /// dictionary if the file doesn't exist or is malformed.
    /// </summary>
    private Dictionary<string, JsonElement> ReadNestedJson(string? path)
    {
        if (path is null || !File.Exists(path))
            return new Dictionary<string, JsonElement>(StringComparer.Ordinal);

        try
        {
            var json = File.ReadAllText(path);
            if (string.IsNullOrWhiteSpace(json))
                return new Dictionary<string, JsonElement>(StringComparer.Ordinal);

            using var doc = JsonDocument.Parse(json);
            var result = new Dictionary<string, JsonElement>(StringComparer.Ordinal);
            foreach (var property in doc.RootElement.EnumerateObject())
                result[property.Name] = property.Value.Clone();
            return result;
        }
        catch (Exception ex) when (ex is IOException or JsonException)
        {
            _logger?.LogError(ex, "Failed to read settings from '{Path}'", path);
            return new Dictionary<string, JsonElement>(StringComparer.Ordinal);
        }
    }

    /// <summary>
    /// Writes the nested JSON dictionary to disk and triggers an IConfiguration reload
    /// so IOptionsMonitor and all configuration consumers see the change immediately.
    /// </summary>
    private void PersistAndReload(string? path, Dictionary<string, JsonElement> data)
    {
        if (path is null) return;

        var directory = Path.GetDirectoryName(path);
        if (!string.IsNullOrEmpty(directory))
            Directory.CreateDirectory(directory);

        var json = JsonSerializer.Serialize(data, WriteContext.DictionaryStringJsonElement);
        File.WriteAllText(path, json);

        // Trigger reload so IConfiguration/IOptionsMonitor pick up the change.
        _configurationRoot.Reload();
    }

    /// <summary>
    /// Reads a specific dot-namespaced key's value directly from a nested JSON file.
    /// Returns null if the file doesn't exist, the namespace/key is absent, or
    /// the file is malformed. This preserves the original JSON type (bool, number,
    /// string) that IConfiguration would flatten to a string.
    /// </summary>
    private JsonElement? ReadValueFromFile(string? path, string key)
    {
        if (path is null || !File.Exists(path))
            return null;

        try
        {
            var json = File.ReadAllText(path);
            if (string.IsNullOrWhiteSpace(json))
                return null;

            using var doc = JsonDocument.Parse(json);
            var dotIndex = key.IndexOf('.');
            if (dotIndex <= 0)
            {
                // No namespace — look for the key directly at the top level.
                return doc.RootElement.TryGetProperty(key, out var val) ? val.Clone() : null;
            }

            var ns = key[..dotIndex];
            var leafKey = key[(dotIndex + 1)..];

            if (!doc.RootElement.TryGetProperty(ns, out var section) ||
                section.ValueKind != JsonValueKind.Object)
                return null;

            return section.TryGetProperty(leafKey, out var leafVal) ? leafVal.Clone() : null;
        }
        catch (Exception ex) when (ex is IOException or JsonException)
        {
            _logger?.LogError(ex, "Failed to read setting '{Key}' from '{Path}'", key, path);
            return null;
        }
    }

    private static string? GetExpectedType(JsonElement schema) =>
        schema.TryGetProperty("type", out var typeProp) ? typeProp.GetString() : null;

    private static bool MatchesType(JsonElement value, string expectedType) => expectedType switch
    {
        "string" => value.ValueKind == JsonValueKind.String,
        "number" or "integer" => value.ValueKind == JsonValueKind.Number,
        "boolean" => value.ValueKind is JsonValueKind.True or JsonValueKind.False,
        "array" => value.ValueKind == JsonValueKind.Array,
        "object" => value.ValueKind == JsonValueKind.Object,
        _ => true
    };
}
