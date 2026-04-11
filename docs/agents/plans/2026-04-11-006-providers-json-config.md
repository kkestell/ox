# Simplify providers with ~/.ur/providers.json

## Goal

Replace the five hardcoded provider classes and their scattered model metadata with a single `~/.ur/providers.json` configuration file that declares which providers exist, what models they offer, and what their context windows are. Collapse the three OpenAI-compatible providers (OpenAI, OpenRouter, ZaiCoding) into one generic implementation. Delete ModelCatalog entirely.

## Desired outcome

- A `~/.ur/providers.json` file is the single source of truth for provider+model metadata.
- If the file is missing, Ox exits immediately with a clear error message telling the user to create it.
- The file is read-only from the application's perspective (never written by Ox/Ur).
- It is loaded fresh when a new session starts.
- `context_in` is mandatory for every model entry — no fallback, no "unknown context window" codepaths. This is required for session compaction.
- Adding a new OpenAI-compatible provider (e.g. Groq, Together) requires only editing the JSON — no code changes.
- The `IProvider` interface is smaller (no more `GetContextWindowAsync`, `ListModelIdsAsync`).
- Three provider files are deleted, ModelCatalog is deleted, and ~450 lines of scattered model metadata code is removed.
- All nullable context window handling (`int?`, null checks, UI hiding) is deleted and replaced with non-nullable `int` throughout.

## How we got here

The codebase has five provider implementations, but three of them (OpenAI, OpenRouter, ZaiCoding) use the exact same SDK (`OpenAI.Chat.ChatClient`) with different endpoint URIs. Each provider independently maintains model lists and context windows via hardcoded dictionaries, remote API calls, or a cached catalog. This duplication makes adding models or providers a code change when it should be a config change. The `ModelCatalog` (OpenRouter API client + disk cache) is ~180 lines of code whose output (`AvailableModels`, `AllModels`, `GetModel`, `RefreshModelsAsync`) is entirely dead — nothing in production code calls those methods.

## Recommended approach

- **providers.json** declares WHAT is available (providers, models, context windows, URLs).
- **Provider classes** handle HOW to talk to each provider type (SDK construction, API keys).
- Three provider "types" remain: `openai-compatible`, `google`, `ollama`.
- Providers are instantiated dynamically from the config — no more hardcoded DI registration per provider.

### providers.json format

`context_in` is mandatory on every model. No `context_out` field — we'll add it when we need it.

```json
{
    "openai": {
        "type": "openai-compatible",
        "models": [
            { "name": "GPT-4o", "id": "gpt-4o", "context_in": 128000 },
            { "name": "GPT-4.1", "id": "gpt-4.1", "context_in": 1047576 }
        ]
    },
    "openrouter": {
        "type": "openai-compatible",
        "url": "https://openrouter.ai/api/v1",
        "models": [
            { "name": "Claude 4 Sonnet", "id": "anthropic/claude-sonnet-4", "context_in": 200000 }
        ]
    },
    "zai-coding": {
        "type": "openai-compatible",
        "url": "https://api.z.ai/api/coding/paas/v4",
        "models": [
            { "name": "GLM 4.7", "id": "glm-4.7", "context_in": 200000 }
        ]
    },
    "ollama": {
        "type": "ollama",
        "url": "http://kyles-mac-mini.local:11434",
        "models": [
            { "name": "Llama 3", "id": "llama3", "context_in": 8192 }
        ]
    },
    "google": {
        "type": "google",
        "models": [
            { "name": "Gemini 3.1 Pro", "id": "gemini-3.1-pro-preview", "context_in": 1048576 }
        ]
    }
}
```

## Related code

### Files to delete

- `src/Ur/Providers/OpenAiProvider.cs` — Replaced by generic `OpenAiCompatibleProvider`
- `src/Ur/Providers/OpenRouterProvider.cs` — Replaced by generic `OpenAiCompatibleProvider`
- `src/Ur/Providers/ZaiCodingProvider.cs` — Replaced by generic `OpenAiCompatibleProvider`
- `src/Ur/Providers/ModelCatalog.cs` — Model metadata now comes from providers.json, not OpenRouter API
- `src/Ur/Providers/ModelInfo.cs` — Replaced by simpler config-driven model record

### Files to create

- `src/Ur/Providers/OpenAiCompatibleProvider.cs` — Single provider for all OpenAI-protocol APIs
- `src/Ur/Providers/ProviderConfig.cs` — Loads, deserializes, and queries providers.json

### Files to modify

- `src/Ur/Providers/IProvider.cs` — Remove `GetContextWindowAsync` and `ListModelIdsAsync`; model metadata is now a config concern, not a provider concern
- `src/Ur/Providers/GoogleProvider.cs` — Delete `KnownContextWindows`, `GetContextWindowAsync`, `ListModelIdsAsync`; constructor takes name from config
- `src/Ur/Providers/OllamaProvider.cs` — Delete context window cache, model list cache, `SettingsWriter` dependency, `ResolveUri`; constructor takes URI from config
- `src/Ur/Providers/ProviderRegistry.cs` — No structural changes, but now populated from config loop instead of hardcoded DI
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Load providers.json, create providers from config entries in a loop, remove ModelCatalog registration, remove `ollama.uri` schema registration
- `src/Ur/Hosting/UrHost.cs` — `ResolveContextWindowAsync` delegates to `ProviderConfig` instead of provider; becomes synchronous
- `src/Ur/Configuration/UrConfiguration.cs` — Remove `ModelCatalog` dependency, `AvailableModels`, `AllModels`, `GetModel`, `RefreshModelsAsync` (all dead code); `ListAllModelIdsAsync` reads from `ProviderConfig` (becomes sync)
- `src/Ox/Program.cs` — `ShowAvailableModelsAsync` calls into the new config-based model listing
- `src/Ox/OxApp.cs` — `ResolveContextWindowAsync` call site adjusts (may become sync)
- `src/Ur/Providers/Fake/FakeProvider.cs` — Remove `GetContextWindowAsync` and `ListModelIdsAsync` (interface shrinks); FakeProvider stays as-is for test injection, not in providers.json
- `tests/Ur.Tests/ContextWindowTests.cs` — Rewrite: test config-based context window resolution instead of per-provider methods
- `tests/Ur.Tests/ProviderTests.cs` — Rewrite: remove context window and model listing tests; update provider construction (OpenAiCompatibleProvider replaces 3 classes)
- `tests/Ur.Tests/ProviderRegistryTests.cs` — Update: use `OpenAiCompatibleProvider` instead of `OpenRouterProvider`
- `tests/Ur.Tests/TestSupport/TestEnvironment.cs` — Delete `TestCatalog` (ModelCatalog gone); add helper to write test `providers.json` files

## Current state

### Dead code being removed

These are already unused — removing them is pure cleanup:

| Member | Location | Active callers |
|---|---|---|
| `UrConfiguration.AvailableModels` | `UrConfiguration.cs:88` | 0 |
| `UrConfiguration.AllModels` | `UrConfiguration.cs:99` | 0 |
| `UrConfiguration.GetModel()` | `UrConfiguration.cs:128` | 0 |
| `UrConfiguration.RefreshModelsAsync()` | `UrConfiguration.cs:130` | 0 |
| `ModelCatalog.RefreshAsync()` | `ModelCatalog.cs:77` | 0 (only via dead wrapper) |

### Existing patterns to follow

- **API key convention**: service `"ur"`, account = provider name (lowercase). `OpenAiCompatibleProvider` will use the config key (e.g. `"openai"`, `"openrouter"`) as the keyring account — same pattern as today.
- **Singleton providers**: all providers are singletons in DI. This stays the same.
- **FakeProvider injection**: via `UrStartupOptions.FakeProvider`, conditional registration. Unchanged.
- **AoT JSON serialization**: use `[JsonSerializable]` source-generated context for providers.json deserialization, matching the `ModelCatalogJsonContext` pattern being deleted.

## Structural considerations

**Hierarchy**: `ProviderConfig` sits at the same level as `ProviderRegistry` — both are infrastructure singletons consumed by `UrHost` and `UrConfiguration`. No layer violations.

**Abstraction**: Cleanly separates "what models exist" (config) from "how to talk to them" (provider). Today these are mixed together inside each provider class.

**Modularization**: The three near-identical OpenAI-compatible providers collapse into one. The result is 3 provider types matching 3 actual SDK differences — no false distinction between providers that share a protocol.

**Encapsulation**: Providers no longer need to know about model metadata. Their interface shrinks to identity + client construction + readiness. The config layer handles everything else.

## Refactoring

Refactoring happens in phases to keep each step testable:

1. **Extract model metadata from providers** — Move context windows and model lists out of providers before changing the source. This validates the new interface shape with existing data.
2. **Introduce ProviderConfig** — Add the config loader and wire it into DI. Existing providers still work alongside it.
3. **Collapse OpenAI-compatible providers** — Replace three files with one. Update DI to use config-driven instantiation.
4. **Delete ModelCatalog and dead code** — Final cleanup.

## Implementation plan

### Phase 1: Create ProviderConfig (the new config layer)

- [x] Create `src/Ur/Providers/ProviderConfig.cs` with:
  - JSON model classes: `ProviderConfigEntry` (type, url, models), `ProviderModelEntry` (name, id, context_in). No `context_out` — we'll add it when needed.
  - Source-generated `[JsonSerializable]` context for AoT compatibility
  - `ProviderConfig.Load(string path)` — reads and deserializes `~/.ur/providers.json`. If the file is missing, throws a clear exception (Ox catches this at startup and exits with an error message). If `context_in` is missing or zero on any model entry, throws with a message identifying the offending provider+model.
  - `GetContextWindow(string providerName, string modelId)` → `int` — looks up context_in from the config. Throws if provider or model not found (this is a hard invariant — every model in the system has a context window).
  - `ListModelIds(string providerName)` → `IReadOnlyList<string>?` — returns model IDs for a provider
  - `ListAllModelIds()` → `IReadOnlyList<string>` — all models, prefixed with provider name, sorted
  - `ProviderNames` → `IReadOnlyCollection<string>` — all configured provider names
  - `GetEntry(string providerName)` → `ProviderConfigEntry?` — for DI to read type/url
- [x] Wire exit-on-missing-file: In `Program.Main` (or `ServiceCollectionExtensions`), catch the missing-file exception and write a clear error to stderr, e.g. `"Error: ~/.ur/providers.json not found. Create it to configure your model providers. See docs/providers.md for the format."`, then exit with non-zero code.
- [x] Write unit tests for `ProviderConfig`: loading valid JSON, missing file throws, malformed JSON throws, missing `context_in` throws, lookup by provider+model returns non-nullable int, unknown provider throws

### Phase 2: Slim down IProvider interface

- [x] Remove `GetContextWindowAsync` and `ListModelIdsAsync` from `IProvider`
- [x] Remove these methods from all implementations: `FakeProvider`, `GoogleProvider`, `OllamaProvider` (and the three about-to-be-deleted providers)
- [x] Verify build compiles

### Phase 3: Create OpenAiCompatibleProvider

- [x] Create `src/Ur/Providers/OpenAiCompatibleProvider.cs`:
  - Constructor takes `string name`, `Uri? endpoint`, `IKeyring keyring`
  - `Name` → the config key (e.g. "openai", "openrouter", "zai-coding")
  - `RequiresApiKey` → `true`
  - `CreateChatClient(model)` → constructs `OpenAI.Chat.ChatClient` with optional custom endpoint, same SDK call as today
  - `GetBlockingIssue()` → checks keyring for `("ur", name)`, same pattern as today
- [x] Write unit tests: blocking issue without key, no blocking issue with key, client creation with and without custom endpoint

### Phase 4: Simplify GoogleProvider and OllamaProvider

- [x] **GoogleProvider**: Remove `KnownContextWindows` dictionary. Constructor stays the same (just takes `IKeyring`). The class shrinks to ~30 lines: `Name`, `RequiresApiKey`, `CreateChatClient`, `GetBlockingIssue`.
- [x] **OllamaProvider**: Remove `_contextWindowCache`, `_modelListCache`, `_modelListCached`, `ResolveUri()`, `SettingsWriter` dependency. Constructor takes `string name`, `Uri endpoint`. The class shrinks to ~15 lines: `Name`, `RequiresApiKey`, `CreateChatClient`, `GetBlockingIssue`.

### Phase 5: Wire config-driven provider creation in DI

- [x] In `ServiceCollectionExtensions.AddUr()`:
  - Register `ProviderConfig` singleton (load from `~/.ur/providers.json`)
  - Replace the five hardcoded provider registrations with a loop over `ProviderConfig.ProviderNames`:
    ```
    for each (name, entry) in providerConfig:
      switch entry.Type:
        "openai-compatible" → new OpenAiCompatibleProvider(name, entry.Url, keyring)
        "google"            → new GoogleProvider(keyring)
        "ollama"            → new OllamaProvider(name, entry.Url ?? default)
        unknown             → log warning, skip
    ```
  - Remove `ModelCatalog` registration
  - Remove `ollama.uri` from `RegisterCoreSchemas`
- [x] FakeProvider registration stays unchanged (conditional via `UrStartupOptions`)

### Phase 6: Update UrHost and UrConfiguration

- [x] **UrHost.ResolveContextWindowAsync** → rename to `ResolveContextWindow` (sync). Return type changes from `Task<int?>` to `int`. Delegates to `ProviderConfig.GetContextWindow()`. No more null — the config guarantees every model has a context window.
- [x] **UrConfiguration**: Remove `ModelCatalog` field and constructor parameter. Delete `AvailableModels`, `AllModels`, `GetModel`, `RefreshModelsAsync`. Change `ListAllModelIdsAsync` to `ListAllModelIds()` — sync, delegates to `ProviderConfig.ListAllModelIds()`.
- [x] **Program.ShowAvailableModelsAsync** → `ShowAvailableModels` (sync). Calls `ProviderConfig.ListAllModelIds()`.
- [x] **OxApp context window resolution** (lines 461-490): Simplify the entire block:
  - `_contextWindowCache` type changes from `Dictionary<string, int?>` to `Dictionary<string, int>`
  - Remove the `try/catch` around resolution (config lookup can't fail for a valid model)
  - Remove the `contextWindow is > 0` guard — it's always > 0
  - `_contextPercent` type changes from `int?` to `int` (or stays nullable only for the "no turn completed yet" state — before first turn there's no token count to display)

### Phase 6b: Delete all nullable context window fallback paths

Every `int?` context window path in the codebase becomes non-nullable. Specific sites:

- [x] **`IProvider.GetContextWindowAsync`** — already deleted in Phase 2, but verify no remnants
- [x] **`UrHost.ResolveContextWindowAsync`** (`UrHost.cs:121`) — return type `Task<int?>` → `int`, remove null-return for unknown provider (throw instead, or rely on ProviderConfig)
- [x] **`OxApp._contextWindowCache`** (`OxApp.cs:48`) — `Dictionary<string, int?>` → `Dictionary<string, int>`
- [x] **`OxApp` cache miss block** (`OxApp.cs:464-480`) — remove try/catch, remove `contextWindow = null` fallback
- [x] **`OxApp._contextPercent` calculation** (`OxApp.cs:483-485`) — remove `contextWindow is > 0` guard, always compute percentage
- [x] **`InputStatusFormatter.Compose`** (`InputStatusFormatter.cs:19-28`) — `contextPercent` parameter stays `int?` (null = no turn yet), but the "have a turn but unknown window" path is gone. The two-branch logic (`contextPercent is not null` vs just modelId) stays since contextPercent is null before the first turn completes.
- [x] **`InputAreaView`** (`InputAreaView.cs:115-124`) — no change needed (already handles null statusRight for pre-first-turn state)
- [x] **All provider `GetContextWindowAsync` implementations** — already deleted in Phase 2
- [x] **All test assertions checking `Assert.Null(result)` for context windows** — delete or convert to valid-value assertions

### Phase 7: Delete dead files and code

- [x] Delete `src/Ur/Providers/OpenAiProvider.cs`
- [x] Delete `src/Ur/Providers/OpenRouterProvider.cs`
- [x] Delete `src/Ur/Providers/ZaiCodingProvider.cs`
- [x] Delete `src/Ur/Providers/ModelCatalog.cs`
- [x] Delete `src/Ur/Providers/ModelInfo.cs`
- [x] Remove `ModelCatalogJsonContext` (in same file or partial — verify)
- [x] Remove `TestCatalog` from `tests/Ur.Tests/TestSupport/TestEnvironment.cs`

### Phase 8: Update tests

- [x] **ContextWindowTests**: Rewrite to test `ProviderConfig.GetContextWindow()` with various config shapes. Add test for `UrHost.ResolveContextWindowAsync` dispatching through config.
- [x] **ProviderTests**: Rewrite OpenAI/OpenRouter/Google/ZaiCoding/Ollama tests to use `OpenAiCompatibleProvider`, `GoogleProvider`, `OllamaProvider`. Test blocking issues, client creation, name matching. Remove context window and model listing tests (those are now ProviderConfig tests).
- [x] **ProviderRegistryTests**: Replace `OpenRouterProvider` references with `OpenAiCompatibleProvider`.
- [x] **TestEnvironment**: Delete `TestCatalog`. Add `TestProviderConfig` helper that writes a temporary providers.json and returns a `ProviderConfig`.
- [x] **TestHostBuilder**: Remove `ModelCatalog` from the DI path. Add a way to inject a test `ProviderConfig` (or write a temp providers.json in the `TempWorkspace.UserDataDirectory`).

### Phase 9: Verify end-to-end

- [x] Run `dotnet build` — verify no compilation errors
- [x] Run `dotnet test` — verify all tests pass
- [x] Manual smoke test: start Ox with a real `~/.ur/providers.json`, select a model, verify context window displays correctly

## Validation

- **Tests**: All existing test scenarios must pass with equivalent coverage. New tests for `ProviderConfig` loading, lookup, error handling (missing file, missing context_in, malformed JSON). Context window tests assert non-nullable `int` values — no more `Assert.Null` on context windows.
- **Build**: `dotnet build` clean with no warnings from the changed files. Verify no remaining `int?` context window types.
- **Manual**:
  - Launch Ox without `~/.ur/providers.json` → verify it exits with a clear error message.
  - Launch Ox with a valid `providers.json` → type `?` at model prompt → verify all configured models appear.
  - Start a conversation → verify context window percentage always displays in status bar (never hidden).
  - Verify a providers.json with a model missing `context_in` fails to load with an error identifying the bad entry.

## Impact assessment

- **Code paths affected**: Provider construction, model discovery, context window resolution, DI registration, configuration phase (model selection prompt).
- **Data impact**: New file `~/.ur/providers.json` required. Existing `~/.ur/cache/models.json` (ModelCatalog disk cache) becomes orphaned and can be ignored.
- **Dependency impact**: `ModelCatalog`'s `HttpClient` usage (fetching from OpenRouter API) is removed. The `OllamaSharp` dependency stays (still needed for `OllamaApiClient`). No new NuGet packages needed.
- **Setting removed**: `ollama.uri` moves from `~/.ur/settings.json` to `~/.ur/providers.json` as the `url` field on the ollama entry. Users who configured this setting need to move the value.

## Decisions

- **Missing providers.json**: Ox exits with a clear error message. No silent fallback, no auto-generation.
- **context_out**: Omitted from the schema entirely. We'll add it when we need it.
- **context_in**: Mandatory on every model entry. Missing or zero is a hard parse error. All nullable context window handling (`int?`) throughout the codebase is deleted — context windows are always known.
