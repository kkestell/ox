using Ur.Providers;
using Ur.Tests.TestSupport;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="ProviderRegistry"/> — lookup, registration, and unknown-provider behavior.
/// </summary>
public sealed class ProviderRegistryTests
{
    [Fact]
    public void Get_RegisteredProvider_ReturnsProvider()
    {
        var registry = new ProviderRegistry();
        var keyring = new TestKeyring();
        var provider = new OpenRouterProvider(keyring, TestCatalog.CreateEmpty());
        registry.Register(provider);

        var result = registry.Get("openrouter");

        Assert.NotNull(result);
        Assert.Equal("openrouter", result.Name);
    }

    [Fact]
    public void Get_UnknownProvider_ReturnsNull()
    {
        var registry = new ProviderRegistry();

        Assert.Null(registry.Get("nonexistent"));
    }

    [Fact]
    public void Get_IsCaseInsensitive()
    {
        var registry = new ProviderRegistry();
        var keyring = new TestKeyring();
        registry.Register(new OpenRouterProvider(keyring, TestCatalog.CreateEmpty()));

        Assert.NotNull(registry.Get("OpenRouter"));
        Assert.NotNull(registry.Get("OPENROUTER"));
    }

    [Fact]
    public void Register_DuplicateName_ThrowsInvalidOperationException()
    {
        var registry = new ProviderRegistry();
        var keyring = new TestKeyring();
        registry.Register(new OpenRouterProvider(keyring, TestCatalog.CreateEmpty()));

        Assert.Throws<InvalidOperationException>(() =>
            registry.Register(new OpenRouterProvider(keyring, TestCatalog.CreateEmpty())));
    }

    [Fact]
    public void ProviderNames_ReturnsAllRegisteredNames()
    {
        var registry = new ProviderRegistry();
        var keyring = new TestKeyring();
        registry.Register(new OpenRouterProvider(keyring, TestCatalog.CreateEmpty()));
        registry.Register(new OpenAiProvider(keyring));

        Assert.Contains("openrouter", registry.ProviderNames);
        Assert.Contains("openai", registry.ProviderNames);
    }
}
