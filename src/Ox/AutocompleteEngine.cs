using Ur.Skills;

namespace Ox;

/// <summary>
/// Provides tab-completion for slash commands in the input field.
///
/// Given a partial input like "/se", the engine searches the command registry
/// (built-ins first, then skills) for the first name that starts with the
/// typed prefix and returns the remaining suffix ("t" for "set"). Returns
/// null when there's no match, the input isn't a slash command, or the input
/// is already an exact match.
/// </summary>
public sealed class AutocompleteEngine(CommandRegistry commands)
{
    /// <summary>
    /// Compute the completion suffix for the current input buffer, or null
    /// if no completion is available.
    /// </summary>
    /// <param name="input">
    /// The full text currently in the input field (e.g. "/se").
    /// </param>
    public string? GetCompletion(string input)
    {
        // Only complete slash commands — must start with "/" and have at least
        // one letter after the slash with no arguments (no spaces).
        if (input.Length < 2 || input[0] != '/' || input.Contains(' '))
            return null;

        var prefix = input[1..]; // strip the leading "/"

        foreach (var name in commands.UserInvocableNames)
        {
            if (!name.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
                continue;

            // Exact match — nothing left to suggest.
            if (name.Length == prefix.Length)
                return null;

            // Return the suffix in the registry's casing.
            return name[prefix.Length..];
        }

        return null;
    }
}
