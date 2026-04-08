# Multi-Provider Support (Ollama, OpenAI, Google GenAI)

## Goal

Add support for Ollama, OpenAI, and Google GenAI as first-class providers alongside the existing OpenRouter provider. Models are addressed using `provider/model` nomenclature (e.g. `google/gemini-3-flash-preview`, `openai/gpt-5-nano`, `ollama/qwen3:4b`, `openrouter/anthropic/claude-3.5-sonnet`).

## Desired outcome

- `ur config set-model google/gemini-3-flash-preview` selects a Google model.
- `ur config set-model openai/gpt-5-nano` selects an OpenAI model.
- `ur config set-model ollama/qwen3:4b` selects an Ollama model.
- `ur config set-model openrouter/anthropic/claude-3.5-sonnet` continues to work.
- Each provider resolves its own API key and endpoint.
- Ollama has a configurable `uri` setting (`ollama.uri`, default `http://localhost:11434`).
- OpenAI and Google need no special settings — just an API key in the keyring.
- All providers that require API keys (OpenRouter, OpenAI, Google) store them in the OS keyring under `service="ur", account="<provider-name>"`.
- The model catalog and readiness checks adapt to the selected provider.

## Approaches considered

### Option A — Provider abstraction with registration

- Summary: Define an `IProvider` interface. Each provider implements it. A `ProviderRegistry` maps provider prefixes to implementations. `ChatClientFactory` delegates to the matched provider.
- Pros: Clean abstraction, extensible, testable. Providers are self-contained modules.
- Cons: More types. Slightly heavier upfront.
- Failure modes: Over-abstraction if providers turn out to be trivially different.

### Option B — Switch statement in ChatClientFactory

- Summary: Parse the provider prefix from the model ID. Switch on it in `ChatClientFactory.Create()` to construct the right `IChatClient`.
- Pros: Minimal new types. Fast to implement.
- Cons: Monolithic. Adding a provider means editing the factory. Settings, keys, and client construction all mixed in one place. Harder to test individual providers.
- Failure modes: Grows unwieldy as providers multiply.

## Recommended approach

**Option A — Provider abstraction with registration.** The providers have genuinely different concerns (different API keys, different client construction, Ollama needs a URI setting, OpenRouter needs a model catalog). An interface makes each provider self-contained and testable. The abstraction is justified by the four concrete implementations we'll ship.

Key tradeoffs: A few more types, but each is small and focused. Worth it for separation of concerns.

## Related code

- `src/Ur/Providers/ChatClientFactory.cs` — Current hardcoded OpenRouter factory. Will be replaced by provider dispatch.
- `src/Ur/Providers/ModelCatalog.cs` — OpenRouter-specific model catalog. Stays, but becomes the OpenRouter provider's concern.
- `src/Ur/Providers/ModelInfo.cs` — Model metadata record. Stays as-is.
- `src/Ur/UrHost.cs:110-120` — `CreateChatClient()` currently gets a single API key and calls `ChatClientFactory`. Must dispatch to the right provider.
- `src/Ur/Configuration/UrConfiguration.cs` — Manages API key (hardcoded to OpenRouter keyring account), readiness checks, model catalog access. Needs per-provider key awareness.
- `src/Ur/Configuration/ChatReadiness.cs` / `ChatBlockingIssue.cs` — Readiness checks currently only check for a single "API key". Must check the _relevant_ provider's key.
- `src/Ur/Configuration/Keyring/IKeyring.cs` — Secret storage. Each provider will use a different account name.
- `src/Ur/Configuration/SettingsSchemaRegistry.cs` — Where `ollama.uri` schema will be registered.
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — DI registration. Provider registry needs to be wired here.
- `src/Ur/Hosting/UrStartupOptions.cs` — Test overrides.
- `src/Ur/Ur.csproj` — Dependencies. Already has `Microsoft.Extensions.AI.OpenAI` and `Google_GenerativeAI.Microsoft`. Needs `OllamaSharp`.
- `src/Ur.Cli/Commands/ConfigCommands.cs` — `set-api-key`/`clear-api-key` are OpenRouter-specific. Need to become provider-aware.
- `src/Ur.Cli/Commands/ModelCommands.cs` — `ur models refresh` / `ur models list` are OpenRouter-specific. Need to scope to provider or remove catalog requirement for non-OpenRouter providers.
- `src/Ur.Tui/Program.cs:201-206` — TUI startup prompts for OpenRouter API key. Needs provider awareness.
- `.ignore/OllamaSharp/src/OllamaSharp/OllamaApiClient.cs` — Reference: `OllamaApiClient` directly implements `IChatClient`. Constructor: `new OllamaApiClient(uri, defaultModel)`.
- `.ignore/dotnet-genai/Google.GenAI/GoogleGenAIExtensions.cs` — Reference: `client.AsIChatClient(modelId)`. Client constructor: `new Client(apiKey: key)`.

## Current state

- Only OpenRouter is supported. Everything — `ChatClientFactory`, `ModelCatalog`, `UrConfiguration.GetApiKey()`, readiness checks, CLI commands — assumes a single provider.
- API key is stored in keyring under `service="ur", account="openrouter"`.
- Model IDs already use `provider/model` format (e.g. `openai/gpt-4o`) but this is OpenRouter's namespacing, not ours.
- `Google_GenerativeAI.Microsoft` is already a dependency. `Microsoft.Extensions.AI.OpenAI` is already a dependency (used for OpenRouter via OpenAI SDK).

## Structural considerations

**Hierarchy**: The new provider layer sits between `UrHost`/`UrConfiguration` and the underlying SDK clients. Providers are internal implementation details — the rest of the system continues to use `IChatClient`.

**Abstraction**: Each provider encapsulates its own client construction, API key resolution, and settings. The system only sees `IProvider` → `IChatClient`.

**Modularization**: One file per provider. The registry is a thin lookup. No God module risk — each provider is 20-40 lines.

**Encapsulation**: Provider-specific details (endpoint URIs, SDK types, key resolution) stay inside each provider implementation. `UrHost.CreateChatClient()` only knows about `IProvider`.

## Refactoring

### R1. Extract provider prefix parsing

The model ID `provider/model` needs consistent parsing. For OpenRouter, the model portion is itself slash-delimited (e.g. `openrouter/anthropic/claude-3.5-sonnet` → provider=`openrouter`, model=`anthropic/claude-3.5-sonnet`). Define a `ModelId` record: `ModelId.Parse("openrouter/anthropic/claude-3.5-sonnet")` → `{ Provider: "openrouter", Model: "anthropic/claude-3.5-sonnet" }`.

### R2. Generalize API key storage

Currently `UrConfiguration` hardcodes `SecretAccount = "openrouter"`. All providers that need API keys (OpenRouter, OpenAI, Google) will store them in the keyring under `service="ur", account="<provider-name>"` (e.g. `account="openai"`, `account="google"`). The provider interface will declare its key requirements, and `UrConfiguration` will resolve keys by provider name.

### R3. Generalize readiness checks

`ChatBlockingIssue.MissingApiKey` assumes one global key. It should check whether the _selected provider_ has its key. Ollama needs no API key at all — just a reachable URI.

## Research

### Repo findings

- `OllamaApiClient` directly implements `IChatClient` — no `.AsIChatClient()` needed. Constructor: `new OllamaApiClient(uri, defaultModel)`.
- Google GenAI uses `new Client(apiKey: key).AsIChatClient(modelId)` via extension method in `Microsoft.Extensions.AI` namespace.
- OpenAI uses `new OpenAI.Chat.ChatClient(model, credential, options).AsIChatClient()`. For direct OpenAI (not OpenRouter), omit the custom endpoint.
- Environment variables: `OPENAI_API_KEY`, `GOOGLE_API_KEY` are available in `.env`.

## Implementation plan

### Phase 1 — Provider abstraction and registry

- [x] Add `OllamaSharp` NuGet package to `src/Ur/Ur.csproj`.
- [x] Create `src/Ur/Providers/ModelId.cs` — A `readonly record struct ModelId(string Provider, string Model)` with a static `Parse(string raw)` method. Rule: the first `/`-delimited segment is the provider, the remainder is the model. E.g. `openrouter/anthropic/claude-3.5-sonnet` → `Provider="openrouter"`, `Model="anthropic/claude-3.5-sonnet"`.
- [x] Create `src/Ur/Providers/IProvider.cs` — Interface:

  ```csharp
  internal interface IProvider
  {
      /// The provider prefix (e.g. "openrouter", "ollama", "openai", "google").
      string Name { get; }

      /// Creates an IChatClient for the given model portion of the ID.
      IChatClient CreateChatClient(string model);

      /// Whether this provider requires an API key at all.
      bool RequiresApiKey { get; }

      /// Checks whether the provider is ready (has key, reachable, etc.).
      /// Returns null if ready, or a human-readable issue string.
      string? GetBlockingIssue();
  }
  ```

- [x] Create `src/Ur/Providers/ProviderRegistry.cs` — Holds a `Dictionary<string, IProvider>` keyed by provider name. Methods: `Get(string name)`, `Register(IProvider)`.

### Phase 2 — Implement four providers

- [x] Create `src/Ur/Providers/OpenRouterProvider.cs` — Wraps the existing `ChatClientFactory` logic. Gets its API key from keyring account `"openrouter"`. Owns the `ModelCatalog`.
- [x] Create `src/Ur/Providers/OpenAiProvider.cs` — Gets its API key from keyring account `"openai"`. Creates `new OpenAI.Chat.ChatClient(model, new ApiKeyCredential(key)).AsIChatClient()` (no custom endpoint).
- [x] Create `src/Ur/Providers/GoogleProvider.cs` — Gets its API key from keyring account `"google"`. Creates `new Google.GenAI.Client(apiKey: key).AsIChatClient(model)`.
- [x] Create `src/Ur/Providers/OllamaProvider.cs` — Reads `ollama.uri` from settings (default `http://localhost:11434`). Creates `new OllamaApiClient(uri, model)` — no API key needed.

### Phase 3 — Wire into DI and host

- [x] In `ServiceCollectionExtensions`, register `ProviderRegistry` as a singleton. Register all four providers. Register the `ollama.uri` setting schema (type: string) in `RegisterCoreSchemas`.
- [x] Update `UrHost.CreateChatClient(string modelId)` to parse the model ID with `ModelId.Parse()`, look up the provider from the registry, and call `provider.CreateChatClient(modelId.Model)`. Remove the `ChatClientFactory.Create()` call. Keep the `_chatClientFactoryOverride` escape hatch for tests.
- [x] Delete `ChatClientFactory.cs` (its logic moves into `OpenRouterProvider`).

### Phase 4 — Update configuration and readiness

- [x] Update `UrConfiguration.GetApiKey()` → make it provider-aware. Add `GetApiKey(string providerName)` that reads from keyring with `service="ur", account=providerName`. OpenRouter, OpenAI, and Google all use the keyring. Ollama needs no key.
- [x] Update `ChatBlockingIssue` and readiness check in `UrConfiguration.GetBlockingIssues()`: instead of checking a single hardcoded key, delegate to the selected provider's `GetBlockingIssue()`. If no model is selected, that's still a blocking issue. If a model is selected but the provider isn't ready, that's the blocking issue.
- [x] Update `UrConfiguration.SetApiKeyAsync`/`ClearApiKeyAsync` to accept a provider name parameter. The keyring account becomes the provider name (e.g. `SetApiKeyAsync("openai", key)` stores under `account="openai"`).

### Phase 5 — Update CLI commands

- [x] Update `ConfigCommands.BuildSetApiKey()` to accept a `--provider` option (e.g. `ur config set-api-key <key> --provider openai`). Defaults to `openrouter` for backwards compatibility. The keyring account is the provider name. All three keyed providers (openrouter, openai, google) use this same flow.
- [x] Update `ConfigCommands.BuildClearApiKey()` similarly with `--provider`.
- [x] Update `ModelCommands` — `ur models refresh` and `ur models list` only apply to OpenRouter (it's the only provider with a remote catalog). The commands should note this. For other providers, models are not browsable — you just know the model name.
- [x] Update `StatusCommand` and `ChatCommand` blocking issue messages to be provider-aware.
- [x] Update `Ur.Tui/Program.cs` startup flow: if the selected model's provider requires an API key and none is in the keyring, prompt the user for it (e.g. "No API key for 'openai'. Enter your OpenAI API key (or blank to exit):"). Store the entered key in the keyring under the provider's account.

### Phase 6 — Tests

- [x] Unit test `ModelId.Parse()` — basic cases (`openai/gpt-5-nano`, `openrouter/anthropic/claude-3.5-sonnet`, `ollama/qwen3:4b`), edge cases (no slash, empty).
- [x] Unit test each provider's `GetBlockingIssue()` — with and without keys/settings.
- [x] Unit test `ProviderRegistry` — lookup by name, unknown provider.
- [x] Integration smoke tests using real API keys from `.env`:
  - `google/gemini-3-flash-preview` — send a simple prompt, verify streaming response. **USE EXACTLY `gemini-3-flash-preview` — NOT `gemini-2.0-flash` or any other model name.**
  - `openai/gpt-5-nano` — send a simple prompt, verify streaming response. **USE EXACTLY `gpt-5-nano` — NOT `gpt-4o` or any other model name.**
  - `ollama/qwen3:4b` — send a simple prompt against local Ollama, verify streaming response. **USE EXACTLY `qwen3:4b`.**
  - These tests should be marked with a `[Trait]` or similar so they can be skipped in CI (they require live API keys and a running Ollama).

### Phase 7 — Cleanup

- [x] Remove OpenRouter-specific language from doc comments across the codebase (e.g. `ChatBlockingIssue.MissingApiKey` doc, `UrStartupOptions.ChatClientFactoryOverride` doc, `UrConfiguration` doc).
- [x] Verify `boo` builds cleanly and all existing tests pass.

## Impact assessment

- Code paths affected: `UrHost.CreateChatClient`, `UrConfiguration` (API key, readiness), `ChatClientFactory` (deleted), all CLI config/model commands, TUI startup flow.
- Data or schema impact: New setting `ollama.uri`. Keyring gains new accounts: `openai`, `google` (alongside existing `openrouter`).
- Dependency impact: New NuGet dependency `OllamaSharp`.

## Validation

- Tests: Unit tests for `ModelId`, providers, registry. Integration smoke tests for all four providers.
- Lint/format/typecheck: `dotnet build` must pass.
- Manual verification: Run `boo` — full build + test suite. Then manually test each provider with `ur chat -m <provider/model> "hello"`.
