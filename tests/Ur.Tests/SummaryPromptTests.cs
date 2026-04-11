using Ur.Compaction;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="SummaryPrompt"/>. Verifies the prompt structure contains
/// the expected section headers and key instructions.
/// </summary>
public sealed class SummaryPromptTests
{
    [Fact]
    public void Build_ContainsRequiredSections()
    {
        var prompt = SummaryPrompt.Build();

        Assert.Contains("Primary Request and Intent", prompt);
        Assert.Contains("Key Files and Code Changes", prompt);
        Assert.Contains("Errors and Fixes", prompt);
        Assert.Contains("Current Work / Pending Tasks", prompt);
        Assert.Contains("User Messages", prompt);
    }

    [Fact]
    public void Build_InstructsNoToolCalls()
    {
        var prompt = SummaryPrompt.Build();

        // The prompt must explicitly tell the model not to use tool calls.
        Assert.Contains("Do NOT use tool calls", prompt);
    }

    [Fact]
    public void Build_IsNonEmpty()
    {
        var prompt = SummaryPrompt.Build();

        Assert.False(string.IsNullOrWhiteSpace(prompt));
    }
}
