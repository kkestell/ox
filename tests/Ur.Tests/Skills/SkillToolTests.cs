using Microsoft.Extensions.AI;
using Ur.Skills;

namespace Ur.Tests.Skills;

public sealed class SkillToolTests
{
    private static SkillRegistry BuildRegistry(params SkillDefinition[] skills) =>
        new(skills);

    private static SkillDefinition MakeSkill(
        string name,
        string content = "Skill content for $ARGUMENTS",
        bool disableModelInvocation = false,
        bool userInvocable = true) =>
        new()
        {
            Name = name,
            Description = $"The {name} skill",
            Content = content,
            DisableModelInvocation = disableModelInvocation,
            UserInvocable = userInvocable,
            SkillDirectory = $"/skills/{name}",
            Source = "user",
        };

    private static async Task<string?> InvokeAsync(
        AIFunction tool,
        params (string Key, object? Value)[] args)
    {
        var dict = new Dictionary<string, object?>();
        foreach (var (key, value) in args)
            dict[key] = value;
        return (string?)await tool.InvokeAsync(new AIFunctionArguments(dict));
    }

    // ─── Successful invocation ────────────────────────────────────────

    [Fact]
    public async Task Invoke_ValidSkill_ReturnsExpandedContent()
    {
        var registry = BuildRegistry(MakeSkill("commit", "Committing: $ARGUMENTS"));
        var tool = new SkillTool(registry, "session-1");

        var result = await InvokeAsync(tool,
            ("skill", "commit"),
            ("args", "fix typo"));

        Assert.Equal("Committing: fix typo", result);
    }

    // ─── Unknown skill ────────────────────────────────────────────────

    [Fact]
    public async Task Invoke_UnknownSkill_ReturnsError()
    {
        var registry = BuildRegistry();
        var tool = new SkillTool(registry, "session-1");

        var result = await InvokeAsync(tool, ("skill", "nonexistent"));

        Assert.Contains("Unknown skill", result);
    }

    // ─── Model invocation disabled ────────────────────────────────────

    [Fact]
    public async Task Invoke_DisableModelInvocation_ReturnsError()
    {
        var registry = BuildRegistry(
            MakeSkill("secret", disableModelInvocation: true));
        var tool = new SkillTool(registry, "session-1");

        var result = await InvokeAsync(tool, ("skill", "secret"));

        Assert.Contains("cannot be invoked by the model", result);
    }

    // ─── Leading slash stripped ───────────────────────────────────────

    [Fact]
    public async Task Invoke_LeadingSlash_StrippedFromSkillName()
    {
        var registry = BuildRegistry(MakeSkill("deploy", "Deploying $ARGUMENTS"));
        var tool = new SkillTool(registry, "session-1");

        var result = await InvokeAsync(tool,
            ("skill", "/deploy"),
            ("args", "prod"));

        Assert.Equal("Deploying prod", result);
    }

    // ─── No args provided ─────────────────────────────────────────────

    [Fact]
    public async Task Invoke_NoArgs_ExpandsWithEmptyArgs()
    {
        var registry = BuildRegistry(MakeSkill("status", "Status check: $ARGUMENTS"));
        var tool = new SkillTool(registry, "session-1");

        var result = await InvokeAsync(tool, ("skill", "status"));

        Assert.Equal("Status check: ", result);
    }

    // ─── Session ID substitution ──────────────────────────────────────

    [Fact]
    public async Task Invoke_SessionIdSubstituted()
    {
        var registry = BuildRegistry(
            MakeSkill("track", "Session ${UR_SESSION_ID} action"));
        var tool = new SkillTool(registry, "my-session-42");

        var result = await InvokeAsync(tool, ("skill", "track"));

        Assert.Equal("Session my-session-42 action", result);
    }
}
