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
    internal static void WriteValue(Utf8JsonWriter writer, LuaValue value)
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
    /// </summary>
    private static void WriteTable(Utf8JsonWriter writer, LuaTable table)
    {
        if (table.ArrayLength > 0 && table.HashMapCount == 0)
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
                if (key.TryRead<string>(out var k))
                {
                    writer.WritePropertyName(k);
                    WriteValue(writer, value);
                }
            }
            writer.WriteEndObject();
        }
    }
}
