using System.Text.Json;

namespace Ur.Configuration;

/// <summary>
/// Loads and merges settings from user and workspace files,
/// validates against the schema registry.
/// </summary>
public sealed class SettingsLoader
{
    private readonly SettingsSchemaRegistry _schemaRegistry;

    public SettingsLoader(SettingsSchemaRegistry schemaRegistry)
    {
        _schemaRegistry = schemaRegistry;
    }

    /// <summary>
    /// Loads settings from the user and workspace paths, merging workspace over user.
    /// </summary>
    public Settings Load(string? userSettingsPath, string? workspaceSettingsPath)
    {
        var userValues = ReadValues(userSettingsPath);
        var workspaceValues = ReadValues(workspaceSettingsPath);
        var merged = Merge(userValues, workspaceValues);

        Validate(_schemaRegistry, merged);

        return new Settings(
            _schemaRegistry,
            userSettingsPath,
            workspaceSettingsPath,
            userValues,
            workspaceValues,
            merged);
    }

    internal static Dictionary<string, JsonElement> ReadValues(string? path)
    {
        var values = new Dictionary<string, JsonElement>(StringComparer.Ordinal);

        if (path is null || !File.Exists(path))
            return values;

        var json = File.ReadAllText(path);
        using var doc = JsonDocument.Parse(json);

        foreach (var property in doc.RootElement.EnumerateObject())
            values[property.Name] = property.Value.Clone();

        return values;
    }

    internal static Dictionary<string, JsonElement> Merge(
        Dictionary<string, JsonElement> userValues,
        Dictionary<string, JsonElement> workspaceValues)
    {
        var merged = new Dictionary<string, JsonElement>(userValues.Count + workspaceValues.Count, StringComparer.Ordinal);

        foreach (var (key, value) in userValues)
            merged[key] = value.Clone();

        foreach (var (key, value) in workspaceValues)
            merged[key] = value.Clone();

        return merged;
    }

    internal static void Validate(
        SettingsSchemaRegistry schemaRegistry,
        Dictionary<string, JsonElement> values)
    {
        var errors = new List<string>();

        foreach (var (key, value) in values)
        {
            if (!schemaRegistry.IsKnown(key))
            {
                // Unknown keys: warn but don't fail.
                // TODO: hook into a logging/warning system
                continue;
            }

            if (schemaRegistry.TryGetSchema(key, out var schema))
            {
                var expectedType = GetExpectedType(schema);
                if (expectedType is not null && !MatchesType(value, expectedType))
                {
                    errors.Add(
                        $"Setting '{key}': expected type '{expectedType}', got '{value.ValueKind}'.");
                }
            }
        }

        if (errors.Count > 0)
            throw new SettingsValidationException(errors);
    }

    private static string? GetExpectedType(JsonElement schema)
    {
        if (schema.TryGetProperty("type", out var typeProp))
            return typeProp.GetString();
        return null;
    }

    private static bool MatchesType(JsonElement value, string expectedType) => expectedType switch
    {
        "string" => value.ValueKind == JsonValueKind.String,
        "number" => value.ValueKind == JsonValueKind.Number,
        "integer" => value.ValueKind == JsonValueKind.Number,
        "boolean" => value.ValueKind is JsonValueKind.True or JsonValueKind.False,
        "array" => value.ValueKind == JsonValueKind.Array,
        "object" => value.ValueKind == JsonValueKind.Object,
        _ => true,
    };
}
