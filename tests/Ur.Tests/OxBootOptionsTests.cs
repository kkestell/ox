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
}
