using Ur.Providers;
using Ur.Providers.Fake;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for context window resolution through ProviderConfig and UrHost.
///
/// With providers.json, context window sizes are declared in the config file
/// rather than queried from individual providers. These tests verify that
/// UrHost dispatches correctly via ProviderConfig.
/// </summary>
public sealed class ContextWindowTests
{
    // ─── UrHost.ResolveContextWindow ────────────────────────────────

    [Fact]
    public async Task UrHost_Resolve_KnownModel_ReturnsContextWindow()
    {
        // The test providers.json declares openai/gpt-4o with 128000 context.
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var result = host.ResolveContextWindow("openai/gpt-4o");

        Assert.Equal(128_000, result);
    }

    [Fact]
    public async Task UrHost_Resolve_UnknownProvider_ReturnsNull()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var result = host.ResolveContextWindow("nonexistent/some-model");

        Assert.Null(result);
    }

    [Fact]
    public async Task UrHost_Resolve_UnknownModel_ReturnsNull()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var result = host.ResolveContextWindow("openai/nonexistent-model");

        Assert.Null(result);
    }

    [Fact]
    public async Task UrHost_Resolve_OllamaModel_ReturnsContextWindow()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var result = host.ResolveContextWindow("ollama/qwen3:4b");

        Assert.Equal(40_960, result);
    }

    [Fact]
    public async Task UrHost_Resolve_GoogleModel_ReturnsContextWindow()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var result = host.ResolveContextWindow("google/gemini-3.1-pro-preview");

        Assert.Equal(1_048_576, result);
    }

    [Fact]
    public async Task UrHost_Resolve_InvalidModelId_ReturnsNull()
    {
        // A model ID without a slash can't be parsed — should return null, not throw.
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var result = host.ResolveContextWindow("no-slash-model");

        Assert.Null(result);
    }
}
