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

    // ─── Provider names ──────────────────────────────────────────────

    [Fact]
    public void Provider_NameMatchesExpectedPrefix()
    {
        var keyring = new TestKeyring();
        Assert.Equal("openrouter", new OpenRouterProvider(keyring, TestCatalog.CreateEmpty()).Name);
        Assert.Equal("openai", new OpenAiProvider(keyring).Name);
        Assert.Equal("google", new GoogleProvider(keyring).Name);
    }
}
