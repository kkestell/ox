using Ox.Agent.Skills;

namespace Ox.Tests.Agent.Skills;

public sealed class SkillPromptSectionBuilderTests
{
    private static SkillDefinition MakeSkill(
        string name,
        string? description = null,
        string? whenToUse = null,
        bool disableModelInvocation = false) =>
        new()
        {
            Name = name,
            Description = description ?? "",
            WhenToUse = whenToUse,
            DisableModelInvocation = disableModelInvocation,
            Content = "content",
            SkillDirectory = $"/skills/{name}",
            Source = "user"
        };

    [Fact]
    public void Build_EmptyRegistry_ReturnsEmptyString()
    {
        var registry = new SkillRegistry();

        var section = SkillPromptSectionBuilder.Build(registry);

        Assert.Equal("", section);
    }

    [Fact]
    public void Build_OnlyDisabledSkills_ReturnsEmptyString()
    {
        var registry = new SkillRegistry([
            MakeSkill("hidden", disableModelInvocation: true)
        ]);

        var section = SkillPromptSectionBuilder.Build(registry);

        Assert.Equal("", section);
    }

    [Fact]
    public void Build_IncludesSkillNameAndDescription()
    {
        var registry = new SkillRegistry([
            MakeSkill("commit", description: "Commit staged changes")
        ]);

        var section = SkillPromptSectionBuilder.Build(registry);

        Assert.Contains("commit", section);
        Assert.Contains("Commit staged changes", section);
    }

    [Fact]
    public void Build_IncludesWhenToUse()
    {
        var registry = new SkillRegistry([
            MakeSkill("deploy", description: "Deploy the app", whenToUse: "When user wants to deploy")
        ]);

        var section = SkillPromptSectionBuilder.Build(registry);

        Assert.Contains("When user wants to deploy", section);
    }

    [Fact]
    public void Build_TruncatesLongWhenToUse()
    {
        var longHint = new string('x', 300);
        var registry = new SkillRegistry([
            MakeSkill("verbose", whenToUse: longHint)
        ]);

        var section = SkillPromptSectionBuilder.Build(registry);

        Assert.DoesNotContain(longHint, section);
        Assert.Contains("...", section);
    }

    [Fact]
    public void Build_WhenToUseExactly250Chars_NotTruncated()
    {
        var exact = new string('y', 250);
        var registry = new SkillRegistry([
            MakeSkill("exact", whenToUse: exact)
        ]);

        var section = SkillPromptSectionBuilder.Build(registry);

        Assert.Contains(exact, section);
        Assert.DoesNotContain("...", section);
    }

    [Fact]
    public void Build_ExcludesModelDisabledSkills()
    {
        var registry = new SkillRegistry([
            MakeSkill("visible", description: "I should appear"),
            MakeSkill("hidden", description: "I should not", disableModelInvocation: true)
        ]);

        var section = SkillPromptSectionBuilder.Build(registry);

        Assert.Contains("visible", section);
        Assert.DoesNotContain("hidden", section);
    }

    [Fact]
    public void Build_ListsMultipleSkills()
    {
        var registry = new SkillRegistry([
            MakeSkill("commit", description: "Commit changes"),
            MakeSkill("deploy", description: "Deploy app"),
            MakeSkill("review", description: "Review PR")
        ]);

        var section = SkillPromptSectionBuilder.Build(registry);

        Assert.Contains("- commit: Commit changes", section);
        Assert.Contains("- deploy: Deploy app", section);
        Assert.Contains("- review: Review PR", section);
    }
}
