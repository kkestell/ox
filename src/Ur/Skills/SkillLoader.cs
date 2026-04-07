namespace Ur.Skills;

/// <summary>
/// Discovers and loads skills from the filesystem. Skills live in subdirectories
/// of a skills root (either user-scoped ~/.ur/skills/ or workspace-scoped .ur/skills/),
/// where each subdirectory contains a SKILL.md file.
///
/// Loading follows the same resilience pattern as extension loading: individual
/// malformed or unreadable skill files are logged to stderr and skipped rather
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
        string workspaceSkillsDir)
    {
        var userSkills = LoadFromDirectory(userSkillsDir, "user");
        var workspaceSkills = LoadFromDirectory(workspaceSkillsDir, "workspace");

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
        string source)
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
                // Same resilience pattern as extension loading — log and skip.
                Console.Error.WriteLine(
                    $"Skill '{Path.GetFileName(subDir)}' skipped: {ex.Message}");
            }
        }

        return skills;
    }
}
