using Ur.Skills;
using Ox;

namespace Ur.Tests.Skills;

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

/// <summary>
/// Tests for the argument-completion phase of <see cref="AutocompleteEngine.GetCompletion"/>.
///
/// The argument phase triggers when input contains a space (e.g. "/model open").
/// The engine prefix-matches the typed argument against the per-command argument
/// list and returns the suffix needed to reach the first match.
/// </summary>
public sealed class AutocompleteEngineArgumentTests
{
    // Representative set: sorted alphabetically so prefix queries are predictable.
    private static readonly IReadOnlyList<string> ModelIds =
    [
        "anthropic/claude-3",
        "openai/gpt-4o",
        "openai/gpt-5",
    ];

    private static AutocompleteEngine BuildEngineWithArgs(
        IReadOnlyDictionary<string, IReadOnlyList<string>>? args = null)
    {
        var builtIns = new BuiltInCommandRegistry();
        var skills = new SkillRegistry([]);
        var registry = new CommandRegistry(builtIns, skills);
        var completions = args ?? new Dictionary<string, IReadOnlyList<string>>
        {
            ["model"] = ModelIds,
        };
        return new AutocompleteEngine(registry, completions);
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_PrefixMatch_ReturnsSuffix()
    {
        // "/model openai/gpt" should suggest "-5" (first alphabetical match for "openai/gpt").
        // Both "openai/gpt-4o" and "openai/gpt-5" start with "openai/gpt"; the first
        // alphabetical entry ("openai/gpt-4o") wins.
        var engine = BuildEngineWithArgs();

        Assert.Equal("-4o", engine.GetCompletion("/model openai/gpt"));
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_ExactMatch_ReturnsNull()
    {
        // "/model openai/gpt-4o" is a full, exact argument — nothing left to suggest.
        var engine = BuildEngineWithArgs();

        Assert.Null(engine.GetCompletion("/model openai/gpt-4o"));
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_NoMatch_ReturnsNull()
    {
        // "/model nonexistent" doesn't prefix-match any known model ID.
        var engine = BuildEngineWithArgs();

        Assert.Null(engine.GetCompletion("/model nonexistent"));
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_EmptyArg_ReturnsNull()
    {
        // "/model " (trailing space, zero characters typed after it) should not
        // suggest anything — at least one character after the space is required.
        var engine = BuildEngineWithArgs();

        Assert.Null(engine.GetCompletion("/model "));
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_CaseInsensitiveCommandName_Matches()
    {
        // "/Model open" has a mixed-case command name; the engine normalizes to
        // lowercase before dictionary lookup, so it matches the "model" entry.
        var engine = BuildEngineWithArgs();

        Assert.Equal("ai/gpt-4o", engine.GetCompletion("/Model open"));
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_UnregisteredCommand_ReturnsNull()
    {
        // "/set something" has no argument completion dictionary entry — null.
        var engine = BuildEngineWithArgs();

        Assert.Null(engine.GetCompletion("/set something"));
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_OtherRegisteredCommand_Matches()
    {
        // The argument-completion dictionary can hold entries for other commands too,
        // not just "model". Verify that "/deploy prod" works for a "deploy" entry.
        var args = new Dictionary<string, IReadOnlyList<string>>
        {
            ["deploy"] = ["prod", "staging", "dev"],
        };
        var engine = BuildEngineWithArgs(args);

        Assert.Equal("aging", engine.GetCompletion("/deploy st"));
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_NoArgumentCompletionsDictionary_ReturnsNull()
    {
        // When the engine is constructed without an argument-completion dictionary,
        // any input with a space returns null rather than throwing.
        var builtIns = new BuiltInCommandRegistry();
        var skills = new SkillRegistry([]);
        var registry = new CommandRegistry(builtIns, skills);
        var engine = new AutocompleteEngine(registry); // no dictionary

        Assert.Null(engine.GetCompletion("/model open"));
    }

    [Fact]
    public void GetCompletion_ArgumentPhase_CommandNamePhaseUnchanged()
    {
        // Argument completions should not affect command-name completion (no space).
        // "/mo" should still return "del" even when argument completions are registered.
        var engine = BuildEngineWithArgs();

        Assert.Equal("del", engine.GetCompletion("/mo"));
    }
}
