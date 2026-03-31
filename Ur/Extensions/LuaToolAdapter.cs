using System.Text.Json;
using Lua;
using Microsoft.Extensions.AI;

namespace Ur.Extensions;

/// <summary>
/// Wraps a Lua function as an <see cref="AIFunction"/> so the agent loop
/// can invoke extension-defined tools through the standard tool registry.
/// </summary>
internal sealed class LuaToolAdapter : AIFunction
{
    private readonly LuaState _state;
    private readonly LuaFunction _handler;
    private readonly string _name;
    private readonly string _description;
    private readonly JsonElement _jsonSchema;

    public LuaToolAdapter(
        string name,
        string description,
        JsonElement jsonSchema,
        LuaState state,
        LuaFunction handler)
    {
        _name = name;
        _description = description;
        _jsonSchema = jsonSchema;
        _state = state;
        _handler = handler;
    }

    public override string Name => _name;
    public override string Description => _description;
    public override JsonElement JsonSchema => _jsonSchema;

    protected override async ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        // Marshal arguments into a Lua table.
        var argsTable = new LuaTable();
        foreach (var (key, value) in arguments)
            argsTable[key] = MarshalToLua(value);

        // Call the Lua handler with the arguments table.
        var results = await _state.CallAsync(
            _handler,
            new LuaValue[] { argsTable },
            cancellationToken);

        if (results.Length == 0)
            return null;

        return MarshalFromLua(results[0]);
    }

    private static LuaValue MarshalToLua(object? value) => value switch
    {
        null => LuaValue.Nil,
        string s => s,
        bool b => b,
        int i => i,
        long l => l,
        float f => f,
        double d => d,
        JsonElement je => MarshalJsonElementToLua(je),
        _ => value.ToString() ?? ""
    };

    private static LuaValue MarshalJsonElementToLua(JsonElement element) => element.ValueKind switch
    {
        JsonValueKind.String => element.GetString() ?? "",
        JsonValueKind.Number => element.GetDouble(),
        JsonValueKind.True => true,
        JsonValueKind.False => false,
        JsonValueKind.Null or JsonValueKind.Undefined => LuaValue.Nil,
        JsonValueKind.Object => MarshalJsonObjectToLuaTable(element),
        JsonValueKind.Array => MarshalJsonArrayToLuaTable(element),
        _ => LuaValue.Nil
    };

    private static LuaTable MarshalJsonObjectToLuaTable(JsonElement obj)
    {
        var table = new LuaTable();
        foreach (var prop in obj.EnumerateObject())
            table[prop.Name] = MarshalJsonElementToLua(prop.Value);
        return table;
    }

    private static LuaTable MarshalJsonArrayToLuaTable(JsonElement arr)
    {
        var table = new LuaTable();
        var index = 1; // Lua arrays are 1-indexed
        foreach (var item in arr.EnumerateArray())
            table[index++] = MarshalJsonElementToLua(item);
        return table;
    }

    private static object? MarshalFromLua(LuaValue value)
    {
        if (value.TryRead<string>(out var s)) return s;
        if (value.TryRead<double>(out var d)) return d;
        if (value.TryRead<bool>(out var b)) return b;
        if (value.TryRead<LuaTable>(out var t)) return MarshalLuaTableToJson(t);
        return null;
    }

    private static string MarshalLuaTableToJson(LuaTable table)
    {
        using var stream = new System.IO.MemoryStream();
        using var writer = new Utf8JsonWriter(stream);
        WriteLuaValue(writer, table);
        writer.Flush();
        return System.Text.Encoding.UTF8.GetString(stream.ToArray());
    }

    private static void WriteLuaValue(Utf8JsonWriter writer, LuaValue value)
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
            // Determine if this is an array (sequential integer keys from 1) or object.
            if (t.ArrayLength > 0 && t.HashMapCount == 0)
            {
                writer.WriteStartArray();
                for (var i = 1; i <= t.ArrayLength; i++)
                    WriteLuaValue(writer, t[i]);
                writer.WriteEndArray();
            }
            else
            {
                writer.WriteStartObject();
                foreach (var (k, v) in t)
                {
                    if (k.TryRead<string>(out var key))
                    {
                        writer.WritePropertyName(key);
                        WriteLuaValue(writer, v);
                    }
                }
                writer.WriteEndObject();
            }
        }
        else
        {
            writer.WriteNullValue();
        }
    }
}
