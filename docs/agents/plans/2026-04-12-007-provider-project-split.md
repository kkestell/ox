# Refactor providers into separate projects

## Goal

Move each LLM provider into its own .csproj project and simplify providers.json by removing the `name` and `type` fields. Built-in providers are identified by their JSON key; unknown keys automatically use an OpenAI-compatible fallback.

## Desired outcome

- Six provider projects: `Ur.Providers.Google`, `Ur.Providers.OpenAI`, `Ur.Providers.OpenRouter`, `Ur.Providers.Ollama`, `Ur.Providers.ZaiCoding`, `Ur.Providers.OpenAiCompatible`
- Each provider owns its display name, default endpoint, and NuGet dependencies
- `Ur.csproj` no longer carries provider-specific NuGet packages (GeminiDotnet, OllamaSharp, Microsoft.Extensions.AI.OpenAI)
- `providers.json` uses key-based dispatch — no `name` or `type` fields for built-in providers; custom providers use an optional `name` plus required `url`
- `IProvider` gains a `DisplayName` property so display names move from JSON config into provider code
- The `thinking` per-model flag and DeepSeek-R1 special-casing are removed (thinking will become a runtime setting in a separate feature)

## How we got here

The request was concrete from the start: six named providers, a simplified providers.json schema, and key-based dispatch. We explored the current architecture (single `OpenAiCompatibleProvider` handling OpenAI/OpenRouter/ZaiCoding, `GoogleProvider`, `OllamaProvider`, all internal to Ur), confirmed the type-switch in `ProviderRegistration.cs`, and chose independent projects over a shared-base approach.

## Approaches considered

### Option A — Independent projects (recommended)

- Summary: Each provider is a standalone .csproj with its own NuGet dependencies. OpenAI-protocol providers (OpenAI, OpenRouter, ZaiCoding, OpenAiCompatible) each independently reference `Microsoft.Extensions.AI.OpenAI` and create their own `OpenAI.Chat.ChatClient`. No inter-provider project dependencies.
- Pros: Flat dependency graph; each provider can diverge independently; easy to add/remove providers
- Cons: ~30 lines of OpenAI SDK client creation repeated across 4 projects
- Failure modes: None meaningful — the duplication is trivial and self-contained

### Option B — Shared base project

- Summary: `Ur.Providers.OpenAiCompatible` provides a base class. `Ur.Providers.OpenAI`, `Ur.Providers.OpenRouter`, and `Ur.Providers.ZaiCoding` reference it and configure their defaults.
- Pros: Less duplication in SDK setup
- Cons: Couples OpenAI-protocol providers together; base class must be generic enough for all; harder to diverge later
- Failure modes: If one provider needs protocol-level changes (e.g., OpenAI adds native thinking), the shared base becomes a constraint

## Recommended approach

Option A — Independent projects.

The duplication is ~30 lines of trivially identical code per OpenAI-protocol provider. The flat dependency graph is worth far more than eliminating that. Each provider is self-contained, testable in isolation, and can diverge without coordination.

## Related code

- `src/Ur/Providers/IProvider.cs` — Interface that all providers implement. Needs `DisplayName` added.
- `src/Ur/Providers/OpenAiCompatibleProvider.cs` — Currently handles OpenAI/OpenRouter/ZaiCoding. Will be replaced by individual providers; a simplified version remains for custom providers.
- `src/Ur/Providers/GoogleProvider.cs` — Moves to its own project.
- `src/Ur/Providers/OllamaProvider.cs` — Moves to its own project.
- `src/Ur/Providers/DeepSeekThinkingChatClient.cs` — Thinking wrapper; remove as part of thinking-flag cleanup.
- `src/Ur/Providers/ProviderRegistry.cs` — No changes; it just collects `IProvider` instances from DI.
- `src/Ur/Providers/ModelId.cs` — No changes; "provider/model" parsing is unchanged.
- `src/Ox/Configuration/ProviderRegistration.cs` — Type-switch becomes key-match. The core of the routing change.
- `src/Ox/Configuration/ProviderConfig.cs` — Remove `name`, `type`, `thinking` from deserialization. Add `name` as optional (for custom providers).
- `src/Ox/Configuration/OxConfiguration.cs` — `ListProviders()` reads display names from `IProvider.DisplayName` instead of JSON.
- `src/Ox/Ox.csproj` — Gains references to all six provider projects.
- `src/Ur/Ur.csproj` — Loses GeminiDotnet, OllamaSharp, Microsoft.Extensions.AI.OpenAI.
- `Ox.slnx` — Gains six new project entries.
- `providers.json` — Simplified schema.
- `docs/development/adding-llm-providers.md` — Must be rewritten for new architecture.

## Current state

- All concrete providers live in `src/Ur/Providers/` as `internal` classes with `InternalsVisibleTo("Ox")`
- `Ur.csproj` carries all provider NuGet packages (Gemini, OpenAI, Ollama)
- `ProviderRegistration.cs` in Ox uses a `switch (entry.Type)` to construct providers from JSON `type` field
- `ProviderConfig` requires `type` on every entry and `name` is used for display names
- `OpenAiCompatibleProvider` is parameterized by name/endpoint to serve three different providers

## Structural considerations

**Hierarchy**: Provider projects form a new layer between Ur (core) and Ox (app). Each provider references Ur for `IProvider` and `IKeyring`. Ox references all provider projects for registration. Dependencies flow strictly downward: Ox → Provider projects → Ur. This is cleaner than the current `InternalsVisibleTo` backdoor.

**Abstraction**: Moving providers out of Ur eliminates the need for `InternalsVisibleTo("Ox")`. Providers become public classes in their own assemblies. `IProvider` stays in Ur as the abstraction boundary — the agent loop, sessions, and tools remain provider-agnostic.

**Modularization**: Each provider project is a single-purpose module: one provider class, its NuGet dependencies, nothing else. Ur sheds provider-specific packages and becomes purely the core library (sessions, agent loop, tools, configuration abstractions).

**Encapsulation**: Provider internals (SDK client creation, endpoint defaults) are encapsulated within each project. Ox only sees `IProvider`. The `ProviderRegistration` switch becomes a simple key-to-constructor mapping instead of reaching into provider internals.

## Refactoring

These refactors happen before the provider split to simplify the migration.

### 1. Remove thinking-model infrastructure

Remove the `Thinking` property from `ProviderModelEntry`, the `thinkingModelIds` constructor parameter from `OpenAiCompatibleProvider`, the DeepSeek-R1 special-case check, and `DeepSeekThinkingChatClient`. This shrinks the provider surface before splitting.

### 2. Add `DisplayName` to `IProvider`

Add `string DisplayName { get; }` so providers own their human-readable name. Update the existing providers (`GoogleProvider` → "Google", `OllamaProvider` → "Ollama", `OpenAiCompatibleProvider` gets display name from constructor). Update `OxConfiguration.ListProviders()` to read from `IProvider.DisplayName`. Update `FakeProvider` to return "Fake".

### 3. Remove `name` and `type` from `ProviderConfigEntry`

Drop the `Name` and `Type` properties from the deserialization model. `name` becomes optional (used only for custom providers). `type` is eliminated entirely — the JSON key determines the provider.

## Implementation plan

### Pre-split refactoring (in Ur, before creating new projects)

- [ ] Remove `Thinking` property from `ProviderModelEntry` in `ProviderConfig.cs`
- [ ] Remove `DeepSeekThinkingChatClient.cs` from `src/Ur/Providers/`
- [ ] Remove the `thinkingModelIds` parameter and DeepSeek wrapping logic from `OpenAiCompatibleProvider`
- [ ] Add `string DisplayName { get; }` to `IProvider`
- [ ] Implement `DisplayName` on `GoogleProvider` ("Google"), `OllamaProvider` ("Ollama"), `OpenAiCompatibleProvider` (from constructor), `FakeProvider` ("Fake")
- [ ] Update `OxConfiguration.ListProviders()` to use `IProvider.DisplayName` instead of `ProviderConfigEntry.Name`
- [ ] Remove `Type` property from `ProviderConfigEntry`; remove `type` from JSON
- [ ] Make `Name` optional on `ProviderConfigEntry` (only meaningful for custom providers)
- [ ] Update `ProviderRegistration.cs` to switch on the JSON key instead of `entry.Type`

### Create provider projects

- [ ] Create `src/Ur.Providers.Google/Ur.Providers.Google.csproj` — references Ur, GeminiDotnet.Extensions.AI
- [ ] Create `src/Ur.Providers.Google/GoogleProvider.cs` — move from `src/Ur/Providers/GoogleProvider.cs`, make `public`, hardcode `DisplayName` = "Google"
- [ ] Create `src/Ur.Providers.OpenAI/Ur.Providers.OpenAI.csproj` — references Ur, Microsoft.Extensions.AI.OpenAI
- [ ] Create `src/Ur.Providers.OpenAI/OpenAiProvider.cs` — new class; default endpoint = null (standard OpenAI), `DisplayName` = "OpenAI", keyring account = "openai". Accepts optional `Uri? endpoint` from JSON for override.
- [ ] Create `src/Ur.Providers.OpenRouter/Ur.Providers.OpenRouter.csproj` — references Ur, Microsoft.Extensions.AI.OpenAI
- [ ] Create `src/Ur.Providers.OpenRouter/OpenRouterProvider.cs` — default endpoint = `https://openrouter.ai/api/v1`, `DisplayName` = "OpenRouter", keyring account = "openrouter". Accepts optional `Uri? endpoint` from JSON for override.
- [ ] Create `src/Ur.Providers.Ollama/Ur.Providers.Ollama.csproj` — references Ur, OllamaSharp
- [ ] Create `src/Ur.Providers.Ollama/OllamaProvider.cs` — move from `src/Ur/Providers/OllamaProvider.cs`, make `public`, hardcode `DisplayName` = "Ollama", default endpoint = `http://localhost:11434`
- [ ] Create `src/Ur.Providers.ZaiCoding/Ur.Providers.ZaiCoding.csproj` — references Ur, Microsoft.Extensions.AI.OpenAI
- [ ] Create `src/Ur.Providers.ZaiCoding/ZaiCodingProvider.cs` — default endpoint = `https://api.z.ai/api/coding/paas/v4`, `DisplayName` = "Z.AI Coding Plan", keyring account = "zai-coding"
- [ ] Create `src/Ur.Providers.OpenAiCompatible/Ur.Providers.OpenAiCompatible.csproj` — references Ur, Microsoft.Extensions.AI.OpenAI
- [ ] Create `src/Ur.Providers.OpenAiCompatible/OpenAiCompatibleProvider.cs` — generic fallback for custom providers. Constructor takes name, displayName, endpoint (required), keyring. `DisplayName` from JSON `name` field or key.

### Wire up and clean up

- [ ] Add all six provider projects to `Ox.slnx`
- [ ] Add `ProjectReference` entries to `src/Ox/Ox.csproj` for all six provider projects
- [ ] Update `ProviderRegistration.cs` — key-based switch: `"openai"` → `OpenAiProvider`, `"google"` → `GoogleProvider`, `"openrouter"` → `OpenRouterProvider`, `"ollama"` → `OllamaProvider`, `"zai-coding"` → `ZaiCodingProvider`, default → `OpenAiCompatibleProvider`. Pass optional `entry.Endpoint` to built-in providers that accept URL overrides.
- [ ] Remove old provider files from `src/Ur/Providers/`: `GoogleProvider.cs`, `OllamaProvider.cs`, `OpenAiCompatibleProvider.cs`
- [ ] Remove provider-specific NuGet packages from `src/Ur/Ur.csproj`: `GeminiDotnet.Extensions.AI`, `Microsoft.Extensions.AI.OpenAI`, `OllamaSharp`
- [ ] Ur.csproj keeps `Microsoft.Extensions.AI` (the abstraction, not the OpenAI binding)
- [ ] Remove `InternalsVisibleTo("Ox")` from Ur if it was only needed for provider construction
- [ ] Update `providers.json` to remove `name` and `type` fields from all entries. Add `name` to the custom-provider example.
- [ ] Update `docs/development/adding-llm-providers.md` for the new architecture

### Test updates

- [ ] Update any Ur.Tests that reference moved provider classes to use the new namespaces/projects
- [ ] Add test project references if Ur.Tests needs to test individual provider projects
- [ ] Verify `dotnet build` succeeds for the full solution
- [ ] Verify boo smoke tests pass

## Impact assessment

- **Code paths affected**: Provider construction (ProviderRegistration), provider display names (OxConfiguration.ListProviders), connect wizard (reads display names), providers.json schema
- **Data or schema impact**: `providers.json` format changes — `name` removed from built-in entries, `type` removed entirely, `thinking` removed from models
- **Dependency or API impact**: `IProvider` gains `DisplayName`. Provider NuGet packages move from Ur to individual projects. Ox gains six new project references. No external API changes.

## Validation

- `dotnet build Ox.slnx` compiles cleanly
- `dotnet test` passes all existing tests
- boo smoke tests pass with the new providers.json format
- Each built-in provider key in providers.json resolves to the correct provider type
- A custom provider entry (unknown key) falls back to OpenAiCompatible
- The connect wizard shows correct display names for all providers
- Model autocomplete works for all providers

## Open questions

- Should built-in OpenAI-protocol providers (OpenAI, OpenRouter, ZaiCoding) accept a `url` override from providers.json, or ignore it entirely? The plan assumes they accept it for flexibility, but hardcoding entirely would be simpler.
