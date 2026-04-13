using Ox.Agent.Skills;

namespace Ox.Tests.Agent.Skills;

public sealed class SkillRegistryTests
{
    private static SkillDefinition MakeSkill(
        string name,
        bool userInvocable = true,
        bool disableModelInvocation = false) =>
        new()
        {
            Name = name,
            Description = $"The {name} skill",
            UserInvocable = userInvocable,
            DisableModelInvocation = disableModelInvocation,
            Content = "content",
            SkillDirectory = $"/skills/{name}",
            Source = "user"
        };

    [Fact]
    public void Get_CaseInsensitiveLookup()
    {
        var registry = new SkillRegistry([MakeSkill("Commit")]);

        Assert.NotNull(registry.Get("commit"));
        Assert.NotNull(registry.Get("COMMIT"));
        Assert.NotNull(registry.Get("Commit"));
    }

    [Fact]
    public void Get_UnknownSkill_ReturnsNull()
    {
        var registry = new SkillRegistry([MakeSkill("commit")]);

        Assert.Null(registry.Get("deploy"));
    }

    [Fact]
    public void ModelInvocable_ExcludesDisabledSkills()
    {
        var registry = new SkillRegistry([
            MakeSkill("public-skill"),
            MakeSkill("hidden", disableModelInvocation: true)
        ]);

        var modelSkills = registry.ModelInvocable();

        Assert.Single(modelSkills);
        Assert.Equal("public-skill", modelSkills[0].Name);
    }

    [Fact]
    public void UserInvocable_ExcludesNonUserSkills()
    {
        var registry = new SkillRegistry([
            MakeSkill("slash-cmd"),
            MakeSkill("model-only", userInvocable: false)
        ]);

        var userSkills = registry.UserInvocable();

        Assert.Single(userSkills);
        Assert.Equal("slash-cmd", userSkills[0].Name);
    }

    [Fact]
    public void All_ReturnsAllSkillsRegardlessOfFlags()
    {
        var registry = new SkillRegistry([
            MakeSkill("a"),
            MakeSkill("b", userInvocable: false),
            MakeSkill("c", disableModelInvocation: true)
        ]);

        Assert.Equal(3, registry.All().Count);
    }

    [Fact]
    public void Empty_RegistryReturnsEmptyLists()
    {
        var registry = new SkillRegistry();

        Assert.Empty(registry.All());
        Assert.Empty(registry.ModelInvocable());
        Assert.Empty(registry.UserInvocable());
        Assert.Null(registry.Get("anything"));
    }

    [Fact]
    public void ModelInvocable_IncludesNonUserInvocableSkills()
    {
        // A skill can be model-only: user-invocable=false but model invocation allowed.
        var registry = new SkillRegistry([
            MakeSkill("model-only", userInvocable: false, disableModelInvocation: false)
        ]);

        Assert.Single(registry.ModelInvocable());
        Assert.Empty(registry.UserInvocable());
    }

    [Fact]
    public void UserInvocable_IncludesModelDisabledSkills()
    {
        // A skill can be user-only: disable-model-invocation=true but user-invocable=true.
        var registry = new SkillRegistry([
            MakeSkill("user-only", userInvocable: true, disableModelInvocation: true)
        ]);

        Assert.Empty(registry.ModelInvocable());
        Assert.Single(registry.UserInvocable());
    }
}
