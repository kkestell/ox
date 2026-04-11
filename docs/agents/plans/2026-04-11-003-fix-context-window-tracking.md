# Fix context window tracking across providers

## Goal

Replace the hard-coded 200k context window denominator with the actual context window size for the active model, so the context full % displayed in the TUI is accurate for all providers.

## Desired outcome

The status line shows a correct context fill percentage regardless of provider. A Gemini 3 Flash session at 582k input tokens shows ~55%, not 291%.

## How we got here

The context % calculation in `OxApp.cs:449-451` hard-codes 200,000 as the denominator. This was always a placeholder — the TODO comment says "A real implementation would read max tokens from the model info." The `ModelCatalog` only fetches metadata from OpenRouter, so non-OpenRouter providers (Google, OpenAI, Ollama) have no model info at all. With Gemini 3 Flash's 1M-token context window, the percentage overflows to 291%.

Token usage data from GeminiDotnet is accurate — `usageMetadata.PromptTokenCount` maps correctly to `InputTokenCount` via `GeminiToMEAIMapper.CreateMappedUsageDetails`. The problem is entirely the denominator.

## Approaches considered

### Option 1 — Provider-level context window resolution

- Summary: Add `GetContextWindowAsync(string model, CancellationToken ct)` to `IProvider`. Each provider resolves context window using its own authoritative source. Thread through `UrHost` to OxApp.
- Pros: Each provider encapsulates its own metadata strategy. Google and Ollama can query live APIs (always accurate). Follows the existing provider abstraction pattern.
- Cons: Adds an async method to the provider interface. Google/Ollama need HTTP calls (mitigated by caching).
- Failure modes: API call fails (graceful fallback to null — display "?" or omit percentage).

### Option 2 — Static fallback table

- Summary: Maintain a single dictionary of model name → context window size for all providers. No API calls.
- Pros: Simple, no network calls, deterministic.
- Cons: Stale the moment a new model ships. Requires manual updates. Violates the provider abstraction — one file needs to know about every model from every provider.
- Failure modes: Model not in table → no percentage.

### Option 3 — Extend ModelCatalog to aggregate multiple sources

- Summary: Make ModelCatalog fetch from OpenRouter, Google, OpenAI, and Ollama APIs. Unified model registry.
- Pros: Single point of model metadata.
- Cons: Overengineered — ModelCatalog is specifically an OpenRouter concept (model browsing, pricing, etc.). Jamming other providers into it conflates "model catalog for selection" with "model metadata for runtime."
- Failure modes: Complex caching, multi-source merge logic.

## Recommended approach

**Option 1 — Provider-level context window resolution.** Each provider already encapsulates client construction and API key resolution. Context window metadata is the same kind of provider-specific concern. The interface addition is small, the implementations are straightforward, and it scales to future providers without any central registry changes.

## Related code

- `src/Ur/Providers/IProvider.cs` — Provider interface; add `GetContextWindowAsync`
- `src/Ur/Providers/GoogleProvider.cs` — Google implementation; call Gemini `models.get` API via GeminiDotnet
- `src/Ur/Providers/OpenRouterProvider.cs` — OpenRouter implementation; delegate to `ModelCatalog`
- `src/Ur/Providers/OpenAiProvider.cs` — OpenAI implementation; static lookup table
- `src/Ur/Providers/OllamaProvider.cs` — Ollama implementation; call `/api/show` endpoint
- `src/Ur/Providers/Fake/FakeProvider.cs` — Fake implementation; return fixed value
- `src/Ur/Hosting/UrHost.cs` — Add `ResolveContextWindowAsync` that parses model ID and dispatches to provider
- `src/Ox/OxApp.cs:447-452` — Replace hard-coded 200k with resolved context window
- `src/Ur/Providers/ModelCatalog.cs` — Existing OpenRouter catalog (OpenRouterProvider delegates here)
- `.ignored/GeminiDotnet/src/GeminiDotnet/V1Beta/ModelsClient.cs:24-31` — `GetModelAsync` returns `Model.InputTokenLimit`
- `.ignored/GeminiDotnet/src/GeminiDotnet/V1Beta/Models/Model.cs:37-39` — `InputTokenLimit` property

## Current state

- `OxApp.cs:449-451` divides input tokens by hard-coded 200,000
- `ModelInfo` record has a `ContextLength` field but it's only populated for OpenRouter models
- `UrHost.CreateChatClient` already does provider/model parsing and dispatch — `ResolveContextWindowAsync` follows the same pattern
- OxApp receives `UrHost` in its constructor but doesn't store it as a field
- GeminiDotnet's `GeminiChatClient` uses v1beta API internally; `ModelsClient.GetModelAsync(model)` returns `Model` with `InputTokenLimit`
- GeminiDotnet exposes `IGeminiClient` via `IChatClient.GetService(typeof(IGeminiClient))` but GoogleProvider doesn't need the chat client for model metadata — it can create a separate lightweight `GeminiClient`

## Structural considerations

**Hierarchy**: `IProvider` is the right abstraction level. Context window metadata is provider-specific knowledge — Google knows about Gemini models, Ollama knows about local models. Pushing this into `ModelCatalog` would break the separation between "OpenRouter browsing catalog" and "runtime model metadata."

**Encapsulation**: Each provider's resolution strategy stays internal. GoogleProvider uses GeminiDotnet, OllamaProvider uses HTTP, OpenRouterProvider delegates to the catalog it already depends on. No provider leaks its metadata source.

**Modularization**: `UrHost.ResolveContextWindowAsync` is the single public entry point. OxApp doesn't need to know which provider is active or how metadata is resolved.

## Implementation plan

### IProvider interface and implementations

- [x] Add `Task<int?> GetContextWindowAsync(string model, CancellationToken ct = default)` to `IProvider`
- [x] **GoogleProvider**: Create a `GeminiClient` (same API key, same options pattern as `CreateChatClient`), call `client.V1Beta.Models.GetModelAsync(model)`, return `Model.InputTokenLimit`. Cache results in a `Dictionary<string, int?>` to avoid repeat API calls.
- [x] **OpenRouterProvider**: Accept `ModelCatalog` as a constructor dependency (injected via DI). Return `_modelCatalog.GetModel(model)?.ContextLength`.
- [x] **OpenAiProvider**: Static `Dictionary<string, int>` of known model context sizes (gpt-4o → 128,000, gpt-4.1 → 1,047,576, etc.). Return lookup result or null for unknown models.
- [x] **OllamaProvider**: Uses OllamaSharp's `ShowModelAsync` to call `/api/show`, reads `Info.ExtraInfo["general.context_length"]`. Cache per model. Return null on failure.
- [x] **FakeProvider**: Return a fixed value (200,000).

### UrHost public API

- [x] Add `async Task<int?> ResolveContextWindowAsync(string modelId, CancellationToken ct = default)` to `UrHost`. Parse `ModelId`, look up provider, call `provider.GetContextWindowAsync(parsedModel, ct)`. Return null if provider unknown.

### OxApp wiring

- [x] Store `UrHost` as a `readonly` field `_host` in `OxApp` (it's already received in the constructor but not retained).
- [x] Add a `Dictionary<string, int?> _contextWindowCache` field to avoid re-resolving every turn.
- [x] In the `TurnCompleted` handler: look up context window from cache (keyed on `_session.ActiveModelId`). On cache miss, fire-and-forget `_host.ResolveContextWindowAsync` and store the result. Compute `_contextPercent` using the resolved value. If no context window is known, set `_contextPercent = null` (the formatter already handles null by omitting the percentage).

### DI registration updates

- [x] In `ServiceCollectionExtensions.AddUr`: pass `ModelCatalog` to `OpenRouterProvider`'s constructor.

## Validation

- Tests:
  - [ ] Unit test: `GoogleProvider.GetContextWindowAsync` returns cached value on second call (mock the GeminiClient). — Skipped: requires mocking GeminiClient internals; covered by integration test.
  - [x] Unit test: `OpenRouterProvider.GetContextWindowAsync` returns null for unknown models (catalog empty).
  - [x] Unit test: `OllamaProvider.GetContextWindowAsync` returns null when endpoint unreachable.
  - [x] Unit test: `FakeProvider.GetContextWindowAsync` returns 200,000.
  - [x] Unit test: `UrHost.ResolveContextWindowAsync` dispatches correctly.
  - [ ] Unit test: `InputStatusFormatter.Compose` with various percentage/model combinations (already exists, but verify null % case). — Pre-existing test; not modified.
- Build: `dotnet build` passes — 0 warnings, 0 errors.
- Manual verification: Run with `google/gemini-3-flash-preview`, confirm status line shows a sane percentage (~55% at 582k tokens, not 291%).

## Open questions

- ~~For the OllamaProvider, is there a simpler way to get context window than calling `/api/show`?~~ **Resolved**: OllamaSharp's `ShowModelAsync` wraps `/api/show` natively. Context length is in `response.Info.ExtraInfo["general.context_length"]`.
