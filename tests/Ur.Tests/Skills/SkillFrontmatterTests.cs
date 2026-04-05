using Ur.Skills;

namespace Ur.Tests.Skills;

public sealed class SkillFrontmatterTests
{
    private const string SkillDir = "/test/skills/my-skill";
    private const string Source = "user";

    // ─── Full frontmatter ─────────────────────────────────────────────

    [Fact]
    public void Parse_CompleteFrontmatter_MapsAllFields()
    {
        var content = """
            ---
            name: commit
            description: Commit staged changes
            when-to-use: When the user asks to commit
            user-invocable: true
            disable-model-invocation: false
            argument-hint: <message>
            arguments: message, scope
            context: inline
            agent: coder
            paths: src/**/*.cs, tests/**/*.cs
            allowed-tools: bash, read_file
            model: gpt-4
            version: 1.0.0
            ---
            Commit with message: $message in scope $scope
            """;

        var skill = SkillFrontmatter.Parse(content, SkillDir, Source);

        Assert.Equal("commit", skill.Name);
        Assert.Equal("Commit staged changes", skill.Description);
        Assert.Equal("When the user asks to commit", skill.WhenToUse);
        Assert.True(skill.UserInvocable);
        Assert.False(skill.DisableModelInvocation);
        Assert.Equal("<message>", skill.ArgumentHint);
        Assert.Equal(["message", "scope"], skill.ArgumentNames!);
        Assert.Equal("inline", skill.Context);
        Assert.Equal("coder", skill.Agent);
        Assert.Equal(["src/**/*.cs", "tests/**/*.cs"], skill.Paths!);
        Assert.Equal(["bash", "read_file"], skill.AllowedTools!);
        Assert.Equal("gpt-4", skill.Model);
        Assert.Equal("1.0.0", skill.Version);
        Assert.Contains("Commit with message", skill.Content);
        Assert.Equal(SkillDir, skill.SkillDirectory);
        Assert.Equal(Source, skill.Source);
    }

    // ─── Minimal (no frontmatter) ─────────────────────────────────────

    [Fact]
    public void Parse_NoFrontmatter_UsesDirectoryNameAndDefaults()
    {
        var content = "Just some prompt text, no frontmatter.";

        var skill = SkillFrontmatter.Parse(content, SkillDir, Source);

        // Name derived from directory: "/test/skills/my-skill" → "my-skill"
        Assert.Equal("my-skill", skill.Name);
        Assert.Equal("", skill.Description);
        Assert.True(skill.UserInvocable);
        Assert.False(skill.DisableModelInvocation);
        Assert.Null(skill.ArgumentNames);
        Assert.Equal(content, skill.Content);
    }

    // ─── Missing name defaults to directory ───────────────────────────

    [Fact]
    public void Parse_MissingName_DefaultsToDirectoryName()
    {
        var content = """
            ---
            description: A skill without an explicit name
            ---
            Do the thing.
            """;

        var skill = SkillFrontmatter.Parse(content, "/skills/auto-named", Source);

        Assert.Equal("auto-named", skill.Name);
    }

    // ─── Comma-separated fields ───────────────────────────────────────

    [Fact]
    public void Parse_CommaSeparatedArguments_ParsedIntoArray()
    {
        var content = """
            ---
            arguments: first, second, third
            ---
            Content
            """;

        var skill = SkillFrontmatter.Parse(content, SkillDir, Source);

        Assert.Equal(["first", "second", "third"], skill.ArgumentNames!);
    }

    [Fact]
    public void Parse_CommaSeparatedPaths_ParsedIntoArray()
    {
        var content = """
            ---
            paths: "*.cs, *.fs"
            ---
            Content
            """;

        var skill = SkillFrontmatter.Parse(content, SkillDir, Source);

        Assert.NotNull(skill.Paths);
        Assert.Equal(2, skill.Paths.Length);
        Assert.Equal("*.cs", skill.Paths[0]);
        Assert.Equal("*.fs", skill.Paths[1]);
    }

    // ─── Boolean fields ──────────────────────────────────────────────

    [Fact]
    public void Parse_UserInvocableFalse_ParsedCorrectly()
    {
        var content = """
            ---
            user-invocable: false
            ---
            Hidden from users.
            """;

        var skill = SkillFrontmatter.Parse(content, SkillDir, Source);

        Assert.False(skill.UserInvocable);
    }

    [Fact]
    public void Parse_DisableModelInvocationTrue_ParsedCorrectly()
    {
        var content = """
            ---
            disable-model-invocation: true
            ---
            User only.
            """;

        var skill = SkillFrontmatter.Parse(content, SkillDir, Source);

        Assert.True(skill.DisableModelInvocation);
    }

    // ─── Edge cases ──────────────────────────────────────────────────

    [Fact]
    public void Parse_EmptyFrontmatter_UsesDefaults()
    {
        var content = """
            ---
            ---
            Just body content.
            """;

        var skill = SkillFrontmatter.Parse(content, SkillDir, Source);

        Assert.Equal("my-skill", skill.Name);
        Assert.Equal("", skill.Description);
        Assert.Contains("Just body content", skill.Content);
    }

    [Fact]
    public void Parse_FrontmatterWithoutClosingDelimiter_TreatedAsNoFrontmatter()
    {
        // A "---" at the start but no closing "---" means no valid frontmatter block.
        var content = "---\nname: broken\nThis is not closed properly.";

        var skill = SkillFrontmatter.Parse(content, SkillDir, Source);

        // Falls back to treating entire content as body.
        Assert.Equal("my-skill", skill.Name);
        Assert.Equal(content, skill.Content);
    }
}
