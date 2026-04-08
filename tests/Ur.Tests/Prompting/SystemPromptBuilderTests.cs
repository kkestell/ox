using Ur.Prompting;

namespace Ur.Tests.Prompting;

public sealed class SystemPromptBuilderTests
{
    [Fact]
    public void Build_WithoutSections_ContainsBaselinePrompt()
    {
        var prompt = SystemPromptBuilder.Build();

        Assert.NotNull(prompt);
        Assert.Contains("todo_write", prompt);
        Assert.DoesNotContain("following skills", prompt);
    }

    [Fact]
    public void Build_WithSection_AppendsSectionAfterBaselinePrompt()
    {
        var prompt = SystemPromptBuilder.Build("Skills section");

        Assert.NotNull(prompt);
        Assert.Contains("todo_write", prompt);
        Assert.Contains("Skills section", prompt);
        Assert.True(prompt.IndexOf("todo_write", StringComparison.Ordinal)
                    < prompt.IndexOf("Skills section", StringComparison.Ordinal));
    }

    [Fact]
    public void Build_IgnoresEmptySections()
    {
        var prompt = SystemPromptBuilder.Build("", "  ", null);

        Assert.NotNull(prompt);
        Assert.Contains("todo_write", prompt);
        Assert.DoesNotContain("following skills", prompt);
    }
}
