using Ur.Skills;

namespace Ur.Tests.Skills;

public sealed class SystemPromptBuilderTests
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
    public void Build_EmptyRegistry_ReturnsNull()
    {
        var registry = new SkillRegistry();

        Assert.Null(SystemPromptBuilder.Build(registry));
    }

    [Fact]
    public void Build_OnlyDisabledSkills_ReturnsNull()
    {
        var registry = new SkillRegistry([
            MakeSkill("hidden", disableModelInvocation: true)
        ]);

        // No model-invocable skills → no system prompt.
        Assert.Null(SystemPromptBuilder.Build(registry));
    }

    [Fact]
    public void Build_IncludesSkillNameAndDescription()
    {
        var registry = new SkillRegistry([
            MakeSkill("commit", description: "Commit staged changes")
        ]);

        var prompt = SystemPromptBuilder.Build(registry);

        Assert.NotNull(prompt);
        Assert.Contains("commit", prompt);
        Assert.Contains("Commit staged changes", prompt);
    }

    [Fact]
    public void Build_IncludesWhenToUse()
    {
        var registry = new SkillRegistry([
            MakeSkill("deploy", description: "Deploy the app", whenToUse: "When user wants to deploy")
        ]);

        var prompt = SystemPromptBuilder.Build(registry);

        Assert.NotNull(prompt);
        Assert.Contains("When user wants to deploy", prompt);
    }

    [Fact]
    public void Build_TruncatesLongWhenToUse()
    {
        var longHint = new string('x', 300);
        var registry = new SkillRegistry([
            MakeSkill("verbose", whenToUse: longHint)
        ]);

        var prompt = SystemPromptBuilder.Build(registry);

        Assert.NotNull(prompt);
        // Should be truncated to 250 chars + "..."
        Assert.DoesNotContain(longHint, prompt);
        Assert.Contains("...", prompt);
    }

    [Fact]
    public void Build_WhenToUseExactly250Chars_NotTruncated()
    {
        var exact = new string('y', 250);
        var registry = new SkillRegistry([
            MakeSkill("exact", whenToUse: exact)
        ]);

        var prompt = SystemPromptBuilder.Build(registry);

        Assert.NotNull(prompt);
        Assert.Contains(exact, prompt);
        Assert.DoesNotContain("...", prompt);
    }

    [Fact]
    public void Build_ExcludesModelDisabledSkills()
    {
        var registry = new SkillRegistry([
            MakeSkill("visible", description: "I should appear"),
            MakeSkill("hidden", description: "I should not", disableModelInvocation: true)
        ]);

        var prompt = SystemPromptBuilder.Build(registry);

        Assert.NotNull(prompt);
        Assert.Contains("visible", prompt);
        Assert.DoesNotContain("hidden", prompt);
    }

    [Fact]
    public void Build_ListsMultipleSkills()
    {
        var registry = new SkillRegistry([
            MakeSkill("commit", description: "Commit changes"),
            MakeSkill("deploy", description: "Deploy app"),
            MakeSkill("review", description: "Review PR")
        ]);

        var prompt = SystemPromptBuilder.Build(registry);

        Assert.NotNull(prompt);
        Assert.Contains("- commit: Commit changes", prompt);
        Assert.Contains("- deploy: Deploy app", prompt);
        Assert.Contains("- review: Review PR", prompt);
    }
}
