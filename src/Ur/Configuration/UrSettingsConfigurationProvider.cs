using System.Text.Json;
using Microsoft.Extensions.Configuration;

namespace Ur.Configuration;

/// <summary>
/// Custom <see cref="ConfigurationProvider"/> that reads a single nested-JSON
/// settings file and flattens it into <see cref="IConfiguration"/>'s colon-separated
/// key model.
///
/// For example, the file:
///   {"ur": {"model": "gpt-4"}, "my-extension": {"debug": "true"}}
///
/// produces these configuration keys:
///   ur:model = gpt-4
///   my-extension:debug = true
///
/// On first load, if the file contains flat dot-namespaced keys from the old
/// format (e.g. "ur.model"), it is automatically migrated to nested JSON and
/// rewritten in place. This handles existing installations transparently.
/// </summary>
internal sealed class UrSettingsConfigurationProvider : ConfigurationProvider
{
    private readonly string? _filePath;

    public UrSettingsConfigurationProvider(string? filePath)
    {
        _filePath = filePath;
    }

    /// <summary>
    /// Reads the JSON settings file, migrating from flat to nested format if needed,
    /// then flattens the nested structure into the Data dictionary.
    /// </summary>
    public override void Load()
    {
        Data = new Dictionary<string, string?>(StringComparer.OrdinalIgnoreCase);

        if (_filePath is null || !File.Exists(_filePath))
            return;

        string json;
        try
        {
            json = File.ReadAllText(_filePath);
        }
        catch (IOException)
        {
            // File disappeared between exists check and read — treat as empty.
            return;
        }

        if (string.IsNullOrWhiteSpace(json))
            return;

        JsonDocument doc;
        try
        {
            doc = JsonDocument.Parse(json);
        }
        catch (JsonException)
        {
            // Malformed JSON — treat as empty rather than crashing startup.
            return;
        }

        using (doc)
        {
            if (NeedsFlatToNestedMigration(doc.RootElement))
            {
                var nested = MigrateFlatToNested(doc.RootElement);
                PersistNestedJson(nested);

                // Re-parse from the migrated structure.
                using var migratedDoc = JsonDocument.Parse(
                    JsonSerializer.Serialize(nested, SettingsJsonContext.Default.DictionaryStringJsonElement));
                FlattenJsonElement(migratedDoc.RootElement, prefix: null);
            }
            else
            {
                FlattenJsonElement(doc.RootElement, prefix: null);
            }
        }
    }

    /// <summary>
    /// Detects the old flat format by checking whether any top-level key contains
    /// a dot but its value is NOT an object. In the new nested format, dots only
    /// appear inside sub-objects (e.g. {"ur": {"model": "x"}}), so a top-level
    /// "ur.model" string value is a clear signal of the old format.
    /// </summary>
    internal static bool NeedsFlatToNestedMigration(JsonElement root)
    {
        if (root.ValueKind != JsonValueKind.Object)
            return false;

        foreach (var property in root.EnumerateObject())
        {
            if (property.Name.Contains('.') && property.Value.ValueKind != JsonValueKind.Object)
                return true;
        }

        return false;
    }

    /// <summary>
    /// Converts flat dot-namespaced keys to nested JSON objects.
    /// "ur.model" → {"ur": {"model": value}}.
    /// Keys without dots are preserved as-is.
    /// </summary>
    internal static Dictionary<string, JsonElement> MigrateFlatToNested(JsonElement root)
    {
        // Build an intermediate tree: namespace → (leaf-key → value).
        var namespaces = new Dictionary<string, Dictionary<string, JsonElement>>(StringComparer.Ordinal);
        var topLevel = new Dictionary<string, JsonElement>(StringComparer.Ordinal);

        foreach (var property in root.EnumerateObject())
        {
            var dotIndex = property.Name.IndexOf('.');
            if (dotIndex > 0 && property.Value.ValueKind != JsonValueKind.Object)
            {
                var ns = property.Name[..dotIndex];
                var key = property.Name[(dotIndex + 1)..];

                if (!namespaces.TryGetValue(ns, out var nsDict))
                {
                    nsDict = new Dictionary<string, JsonElement>(StringComparer.Ordinal);
                    namespaces[ns] = nsDict;
                }

                nsDict[key] = property.Value.Clone();
            }
            else
            {
                topLevel[property.Name] = property.Value.Clone();
            }
        }

        // Merge namespace groups into the result as nested objects.
        var result = new Dictionary<string, JsonElement>(StringComparer.Ordinal);

        foreach (var (key, value) in topLevel)
            result[key] = value;

        foreach (var (ns, entries) in namespaces)
        {
            // Serialize the sub-dictionary as a JsonElement to get an object value.
            var json = JsonSerializer.Serialize(entries, SettingsJsonContext.Default.DictionaryStringJsonElement);
            using var doc = JsonDocument.Parse(json);
            result[ns] = doc.RootElement.Clone();
        }

        return result;
    }

    /// <summary>
    /// Recursively flattens a JSON element into the Data dictionary using
    /// IConfiguration's colon-separated key convention.
    /// </summary>
    private void FlattenJsonElement(JsonElement element, string? prefix)
    {
        switch (element.ValueKind)
        {
            case JsonValueKind.Object:
                foreach (var property in element.EnumerateObject())
                {
                    var key = prefix is null ? property.Name : $"{prefix}:{property.Name}";
                    FlattenJsonElement(property.Value, key);
                }
                break;

            case JsonValueKind.Array:
                var index = 0;
                foreach (var item in element.EnumerateArray())
                {
                    var key = $"{prefix}:{index}";
                    FlattenJsonElement(item, key);
                    index++;
                }
                break;

            default:
                // Leaf value — store the raw text representation.
                if (prefix is not null)
                    Data[prefix] = element.ToString();
                break;
        }
    }

    /// <summary>
    /// Writes the migrated nested JSON back to the settings file.
    /// Best-effort: if the write fails, we still load from the in-memory migration.
    /// </summary>
    private void PersistNestedJson(Dictionary<string, JsonElement> nested)
    {
        if (_filePath is null) return;

        try
        {
            var directory = Path.GetDirectoryName(_filePath);
            if (!string.IsNullOrEmpty(directory))
                Directory.CreateDirectory(directory);

            var options = new JsonSerializerOptions { WriteIndented = true };
            var context = new SettingsJsonContext(options);
            var json = JsonSerializer.Serialize(nested, context.DictionaryStringJsonElement);
            File.WriteAllText(_filePath, json);
        }
        catch (IOException)
        {
            // Migration persistence is best-effort — the in-memory state is correct
            // regardless, and the file will be migrated again next time.
        }
    }
}
