using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Tools;

/// <summary>
/// Shared helpers for extracting typed arguments from AIFunctionArguments.
/// Tool arguments arrive as strings from tests but as JsonElement from real
/// LLM responses — these helpers handle both transparently.
/// </summary>
internal static class ToolArgHelpers
{
    public static string GetRequiredString(AIFunctionArguments args, string key)
    {
        if (!args.TryGetValue(key, out var value) || value is null)
            throw new ArgumentException($"Missing required parameter: {key}");

        return value switch
        {
            string s => s,
            JsonElement { ValueKind: JsonValueKind.String } je => je.GetString()!,
            _ => value.ToString()!
        };
    }

    public static string? GetOptionalString(AIFunctionArguments args, string key)
    {
        if (!args.TryGetValue(key, out var value) || value is null)
            return null;

        return value switch
        {
            string s => s,
            JsonElement { ValueKind: JsonValueKind.String } je => je.GetString(),
            _ => value.ToString()
        };
    }

    public static int? GetOptionalInt(AIFunctionArguments args, string key)
    {
        if (!args.TryGetValue(key, out var value) || value is null)
            return null;

        return value switch
        {
            int i => i,
            long l => (int)l,
            double d => (int)d,
            JsonElement { ValueKind: JsonValueKind.Number } je => je.GetInt32(),
            _ => null
        };
    }
}
