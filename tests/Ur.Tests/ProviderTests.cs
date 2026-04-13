using Ur.Providers;
using Ur.Providers.Google;
using Ur.Providers.Ollama;
using Ur.Providers.OpenAI;
using Ur.Providers.OpenAiCompatible;
using Ur.Providers.OpenRouter;
using Ur.Providers.ZaiCoding;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for individual <see cref="IProvider"/> implementations — focusing on
/// <see cref="IProvider.GetBlockingIssue"/> behavior with and without API keys/settings.
///
/// Each built-in provider (OpenAI, OpenRouter, Google, Ollama, ZaiCoding) lives in
/// its own project. The generic <see cref="OpenAiCompatibleProvider"/> serves as
/// a fallback for custom OpenAI-protocol providers.
/// </summary>
public sealed class ProviderTests
{
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

    [Fact]
    public void OpenAi_HasCorrectDisplayName()
    {
        var provider = new OpenAiProvider(new TestKeyring());
        Assert.Equal("OpenAI", provider.DisplayName);
    }

    // ─── OpenRouterProvider ─────────────────────────────────────────

    [Fact]
    public void OpenRouter_WithoutApiKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        var provider = new OpenRouterProvider(keyring);

        var issue = provider.GetBlockingIssue();

        Assert.NotNull(issue);
        Assert.Contains("openrouter", issue);
    }

    [Fact]
    public void OpenRouter_WithApiKey_ReportsNoBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "openrouter", "sk-test-key");
        var provider = new OpenRouterProvider(keyring);

        Assert.Null(provider.GetBlockingIssue());
    }

    // ─── ZaiCodingProvider ──────────────────────────────────────────

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

    // ─── OpenAiCompatibleProvider (custom fallback) ─────────────────

    [Fact]
    public void OpenAiCompatible_CustomEndpoint_WithoutApiKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        var provider = new OpenAiCompatibleProvider(
            "custom-provider", "Custom Provider", new Uri("https://custom.example.com/v1"), keyring);

        var issue = provider.GetBlockingIssue();

        Assert.NotNull(issue);
        Assert.Contains("custom-provider", issue);
    }

    [Fact]
    public void OpenAiCompatible_CustomEndpoint_WithApiKey_ReportsNoBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "custom-provider", "sk-test-key");
        var provider = new OpenAiCompatibleProvider(
            "custom-provider", "Custom Provider", new Uri("https://custom.example.com/v1"), keyring);

        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public void OpenAiCompatible_CreateChatClient_WithCustomEndpoint_ReturnsNonNull()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "custom-provider", "sk-test-key");
        var provider = new OpenAiCompatibleProvider(
            "custom-provider", "Custom Provider", new Uri("https://custom.example.com/v1"), keyring);

        var client = provider.CreateChatClient("test-model");
        Assert.NotNull(client);
    }

    [Fact]
    public void OpenAiCompatible_NameAndDisplayNameFromConstructor()
    {
        var keyring = new TestKeyring();
        var provider = new OpenAiCompatibleProvider(
            "my-provider", "My Custom LLM", new Uri("https://example.com/v1"), keyring);
        Assert.Equal("my-provider", provider.Name);
        Assert.Equal("My Custom LLM", provider.DisplayName);
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
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

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

    [Fact]
    public void Ollama_CreateChatClient_ReturnsNonNull()
    {
        var provider = new OllamaProvider(new Uri("http://localhost:11434"));
        var client = provider.CreateChatClient("test-model");
        Assert.NotNull(client);
    }

    // ─── Whitespace-only API key ────────────────────────────────────

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

    // ─── Config-driven provider registration ────────────────────────

    [Fact]
    public async Task DI_RegistersAllProviders_FromConfig()
    {
        // The default test providers.json declares 5 providers. Verify they all
        // appear in the registry after DI resolution.
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var registry = host.Configuration.ProviderRegistry;
        Assert.NotNull(registry.Get("openai"));
        Assert.NotNull(registry.Get("google"));
        Assert.NotNull(registry.Get("ollama"));
        Assert.NotNull(registry.Get("openrouter"));
        Assert.NotNull(registry.Get("zai-coding"));
    }

    [Fact]
    public async Task DI_ProvidersHaveCorrectRequiresApiKey()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var registry = host.Configuration.ProviderRegistry;

        // openai, openrouter, zai-coding all require API keys.
        Assert.True(registry.Get("openai")!.RequiresApiKey);
        Assert.True(registry.Get("openrouter")!.RequiresApiKey);
        Assert.True(registry.Get("zai-coding")!.RequiresApiKey);

        // ollama should not require API key.
        Assert.False(registry.Get("ollama")!.RequiresApiKey);
    }
}
