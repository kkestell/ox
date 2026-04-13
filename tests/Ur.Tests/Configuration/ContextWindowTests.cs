using Ox.Configuration;
using Ur.Tests.TestSupport;

namespace Ur.Tests.Configuration;

/// <summary>
/// Tests for context window resolution through ModelCatalog.
///
/// With providers.json, context window sizes are declared in the config file.
/// ModelCatalog.ResolveContextWindow first checks ProviderConfig, then falls
/// back to the IProvider instance (for FakeProvider scenarios).
/// </summary>
public sealed class ContextWindowTests
{
    // ─── ModelCatalog.ResolveContextWindow ───────────────────────

    [Fact]
    public void Resolve_KnownModel_ReturnsContextWindow()
    {
        // The test providers.json declares openai/gpt-4o with 128000 context.
        var config = TestProviderConfig.CreateDefault();
        var oxConfig = new ModelCatalog(config, []);

        var result = oxConfig.ResolveContextWindow("openai/gpt-4o");

        Assert.Equal(128_000, result);
    }

    [Fact]
    public void Resolve_UnknownProvider_ReturnsNull()
    {
        var config = TestProviderConfig.CreateDefault();
        var oxConfig = new ModelCatalog(config, []);

        var result = oxConfig.ResolveContextWindow("nonexistent/some-model");

        Assert.Null(result);
    }

    [Fact]
    public void Resolve_UnknownModel_ReturnsNull()
    {
        var config = TestProviderConfig.CreateDefault();
        var oxConfig = new ModelCatalog(config, []);

        var result = oxConfig.ResolveContextWindow("openai/nonexistent-model");

        Assert.Null(result);
    }

    [Fact]
    public void Resolve_OllamaModel_ReturnsContextWindow()
    {
        var config = TestProviderConfig.CreateDefault();
        var oxConfig = new ModelCatalog(config, []);

        var result = oxConfig.ResolveContextWindow("ollama/qwen3:4b");

        Assert.Equal(40_960, result);
    }

    [Fact]
    public void Resolve_GoogleModel_ReturnsContextWindow()
    {
        var config = TestProviderConfig.CreateDefault();
        var oxConfig = new ModelCatalog(config, []);

        var result = oxConfig.ResolveContextWindow("google/gemini-3.1-pro-preview");

        Assert.Equal(1_048_576, result);
    }

    [Fact]
    public void Resolve_InvalidModelId_ReturnsNull()
    {
        // A model ID without a slash can't be parsed — should return null, not throw.
        var config = TestProviderConfig.CreateDefault();
        var oxConfig = new ModelCatalog(config, []);

        var result = oxConfig.ResolveContextWindow("no-slash-model");

        Assert.Null(result);
    }
}
