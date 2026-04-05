namespace Ur.Skills;

/// <summary>
/// Holds loaded skills and provides lookup by name. This is the public API
/// surface for the skills subsystem — internal details like frontmatter parsing
/// and directory scanning are encapsulated behind this class.
///
/// Skills are loaded once at startup and are immutable for the lifetime of
/// the host. The registry provides filtered views for different consumers:
/// the model sees only skills it can invoke, the user sees only skills
/// available as slash commands.
/// </summary>
public sealed class SkillRegistry
{
    private readonly Dictionary<string, SkillDefinition> _skills;

    // Precomputed filtered views. The registry is immutable after construction,
    // so these never need invalidation — avoids per-turn LINQ allocations.
    private readonly IReadOnlyList<SkillDefinition> _modelInvocable;
    private readonly IReadOnlyList<SkillDefinition> _userInvocable;
    private readonly IReadOnlyList<SkillDefinition> _all;

    public SkillRegistry(IEnumerable<SkillDefinition> skills)
    {
        // Case-insensitive lookup so "/Commit" and "/commit" both resolve.
        _skills = new Dictionary<string, SkillDefinition>(StringComparer.OrdinalIgnoreCase);
        foreach (var skill in skills)
            _skills[skill.Name] = skill;

        _modelInvocable = _skills.Values.Where(s => !s.DisableModelInvocation).ToList();
        _userInvocable = _skills.Values.Where(s => s.UserInvocable).ToList();
        _all = _skills.Values.ToList();
    }

    /// <summary>
    /// Creates an empty registry (no skills loaded). Used when no skill
    /// directories exist or during tests that don't need skills.
    /// </summary>
    public SkillRegistry() : this([]) { }

    /// <summary>Look up a skill by name (case-insensitive). Returns null if not found.</summary>
    public SkillDefinition? Get(string name) =>
        _skills.GetValueOrDefault(name);

    /// <summary>All skills the model can invoke (excludes DisableModelInvocation).</summary>
    public IReadOnlyList<SkillDefinition> ModelInvocable() => _modelInvocable;

    /// <summary>All skills the user can invoke via slash commands (excludes !UserInvocable).</summary>
    public IReadOnlyList<SkillDefinition> UserInvocable() => _userInvocable;

    /// <summary>All loaded skills regardless of invocation restrictions.</summary>
    public IReadOnlyList<SkillDefinition> All() => _all;
}
