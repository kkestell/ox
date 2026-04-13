using Ox.Agent.Skills;

namespace Ox.App;

/// <summary>
/// Provides tab-completion for slash commands in the input field.
///
/// Two completion phases, distinguished by the presence of a space in input:
///
///   1. Command-name phase (no space): "/se" → "t" (suffix to reach "set").
///      Searches the command registry — built-ins first, then skills.
///
///   2. Argument phase (has space): "/model open" → "ai/gpt-5.4" (suffix).
///      Looks up the command name in the per-command argument-completion
///      dictionary and prefix-matches the typed argument against the list.
///      Requires at least one character after the space before suggesting.
///
/// The engine is command-agnostic in both phases: it doesn't know what
/// "model" means, only that some commands have completable argument lists.
/// This keeps command business logic in the Agent layer and UI mechanics in Ox.
/// </summary>
public sealed class AutocompleteEngine(
    CommandRegistry commands,
    IReadOnlyDictionary<string, IReadOnlyList<string>>? argumentCompletions = null)
{
    /// <summary>
    /// Compute the completion suffix for the current input buffer, or null
    /// if no completion is available.
    /// </summary>
    /// <param name="input">
    /// The full text currently in the input field (e.g. "/se" or "/model open").
    /// </param>
    public string? GetCompletion(string input)
    {
        // Only complete slash commands — must start with "/" and have at least
        // one letter after the slash.
        if (input.Length < 2 || input[0] != '/')
            return null;

        var spaceIndex = input.IndexOf(' ');

        // Argument phase: input contains a space — complete the argument.
        if (spaceIndex >= 0)
            return GetArgumentCompletion(input, spaceIndex);

        // Command-name phase: no space yet — complete the command name.
        return GetCommandNameCompletion(input);
    }

    /// <summary>
    /// Complete the command name (no space in input). Returns the suffix needed
    /// to reach the first matching command, or null when there is no match or
    /// the typed text is already an exact match.
    /// </summary>
    private string? GetCommandNameCompletion(string input)
    {
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

    /// <summary>
    /// Complete the argument portion (space already present). Looks up the
    /// command name in the argument-completion dictionary and prefix-matches
    /// the typed argument against the list. Returns null when the command has
    /// no registered completions, the argument is empty, or there is no match.
    /// </summary>
    private string? GetArgumentCompletion(string input, int spaceIndex)
    {
        if (argumentCompletions is null)
            return null;

        // Extract and normalize the command name (between "/" and the first space).
        // Dictionary keys are stored lowercase so we match case-insensitively.
        var commandName = input[1..spaceIndex].ToLowerInvariant();
        if (!argumentCompletions.TryGetValue(commandName, out var candidates))
            return null;

        // The typed argument is everything after the first space.
        var argPrefix = input[(spaceIndex + 1)..];

        // Require at least one character so we don't suggest on bare "/model ".
        if (argPrefix.Length == 0)
            return null;

        foreach (var candidate in candidates)
        {
            if (!candidate.StartsWith(argPrefix, StringComparison.OrdinalIgnoreCase))
                continue;

            // Exact match — nothing to append.
            if (candidate.Length == argPrefix.Length)
                return null;

            // Return the suffix in the candidate's casing.
            return candidate[argPrefix.Length..];
        }

        return null;
    }
}
