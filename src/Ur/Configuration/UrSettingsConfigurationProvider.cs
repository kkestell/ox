using System.Text.Json;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Logging;

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
/// </summary>
internal sealed class UrSettingsConfigurationProvider : ConfigurationProvider
{
    private readonly string? _filePath;
    private readonly ILogger? _logger;

    public UrSettingsConfigurationProvider(string? filePath, ILogger? logger = null)
    {
        _filePath = filePath;
        _logger = logger;
    }

    /// <summary>
    /// Reads the JSON settings file and flattens the nested structure into the
    /// Data dictionary.
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
        catch (IOException ex)
        {
            // File disappeared between exists check and read — treat as empty.
            _logger?.LogWarning("Settings file at '{Path}' could not be read: {Error}",
                _filePath, ex.Message);
            return;
        }

        if (string.IsNullOrWhiteSpace(json))
            return;

        JsonDocument doc;
        try
        {
            doc = JsonDocument.Parse(json);
        }
        catch (JsonException ex)
        {
            // Malformed JSON — treat as empty rather than crashing startup.
            _logger?.LogWarning("Settings file at '{Path}' contains malformed JSON: {Error}",
                _filePath, ex.Message);
            return;
        }

        using (doc)
        {
            FlattenJsonElement(doc.RootElement, prefix: null);
        }
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

}
