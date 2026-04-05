using Ur.Skills;

namespace Ur.Tests.Skills;

public sealed class SkillLoaderTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "ur-skill-loader-tests",
        Guid.NewGuid().ToString("N"));

    private string UserSkillsDir => Path.Combine(_root, "user-skills");
    private string WorkspaceSkillsDir => Path.Combine(_root, "workspace-skills");

    public SkillLoaderTests()
    {
        Directory.CreateDirectory(UserSkillsDir);
        Directory.CreateDirectory(WorkspaceSkillsDir);
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
            Directory.Delete(_root, recursive: true);
    }

    private void WriteSkill(string parentDir, string skillName, string content)
    {
        var dir = Path.Combine(parentDir, skillName);
        Directory.CreateDirectory(dir);
        File.WriteAllText(Path.Combine(dir, "SKILL.md"), content);
    }

    // ─── Basic loading ────────────────────────────────────────────────

    [Fact]
    public async Task LoadFromDirectory_LoadsValidSkills()
    {
        WriteSkill(UserSkillsDir, "hello", """
            ---
            name: hello
            description: Say hello
            ---
            Hello, world!
            """);

        WriteSkill(UserSkillsDir, "greet", """
            ---
            name: greet
            description: Greet someone
            ---
            Hi there, $ARGUMENTS!
            """);

        var skills = await SkillLoader.LoadFromDirectoryAsync(UserSkillsDir, "user");

        Assert.Equal(2, skills.Count);
        Assert.Contains(skills, s => s.Name == "hello");
        Assert.Contains(skills, s => s.Name == "greet");
    }

    // ─── Missing directory ────────────────────────────────────────────

    [Fact]
    public async Task LoadFromDirectory_NonexistentDirectory_ReturnsEmpty()
    {
        var skills = await SkillLoader.LoadFromDirectoryAsync(
            "/nonexistent/path/skills", "user");

        Assert.Empty(skills);
    }

    // ─── Skips directories without SKILL.md ───────────────────────────

    [Fact]
    public async Task LoadFromDirectory_SkipsDirectoriesWithoutSkillMd()
    {
        WriteSkill(UserSkillsDir, "valid", "---\nname: valid\n---\nContent");
        // Create a directory without SKILL.md.
        Directory.CreateDirectory(Path.Combine(UserSkillsDir, "no-skill-file"));

        var skills = await SkillLoader.LoadFromDirectoryAsync(UserSkillsDir, "user");

        Assert.Single(skills);
        Assert.Equal("valid", skills[0].Name);
    }

    // ─── Malformed SKILL.md is skipped without crashing ──────────────

    [Fact]
    public async Task LoadFromDirectory_MalformedSkillFile_SkippedGracefully()
    {
        WriteSkill(UserSkillsDir, "good", "---\nname: good\n---\nGood content");
        // Write a SKILL.md with structurally invalid YAML (unclosed flow mapping)
        // to trigger a parse error regardless of deserialization strategy.
        WriteSkill(UserSkillsDir, "bad", "---\nname: {unclosed\n---\nBad");

        var skills = await SkillLoader.LoadFromDirectoryAsync(UserSkillsDir, "user");

        // The good skill should still load; the bad one is skipped.
        Assert.Single(skills);
        Assert.Equal("good", skills[0].Name);
    }

    // ─── Workspace overrides user on name collision ───────────────────

    [Fact]
    public async Task LoadAll_WorkspaceSkillsOverrideUserSkills()
    {
        WriteSkill(UserSkillsDir, "commit", """
            ---
            name: commit
            description: User-level commit skill
            ---
            User commit
            """);

        WriteSkill(WorkspaceSkillsDir, "commit", """
            ---
            name: commit
            description: Workspace-level commit skill
            ---
            Workspace commit
            """);

        var skills = await SkillLoader.LoadAllAsync(UserSkillsDir, WorkspaceSkillsDir);

        // Only one "commit" skill should exist, and it should be the workspace version.
        var commit = Assert.Single(skills, s => s.Name == "commit");
        Assert.Equal("workspace", commit.Source);
        Assert.Equal("Workspace-level commit skill", commit.Description);
    }

    // ─── Both sources merge correctly ─────────────────────────────────

    [Fact]
    public async Task LoadAll_MergesSkillsFromBothSources()
    {
        WriteSkill(UserSkillsDir, "user-only", "---\nname: user-only\n---\nUser skill");
        WriteSkill(WorkspaceSkillsDir, "ws-only", "---\nname: ws-only\n---\nWorkspace skill");

        var skills = await SkillLoader.LoadAllAsync(UserSkillsDir, WorkspaceSkillsDir);

        Assert.Equal(2, skills.Count);
        Assert.Contains(skills, s => s.Name == "user-only");
        Assert.Contains(skills, s => s.Name == "ws-only");
    }

    // ─── Source tracking ──────────────────────────────────────────────

    [Fact]
    public async Task LoadFromDirectory_SetsSourceCorrectly()
    {
        WriteSkill(UserSkillsDir, "test", "---\nname: test\n---\nContent");

        var skills = await SkillLoader.LoadFromDirectoryAsync(UserSkillsDir, "user");

        Assert.Single(skills);
        Assert.Equal("user", skills[0].Source);
    }
}
