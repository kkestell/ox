namespace Ur.Tools;

/// <summary>
/// Extracts a human-readable target string from a tool call's arguments for
/// permission prompts (e.g. the file path being written, the command being run).
///
/// Implementations receive a read-only dictionary of the raw arguments, keeping
/// the tool registration surface free of framework-specific types like
/// <c>Microsoft.Extensions.AI.AIFunctionArguments</c>.
/// </summary>
public interface ITargetExtractor
{
    /// <summary>
    /// Extracts a target string from the given tool arguments.
    /// </summary>
    string Extract(IReadOnlyDictionary<string, object?> arguments);
}

/// <summary>
/// Factory methods for common <see cref="ITargetExtractor"/> patterns.
/// Most tools just need to pull a single named string argument — the
/// <see cref="FromKey"/> factory covers that without requiring a custom class.
/// </summary>
public static class TargetExtractors
{
    /// <summary>
    /// Creates an extractor that reads a named string argument, falling back
    /// to <paramref name="fallback"/> when the key is missing or null.
    /// Covers the majority of built-in tools (file_path, command, pattern, etc.).
    /// </summary>
    public static ITargetExtractor FromKey(string key, string fallback = "(unknown)")
        => new KeyExtractor(key, fallback);

    /// <summary>
    /// Like <see cref="FromKey"/>, but truncates long values to <paramref name="maxLength"/>
    /// characters with an ellipsis. Used for free-text arguments like task descriptions
    /// where the full value would overflow a permission prompt line.
    /// </summary>
    public static ITargetExtractor FromKeyTruncated(string key, int maxLength = 60, string fallback = "(unknown)")
        => new TruncatingKeyExtractor(key, maxLength, fallback);

    /// <summary>
    /// Single-key string extractor. Delegates to <see cref="ToolArgHelpers.GetOptionalString"/>
    /// for consistent coercion of <see cref="System.Text.Json.JsonElement"/> values.
    /// </summary>
    private sealed class KeyExtractor(string key, string fallback) : ITargetExtractor
    {
        public string Extract(IReadOnlyDictionary<string, object?> arguments)
        {
            // Wrap in AIFunctionArguments so we can reuse the existing coercion
            // logic in ToolArgHelpers (handles both string and JsonElement values).
            var dict = new Dictionary<string, object?>(arguments);
            var wrapped = new Microsoft.Extensions.AI.AIFunctionArguments(dict);
            return ToolArgHelpers.GetOptionalString(wrapped, key) ?? fallback;
        }
    }

    private sealed class TruncatingKeyExtractor(string key, int maxLength, string fallback) : ITargetExtractor
    {
        public string Extract(IReadOnlyDictionary<string, object?> arguments)
        {
            var dict = new Dictionary<string, object?>(arguments);
            var wrapped = new Microsoft.Extensions.AI.AIFunctionArguments(dict);
            var value = ToolArgHelpers.GetOptionalString(wrapped, key) ?? fallback;
            return value.Length <= maxLength ? value : value[..maxLength] + "…";
        }
    }
}
