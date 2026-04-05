using System.Text.Json;
using Lua;
using Microsoft.Extensions.AI;

namespace Ur.Extensions;

/// <summary>
/// Wraps a Lua function as an <see cref="AIFunction"/> so the agent loop
/// can invoke extension-defined tools through the standard tool registry.
///
/// Marshalling between .NET and Lua values is delegated to
/// <see cref="LuaValueMarshaller"/> — this adapter only handles the call
/// mechanics (building the args table, invoking the handler, unwrapping results).
/// </summary>
internal sealed class LuaToolAdapter(
    string name,
    string description,
    JsonElement jsonSchema,
    LuaState state,
    LuaFunction handler)
    : AIFunction
{
    public override string Name { get; } = name;
    public override string Description { get; } = description;
    public override JsonElement JsonSchema { get; } = jsonSchema;

    protected override async ValueTask<object?> InvokeCoreAsync(
        AIFunctionArguments arguments,
        CancellationToken cancellationToken)
    {
        var argsTable = new LuaTable();
        foreach (var (key, value) in arguments)
            argsTable[key] = LuaValueMarshaller.ToLua(value);

        var results = await state.CallAsync(
            handler,
            new LuaValue[] { argsTable },
            cancellationToken);

        return results.Length == 0 ? null : LuaValueMarshaller.FromLua(results[0]);
    }
}
