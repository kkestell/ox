using Lua;

namespace Ur.Extensions;

/// <summary>
/// Shared helpers for extracting typed values from Lua tables.
/// Used by both <see cref="ExtensionLoader"/> (for tool registration fields)
/// and <see cref="ManifestParser"/> (for manifest metadata fields).
/// </summary>
internal static class LuaTableHelpers
{
    internal static string ReadRequiredString(LuaTable table, string key) =>
        table[key].TryRead<string>(out var s)
            ? s
            : throw new InvalidOperationException($"Missing required field '{key}'.");

    internal static string? ReadOptionalString(LuaTable table, string key) =>
        table[key].TryRead<string>(out var s) ? s : null;
}
