# Ollama Model Discovery

## Goal

Add model listing/discovery to the provider system so that providers can report
which models they offer. Implement it for Ollama first (via `/api/tags`), then
wire existing providers into the same interface. This unblocks future UI
features like model browsers and the `/model` command.

## Desired outcome

- `IProvider` gains a `ListModelIdsAsync` method that returns available model IDs.
- `OllamaProvider` queries the local Ollama instance and returns installed models.
- `OpenRouterProvider` delegates to its existing `ModelCatalog`.
- Static providers (`OpenAiProvider`, `ZaiCodingProvider`) return their known models.
- `FakeProvider` returns its built-in scenario names.
- `UrConfiguration` exposes an aggregated model list across all providers.
- The pre-TUI configuration prompt uses this to validate model input (or show
  available models on empty input).
- `docs/adding-llm-providers.md` is updated to document the new interface member.

## How we got here

The Ollama provider already exists with URL configuration and context window
resolution. The missing piece is model discovery — there's no way to ask "what
models does this provider have?" The OpenRouter provider has a `ModelCatalog`
for this, but it's hardcoded to the OpenRouter API. Rather than building
another catalog, the cleanest approach is to add listing as a first-class
provider capability.

## Approaches considered

### Option A: Add `ListModelIdsAsync` to `IProvider`

- Summary: Extend the provider interface with a method that returns available
  model ID strings. Each provider implements it using its own source (API call,
  static table, catalog lookup).
- Pros: Clean, extensible, provider-agnostic. New providers automatically
  participate. Simple return type (string list) avoids forcing Ollama to fake
  OpenRouter-specific metadata.
- Cons: Changes the interface — all 6 existing providers need updating. But
  the change is small (return null or a static list for most).
- Failure modes: Ollama unreachable at list time → return null/empty, same as
  today's context window handling.

### Option B: Ollama-specific model catalog

- Summary: Create `OllamaModelCatalog` mirroring `ModelCatalog` but hitting
  Ollama's `/api/tags`.
- Pros: Isolated change, no interface modifications.
- Cons: Duplicates catalog infrastructure. UrConfiguration would need to know
  about each catalog type. Doesn't generalize — every new provider that wants
  listing needs its own catalog class.
- Failure modes: Same as A, plus catalog management complexity.

### Option C: Abstract `ModelCatalog` into a generic interface

- Summary: Create `IModelCatalog`, have `OpenRouterModelCatalog` and
  `OllamaModelCatalog` implementations.
- Pros: Reusable catalog abstraction with caching, refresh, etc.
- Cons: Over-engineering for what each provider actually needs. The catalogs
  have fundamentally different shapes (OpenRouter has pricing/modality/params;
  Ollama has name/size/family). The abstraction would be so thin it adds
  complexity without value.
- Failure modes: Premature abstraction that doesn't fit future providers.

## Recommended approach

**Option A** — add `ListModelIdsAsync` to `IProvider`.

- Why: Model listing is a fundamental provider capability. It belongs on the
  interface, not bolted on as a separate subsystem. The return type is just
  `IReadOnlyList<string>?` — the simplest possible contract. Each provider
  already knows its models; this just exposes that knowledge.
- Key tradeoffs: All providers must implement the new method, but most
  implementations are 1-3 lines.

## Related code

- `src/Ur/Providers/IProvider.cs` — The interface to extend
- `src/Ur/Providers/OllamaProvider.cs` — Primary target; will call `ListLocalModelsAsync`
- `src/Ur/Providers/OpenRouterProvider.cs` — Delegates to ModelCatalog
- `src/Ur/Providers/OpenAiProvider.cs` — Static list from `KnownContextWindows` keys
- `src/Ur/Providers/ZaiCodingProvider.cs` — Static list from its known models
- `src/Ur/Providers/GoogleProvider.cs` — Returns null (no listing API readily available)
- `src/Ur/Providers/Fake/FakeProvider.cs` — Returns built-in scenario names
- `src/Ur/Providers/ModelCatalog.cs` — OpenRouter's catalog, used by OpenRouterProvider
- `src/Ur/Configuration/UrConfiguration.cs` — Aggregation point for all-provider model listing
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — DI registration (no changes needed for this)
- `src/Ox/Program.cs` — Pre-TUI configuration prompt (consumer of model listing)
- `tests/Ur.Tests/ProviderTests.cs` — Provider unit tests
- `tests/Ur.Tests/FakeProviderTests.cs` — Fake provider tests
- `docs/adding-llm-providers.md` — Documentation to update

## Current state

- `OllamaProvider` exists with `ollama.uri` setting, context window caching,
  and graceful fallback when Ollama is unreachable.
- `ModelCatalog` is OpenRouter-only: fetches from `https://openrouter.ai/api/v1/models`,
  caches to `~/.ur/cache/models.json`, stores `ModelInfo` records with pricing and modality.
- `UrConfiguration.AvailableModels` and `AllModels` only read from the OpenRouter catalog.
  These are currently unused by the TUI.
- `ModelInfo` record has OpenRouter-specific fields (pricing, modality, supported params)
  that don't apply to Ollama models.
- OllamaSharp 5.4.25 provides `OllamaApiClient.ListLocalModelsAsync()` which
  hits `/api/tags` and returns `IEnumerable<Model>` with Name, Size, Details, etc.
- The pre-TUI configuration prompt (`Program.RunConfigurationPhaseAsync`) asks
  "Enter model (provider/model): " with no discovery or suggestions.

## Structural considerations

**Hierarchy**: The new method fits naturally on `IProvider` — it's the same
abstraction level as `CreateChatClient` and `GetContextWindowAsync`. No layer
violations.

**Abstraction**: Returning `IReadOnlyList<string>?` keeps the abstraction clean.
We don't force a common metadata type (like `ModelInfo`) across providers with
fundamentally different capabilities. Model IDs are the universal currency.

**Modularization**: Each provider's listing logic stays inside its own class.
`UrConfiguration` aggregates by iterating providers — it doesn't need to know
implementation details.

**Encapsulation**: `OllamaProvider` already encapsulates URI resolution and
OllamaSharp usage. Adding `ListLocalModelsAsync` follows the same pattern as
the existing `GetContextWindowAsync` (create a client, make a call, cache if
appropriate).

## Implementation plan

### Interface change

- [x] Add `Task<IReadOnlyList<string>?> ListModelIdsAsync(CancellationToken ct = default)`
      to `IProvider` in `src/Ur/Providers/IProvider.cs`. Document that it returns
      model ID strings (the portion after the provider prefix), or null if listing
      is not supported/available.

### Provider implementations

- [x] **OllamaProvider**: Implement `ListModelIdsAsync` using
      `OllamaApiClient.ListLocalModelsAsync()`. Create a client using
      `ResolveUri()` (same pattern as `GetContextWindowAsync`). Return
      `model.Name` for each result. Catch exceptions and return null if Ollama
      is unreachable. Cache the result for the session (same session-cache
      pattern as `_contextWindowCache`) to avoid repeated local calls, with an
      explicit refresh path if needed later.

- [x] **OpenRouterProvider**: Implement `ListModelIdsAsync` by returning
      `_modelCatalog.Models.Select(m => m.Id).ToList()`. Returns null/empty if
      catalog is empty.

- [x] **OpenAiProvider**: Implement `ListModelIdsAsync` returning the keys
      from the existing `KnownContextWindows` dictionary as a static list.

- [x] **ZaiCodingProvider**: Same pattern — return keys from its known models
      dictionary.

- [x] **GoogleProvider**: Return null. Google's model listing API exists but
      isn't worth integrating for this round.

- [x] **FakeProvider**: Return `BuiltInScenarios.Names` (or equivalent).
      Add a static `Names` property to `BuiltInScenarios` if one doesn't exist.

### Aggregation in UrConfiguration

- [x] Add a method to `UrConfiguration` that aggregates models across all
      providers. Something like:
      ```csharp
      public async Task<IReadOnlyList<string>> ListAllModelIdsAsync(CancellationToken ct = default)
      ```
      Iterates `ProviderRegistry`, calls `ListModelIdsAsync` on each, prefixes
      results with `"{provider.Name}/"`, and returns a sorted combined list.
      Providers that return null are skipped.

### Pre-TUI integration

- [x] In `Program.RunConfigurationPhaseAsync`, when the user is prompted for a
      model, show available models if they enter `?` or press Enter with no
      input. Call `UrConfiguration.ListAllModelIdsAsync()` and display the
      results grouped by provider.

### Tests

- [x] Add unit tests for `OllamaProvider.ListModelIdsAsync` — test the null
      return case (Ollama unreachable) by verifying the method doesn't throw.
      Testing the success path requires a live Ollama instance, so that belongs
      in integration tests.

- [x] Add unit tests for the static providers (`OpenAiProvider`,
      `ZaiCodingProvider`) — verify they return their known model lists.

- [x] Add unit tests for `OpenRouterProvider.ListModelIdsAsync` using
      `TestCatalog.CreateWithModels` — verify it delegates to the catalog.

- [x] Add a unit test for `FakeProvider.ListModelIdsAsync` — verify it returns
      built-in scenario names.

- [x] Verify `UrConfiguration.ListAllModelIdsAsync` aggregates and prefixes
      correctly (use FakeProvider + a test provider or mock).

### Documentation

- [x] Update `docs/adding-llm-providers.md`:
  - Add `ListModelIdsAsync` to the IProvider Interface Reference table.
  - Add a "Model Discovery" section to Common Patterns describing the three
    strategies: API call (Ollama), catalog delegation (OpenRouter), static list
    (OpenAI/ZaiCoding).
  - Update the checklist to include implementing `ListModelIdsAsync`.
  - Fix the doc's claim that `ModelCatalog` comment says "the only provider with
    a browsable remote model catalog" — Ollama now also has discovery.

### Validation

- [x] Run `dotnet build` — verify clean compilation.
- [x] Run `dotnet test` — verify all new and existing tests pass.
- [ ] Manual test: start the app with Ollama running, enter `?` at the model
      prompt, verify Ollama models appear in the list.
- [ ] Manual test: start the app without Ollama running, verify graceful
      degradation (no crash, Ollama section absent or empty).

## Impact assessment

- **Code paths affected**: `IProvider` and all 6 implementations,
  `UrConfiguration`, `Program.RunConfigurationPhaseAsync`.
- **Data or schema impact**: None — no new config keys, no new cache files.
- **Dependency or API impact**: No new NuGet packages. OllamaSharp's
  `ListLocalModelsAsync` is already available in the installed version.

## Gaps and follow-up

- **`/model` command**: The TUI's `/model` command is a stub. This plan builds
  the infrastructure it needs but doesn't implement the command itself.
- **Model metadata enrichment**: Ollama's `/api/tags` returns basic info
  (name, size, family). Richer metadata (context window, capabilities) requires
  per-model `/api/show` calls. This plan returns only IDs; metadata enrichment
  is a separate concern.
- **Model catalog unification**: `UrConfiguration.AvailableModels` and
  `AllModels` still only read from the OpenRouter `ModelCatalog`. Unifying
  these with the new `ListAllModelIdsAsync` is future work — it requires
  deciding how to handle the `ModelInfo` type mismatch.

## Open questions

None — all questions resolved.
