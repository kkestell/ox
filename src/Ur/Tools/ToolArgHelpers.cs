using System.Text;
using System.Text.Json;
using Microsoft.Extensions.AI;

namespace Ur.Tools;

/// <summary>
/// Shared helpers for tool argument extraction and output formatting.
/// Tool arguments arrive as strings from tests but as JsonElement from real
/// LLM responses — these helpers handle both transparently.
/// </summary>
internal static class ToolArgHelpers
{
    // Default output limits shared by all tools that truncate process/search output.
    internal const int MaxOutputLines = 2000;
    internal const int MaxOutputBytes = 100 * 1024; // 100 KB

    public static string GetRequiredString(AIFunctionArguments args, string key)
    {
        if (!args.TryGetValue(key, out var value) || value is null)
            throw new ArgumentException($"Missing required parameter: {key}");

        return CoerceToString(value)!;
    }

    public static string? GetOptionalString(AIFunctionArguments args, string key)
    {
        if (!args.TryGetValue(key, out var value) || value is null)
            return null;

        return CoerceToString(value);
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

    /// <summary>
    /// Resolve an optional sub-path against a workspace root. Returns the
    /// workspace root when <paramref name="subPath"/> is null/empty, otherwise
    /// resolves relative paths against the root.
    /// </summary>
    public static string ResolvePath(string workspaceRoot, string? subPath) =>
        string.IsNullOrEmpty(subPath)
            ? workspaceRoot
            : Path.GetFullPath(Path.IsPathRooted(subPath) ? subPath : Path.Combine(workspaceRoot, subPath));

    /// <summary>
    /// Extracts a named string argument from raw tool arguments for permission
    /// target display. Handles both native strings (from tests) and JsonElement
    /// (from real LLM responses). This is the canonical overload used by
    /// <see cref="Ur.AgentLoop.PermissionMeta.TargetExtractor"/> lambdas.
    /// </summary>
    internal static string ExtractStringArg(IDictionary<string, object?> args, string key)
    {
        if (!args.TryGetValue(key, out var v) || v is null)
            return "(unknown)";

        return CoerceToString(v) ?? "(unknown)";
    }

    /// <summary>
    /// Converts a tool argument value to a string. Tool arguments arrive as
    /// native strings from tests but as JsonElement from real LLM responses —
    /// this handles both transparently.
    /// </summary>
    private static string? CoerceToString(object? value) => value switch
    {
        null => null,
        string s => s,
        JsonElement { ValueKind: JsonValueKind.String } je => je.GetString(),
        _ => value.ToString()
    };

    /// <summary>
    /// Truncate output to fit within line and byte limits, appending a
    /// [truncated] marker when content is dropped. Used by bash, grep, and
    /// any other tool that returns unbounded process/search output.
    /// </summary>
    public static string TruncateOutput(
        string output, int maxLines = MaxOutputLines, int maxBytes = MaxOutputBytes)
    {
        var lines = output.Split('\n');
        if (lines.Length <= maxLines && output.Length <= maxBytes)
            return output.TrimEnd();

        var sb = new StringBuilder();
        var count = 0;
        foreach (var line in lines)
        {
            if (count >= maxLines || sb.Length >= maxBytes)
                break;
            sb.AppendLine(line);
            count++;
        }

        return sb.ToString().TrimEnd() + "\n[truncated]";
    }
}
