using Ur.Skills;
using Ox;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="AutocompleteEngine.GetCompletion"/>.
///
/// The engine is tested against the real <see cref="BuiltInCommandRegistry"/> so
/// that priority behavior is exercised with the same data the production code uses.
/// Registration order is: clear, model, quit, set.
/// </summary>
public sealed class AutocompleteEngineTests
{
    private static AutocompleteEngine BuildEngine(params string[] skillNames)
    {
        var builtIns = new BuiltInCommandRegistry();
        var skills = new SkillRegistry(
            skillNames.Select(n => new SkillDefinition
            {
                Name = n, Description = n, UserInvocable = true,
                DisableModelInvocation = false, Content = "",
                SkillDirectory = $"/skills/{n}", Source = "user"
            }));
        return new AutocompleteEngine(new CommandRegistry(builtIns, skills));
    }

    [Fact]
    public void GetCompletion_SingleMatch_ReturnsSuffix()
    {
        // "/se" only matches "set" in the built-in list.
        var engine = BuildEngine();

        Assert.Equal("t", engine.GetCompletion("/se"));
    }

    [Fact]
    public void GetCompletion_MultipleMatches_ReturnsFirstMatch()
    {
        // "/mo" matches "model" — first registered match wins.
        var engine = BuildEngine();

        Assert.Equal("del", engine.GetCompletion("/mo"));
    }

    [Fact]
    public void GetCompletion_MultipleBuiltInsAndSkills_BuiltInWins()
    {
        // "clear" is a built-in; "cherry" is a skill. "/c" should return "lear"
        // (from built-in "clear") because built-ins take priority over skills.
        var engine = BuildEngine("cherry");

        Assert.Equal("lear", engine.GetCompletion("/c"));
    }

    [Fact]
    public void GetCompletion_NoMatch_ReturnsNull()
    {
        var engine = BuildEngine();

        Assert.Null(engine.GetCompletion("/xyz"));
    }

    [Fact]
    public void GetCompletion_InputWithoutSlash_ReturnsNull()
    {
        var engine = BuildEngine();

        Assert.Null(engine.GetCompletion("quit"));
        Assert.Null(engine.GetCompletion("q"));
    }

    [Fact]
    public void GetCompletion_SlashAlone_ReturnsNull()
    {
        var engine = BuildEngine();

        Assert.Null(engine.GetCompletion("/"));
    }

    [Fact]
    public void GetCompletion_ExactMatch_ReturnsNull()
    {
        // When the user has typed the full command name, nothing is left to suggest.
        var engine = BuildEngine();

        Assert.Null(engine.GetCompletion("/quit"));
        Assert.Null(engine.GetCompletion("/clear"));
    }

    [Fact]
    public void GetCompletion_InputWithArguments_ReturnsNull()
    {
        // "/quit now" includes a space — autocomplete doesn't apply once args are present.
        var engine = BuildEngine();

        Assert.Null(engine.GetCompletion("/quit now"));
    }

    [Fact]
    public void GetCompletion_CaseInsensitivePrefix_ReturnsRegistryCasing()
    {
        // Prefix matching is case-insensitive; the returned suffix uses the registry's casing.
        var engine = BuildEngine();

        // "/QU" should match "quit" and return "it" (lowercase, as registered).
        Assert.Equal("it", engine.GetCompletion("/QU"));
    }

    [Fact]
    public void GetCompletion_PrefixLongerThanAnyMatch_ReturnsNull()
    {
        // "/quits" — "quit" doesn't start with "quits".
        var engine = BuildEngine();

        Assert.Null(engine.GetCompletion("/quits"));
    }

    [Fact]
    public void GetCompletion_SkillOnlyMatch_ReturnsSuffix()
    {
        // "deploy" is not a built-in; it should still be suggested when typed.
        var engine = BuildEngine("deploy");

        Assert.Equal("ploy", engine.GetCompletion("/de"));
    }

    [Fact]
    public void GetCompletion_ExactMatchShadowsLongerCommand()
    {
        // "/model" exactly matches "model" — the engine returns null (exact match,
        // nothing to complete) rather than offering a suffix.
        var engine = BuildEngine();
        Assert.Null(engine.GetCompletion("/model"));
    }
}
