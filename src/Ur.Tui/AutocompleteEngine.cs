using System.Text.RegularExpressions;
using Ur.Skills;

namespace Ur.Tui;

/// <summary>
/// Provides prefix-based autocomplete for slash commands typed in the input row.
///
/// This is a TUI concern: given a buffer string, find the first command name that
/// extends the typed prefix and return the untyped suffix so the viewport can
/// render it as ghost text. The engine does not know whether names are built-ins
/// or skills — that distinction belongs to <see cref="CommandRegistry"/>.
/// </summary>
internal sealed partial class AutocompleteEngine(CommandRegistry commands)
{
    // Buffer must be exactly "/<letters>" with no trailing space or arguments.
    // "/" alone or "/123" or "/foo bar" are not autocompleted.
    [GeneratedRegex(@"^/([a-zA-Z]+)$")]
    private static partial Regex SlashPrefixPattern();

    /// <summary>
    /// Returns the completion suffix to show as ghost text, or null if there
    /// is no applicable completion.
    ///
    /// Returns null when:
    ///   - The buffer does not match "/<one-or-more-letters>".
    ///   - No command name starts with the typed prefix.
    ///   - The typed prefix exactly matches a command name (nothing left to complete).
    ///
    /// When multiple commands share the prefix, returns the suffix for the first
    /// match in priority order (built-ins before skills, each in registration/load order).
    /// </summary>
    public string? GetCompletion(string buffer)
    {
        var match = SlashPrefixPattern().Match(buffer);
        if (!match.Success)
            return null;

        var typedPrefix = match.Groups[1].Value;

        foreach (var name in commands.UserInvocableNames)
        {
            if (!name.StartsWith(typedPrefix, StringComparison.OrdinalIgnoreCase))
                continue;

            // Exact match — the user has typed the full name; nothing left to suggest.
            if (name.Length == typedPrefix.Length)
                return null;

            // Return the untyped remainder in the registry's casing.
            return name[typedPrefix.Length..];
        }

        return null;
    }
}
