using Ox.Agent.Providers;

namespace Ox.Tests.Agent.Providers;

/// <summary>
/// Tests for <see cref="ModelId.Parse"/> — the provider/model splitting logic
/// that drives provider dispatch throughout the system.
/// </summary>
public sealed class ModelIdTests
{
    [Fact]
    public void Parse_SimpleProviderAndModel_SplitsCorrectly()
    {
        var id = ModelId.Parse("openai/gpt-5-nano");

        Assert.Equal("openai", id.Provider);
        Assert.Equal("gpt-5-nano", id.Model);
    }

    [Fact]
    public void Parse_OpenRouterNestedModel_KeepsRemainderAsModel()
    {
        // OpenRouter model IDs are themselves slash-delimited (e.g. "anthropic/claude-3.5-sonnet").
        // Only the first segment is the provider; everything after is the model.
        var id = ModelId.Parse("openrouter/anthropic/claude-3.5-sonnet");

        Assert.Equal("openrouter", id.Provider);
        Assert.Equal("anthropic/claude-3.5-sonnet", id.Model);
    }

    [Fact]
    public void Parse_OllamaModelWithTag_PreservesTag()
    {
        var id = ModelId.Parse("ollama/qwen3:4b");

        Assert.Equal("ollama", id.Provider);
        Assert.Equal("qwen3:4b", id.Model);
    }

    [Fact]
    public void Parse_NullInput_ThrowsArgumentException()
    {
        Assert.Throws<ArgumentException>(() => ModelId.Parse(null!));
    }

    [Fact]
    public void Parse_EmptyInput_ThrowsArgumentException()
    {
        Assert.Throws<ArgumentException>(() => ModelId.Parse(""));
    }

    [Fact]
    public void Parse_WhitespaceOnly_ThrowsArgumentException()
    {
        Assert.Throws<ArgumentException>(() => ModelId.Parse("   "));
    }

    [Fact]
    public void Parse_NoSlash_ThrowsArgumentException()
    {
        // A bare model name without a provider prefix is not valid.
        var ex = Assert.Throws<ArgumentException>(() => ModelId.Parse("gpt-5-nano"));
        Assert.Contains("provider/model", ex.Message);
    }

    [Fact]
    public void Parse_LeadingSlash_ThrowsArgumentException()
    {
        // "/model" has an empty provider — invalid.
        Assert.Throws<ArgumentException>(() => ModelId.Parse("/gpt-5-nano"));
    }

    [Fact]
    public void Parse_TrailingSlash_ThrowsArgumentException()
    {
        // "openai/" has an empty model — invalid.
        Assert.Throws<ArgumentException>(() => ModelId.Parse("openai/"));
    }

    [Fact]
    public void Parse_GoogleModel_SplitsCorrectly()
    {
        var id = ModelId.Parse("google/gemini-3-flash-preview");

        Assert.Equal("google", id.Provider);
        Assert.Equal("gemini-3-flash-preview", id.Model);
    }
}
