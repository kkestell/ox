# Adding a New LLM Provider

This guide walks through everything needed to add support for a new LLM provider
(e.g., Anthropic direct, Azure OpenAI, Mistral, etc.) to Ox's Ur layer.

## Architecture Overview

The provider system lives in `src/Ur/Providers/` and follows a simple pattern:

1. **`IProvider`** — the interface every provider implements.
2. **`ProviderRegistry`** — a name → `IProvider` map populated by DI at startup.
3. **`ModelId`** — parses `"provider/model"` strings; the first slash-delimited
   segment is the provider name, the rest is the model identifier passed to
   that provider's SDK.
4. **`UrHost.CreateChatClient(modelId)`** — parses the `ModelId`, looks up the
   provider in the registry, and delegates to `provider.CreateChatClient(model)`.

```
User selects model: "anthropic/claude-sonnet-4-20250514"
        │
        ▼
   ModelId.Parse()
   ├── Provider: "anthropic"
   └── Model: "claude-sonnet-4-20250514"
        │
        ▼
   ProviderRegistry.Get("anthropic")
        │
        ▼
   AnthropicProvider.CreateChatClient("claude-sonnet-4-20250514")
        │
        ▼
   IChatClient (Microsoft.Extensions.AI)
```

All providers return `Microsoft.Extensions.AI.IChatClient`, which is the
AI abstraction layer used throughout Ur. This means the provider's job is
solely to construct and configure the right `IChatClient` — the rest of the
system (agent loop, tool invocation, streaming) is provider-agnostic.

## Step-by-Step

### 1. Create the Provider Class

Add a new file in `src/Ur/Providers/` (e.g., `AnthropicProvider.cs`).
Implement `IProvider`:

```csharp
using Microsoft.Extensions.AI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

/// <summary>
/// Provider for Anthropic's API. API key is stored in the OS keyring
/// under account "anthropic".
/// </summary>
internal sealed class AnthropicProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "anthropic";

    private readonly IKeyring _keyring;

    public AnthropicProvider(IKeyring keyring)
    {
        _keyring = keyring;
    }

    // ── IProvider members ───────────────────────────────────────────

    public string Name => "anthropic";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                "No Anthropic API key configured. Run: ur config set-api-key <key> --provider anthropic");

        // Construct your IChatClient here. The approach depends on the SDK:
        //
        // Option A: SDK natively implements IChatClient (e.g., OllamaApiClient)
        //   return new AnthropicClient(new AnthropicClientOptions { ApiKey = apiKey, ModelId = model });
        //
        // Option B: SDK has its own client type; use AsIChatClient() or build an adapter
        //   return new SomeSdkClient(apiKey, model).AsIChatClient();
        throw new NotImplementedException("Construct your IChatClient here");
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? "No API key for 'anthropic'. Run: ur config set-api-key <key> --provider anthropic"
            : null;
    }

    public Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default)
    {
        // See "Context Window Resolution" below for options.
        return Task.FromResult<int?>(null);
    }

    public Task<IReadOnlyList<string>?> ListModelIdsAsync(CancellationToken ct = default)
    {
        // See "Model Discovery" below for options.
        // Return null if listing is not supported.
        return Task.FromResult<IReadOnlyList<string>?>(null);
    }
}
```

### 2. Register in Dependency Injection

Open `src/Ur/Hosting/ServiceCollectionExtensions.cs` and add your provider
in the `AddUr()` method, alongside the existing providers:

```csharp
services.AddSingleton<IProvider>(sp =>
    new AnthropicProvider(sp.GetRequiredService<IKeyring>()));
```

That's it — `ProviderRegistry` is built by iterating `GetServices<IProvider>()`,
so your provider will be picked up automatically.

### 3. Register Settings Schemas (if needed)

If your provider uses settings from the `SettingsWriter` (e.g., a custom
endpoint URI), register a JSON schema for the setting key so the
configuration system can validate it:

In `ServiceCollectionExtensions.RegisterCoreSchemas()`:

```csharp
registry.Register("anthropic.uri", stringSchema);
```

See `OllamaProvider.UriSettingKey` for a working example of a settings-based
provider that reads configuration from the settings file.

### 4. Add the NuGet Package

If your provider depends on an SDK package, add it to `src/Ur/Ur.csproj`:

```bash
dotnet add src/Ur/Ur.csproj package Anthropic.SDK
```

If the SDK doesn't natively implement `IChatClient`, you may need to write an
adapter or use `AsIChatClient()` if the SDK provides a `Microsoft.Extensions.AI`
integration package.

## IProvider Interface Reference

| Member                                                                 | Purpose                                                                                                                                                       |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `string Name`                                                          | Provider prefix used in model IDs (e.g., `"anthropic"`). Must be unique across all registered providers.                                                      |
| `bool RequiresApiKey`                                                  | Whether the provider needs an API key. Affects readiness checks and UI prompts.                                                                               |
| `IChatClient CreateChatClient(string model)`                           | Creates a chat client for the given model. The `model` parameter is everything after the provider prefix in the model ID.                                     |
| `string? GetBlockingIssue()`                                           | Returns `null` if the provider is ready, or a human-readable issue string (e.g., "No API key for 'openai'"). Called by the readiness system before each turn. |
| `Task<int?> GetContextWindowAsync(string model, CancellationToken ct)` | Returns the context window size in tokens for the given model, or `null` if unknown. Used to display context usage percentage in the UI.                      |
| `Task<IReadOnlyList<string>?> ListModelIdsAsync(CancellationToken ct)` | Returns available model IDs (the portion after the provider prefix), or `null` if listing is not supported. Used by `UrConfiguration.ListAllModelIdsAsync()` for model discovery. |

## Common Patterns

### API Key via OS Keyring

Used by: `OpenAiProvider`, `OpenRouterProvider`, `GoogleProvider`

```csharp
private const string SecretService = "ur";
private const string KeyringAccount = "your-provider";

// In CreateChatClient / GetBlockingIssue:
var apiKey = _keyring.GetSecret(SecretService, KeyringAccount);
```

The convention is service = `"ur"`, account = provider name. Users set the
key via `ur config set-api-key <key> --provider <name>`, which calls
`UrConfiguration.SetApiKeyAsync()`, which stores it in the OS keyring.

### Settings-Based Configuration

Used by: `OllamaProvider`

For non-secret configuration (endpoint URIs, feature flags), use
`SettingsWriter` instead of the keyring:

```csharp
// Define the settings key as a public constant so it can be registered
// in RegisterCoreSchemas.
internal const string UriSettingKey = "yourprovider.uri";

// Read the setting:
var element = _settingsWriter.Get(UriSettingKey);
var uriString = element is { ValueKind: JsonValueKind.String } je
    ? je.GetString()
    : null;
```

### No API Key Required

Used by: `OllamaProvider`, `FakeProvider`

```csharp
public bool RequiresApiKey => false;
public string? GetBlockingIssue() => null;
```

## Context Window Resolution

Each provider resolves context windows differently, depending on what the
upstream API exposes:

| Strategy                | Used by              | Description                                                                                    |
| ----------------------- | -------------------- | ---------------------------------------------------------------------------------------------- |
| **Remote API call**     | `GoogleProvider`     | Queries the provider's model metadata endpoint. Caches results to avoid repeated calls.        |
| **Local model catalog** | `OpenRouterProvider` | Reads from the cached `ModelCatalog` populated at startup. No network call at resolution time. |
| **Static table**        | `OpenAiProvider`     | Hardcoded dictionary of known models. Returns `null` for unknown models.                       |
| **Local daemon call**   | `OllamaProvider`     | Queries the local Ollama `/api/show` endpoint. Caches results.                                 |
| **Fixed value**         | `FakeProvider`       | Returns a constant (200,000) for test determinism.                                             |

Returning `null` is always safe — the UI will simply omit the context usage
percentage rather than display incorrect data.

## Model Discovery

Providers can report which models they offer via `ListModelIdsAsync()`. This
powers the `?` prompt in the pre-TUI configuration phase and
`UrConfiguration.ListAllModelIdsAsync()`, which aggregates models across all
providers with provider-prefixed IDs.

| Strategy                | Used by              | Description                                                                                    |
| ----------------------- | -------------------- | ---------------------------------------------------------------------------------------------- |
| **Local daemon call**   | `OllamaProvider`     | Queries the local Ollama `/api/tags` endpoint. Caches results for the session.                 |
| **Local model catalog** | `OpenRouterProvider`  | Reads model IDs from the cached `ModelCatalog`.                                                |
| **Static table**        | `OpenAiProvider`, `ZaiCodingProvider` | Returns the keys from their `KnownContextWindows` dictionary.                  |
| **Built-in list**       | `FakeProvider`       | Returns built-in scenario names.                                                               |
| **Not supported**       | `GoogleProvider`     | Returns `null`. Google's listing API exists but isn't integrated yet.                          |

Returning `null` is always safe — providers that don't support listing are
simply skipped during aggregation.

## Testing

### Unit Tests

Add tests to `tests/Ur.Tests/`. Your provider should be testable in isolation
by mocking `IKeyring` (and `SettingsWriter` if applicable):

```csharp
[Fact]
public void GetBlockingIssue_ReturnsIssue_WhenNoApiKey()
{
    var keyring = new MockKeyring(); // returns null for GetSecret
    var provider = new AnthropicProvider(keyring);

    var issue = provider.GetBlockingIssue();

    Assert.NotNull(issue);
    Assert.Contains("anthropic", issue);
}
```

See `tests/Ur.Tests/FakeProviderTests.cs` and `tests/Ur.Tests/ProviderTests.cs`
for existing patterns.

### Integration Tests

Integration tests live in `tests/Ur.IntegrationTests/`. These exercise the
full DI pipeline and verify that the provider is registered and discoverable
through `ProviderRegistry`.

### Using the Fake Provider

For end-to-end testing without a real LLM, use the built-in `FakeProvider`.
It implements `IProvider` and replays scripted scenarios:

- Model ID format: `"fake/scenario-name"`
- Always ready (no API key needed)
- Fixed 200,000-token context window

See `src/Ur/Providers/Fake/` for the fake provider and scenario system.

## Checklist

When adding a new provider, verify:

- [ ] Implement `IProvider` in a new file in `src/Ur/Providers/`
- [ ] Choose a unique, lowercase `Name` (used as the provider prefix in model IDs)
- [ ] Register the provider in `ServiceCollectionExtensions.AddUr()`
- [ ] Register any settings schemas in `RegisterCoreSchemas()` if the provider
      uses settings-based configuration
- [ ] Add NuGet package(s) to `src/Ur/Ur.csproj` if needed
- [ ] Implement `GetContextWindowAsync()` with an appropriate strategy
- [ ] Implement `ListModelIdsAsync()` — return model IDs if the provider
      supports listing, or `null` if not
- [ ] Add unit tests to `tests/Ur.Tests/`
- [ ] Verify the provider appears in `ProviderRegistry.ProviderNames` at startup
- [ ] Test with a model ID in `"provider/model"` format (e.g., `"anthropic/claude-sonnet-4-20250514"`)