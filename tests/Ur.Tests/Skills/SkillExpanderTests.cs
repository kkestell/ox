using Ur.Skills;

namespace Ur.Tests.Skills;

public sealed class SkillExpanderTests
{
    private static SkillDefinition MakeSkill(
        string content,
        string[]? argumentNames = null,
        string skillDir = "/skills/test-skill") =>
        new()
        {
            Name = "test",
            Description = "Test skill",
            ArgumentNames = argumentNames,
            Content = content,
            SkillDirectory = skillDir,
            Source = "user"
        };

    // ─── $ARGUMENTS substitution ──────────────────────────────────────

    [Fact]
    public void Expand_ReplacesArgumentsPlaceholder()
    {
        var skill = MakeSkill("Review PR $ARGUMENTS for quality.");

        var result = SkillExpander.Expand(skill, "123", "session-1");

        Assert.Equal("Review PR 123 for quality.", result);
    }

    [Fact]
    public void Expand_EmptyArgs_ReplacesWithEmptyString()
    {
        var skill = MakeSkill("Do the thing: $ARGUMENTS");

        var result = SkillExpander.Expand(skill, "", "session-1");

        Assert.Equal("Do the thing: ", result);
    }

    // ─── Named argument substitution ──────────────────────────────────

    [Fact]
    public void Expand_NamedArguments_ReplacedIndividually()
    {
        var skill = MakeSkill(
            "Commit message: $message\nScope: $scope",
            argumentNames: ["message", "scope"]);

        var result = SkillExpander.Expand(skill, "fix-bug backend", "session-1");

        Assert.Equal("Commit message: fix-bug\nScope: backend", result);
    }

    [Fact]
    public void Expand_NamedArguments_LeftoverGoesToArguments()
    {
        var skill = MakeSkill(
            "First: $first\nRest: $ARGUMENTS",
            argumentNames: ["first"]);

        var result = SkillExpander.Expand(skill, "one two three", "session-1");

        Assert.Equal("First: one\nRest: two three", result);
    }

    [Fact]
    public void Expand_FewerArgsThanNames_MissingBecomesEmpty()
    {
        var skill = MakeSkill(
            "A=$a B=$b",
            argumentNames: ["a", "b"]);

        var result = SkillExpander.Expand(skill, "only-one", "session-1");

        Assert.Equal("A=only-one B=", result);
    }

    [Fact]
    public void Expand_ExactArgCount_ArgumentsBecomesEmpty()
    {
        // When the number of provided args exactly matches the named arg count,
        // $ARGUMENTS should be empty (no leftover). This pins the boundary
        // between "has leftover" and "no leftover" in SplitArgs.
        var skill = MakeSkill(
            "$a $b $ARGUMENTS",
            argumentNames: ["a", "b"]);

        var result = SkillExpander.Expand(skill, "x y", "session-1");

        Assert.Equal("x y ", result);
    }

    // ─── Variable substitutions ───────────────────────────────────────

    [Fact]
    public void Expand_ReplacesSkillDir()
    {
        var skill = MakeSkill(
            "Read from ${OX_SKILL_DIR}/assets/template.md",
            skillDir: "/home/user/.ox/skills/commit");

        var result = SkillExpander.Expand(skill, "", "session-1");

        Assert.Equal("Read from /home/user/.ox/skills/commit/assets/template.md", result);
    }

    [Fact]
    public void Expand_ReplacesSessionId()
    {
        var skill = MakeSkill("Session: ${OX_SESSION_ID}");

        var result = SkillExpander.Expand(skill, "", "abc-123");

        Assert.Equal("Session: abc-123", result);
    }

    // ─── Combined substitutions ───────────────────────────────────────

    [Fact]
    public void Expand_AllSubstitutionsWorkTogether()
    {
        var skill = MakeSkill(
            "Skill dir: ${OX_SKILL_DIR}\nSession: ${OX_SESSION_ID}\nTarget: $target\nArgs: $ARGUMENTS",
            argumentNames: ["target"],
            skillDir: "/skills/multi");

        var result = SkillExpander.Expand(skill, "main extra stuff", "sess-99");

        Assert.Contains("/skills/multi", result);
        Assert.Contains("sess-99", result);
        Assert.Contains("Target: main", result);
        Assert.Contains("Args: extra stuff", result);
    }
}
