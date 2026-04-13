using Microsoft.Extensions.Logging;

namespace Ox.Agent.Skills;

/// <summary>
/// Discovers and loads skills from the filesystem. Skills live in subdirectories
/// of a skills root (either user-scoped ~/.ox/skills/ or workspace-scoped .ox/skills/),
/// where each subdirectory contains a SKILL.md file.
///
/// Individual malformed or unreadable skill files are logged and skipped rather
/// than failing the entire startup.
///
/// All methods are synchronous — skill loading is local filesystem I/O only,
/// and runs during startup where sync construction simplifies DI registration.
/// </summary>
internal static class SkillLoader
{
    /// <summary>
    /// Loads skills from both the user and workspace directories, with workspace
    /// skills taking precedence on name collision. This matches the reference
    /// implementation's "last writer wins by source priority" deduplication.
    /// </summary>
    public static IReadOnlyList<SkillDefinition> LoadAll(
        string userSkillsDir,
        string workspaceSkillsDir,
        ILogger? logger = null)
    {
        var userSkills = LoadFromDirectory(userSkillsDir, "user", logger);
        var workspaceSkills = LoadFromDirectory(workspaceSkillsDir, "workspace", logger);

        // Merge with workspace skills taking precedence over user skills on name collision.
        // Use case-insensitive comparison so "Commit" and "commit" are treated as the same skill.
        var merged = new Dictionary<string, SkillDefinition>(StringComparer.OrdinalIgnoreCase);

        foreach (var skill in userSkills)
            merged[skill.Name] = skill;

        // Workspace skills overwrite user skills — intentional precedence.
        foreach (var skill in workspaceSkills)
            merged[skill.Name] = skill;

        return merged.Values.ToList();
    }

    /// <summary>
    /// Scans a directory for subdirectories containing SKILL.md files.
    /// Each valid SKILL.md is parsed into a <see cref="SkillDefinition"/>.
    /// Directories without SKILL.md are silently skipped; parse errors are
    /// logged to stderr and skipped.
    /// </summary>
    public static IReadOnlyList<SkillDefinition> LoadFromDirectory(
        string skillsDir,
        string source,
        ILogger? logger = null)
    {
        if (!Directory.Exists(skillsDir))
            return [];

        var skills = new List<SkillDefinition>();

        foreach (var subDir in Directory.EnumerateDirectories(skillsDir))
        {
            var skillFile = Path.Combine(subDir, "SKILL.md");
            if (!File.Exists(skillFile))
                continue;

            try
            {
                var content = File.ReadAllText(skillFile);
                var skill = SkillFrontmatter.Parse(content, subDir, source);
                skills.Add(skill);
            }
            catch (Exception ex) when (ex is not OperationCanceledException)
            {
                // Log and skip — one bad skill file shouldn't block startup.
                logger?.LogWarning("Skill '{SkillName}' skipped: {Error}",
                    Path.GetFileName(subDir), ex.Message);
            }
        }

        return skills;
    }
}
