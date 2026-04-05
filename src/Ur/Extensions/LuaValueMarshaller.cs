using System.Text.Json;
using Lua;

namespace Ur.Extensions;

/// <summary>
/// Marshals values between .NET/JSON and Lua representations.
///
/// Extracted from <see cref="LuaToolAdapter"/> so tool adapters delegate to a
/// single shared utility rather than each implementing their own marshalling.
/// If a future adapter (e.g. for a different Lua runtime or coroutine-based tools)
/// needs the same conversions, the logic lives here rather than being duplicated.
/// </summary>
internal static class LuaValueMarshaller
{
    /// <summary>
    /// Marshals a .NET value (typically from <see cref="Microsoft.Extensions.AI.AIFunctionArguments"/>)
    /// to a Lua-compatible value. Handles primitives, strings, and <see cref="JsonElement"/>
    /// (which is the real type arriving from LLM tool calls).
    /// </summary>
    internal static LuaValue ToLua(object? value) => value switch
    {
        null => LuaValue.Nil,
        string s => s,
        bool b => b,
        int i => i,
        long l => l,
        float f => f,
        double d => d,
        JsonElement je => JsonElementToLua(je),
        _ => value.ToString() ?? ""
    };

    /// <summary>
    /// Marshals a Lua value back to a .NET object suitable for tool result serialization.
    /// Tables are converted to JSON strings via <see cref="LuaJsonHelpers"/>;
    /// primitives are returned as their native .NET types.
    /// </summary>
    internal static object? FromLua(LuaValue value)
    {
        if (value.TryRead<string>(out var s)) return s;
        if (value.TryRead<double>(out var d)) return d;
        if (value.TryRead<bool>(out var b)) return b;
        return value.TryRead<LuaTable>(out var t) ? LuaJsonHelpers.ToJsonString(t) : null;
    }

    /// <summary>
    /// Converts a <see cref="JsonElement"/> to the equivalent Lua value. Objects become
    /// <see cref="LuaTable"/> with string keys; arrays become tables with 1-based integer keys.
    /// </summary>
    private static LuaValue JsonElementToLua(JsonElement element) => element.ValueKind switch
    {
        JsonValueKind.String => element.GetString() ?? "",
        JsonValueKind.Number => element.GetDouble(),
        JsonValueKind.True => true,
        JsonValueKind.False => false,
        JsonValueKind.Null or JsonValueKind.Undefined => LuaValue.Nil,
        JsonValueKind.Object => JsonObjectToLuaTable(element),
        JsonValueKind.Array => JsonArrayToLuaTable(element),
        _ => LuaValue.Nil
    };

    private static LuaTable JsonObjectToLuaTable(JsonElement obj)
    {
        var table = new LuaTable();
        foreach (var prop in obj.EnumerateObject())
            table[prop.Name] = JsonElementToLua(prop.Value);
        return table;
    }

    private static LuaTable JsonArrayToLuaTable(JsonElement arr)
    {
        var table = new LuaTable();
        var index = 1; // Lua arrays are 1-indexed
        foreach (var item in arr.EnumerateArray())
            table[index++] = JsonElementToLua(item);
        return table;
    }
}
