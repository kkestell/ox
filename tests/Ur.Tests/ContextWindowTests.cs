using Ur.Providers;
using Ur.Providers.Fake;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for context window resolution across providers and through UrHost.
///
/// Each provider resolves context window size from its own authoritative source.
/// These tests verify that the interface method works correctly for each provider,
/// that caching behaves as expected, and that UrHost dispatches correctly.
/// </summary>
public sealed class ContextWindowTests
{
    // ─── FakeProvider ───────────────────────────────────────────────

    [Fact]
    public async Task FakeProvider_ReturnsFixedContextWindow()
    {
        var provider = new FakeProvider();

        var result = await provider.GetContextWindowAsync("any-model");

        Assert.Equal(200_000, result);
    }

    // ─── OpenAiProvider ─────────────────────────────────────────────

    [Fact]
    public async Task OpenAi_KnownModel_ReturnsContextWindow()
    {
        var provider = new OpenAiProvider(new TestKeyring());

        var result = await provider.GetContextWindowAsync("gpt-4o");

        Assert.Equal(128_000, result);
    }

    [Fact]
    public async Task OpenAi_UnknownModel_ReturnsNull()
    {
        var provider = new OpenAiProvider(new TestKeyring());

        var result = await provider.GetContextWindowAsync("gpt-99-turbo");

        Assert.Null(result);
    }

    [Fact]
    public async Task OpenAi_LookupIsCaseInsensitive()
    {
        var provider = new OpenAiProvider(new TestKeyring());

        var result = await provider.GetContextWindowAsync("GPT-4O");

        Assert.Equal(128_000, result);
    }

    // ─── ZaiCodingProvider ──────────────────────────────────────────

    [Fact]
    public async Task ZaiCoding_KnownModel_ReturnsContextWindow()
    {
        var provider = new ZaiCodingProvider(new TestKeyring());

        var result = await provider.GetContextWindowAsync("glm-4.7");

        Assert.Equal(200_000, result);
    }

    [Fact]
    public async Task ZaiCoding_UnknownModel_ReturnsNull()
    {
        var provider = new ZaiCodingProvider(new TestKeyring());

        var result = await provider.GetContextWindowAsync("glm-99-turbo");

        Assert.Null(result);
    }

    [Fact]
    public async Task ZaiCoding_LookupIsCaseInsensitive()
    {
        var provider = new ZaiCodingProvider(new TestKeyring());

        var result = await provider.GetContextWindowAsync("GLM-4.7");

        Assert.Equal(200_000, result);
    }

    // ─── OpenRouterProvider ─────────────────────────────────────────

    [Fact]
    public async Task OpenRouter_ModelInCatalog_ReturnsContextLength()
    {
        // Pre-populate the catalog with a known model and context length.
        var catalog = TestCatalog.CreateWithModels(
            ("anthropic/claude-3.5-sonnet", 200_000));
        var provider = new OpenRouterProvider(new TestKeyring(), catalog);

        var result = await provider.GetContextWindowAsync("anthropic/claude-3.5-sonnet");

        Assert.Equal(200_000, result);
    }

    [Fact]
    public async Task OpenRouter_ModelNotInCatalog_ReturnsNull()
    {
        var catalog = TestCatalog.CreateEmpty();
        var provider = new OpenRouterProvider(new TestKeyring(), catalog);

        var result = await provider.GetContextWindowAsync("nonexistent/model");

        Assert.Null(result);
    }

    // ─── GoogleProvider ───────────────────────────────────────────────

    [Fact]
    public async Task Google_NoApiKey_ReturnsNull()
    {
        // Without an API key, the provider should short-circuit and return null
        // rather than attempting a network call.
        var provider = new GoogleProvider(new TestKeyring());

        var result = await provider.GetContextWindowAsync("gemini-2.0-flash");

        Assert.Null(result);
    }

    // ─── OllamaProvider ─────────────────────────────────────────────

    [Fact]
    public async Task Ollama_UnreachableEndpoint_ReturnsNull()
    {
        // Point Ollama at a non-existent endpoint so the HTTP call fails.
        // The provider should catch the exception and return null gracefully.
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        // Set Ollama URI to something that won't resolve.
        await host.Configuration.SetStringSettingAsync("ollama.uri", "http://192.0.2.1:99999");

        var provider = host.Configuration.ProviderRegistry.Get("ollama");
        Assert.NotNull(provider);

        var result = await provider.GetContextWindowAsync("nonexistent-model");

        Assert.Null(result);
    }

    // ─── UrHost.ResolveContextWindowAsync ───────────────────────────

    [Fact]
    public async Task UrHost_Resolve_DispatchesToCorrectProvider()
    {
        // The fake provider always returns 200,000 — verify UrHost parses the
        // model ID and delegates to the right provider.
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(
            workspace,
            keyring: new TestKeyring(),
            fakeProvider: new FakeProvider(),
            selectedModelOverride: "fake/hello");

        var result = await host.ResolveContextWindowAsync("fake/hello");

        Assert.Equal(200_000, result);
    }

    [Fact]
    public async Task UrHost_Resolve_UnknownProvider_ReturnsNull()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var result = await host.ResolveContextWindowAsync("nonexistent/some-model");

        Assert.Null(result);
    }

    [Fact]
    public async Task UrHost_Resolve_OpenAiKnownModel_ReturnsContextWindow()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var result = await host.ResolveContextWindowAsync("openai/gpt-4o");

        Assert.Equal(128_000, result);
    }
}
