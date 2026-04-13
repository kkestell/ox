namespace Ur.Skills;

/// <summary>
/// The core data model for a loaded skill. Represents a parsed SKILL.md file
/// with its frontmatter metadata and raw content body. Skills are prompt
/// templates that extend the agent's capabilities — they are pure data,
/// not executable code.
///
/// Some fields are parsed and stored but not yet activated (marked with TODO).
/// This lets us handle the full SKILL.md format without implementing every
/// feature upfront — the data is ready when the feature lands.
/// </summary>
public sealed class SkillDefinition
{
    /// <summary>Unique name used to invoke the skill (e.g. "commit", "review-pr").</summary>
    public required string Name { get; init; }

    /// <summary>Short description shown in system prompt and help listings.</summary>
    public required string Description { get; init; }

    /// <summary>Guidance for the model on when this skill should be invoked.</summary>
    public string? WhenToUse { get; init; }

    /// <summary>Whether users can invoke this skill via /slash commands. Defaults to true.</summary>
    public bool UserInvocable { get; init; } = true;

    /// <summary>If true, the model cannot call this skill via the skill tool.</summary>
    public bool DisableModelInvocation { get; init; }

    /// <summary>Hint text shown to users about what arguments the skill accepts.</summary>
    public string? ArgumentHint { get; init; }

    /// <summary>Named arguments parsed from the comma-separated "arguments" frontmatter field.</summary>
    public string[]? ArgumentNames { get; init; }

    /// <summary>Execution context: "inline" or "fork". TODO: fork support — always runs inline for now.</summary>
    public string? Context { get; init; }

    /// <summary>Agent type for forked execution. TODO: sub-agent mechanism not yet implemented.</summary>
    public string? Agent { get; init; }

    /// <summary>File path patterns for conditional activation. TODO: not yet activated.</summary>
    public string[]? Paths { get; init; }

    /// <summary>Tool restrictions when this skill is active. TODO: not yet enforced.</summary>
    public string[]? AllowedTools { get; init; }

    /// <summary>Model override for this skill's execution. TODO: not yet enforced.</summary>
    public string? Model { get; init; }

    /// <summary>Optional version string from the skill author.</summary>
    public string? Version { get; init; }

    /// <summary>The raw markdown body below the frontmatter — the actual prompt template.</summary>
    public required string Content { get; init; }

    /// <summary>Absolute path to the skill's directory on disk (used for ${OX_SKILL_DIR} expansion).</summary>
    public required string SkillDirectory { get; init; }

    /// <summary>Where this skill was loaded from: "user" (~/.ox/skills/) or "workspace" (.ox/skills/).</summary>
    public required string Source { get; init; }
}
