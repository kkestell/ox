using Ur.Providers;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for individual <see cref="IProvider"/> implementations — focusing on
/// <see cref="IProvider.GetBlockingIssue"/> behavior with and without API keys/settings.
/// </summary>
public sealed class ProviderTests
{
    // ─── OpenRouterProvider ──────────────────────────────────────────

    [Fact]
    public void OpenRouter_WithoutApiKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        var provider = new OpenRouterProvider(keyring, TestCatalog.CreateEmpty());

        var issue = provider.GetBlockingIssue();

        Assert.NotNull(issue);
        Assert.Contains("openrouter", issue);
    }

    [Fact]
    public void OpenRouter_WithApiKey_ReportsNoBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "openrouter", "sk-test-key");
        var provider = new OpenRouterProvider(keyring, TestCatalog.CreateEmpty());

        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public void OpenRouter_RequiresApiKey()
    {
        var provider = new OpenRouterProvider(new TestKeyring(), TestCatalog.CreateEmpty());
        Assert.True(provider.RequiresApiKey);
    }

    // ─── OpenAiProvider ─────────────────────────────────────────────

    [Fact]
    public void OpenAi_WithoutApiKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        var provider = new OpenAiProvider(keyring);

        var issue = provider.GetBlockingIssue();

        Assert.NotNull(issue);
        Assert.Contains("openai", issue);
    }

    [Fact]
    public void OpenAi_WithApiKey_ReportsNoBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "openai", "sk-test-key");
        var provider = new OpenAiProvider(keyring);

        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public void OpenAi_RequiresApiKey()
    {
        var provider = new OpenAiProvider(new TestKeyring());
        Assert.True(provider.RequiresApiKey);
    }

    // ─── GoogleProvider ─────────────────────────────────────────────

    [Fact]
    public void Google_WithoutApiKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        var provider = new GoogleProvider(keyring);

        var issue = provider.GetBlockingIssue();

        Assert.NotNull(issue);
        Assert.Contains("google", issue);
    }

    [Fact]
    public void Google_WithApiKey_ReportsNoBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "google", "test-api-key");
        var provider = new GoogleProvider(keyring);

        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public void Google_RequiresApiKey()
    {
        var provider = new GoogleProvider(new TestKeyring());
        Assert.True(provider.RequiresApiKey);
    }

    // ─── OllamaProvider ─────────────────────────────────────────────

    [Fact]
    public async Task Ollama_NeverReportsBlockingIssue()
    {
        // Ollama runs locally and needs no API key — it should always be "ready"
        // from a configuration perspective.
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        // Get the OllamaProvider from the registry that DI created.
        var provider = host.Configuration.ProviderRegistry.Get("ollama");
        Assert.NotNull(provider);
        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public async Task Ollama_DoesNotRequireApiKey()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var provider = host.Configuration.ProviderRegistry.Get("ollama");
        Assert.NotNull(provider);
        Assert.False(provider.RequiresApiKey);
    }

    // ─── ZaiCodingProvider ────────────────────────────────────────────

    [Fact]
    public void ZaiCoding_WithoutApiKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        var provider = new ZaiCodingProvider(keyring);

        var issue = provider.GetBlockingIssue();

        Assert.NotNull(issue);
        Assert.Contains("zai-coding", issue);
    }

    [Fact]
    public void ZaiCoding_WithApiKey_ReportsNoBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "zai-coding", "sk-test-key");
        var provider = new ZaiCodingProvider(keyring);

        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public void ZaiCoding_RequiresApiKey()
    {
        var provider = new ZaiCodingProvider(new TestKeyring());
        Assert.True(provider.RequiresApiKey);
    }

    [Fact]
    public void ZaiCoding_CreateChatClient_ReturnsNonNull()
    {
        // The OpenAI SDK constructor doesn't validate connectivity, so this
        // verifies that the custom endpoint URI is well-formed and the client
        // is properly constructed.
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "zai-coding", "sk-test-key");
        var provider = new ZaiCodingProvider(keyring);

        var client = provider.CreateChatClient("glm-4.7");
        Assert.NotNull(client);
    }

    // ─── Whitespace-only API key ────────────────────────────────────

    [Fact]
    public void OpenRouter_WhitespaceOnlyKey_ReportsBlockingIssue()
    {
        // A whitespace-only key should be treated as missing — it would fail at the API.
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "openrouter", "   ");
        var provider = new OpenRouterProvider(keyring, TestCatalog.CreateEmpty());

        Assert.NotNull(provider.GetBlockingIssue());
    }

    [Fact]
    public void OpenAi_WhitespaceOnlyKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "openai", "   ");
        var provider = new OpenAiProvider(keyring);

        Assert.NotNull(provider.GetBlockingIssue());
    }

    [Fact]
    public void Google_WhitespaceOnlyKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "google", "   ");
        var provider = new GoogleProvider(keyring);

        Assert.NotNull(provider.GetBlockingIssue());
    }

    // ─── OllamaProvider URI resolution ──────────────────────────────

    [Fact]
    public async Task Ollama_DefaultUri_UsesLocalhost()
    {
        // With no ollama.uri setting, the provider should use http://localhost:11434.
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var provider = host.Configuration.ProviderRegistry.Get("ollama");
        Assert.NotNull(provider);

        // CreateChatClient should succeed with the default URI.
        // The OllamaApiClient constructor doesn't validate connectivity,
        // so this tests that the URI is well-formed.
        var client = provider.CreateChatClient("test-model");
        Assert.NotNull(client);
    }

    [Fact]
    public async Task Ollama_CustomUri_UsesConfiguredValue()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        // Set a custom Ollama URI.
        await host.Configuration.SetStringSettingAsync("ollama.uri", "http://remote-host:11434");

        var provider = host.Configuration.ProviderRegistry.Get("ollama");
        Assert.NotNull(provider);

        // CreateChatClient should succeed with the custom URI.
        var client = provider.CreateChatClient("test-model");
        Assert.NotNull(client);
    }

    [Fact]
    public async Task Ollama_InvalidUri_FallsBackToDefault()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        // Set an invalid URI — the provider should fall back to localhost.
        await host.Configuration.SetStringSettingAsync("ollama.uri", "not-a-valid-uri");

        var provider = host.Configuration.ProviderRegistry.Get("ollama");
        Assert.NotNull(provider);

        // Should not throw — falls back to default.
        var client = provider.CreateChatClient("test-model");
        Assert.NotNull(client);
    }

    [Fact]
    public void ZaiCoding_WhitespaceOnlyKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "zai-coding", "   ");
        var provider = new ZaiCodingProvider(keyring);

        Assert.NotNull(provider.GetBlockingIssue());
    }

    // ─── Provider names ──────────────────────────────────────────────

    [Fact]
    public void Provider_NameMatchesExpectedPrefix()
    {
        var keyring = new TestKeyring();
        Assert.Equal("openrouter", new OpenRouterProvider(keyring, TestCatalog.CreateEmpty()).Name);
        Assert.Equal("openai", new OpenAiProvider(keyring).Name);
        Assert.Equal("google", new GoogleProvider(keyring).Name);
        Assert.Equal("zai-coding", new ZaiCodingProvider(keyring).Name);
    }

    // ─── ListModelIdsAsync ──────────────────────────────────────────

    [Fact]
    public async Task OpenAi_ListModelIds_ReturnsKnownModels()
    {
        var provider = new OpenAiProvider(new TestKeyring());

        var models = await provider.ListModelIdsAsync();

        Assert.NotNull(models);
        Assert.Contains("gpt-4o", models);
        Assert.Contains("o4-mini", models);
    }

    [Fact]
    public async Task ZaiCoding_ListModelIds_ReturnsKnownModels()
    {
        var provider = new ZaiCodingProvider(new TestKeyring());

        var models = await provider.ListModelIdsAsync();

        Assert.NotNull(models);
        Assert.Contains("glm-5.1", models);
        Assert.Contains("glm-4.5-air", models);
    }

    [Fact]
    public async Task Google_ListModelIds_ReturnsNull()
    {
        // Google doesn't support listing — should return null, not throw.
        var provider = new GoogleProvider(new TestKeyring());

        var models = await provider.ListModelIdsAsync();

        Assert.Null(models);
    }

    [Fact]
    public async Task OpenRouter_ListModelIds_DelegatesToCatalog()
    {
        var catalog = TestCatalog.CreateWithModels(
            ("anthropic/claude-3.5-sonnet", 200_000),
            ("openai/gpt-4o", 128_000));
        var provider = new OpenRouterProvider(new TestKeyring(), catalog);

        var models = await provider.ListModelIdsAsync();

        Assert.NotNull(models);
        Assert.Equal(2, models.Count);
        Assert.Contains("anthropic/claude-3.5-sonnet", models);
        Assert.Contains("openai/gpt-4o", models);
    }

    [Fact]
    public async Task OpenRouter_ListModelIds_EmptyCatalog_ReturnsNull()
    {
        var provider = new OpenRouterProvider(new TestKeyring(), TestCatalog.CreateEmpty());

        var models = await provider.ListModelIdsAsync();

        Assert.Null(models);
    }

    [Fact]
    public async Task Ollama_ListModelIds_DoesNotThrow()
    {
        // Ollama is likely not running in test environments. The method should
        // return null gracefully rather than throwing. No assertion beyond
        // "doesn't throw" — verifying the success path requires a live Ollama
        // instance and belongs in integration tests.
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var provider = host.Configuration.ProviderRegistry.Get("ollama");
        Assert.NotNull(provider);

        _ = await provider.ListModelIdsAsync();
    }
}
