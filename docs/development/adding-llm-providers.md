# Adding a New LLM Provider

This guide walks through adding support for a new LLM provider (e.g., Anthropic
direct, Azure OpenAI, Mistral, etc.) to Ox.

## Architecture Overview

The provider system spans two layers:

- **Ur (library)** — owns `IProvider`, `ProviderRegistry`, `ModelId`, and
  `UrHost.CreateChatClient()`. Ur doesn't know which providers exist or what
  models they offer — it dispatches by provider name prefix.
- **Ox (application)** — owns `providers.json`, `ProviderConfig`,
  `OxConfiguration`, and the type-switch that constructs concrete providers.
  Ox decides which providers to register and provides model catalog queries
  (listing, context windows) to the TUI.

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
AI abstraction layer used throughout Ur. The provider's job is solely to
construct and configure the right `IChatClient` — the rest of the system
(agent loop, tool invocation, streaming) is provider-agnostic.

## Two Ways to Add a Provider

### Option A: Built-in provider type (via providers.json)

If the new provider uses a supported protocol (OpenAI-compatible, Google AI,
or Ollama), you can add it to `providers.json` without writing any code:

```json
{
  "providers": {
    "anthropic": {
      "name": "Anthropic",
      "type": "openai-compatible",
      "url": "https://api.anthropic.com/v1",
      "models": [
        { "name": "Claude Sonnet 4", "id": "claude-sonnet-4-20250514", "context_in": 200000 }
      ]
    }
  }
}
```

Ox's `ProviderRegistration.AddProvidersFromConfig()` reads these entries and
constructs the appropriate concrete provider class. The type-switch supports:
- `"openai-compatible"` → `OpenAiCompatibleProvider` (works with any OpenAI-compatible API)
- `"google"` → `GoogleProvider` (Google AI / Gemini)
- `"ollama"` → `OllamaProvider` (local Ollama daemon)

### Option B: Custom `IProvider` implementation

For providers that need custom SDK integration, implement `IProvider` in Ur
and register it in Ox's DI.

#### 1. Create the Provider Class

Add a new file in `src/Ur/Providers/` (e.g., `AnthropicProvider.cs`).
Implement `IProvider`:

```csharp
using Microsoft.Extensions.AI;
using Ur.Configuration.Keyring;

namespace Ur.Providers;

internal sealed class AnthropicProvider : IProvider
{
    private const string SecretService = "ur";
    private const string KeyringAccount = "anthropic";

    private readonly IKeyring _keyring;

    public AnthropicProvider(IKeyring keyring) => _keyring = keyring;

    public string Name => "anthropic";
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

Concrete provider classes stay `internal` to Ur — Ox constructs them via
`InternalsVisibleTo("Ox")`. External consumers who want a truly custom
provider implement the public `IProvider` interface from scratch.

#### 2. Register in Ox

Add a case to the type-switch in `src/Ox/Configuration/ProviderRegistration.cs`:

```csharp
case "anthropic":
    services.AddSingleton<IProvider>(sp =>
        new AnthropicProvider(sp.GetRequiredService<IKeyring>()));
    break;
```

Then add the provider to `providers.json` with `"type": "anthropic"` and
its model list.

#### 3. Add the NuGet Package

If your provider depends on an SDK package, add it to `src/Ur/Ur.csproj`:

```bash
dotnet add src/Ur/Ur.csproj package Anthropic.SDK
```

## IProvider Interface Reference

| Member | Purpose |
| --- | --- |
| `string Name` | Provider prefix used in model IDs (e.g., `"anthropic"`). Must be unique. |
| `bool RequiresApiKey` | Whether the provider needs an API key. Affects readiness checks and wizard. |
| `IChatClient CreateChatClient(string model)` | Creates a chat client for the given model portion of the ID. |
| `string? GetBlockingIssue()` | Returns `null` if ready, or a human-readable issue string. |

## Context Window Resolution

Context windows are declared in `providers.json` under each model's
`context_in` field — there are no provider-level context window methods.
`OxConfiguration.ResolveContextWindow(modelId)` looks up the value from
`ProviderConfig` (loaded from providers.json). The only fallback is
`FakeProvider`, which declares context windows on its test scenarios.

The TUI displays context fill percentage using the resolved window size.
Models not in `providers.json` show no percentage.

## Model Discovery

Model lists are declared in `providers.json` under each provider's `models`
array. `OxConfiguration.ListAllModelIds()` aggregates these into a sorted
list of `"provider/model"` strings for autocomplete. There is no runtime
model discovery — all models must be declared statically.

## Common Patterns

### API Key via OS Keyring

Used by: `OpenAiCompatibleProvider`, `GoogleProvider`

```csharp
private const string SecretService = "ur";
private const string KeyringAccount = "your-provider";
var apiKey = _keyring.GetSecret(SecretService, KeyringAccount);
```

Users set the key via the connect wizard or `set-api-key` command. The
convention is service = `"ur"`, account = provider name.

### No API Key Required

Used by: `OllamaProvider`, `FakeProvider`

```csharp
public bool RequiresApiKey => false;
public string? GetBlockingIssue() => null;
```

## Testing

### Unit Tests

Add tests to `tests/Ur.Tests/`. Providers are testable in isolation by
mocking `IKeyring`:

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

Integration tests in `tests/Ur.IntegrationTests/` exercise the full DI
pipeline with live API keys (gated by environment variables).

### Using the Fake Provider

For end-to-end testing without a real LLM, use `FakeProvider`. Register it
directly in DI: `services.AddSingleton<IProvider>(new FakeProvider())`.

- Model ID format: `"fake/scenario-name"`
- Always ready (no API key needed)
- Fixed 200,000-token context window

See `src/Ur/Providers/Fake/` for the fake provider and scenario system.

## Checklist

When adding a new provider, verify:

- [ ] Add provider entry to `providers.json` with type, models, and context windows
- [ ] If custom SDK integration needed: implement `IProvider` in `src/Ur/Providers/`
- [ ] If custom type: add case to `ProviderRegistration.AddProvidersFromConfig()`
- [ ] Choose a unique, lowercase `Name` (used as the provider prefix in model IDs)
- [ ] Add NuGet package(s) to `src/Ur/Ur.csproj` if needed
- [ ] Add unit tests to `tests/Ur.Tests/`
- [ ] Verify the provider appears in `ProviderRegistry.ProviderNames` at startup
- [ ] Test with a model ID in `"provider/model"` format