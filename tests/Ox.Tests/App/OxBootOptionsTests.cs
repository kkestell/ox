using Ox.App;
using Ox;

namespace Ox.Tests.App;

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
    public void Parse_HeadlessWithYoloAndPrompt_SetsAllFields()
    {
        var opts = OxBootOptions.Parse(["--headless", "--yolo", "--prompt", "hello"]);

        Assert.True(opts.IsHeadless);
        Assert.True(opts.IsYolo);
        Assert.Equal("hello", opts.Prompt);
        Assert.Null(opts.FakeProviderScenario);
        Assert.Empty(opts.RemainingArgs);
    }

    [Fact]
    public void Parse_PromptLastValueWins()
    {
        // --prompt can appear multiple times; the last value wins (no accumulation).
        var opts = OxBootOptions.Parse([
            "--headless", "--yolo",
            "--prompt", "first message",
            "--prompt", "second message"
        ]);

        Assert.True(opts.IsHeadless);
        Assert.Equal("second message", opts.Prompt);
    }

    [Fact]
    public void Parse_ModelOverride_CapturesModelId()
    {
        var opts = OxBootOptions.Parse(["--headless", "--model", "openrouter/some-model", "--prompt", "go"]);

        Assert.True(opts.IsHeadless);
        Assert.Equal("openrouter/some-model", opts.ModelOverride);
        Assert.Equal("go", opts.Prompt);
    }

    [Fact]
    public void Parse_HeadlessWithFakeProvider_BothCoexist()
    {
        var opts = OxBootOptions.Parse([
            "--headless", "--yolo",
            "--fake-provider", "hello",
            "--prompt", "test"
        ]);

        Assert.True(opts.IsHeadless);
        Assert.True(opts.IsYolo);
        Assert.Equal("hello", opts.FakeProviderScenario);
        Assert.Equal("test", opts.Prompt);
    }

    [Fact]
    public void Parse_NoHeadlessFlag_DefaultsToFalse()
    {
        var opts = OxBootOptions.Parse([]);

        Assert.False(opts.IsHeadless);
        Assert.False(opts.IsYolo);
        Assert.Null(opts.Prompt);
        Assert.Null(opts.ModelOverride);
        Assert.Null(opts.MaxIterations);
    }

    [Fact]
    public void Parse_MaxIterationsFlag_ParsesPositiveInteger()
    {
        var opts = OxBootOptions.Parse(["--headless", "--yolo", "--max-iterations", "5", "--prompt", "go"]);

        Assert.True(opts.IsHeadless);
        Assert.Equal(5, opts.MaxIterations);
    }

    [Fact]
    public void Parse_MaxIterationsOne_IsValid()
    {
        // 1 is the minimum meaningful value — cap at one LLM call.
        var opts = OxBootOptions.Parse(["--headless", "--max-iterations", "1", "--prompt", "go"]);

        Assert.Equal(1, opts.MaxIterations);
    }

    [Fact]
    public void Parse_MaxIterationsZero_IsIgnored()
    {
        // Zero is not a valid cap (zero iterations means no LLM call at all — useless).
        // The parser treats it as absent rather than an error, so MaxIterations stays null.
        var opts = OxBootOptions.Parse(["--headless", "--max-iterations", "0", "--prompt", "go"]);

        Assert.Null(opts.MaxIterations);
    }

    [Fact]
    public void Parse_MaxIterationsNegative_IsIgnored()
    {
        var opts = OxBootOptions.Parse(["--headless", "--max-iterations", "-3", "--prompt", "go"]);

        Assert.Null(opts.MaxIterations);
    }

    [Fact]
    public void Parse_MaxIterationsWithoutValue_TreatedAsRemainingArg()
    {
        // --max-iterations at end with no following arg stays in remaining args.
        var opts = OxBootOptions.Parse(["--headless", "--prompt", "go", "--max-iterations"]);

        Assert.Null(opts.MaxIterations);
        Assert.Contains("--max-iterations", opts.RemainingArgs);
    }

    [Fact]
    public void Parse_NoMaxIterationsFlag_DefaultsToNull()
    {
        var opts = OxBootOptions.Parse(["--headless", "--yolo", "--prompt", "hello"]);

        Assert.Null(opts.MaxIterations);
    }

    [Fact]
    public void Parse_HeadlessFlagsWithOtherArgs_SeparatesCorrectly()
    {
        var opts = OxBootOptions.Parse([
            "--environment", "Development",
            "--headless", "--yolo",
            "--prompt", "hello",
            "--model", "google/gemini-3.1-flash",
            "--urls", "http://localhost:5000"
        ]);

        Assert.True(opts.IsHeadless);
        Assert.True(opts.IsYolo);
        Assert.Equal("hello", opts.Prompt);
        Assert.Equal("google/gemini-3.1-flash", opts.ModelOverride);
        Assert.Equal(["--environment", "Development", "--urls", "http://localhost:5000"], opts.RemainingArgs);
    }
}
