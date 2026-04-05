using System.Text.Json;
using Lua;

namespace Ur.Extensions;

/// <summary>
/// Shared Lua value to JSON serialization helpers used by both
/// <see cref="ExtensionLoader"/> (schema conversion) and
/// <see cref="LuaToolAdapter"/> (tool result marshalling).
///
/// Lua tables map to JSON objects or arrays depending on their key structure:
/// tables with only sequential integer keys (1..N) become arrays; all others
/// become objects. This matches Lua's own table semantics.
/// </summary>
internal static class LuaJsonHelpers
{
    /// <summary>
    /// Converts a Lua table into a <see cref="JsonElement"/> by round-tripping
    /// through JSON. Used for schema conversion during manifest evaluation.
    /// </summary>
    internal static JsonElement ToJsonElement(LuaTable table)
    {
        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream))
        {
            WriteTable(writer, table);
        }

        var doc = JsonDocument.Parse(stream.ToArray());
        return doc.RootElement.Clone();
    }

    /// <summary>
    /// Serializes a Lua table to a JSON string. Used for marshalling
    /// tool return values back to the agent loop.
    /// </summary>
    internal static string ToJsonString(LuaTable table)
    {
        using var stream = new MemoryStream();
        using var writer = new Utf8JsonWriter(stream);
        WriteValue(writer, table);
        writer.Flush();
        return System.Text.Encoding.UTF8.GetString(stream.ToArray());
    }

    /// <summary>
    /// Writes a Lua value as JSON. Handles strings, numbers, booleans, tables
    /// (detecting array vs object by checking ArrayLength/HashMapCount), and null.
    /// </summary>
    private static void WriteValue(Utf8JsonWriter writer, LuaValue value)
    {
        if (value.TryRead<string>(out var s))
        {
            writer.WriteStringValue(s);
        }
        else if (value.TryRead<double>(out var d))
        {
            writer.WriteNumberValue(d);
        }
        else if (value.TryRead<bool>(out var b))
        {
            writer.WriteBooleanValue(b);
        }
        else if (value.TryRead<LuaTable>(out var t))
        {
            WriteTable(writer, t);
        }
        else
        {
            writer.WriteNullValue();
        }
    }

    /// <summary>
    /// Writes a Lua table as either a JSON array (sequential integer keys from 1)
    /// or a JSON object (string-keyed hash entries).
    ///
    /// Empty tables are ambiguous in Lua — <c>{}</c> has both ArrayLength and
    /// HashMapCount equal to zero. We default to an empty JSON object, which is
    /// correct for most schema positions (e.g. <c>properties</c>). Callers that
    /// need array semantics for specific fields (e.g. <c>required</c>) should
    /// post-process via <see cref="CoerceSchemaArrayFields"/>.
    /// </summary>
    private static void WriteTable(Utf8JsonWriter writer, LuaTable table)
    {
        if (table is { ArrayLength: > 0, HashMapCount: 0 })
        {
            writer.WriteStartArray();
            for (var i = 1; i <= table.ArrayLength; i++)
                WriteValue(writer, table[i]);
            writer.WriteEndArray();
        }
        else
        {
            writer.WriteStartObject();
            foreach (var (key, value) in table)
            {
                if (!key.TryRead<string>(out var k))
                    continue;
                writer.WritePropertyName(k);
                WriteValue(writer, value);
            }
            writer.WriteEndObject();
        }
    }

    /// <summary>
    /// Fixes up JSON Schema fields that must be arrays but may have been
    /// serialized as empty objects due to the Lua empty-table ambiguity.
    /// Specifically, <c>required</c> must be a JSON array per JSON Schema spec,
    /// but an empty Lua table <c>{}</c> produces <c>{}</c> (object) since Lua
    /// has no way to distinguish empty arrays from empty objects.
    /// </summary>
    internal static JsonElement CoerceSchemaArrayFields(JsonElement schema)
    {
        if (schema.ValueKind != JsonValueKind.Object)
            return schema;

        using var stream = new MemoryStream();
        using (var writer = new Utf8JsonWriter(stream))
        {
            WriteCoercedObject(writer, schema);
        }

        return JsonDocument.Parse(stream.ToArray()).RootElement.Clone();
    }

    private static void WriteCoercedObject(Utf8JsonWriter writer, JsonElement obj)
    {
        writer.WriteStartObject();
        foreach (var prop in obj.EnumerateObject())
        {
            writer.WritePropertyName(prop.Name);

            // "required" must be a JSON array per JSON Schema. Lua's empty
            // table {} serializes as an empty object, which crashes the OpenAI
            // SDK deserializer. Coerce it to an empty array at this position.
            switch (prop)
            {
                case { Name: "required", Value.ValueKind: JsonValueKind.Object }:
                    writer.WriteStartArray();
                    writer.WriteEndArray();
                    break;
                case { Value.ValueKind: JsonValueKind.Object }:
                    // Recurse into nested objects (e.g. properties contain
                    // their own "required" fields in nested schemas).
                    WriteCoercedObject(writer, prop.Value);
                    break;
                default:
                    prop.Value.WriteTo(writer);
                    break;
            }
        }
        writer.WriteEndObject();
    }
}
