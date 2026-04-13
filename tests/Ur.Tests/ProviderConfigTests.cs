using System.Text.Json;
using Ox.Configuration;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="ProviderConfig"/> — loading, validation, and lookup.
/// </summary>
public sealed class ProviderConfigTests : IDisposable
{
    private readonly string _tempDir = Path.Combine(
        Path.GetTempPath(), "ur-providerconfig-tests", Guid.NewGuid().ToString("N"));

    public ProviderConfigTests()
    {
        Directory.CreateDirectory(_tempDir);
    }

    public void Dispose()
    {
        if (Directory.Exists(_tempDir))
            Directory.Delete(_tempDir, recursive: true);
    }

    private string WriteTempJson(string json)
    {
        var path = Path.Combine(_tempDir, "providers.json");
        File.WriteAllText(path, json);
        return path;
    }

    // ─── Load: valid config ──────────────────────────────────────────

    [Fact]
    public void Load_ValidConfig_Succeeds()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);

        Assert.Contains("openai", config.ProviderNames);
    }

    [Fact]
    public void Load_MultipleProviders_AllPresent()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                },
                "google": {
                    "models": [
                        { "name": "Gemini", "id": "gemini-3.1-pro", "context_in": 1048576 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);

        Assert.Equal(2, config.ProviderNames.Count);
        Assert.Contains("openai", config.ProviderNames);
        Assert.Contains("google", config.ProviderNames);
    }

    // ─── Load: error cases ───────────────────────────────────────────

    [Fact]
    public void Load_MissingFile_ThrowsFileNotFound()
    {
        var path = Path.Combine(_tempDir, "nonexistent.json");

        var ex = Assert.Throws<FileNotFoundException>(() => ProviderConfig.Load(path));
        Assert.Contains("providers.json", ex.Message);
    }

    [Fact]
    public void Load_MalformedJson_Throws()
    {
        var path = WriteTempJson("{ not valid json }}}");

        Assert.ThrowsAny<JsonException>(() => ProviderConfig.Load(path));
    }

    [Fact]
    public void Load_MissingContextIn_Throws()
    {
        // context_in defaults to 0 when omitted, which we reject.
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o" }
                    ]
                }
            }
        }
        """);

        var ex = Assert.Throws<InvalidOperationException>(() => ProviderConfig.Load(path));
        Assert.Contains("gpt-4o", ex.Message);
        Assert.Contains("context_in", ex.Message);
    }

    [Fact]
    public void Load_ZeroContextIn_Throws()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "bad": {
                    "models": [
                        { "name": "Bad Model", "id": "bad-model", "context_in": 0 }
                    ]
                }
            }
        }
        """);

        var ex = Assert.Throws<InvalidOperationException>(() => ProviderConfig.Load(path));
        Assert.Contains("bad-model", ex.Message);
    }

    [Fact]
    public void Load_NegativeContextIn_Throws()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "bad": {
                    "models": [
                        { "name": "Bad", "id": "bad", "context_in": -1 }
                    ]
                }
            }
        }
        """);

        var ex = Assert.Throws<InvalidOperationException>(() => ProviderConfig.Load(path));
        Assert.Contains("bad", ex.Message);
    }

    [Fact]
    public void Load_EmptyModels_Throws()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "empty": {
                    "models": []
                }
            }
        }
        """);

        var ex = Assert.Throws<InvalidOperationException>(() => ProviderConfig.Load(path));
        Assert.Contains("empty", ex.Message);
        Assert.Contains("no models", ex.Message);
    }

    // ─── GetEntry ────────────────────────────────────────────────────

    [Fact]
    public void GetEntry_ExistingProvider_ReturnsEntry()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "url": "https://api.openai.com/v1",
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);
        var entry = config.GetEntry("openai");

        Assert.NotNull(entry);
        Assert.Equal("https://api.openai.com/v1", entry.Url);
        Assert.Single(entry.Models);
    }

    [Fact]
    public void GetEntry_MissingProvider_ReturnsNull()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);

        Assert.Null(config.GetEntry("nonexistent"));
    }

    // ─── GetContextWindow ────────────────────────────────────────────

    [Fact]
    public void GetContextWindow_KnownModel_ReturnsValue()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);

        Assert.Equal(128000, config.GetContextWindow("openai", "gpt-4o"));
    }

    [Fact]
    public void GetContextWindow_CaseInsensitive()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);

        Assert.Equal(128000, config.GetContextWindow("openai", "GPT-4O"));
    }

    [Fact]
    public void GetContextWindow_UnknownProvider_Throws()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);

        Assert.Throws<InvalidOperationException>(() =>
            config.GetContextWindow("nonexistent", "gpt-4o"));
    }

    [Fact]
    public void GetContextWindow_UnknownModel_Throws()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);

        Assert.Throws<InvalidOperationException>(() =>
            config.GetContextWindow("openai", "nonexistent-model"));
    }

    // ─── ListModelIds ────────────────────────────────────────────────

    [Fact]
    public void ListModelIds_ExistingProvider_ReturnsIds()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 },
                        { "name": "GPT-4.1", "id": "gpt-4.1", "context_in": 1047576 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);
        var ids = config.ListModelIds("openai");

        Assert.NotNull(ids);
        Assert.Equal(2, ids.Count);
        Assert.Contains("gpt-4o", ids);
        Assert.Contains("gpt-4.1", ids);
    }

    [Fact]
    public void ListModelIds_MissingProvider_ReturnsNull()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);

        Assert.Null(config.ListModelIds("nonexistent"));
    }

    // ─── ListAllModelIds ─────────────────────────────────────────────

    [Fact]
    public void ListAllModelIds_AggregatesAndSorts()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                },
                "google": {
                    "models": [
                        { "name": "Gemini", "id": "gemini-3.1-pro", "context_in": 1048576 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);
        var all = config.ListAllModelIds();

        Assert.Equal(2, all.Count);
        Assert.Contains("openai/gpt-4o", all);
        Assert.Contains("google/gemini-3.1-pro", all);

        // Verify sorted order.
        var sorted = all.OrderBy(m => m, StringComparer.OrdinalIgnoreCase).ToList();
        Assert.Equal(sorted, all);
    }

    [Fact]
    public void ListAllModelIds_MultipleModelsPerProvider()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "A", "id": "model-a", "context_in": 100 },
                        { "name": "B", "id": "model-b", "context_in": 200 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);
        var all = config.ListAllModelIds();

        Assert.Equal(2, all.Count);
        Assert.Contains("openai/model-a", all);
        Assert.Contains("openai/model-b", all);
    }

    // ─── URL parsing in entries ──────────────────────────────────────

    [Fact]
    public void GetEntry_WithUrl_ParsesCorrectly()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openrouter": {
                    "url": "https://openrouter.ai/api/v1",
                    "models": [
                        { "name": "Claude", "id": "anthropic/claude-4", "context_in": 200000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);
        var entry = config.GetEntry("openrouter");

        Assert.NotNull(entry);
        Assert.Equal("https://openrouter.ai/api/v1", entry.Url);
    }

    [Fact]
    public void GetEntry_WithoutUrl_UrlIsNull()
    {
        var path = WriteTempJson("""
        {
            "providers": {
                "openai": {
                    "models": [
                        { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 }
                    ]
                }
            }
        }
        """);

        var config = ProviderConfig.Load(path);
        var entry = config.GetEntry("openai");

        Assert.NotNull(entry);
        Assert.Null(entry.Url);
    }
}
