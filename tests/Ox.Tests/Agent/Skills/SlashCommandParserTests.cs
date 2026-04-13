using Ox.Agent.Skills;

namespace Ox.Tests.Agent.Skills;

/// <summary>
/// Unit tests for <see cref="SlashCommandParser"/>. These pin the parsing
/// and formatting contract that OxSession depends on — the model expects
/// specific tag names and structure from <see cref="SlashCommandParser.FormatExpansion"/>.
/// </summary>
public sealed class SlashCommandParserTests
{
    // ─── ParseName ───────────────────────────────────────────────────

    [Fact]
    public void ParseName_CommandWithArgs_ReturnsName()
    {
        Assert.Equal("commit", SlashCommandParser.ParseName("/commit -m fix"));
    }

    [Fact]
    public void ParseName_CommandWithoutArgs_ReturnsName()
    {
        Assert.Equal("status", SlashCommandParser.ParseName("/status"));
    }

    [Fact]
    public void ParseName_CommandWithMultipleSpaces_ReturnsFirstToken()
    {
        Assert.Equal("deploy", SlashCommandParser.ParseName("/deploy prod us-east-1"));
    }

    // ─── ParseArgs ───────────────────────────────────────────────────

    [Fact]
    public void ParseArgs_CommandWithArgs_ReturnsArgs()
    {
        Assert.Equal("-m fix", SlashCommandParser.ParseArgs("/commit -m fix"));
    }

    [Fact]
    public void ParseArgs_CommandWithoutArgs_ReturnsEmpty()
    {
        Assert.Equal("", SlashCommandParser.ParseArgs("/status"));
    }

    [Fact]
    public void ParseArgs_CommandWithMultipleArgs_ReturnsAllArgs()
    {
        Assert.Equal("prod us-east-1", SlashCommandParser.ParseArgs("/deploy prod us-east-1"));
    }

    // ─── FormatExpansion ─────────────────────────────────────────────

    [Fact]
    public void FormatExpansion_ProducesExpectedTagStructure()
    {
        var result = SlashCommandParser.FormatExpansion("commit", "-m fix", "expanded content");

        // The model relies on these exact tag names to identify slash command output.
        Assert.Contains("<command-name>/commit</command-name>", result);
        Assert.Contains("<command-args>-m fix</command-args>", result);
        Assert.Contains("expanded content", result);
    }

    [Fact]
    public void FormatExpansion_EmptyArgs_IncludesEmptyArgsTag()
    {
        var result = SlashCommandParser.FormatExpansion("status", "", "status output");

        Assert.Contains("<command-name>/status</command-name>", result);
        Assert.Contains("<command-args></command-args>", result);
        Assert.Contains("status output", result);
    }
}
