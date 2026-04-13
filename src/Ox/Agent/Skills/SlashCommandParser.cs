namespace Ox.Agent.Skills;

/// <summary>
/// Pure string operations for slash command parsing and formatting. Extracted
/// from OxSession to keep all slash-command text manipulation in the Skills
/// namespace — OxSession delegates to these methods rather than owning the
/// parsing logic directly.
/// </summary>
internal static class SlashCommandParser
{
    /// <summary>
    /// Extracts the skill name from a slash command input.
    /// For example, "/commit -m fix" → "commit".
    /// </summary>
    internal static string ParseName(string input) => ParseParts(input).Name;

    /// <summary>
    /// Extracts the arguments portion from a slash command input.
    /// For example, "/commit -m fix" → "-m fix". Returns empty string
    /// if no arguments are present.
    /// </summary>
    internal static string ParseArgs(string input) => ParseParts(input).Args;

    private static (string Name, string Args) ParseParts(string input)
    {
        var withoutSlash = input[1..];
        var spaceIndex = withoutSlash.IndexOf(' ');
        return spaceIndex < 0
            ? (withoutSlash, "")
            : (withoutSlash[..spaceIndex], withoutSlash[(spaceIndex + 1)..]);
    }

    /// <summary>
    /// Wraps expanded skill content in tags that tell the model the content
    /// originated from a slash command invocation. Matches the format used by
    /// the reference implementation so the model can distinguish user-typed
    /// text from skill-expanded text.
    /// </summary>
    internal static string FormatExpansion(string skillName, string args, string expandedContent) =>
        $"""
        <command-name>/{skillName}</command-name>
        <command-args>{args}</command-args>

        {expandedContent}
        """;
}
