namespace Ur.Skills;

/// <summary>
/// Expands a skill's content template by substituting variables and arguments.
///
/// Expansion handles four substitution types:
/// 1. <c>$ARGUMENTS</c> — replaced with the raw args string from the invocation.
/// 2. Named arguments — if the skill declares argument names, each <c>$arg_name</c>
///    placeholder is replaced with the corresponding positional argument.
/// 3. <c>${UR_SKILL_DIR}</c> — replaced with the skill's directory path on disk.
/// 4. <c>${UR_SESSION_ID}</c> — replaced with the current session's ID.
///
/// TODO: Shell execution (!`command`) in skill content — requires subprocess
/// execution during expansion plus security considerations.
/// </summary>
internal static class SkillExpander
{
    /// <summary>
    /// Expands a skill's content template with the provided arguments and session context.
    /// </summary>
    /// <param name="skill">The skill whose content to expand.</param>
    /// <param name="args">Raw argument string from the user or model invocation.</param>
    /// <param name="sessionId">The current session's ID for ${UR_SESSION_ID} substitution.</param>
    /// <returns>The fully expanded prompt string ready to send to the model.</returns>
    public static string Expand(SkillDefinition skill, string args, string sessionId)
    {
        var content = skill.Content;

        // Named argument substitution: if the skill declares argument names,
        // split the args string by whitespace and replace each $arg_name placeholder.
        // Any leftover arguments (beyond the named ones) become the new $ARGUMENTS value.
        var remainingArgs = args;

        if (skill.ArgumentNames is { Length: > 0 })
        {
            var parts = SplitArgs(args, skill.ArgumentNames.Length);

            for (var i = 0; i < skill.ArgumentNames.Length; i++)
            {
                var value = i < parts.Length ? parts[i] : "";
                content = content.Replace($"${skill.ArgumentNames[i]}", value, StringComparison.Ordinal);
            }

            // Leftover args: everything after the named arguments.
            remainingArgs = parts.Length > skill.ArgumentNames.Length
                ? parts[^1]
                : "";
        }

        // $ARGUMENTS gets the raw args (or leftover after named args are consumed).
        content = content.Replace("$ARGUMENTS", remainingArgs, StringComparison.Ordinal);

        // Built-in variable substitutions.
        content = content.Replace("${UR_SKILL_DIR}", skill.SkillDirectory, StringComparison.Ordinal);
        content = content.Replace("${UR_SESSION_ID}", sessionId, StringComparison.Ordinal);

        return content;
    }

    /// <summary>
    /// Splits an argument string into at most <paramref name="maxParts"/> pieces.
    /// The last piece captures everything remaining (so named args get individual
    /// tokens while the rest is preserved as a single string for $ARGUMENTS).
    /// </summary>
    private static string[] SplitArgs(string args, int maxParts)
    {
        if (string.IsNullOrWhiteSpace(args))
            return [];

        // Split into maxParts + 1: one for each named arg, plus one for the remainder.
        // The +1 captures leftover text that will be used for $ARGUMENTS.
        return args.Split((char[]?)null, maxParts + 1, StringSplitOptions.RemoveEmptyEntries);
    }
}
