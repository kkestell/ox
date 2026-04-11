using Ur.Providers;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for individual <see cref="IProvider"/> implementations — focusing on
/// <see cref="IProvider.GetBlockingIssue"/> behavior with and without API keys/settings.
///
/// After the providers.json migration, OpenAI, OpenRouter, and ZaiCoding are all
/// backed by <see cref="OpenAiCompatibleProvider"/>. Google and Ollama keep their
/// dedicated implementations.
/// </summary>
public sealed class ProviderTests
{
    // ─── OpenAiCompatibleProvider (generic) ──────────────────────────

    [Fact]
    public void OpenAiCompatible_WithoutApiKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        var provider = new OpenAiCompatibleProvider("openai", null, keyring);

        var issue = provider.GetBlockingIssue();

        Assert.NotNull(issue);
        Assert.Contains("openai", issue);
    }

    [Fact]
    public void OpenAiCompatible_WithApiKey_ReportsNoBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "openai", "sk-test-key");
        var provider = new OpenAiCompatibleProvider("openai", null, keyring);

        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public void OpenAiCompatible_RequiresApiKey()
    {
        var provider = new OpenAiCompatibleProvider("openai", null, new TestKeyring());
        Assert.True(provider.RequiresApiKey);
    }

    [Fact]
    public void OpenAiCompatible_CustomEndpoint_ReportsBlockingIssue()
    {
        // OpenRouter-style provider with custom endpoint but no key.
        var keyring = new TestKeyring();
        var provider = new OpenAiCompatibleProvider(
            "openrouter", new Uri("https://openrouter.ai/api/v1"), keyring);

        var issue = provider.GetBlockingIssue();

        Assert.NotNull(issue);
        Assert.Contains("openrouter", issue);
    }

    [Fact]
    public void OpenAiCompatible_CustomEndpoint_WithApiKey_ReportsNoBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "openrouter", "sk-test-key");
        var provider = new OpenAiCompatibleProvider(
            "openrouter", new Uri("https://openrouter.ai/api/v1"), keyring);

        Assert.Null(provider.GetBlockingIssue());
    }

    [Fact]
    public void OpenAiCompatible_CreateChatClient_WithCustomEndpoint_ReturnsNonNull()
    {
        // Verifies that the OpenAI SDK constructor accepts the custom endpoint.
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "zai-coding", "sk-test-key");
        var provider = new OpenAiCompatibleProvider(
            "zai-coding", new Uri("https://open.bigmodel.cn/api/paas/v4"), keyring);

        var client = provider.CreateChatClient("glm-4.7");
        Assert.NotNull(client);
    }

    [Fact]
    public void OpenAiCompatible_NameMatchesConstructorArg()
    {
        var keyring = new TestKeyring();
        Assert.Equal("openai", new OpenAiCompatibleProvider("openai", null, keyring).Name);
        Assert.Equal("openrouter", new OpenAiCompatibleProvider("openrouter", new Uri("https://openrouter.ai/api/v1"), keyring).Name);
        Assert.Equal("zai-coding", new OpenAiCompatibleProvider("zai-coding", new Uri("https://open.bigmodel.cn/api/paas/v4"), keyring).Name);
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
        // OllamaProvider now takes name + endpoint directly.
        var provider = new OllamaProvider("ollama", new Uri("http://localhost:11434"));
        var client = provider.CreateChatClient("test-model");
        Assert.NotNull(client);
    }

    // ─── Whitespace-only API key ────────────────────────────────────

    [Fact]
    public void OpenAiCompatible_WhitespaceOnlyKey_ReportsBlockingIssue()
    {
        var keyring = new TestKeyring();
        keyring.SetSecret("ur", "openai", "   ");
        var provider = new OpenAiCompatibleProvider("openai", null, keyring);

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
    public async Task DI_OpenAiCompatible_ProvidersHaveCorrectType()
    {
        using var workspace = new TempWorkspace();
        var host = await TestHostBuilder.CreateHostAsync(workspace);

        var registry = host.Configuration.ProviderRegistry;

        // openai, openrouter, zai-coding should all be OpenAiCompatibleProvider.
        Assert.True(registry.Get("openai")!.RequiresApiKey);
        Assert.True(registry.Get("openrouter")!.RequiresApiKey);
        Assert.True(registry.Get("zai-coding")!.RequiresApiKey);

        // ollama should not require API key.
        Assert.False(registry.Get("ollama")!.RequiresApiKey);
    }
}
