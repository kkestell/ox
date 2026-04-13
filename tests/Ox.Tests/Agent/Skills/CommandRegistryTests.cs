using Ox.Agent.Skills;

namespace Ox.Tests.Agent.Skills;

public sealed class CommandRegistryTests
{
    private static SkillDefinition MakeSkill(string name, bool userInvocable = true) =>
        new()
        {
            Name = name,
            Description = $"The {name} skill",
            UserInvocable = userInvocable,
            DisableModelInvocation = false,
            Content = "content",
            SkillDirectory = $"/skills/{name}",
            Source = "user"
        };

    [Fact]
    public void UserInvocableNames_BuiltInsBeforeSkills()
    {
        // The ordering guarantee: all built-in names appear before any skill names.
        var builtIns = new BuiltInCommandRegistry();
        var skills = new SkillRegistry([MakeSkill("deploy"), MakeSkill("release")]);
        var registry = new CommandRegistry(builtIns, skills);

        var names = registry.UserInvocableNames;
        // Find the index of the first skill name in the list.
        var firstSkillIdx = names
            .Select((n, i) => (n, i))
            .First(pair => pair.n == "deploy" || pair.n == "release").i;

        // All built-in names must appear before any skill name.
        // If built-ins and skills were interleaved, firstSkillIdx would be less than builtIns.All.Count.
        Assert.Equal(builtIns.All.Count, firstSkillIdx);
    }

    [Fact]
    public void UserInvocableNames_IncludesAllBuiltIns()
    {
        var builtIns = new BuiltInCommandRegistry();
        var registry = new CommandRegistry(builtIns, new SkillRegistry());

        foreach (var cmd in builtIns.All)
            Assert.Contains(cmd.Name, registry.UserInvocableNames);
    }

    [Fact]
    public void UserInvocableNames_IncludesUserInvocableSkills()
    {
        var builtIns = new BuiltInCommandRegistry();
        var skills = new SkillRegistry([
            MakeSkill("commit"),
            MakeSkill("model-only", userInvocable: false)
        ]);
        var registry = new CommandRegistry(builtIns, skills);

        Assert.Contains("commit", registry.UserInvocableNames);
        Assert.DoesNotContain("model-only", registry.UserInvocableNames);
    }

    [Fact]
    public void UserInvocableNames_ExcludesNonUserInvocableSkills()
    {
        var builtIns = new BuiltInCommandRegistry();
        var skills = new SkillRegistry([MakeSkill("hidden", userInvocable: false)]);
        var registry = new CommandRegistry(builtIns, skills);

        Assert.DoesNotContain("hidden", registry.UserInvocableNames);
    }

    [Fact]
    public void UserInvocableNames_EmptySkillRegistry_OnlyBuiltIns()
    {
        var builtIns = new BuiltInCommandRegistry();
        var registry = new CommandRegistry(builtIns, new SkillRegistry());

        Assert.Equal(builtIns.All.Count, registry.UserInvocableNames.Count);
    }
}
