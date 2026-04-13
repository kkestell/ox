# Adding a New LLM Provider

This guide walks through adding support for a new LLM provider (e.g., Anthropic
direct, Azure OpenAI, Mistral, etc.) to Ox.

## Architecture Overview

The provider system spans three layers:

- **Agent layer** (`src/Ox/Agent/`) — owns `IProvider`, `ProviderRegistry`, `ModelId`, and
  `OxHost.CreateChatClient()`. The Agent layer doesn't know which providers exist or what
  models they offer — it dispatches by provider name prefix.
- **Providers** (`src/Ox/Agent/Providers/`) — each provider lives in its
  own subdirectory. Uses `IProvider` and `IKeyring` from the Agent layer. Contains the
  SDK package dependency and `IChatClient` construction logic.
- **App layer** (`src/Ox/App/`) — owns `providers.json`, `ProviderConfig`,
  `ModelCatalog`, and the key-based dispatch in `ProviderRegistration`
  that constructs concrete providers. The App layer wires all providers
  and decides which to register.

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
AI abstraction layer used throughout the Agent layer. The provider's job is solely to
construct and configure the right `IChatClient` — the rest of the system
(agent loop, tool invocation, streaming) is provider-agnostic.

## Two Ways to Add a Provider

### Option A: Custom endpoint using an existing protocol

If the new provider uses a supported protocol (OpenAI-compatible, Google AI,
or Ollama), you can add it to `providers.json` as a new key. Providers whose
key doesn't match a built-in name fall through to `OpenAiCompatibleProvider`,
which works with any OpenAI-protocol API:

```json
{
  "providers": {
    "anthropic": {
      "url": "https://api.anthropic.com/v1",
      "models": [
        { "name": "Claude Sonnet 4", "id": "claude-sonnet-4-20250514", "context_in": 200000 }
      ]
    }
  }
}
```

Built-in provider keys with dedicated implementations:
- `"openai"` → `OpenAiProvider` (OpenAI with defaults)
- `"google"` → `GoogleProvider` (Google AI / Gemini via GeminiDotnet)
- `"ollama"` → `OllamaProvider` (local Ollama daemon)
- `"openrouter"` → `OpenRouterProvider` (OpenRouter with reasoning field handler)
- `"zai-coding"` → `ZaiCodingProvider` (Z.AI Coding Plan API)

Any other key → `OpenAiCompatibleProvider` (requires `"url"` in the entry).

### Option B: Custom `IProvider` implementation

For providers that need a custom SDK or special behavior (like OpenRouter's
reasoning field renaming), create a dedicated provider project.

#### 1. Create the Provider Project

Create a new directory `src/Ox/Agent/Providers/Anthropic/`:

**`No separate csproj needed — files go directly in the Ox project.`:**
```xml
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net10.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup>
    <!-- No project reference needed — same assembly -->
    <PackageReference Include="Anthropic.SDK" Version="..." />
  </ItemGroup>
</Project>
```

**`AnthropicProvider.cs`:**
```csharp
using Microsoft.Extensions.AI;
using Ox.Agent.Configuration.Keyring;
using Ox.Agent.Providers;

namespace Ox.Agent.Providers.Anthropic;

/// <summary>
/// Anthropic direct API provider. Uses the Anthropic SDK to construct
/// IChatClient instances for Claude models.
/// </summary>
public sealed class AnthropicProvider : IProvider
{
    private const string SecretService = "ox";
    private const string KeyringAccount = "anthropic";

    private readonly IKeyring _keyring;

    public AnthropicProvider(IKeyring keyring) => _keyring = keyring;

    public string Name => "anthropic";
    public string DisplayName => "Anthropic";
    public bool RequiresApiKey => true;

    public IChatClient CreateChatClient(string model)
    {
        var apiKey = _keyring.GetSecret(SecretService, KeyringAccount)
            ?? throw new InvalidOperationException(
                "No Anthropic API key configured.");

        // Construct your IChatClient using the provider's SDK.
        throw new NotImplementedException("Construct your IChatClient here");
    }

    public string? GetBlockingIssue()
    {
        var key = _keyring.GetSecret(SecretService, KeyringAccount);
        return string.IsNullOrWhiteSpace(key)
            ? "No API key for 'anthropic'."
            : null;
    }
}
```

#### 2. Wire Up in Ox

1. Add the provider file to `src/Ox/Agent/Providers/YourProvider/`.
2. Add a case to the key-based dispatch in `src/Ox/App/Configuration/ProviderRegistration.cs`:

```csharp
case "anthropic":
    services.AddSingleton<IProvider>(sp =>
        new AnthropicProvider(sp.GetRequiredService<IKeyring>()));
    break;
```

3. Add the provider entry to `providers.json` with its model list.

#### 3. Add Test References

Write
unit tests in `tests/Ox.Tests/Agent/Providers/`.

## IProvider Interface Reference

| Member | Purpose |
| --- | --- |
| `string Name` | Provider prefix used in model IDs (e.g., `"anthropic"`). Must match the key in providers.json. |
| `string DisplayName` | Human-readable name (e.g., `"Anthropic"`). Shown in the TUI wizard. |
| `bool RequiresApiKey` | Whether the provider needs an API key. Affects readiness checks and wizard. |
| `IChatClient CreateChatClient(string model)` | Creates a chat client for the given model portion of the ID. |
| `string? GetBlockingIssue()` | Returns `null` if ready, or a human-readable issue string. |

## Context Window Resolution

Context windows are declared in `providers.json` under each model's
`context_in` field — there are no provider-level context window methods.
`ModelCatalog.ResolveContextWindow(modelId)` looks up the value from
`ProviderConfig` (loaded from providers.json). The only fallback is
`FakeProvider`, which declares context windows on its test scenarios.

The TUI displays context fill percentage using the resolved window size.
Models not in `providers.json` show no percentage.

## Model Discovery

Model lists are declared in `providers.json` under each provider's `models`
array. `ModelCatalog.ListAllModelIds()` aggregates these into a sorted
list of `"provider/model"` strings for autocomplete. There is no runtime
model discovery — all models must be declared statically.

## Common Patterns

### API Key via OS Keyring

Used by: `OpenAiCompatibleProvider`, `GoogleProvider`

```csharp
private const string SecretService = "ox";
private const string KeyringAccount = "your-provider";
var apiKey = _keyring.GetSecret(SecretService, KeyringAccount);
```

Users set the key via the connect wizard or `set-api-key` command. The
convention is service = `"ox"`, account = provider name.

### No API Key Required

Used by: `OllamaProvider`, `FakeProvider`

```csharp
public bool RequiresApiKey => false;
public string? GetBlockingIssue() => null;
```

## Testing

### Unit Tests

Add tests to `tests/Ox.Tests/Agent/Providers/`. No separate
project reference needed. Providers are testable in isolation by mocking `IKeyring`:

```csharp
[Fact]
public void GetBlockingIssue_ReturnsIssue_WhenNoApiKey()
{
    var keyring = new TestKeyring();
    var provider = new AnthropicProvider(keyring);

    Assert.NotNull(provider.GetBlockingIssue());
}
```

### Integration Tests

Integration tests in `tests/Ox.IntegrationTests/` exercise the full DI
pipeline with live API keys (gated by environment variables).

### Using the Fake Provider

For end-to-end testing without a real LLM, use `FakeProvider`. Register it
directly in DI: `services.AddSingleton<IProvider>(new FakeProvider())`.

- Model ID format: `"fake/scenario-name"`
- Always ready (no API key needed)
- Fixed 200,000-token context window

See `src/Ox/Agent/Providers/Fake/` for the fake provider and scenario system.

## Checklist

When adding a new provider, verify:

- [ ] Create provider class in `src/Ox/Agent/Providers/YourProvider/`
- [ ] Implement `IProvider` with `Name`, `DisplayName`, `RequiresApiKey`, `CreateChatClient()`, `GetBlockingIssue()`
- [ ] Files are part of Ox.csproj automatically
- [ ] No separate project reference needed
- [ ] Add dispatch case in `ProviderRegistration.AddProvidersFromConfig()`
- [ ] Add provider entry to `providers.json` with models and context windows
- [ ] Add unit tests to `tests/Ox.Tests/Agent/Providers/`
- [ ] Verify tests pass with `dotnet test tests/Ox.Tests/`
- [ ] Verify the provider appears in `ProviderRegistry.ProviderNames` at startup
- [ ] Test with a model ID in `"provider/model"` format