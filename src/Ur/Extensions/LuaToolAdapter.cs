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
        if (value.TryRead<LuaTable>(out var t)) return LuaJsonHelpers.ToJsonString(t);
        return null;
    }
}
