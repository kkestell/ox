using Ox;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="OxBootOptions"/> CLI argument parsing.
/// </summary>
public sealed class OxBootOptionsTests
{
    [Fact]
    public void Parse_NoArgs_ReturnsDefaults()
    {
        var opts = OxBootOptions.Parse([]);

        Assert.Null(opts.FakeProviderScenario);
        Assert.Empty(opts.RemainingArgs);
    }

    [Fact]
    public void Parse_FakeProviderFlag_ExtractsScenario()
    {
        var opts = OxBootOptions.Parse(["--fake-provider", "hello"]);

        Assert.Equal("hello", opts.FakeProviderScenario);
        Assert.Empty(opts.RemainingArgs);
    }

    [Fact]
    public void Parse_FakeProviderWithOtherArgs_SeparatesCorrectly()
    {
        var opts = OxBootOptions.Parse(
            ["--environment", "Development", "--fake-provider", "tool-call", "--urls", "http://localhost:5000"]);

        Assert.Equal("tool-call", opts.FakeProviderScenario);
        Assert.Equal(["--environment", "Development", "--urls", "http://localhost:5000"], opts.RemainingArgs);
    }

    [Fact]
    public void Parse_FakeProviderWithFilePath_AcceptsPath()
    {
        var opts = OxBootOptions.Parse(["--fake-provider", "/tmp/my-scenario.json"]);

        Assert.Equal("/tmp/my-scenario.json", opts.FakeProviderScenario);
    }

    [Fact]
    public void Parse_FakeProviderWithoutValue_DoesNotCrash()
    {
        // --fake-provider at end with no following arg should be treated as
        // a regular arg (not consumed).
        var opts = OxBootOptions.Parse(["--fake-provider"]);

        Assert.Null(opts.FakeProviderScenario);
        Assert.Equal(["--fake-provider"], opts.RemainingArgs);
    }

    [Fact]
    public void Parse_UnknownArgs_PassedThrough()
    {
        var opts = OxBootOptions.Parse(["--verbose", "--port", "8080"]);

        Assert.Null(opts.FakeProviderScenario);
        Assert.Equal(["--verbose", "--port", "8080"], opts.RemainingArgs);
    }

    // ── Headless mode flags ──────────────────────────────────────────

    [Fact]
    public void Parse_HeadlessWithYoloAndTurn_SetsAllFields()
    {
        var opts = OxBootOptions.Parse(["--headless", "--yolo", "--turn", "hello"]);

        Assert.True(opts.IsHeadless);
        Assert.True(opts.IsYolo);
        Assert.Equal(["hello"], opts.Turns);
        Assert.Null(opts.FakeProviderScenario);
        Assert.Empty(opts.RemainingArgs);
    }

    [Fact]
    public void Parse_MultipleTurns_AccumulatesInOrder()
    {
        var opts = OxBootOptions.Parse([
            "--headless", "--yolo",
            "--turn", "first message",
            "--turn", "second message",
            "--turn", "third message"
        ]);

        Assert.True(opts.IsHeadless);
        Assert.Equal(["first message", "second message", "third message"], opts.Turns);
    }

    [Fact]
    public void Parse_ModelOverride_CapturesModelId()
    {
        var opts = OxBootOptions.Parse(["--headless", "--model", "openrouter/some-model", "--turn", "go"]);

        Assert.True(opts.IsHeadless);
        Assert.Equal("openrouter/some-model", opts.ModelOverride);
        Assert.Equal(["go"], opts.Turns);
    }

    [Fact]
    public void Parse_HeadlessWithFakeProvider_BothCoexist()
    {
        var opts = OxBootOptions.Parse([
            "--headless", "--yolo",
            "--fake-provider", "hello",
            "--turn", "test"
        ]);

        Assert.True(opts.IsHeadless);
        Assert.True(opts.IsYolo);
        Assert.Equal("hello", opts.FakeProviderScenario);
        Assert.Equal(["test"], opts.Turns);
    }

    [Fact]
    public void Parse_NoHeadlessFlag_DefaultsToFalse()
    {
        var opts = OxBootOptions.Parse([]);

        Assert.False(opts.IsHeadless);
        Assert.False(opts.IsYolo);
        Assert.Empty(opts.Turns);
        Assert.Null(opts.ModelOverride);
    }

    [Fact]
    public void Parse_HeadlessFlagsWithOtherArgs_SeparatesCorrectly()
    {
        var opts = OxBootOptions.Parse([
            "--environment", "Development",
            "--headless", "--yolo",
            "--turn", "hello",
            "--model", "google/gemini-3.1-flash",
            "--urls", "http://localhost:5000"
        ]);

        Assert.True(opts.IsHeadless);
        Assert.True(opts.IsYolo);
        Assert.Equal(["hello"], opts.Turns);
        Assert.Equal("google/gemini-3.1-flash", opts.ModelOverride);
        Assert.Equal(["--environment", "Development", "--urls", "http://localhost:5000"], opts.RemainingArgs);
    }
}
