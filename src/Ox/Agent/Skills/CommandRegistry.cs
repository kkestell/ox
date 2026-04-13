namespace Ox.Agent.Skills;

/// <summary>
/// Unified ordered view of all user-invocable slash command names.
///
/// This is the single point of truth for command priority: built-ins come first
/// (in registration order), skills follow (in load order). When two names share
/// a prefix, the first in this list wins the autocomplete suggestion.
///
/// This is a domain concern — which commands exist and in what order is defined
/// here in the Agent layer, not in the TUI. The TUI layer's AutocompleteEngine depends on
/// this class for prefix matching but knows nothing about the built-in/skill
/// distinction.
/// </summary>
public sealed class CommandRegistry
{
    /// <summary>
    /// All user-invocable command names in priority order: built-ins first, then skills.
    /// </summary>
    public IReadOnlyList<string> UserInvocableNames { get; }

    public CommandRegistry(BuiltInCommandRegistry builtIns, SkillRegistry skills)
    {
        // Pre-allocate for built-ins + user-invocable skills to avoid list resizing.
        var names = new List<string>(builtIns.All.Count + skills.UserInvocable().Count);
        names.AddRange(builtIns.All.Select(c => c.Name));
        names.AddRange(skills.UserInvocable().Select(s => s.Name));
        UserInvocableNames = names.AsReadOnly();
    }
}
