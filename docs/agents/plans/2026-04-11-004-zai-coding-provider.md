# Add Z.AI Coding Provider (`zai-coding`)

## Goal

Add a new LLM provider called `zai-coding` that connects to Z.AI's GLM Coding Plan API. The API is OpenAI-protocol-compatible, so the provider reuses the existing OpenAI SDK with a custom base URL. Users select models as `zai-coding/glm-5.1`, `zai-coding/glm-5-turbo`, `zai-coding/glm-4.7`, or `zai-coding/glm-4.5-air`.

## Desired outcome

- A user can set `ur:model` to `zai-coding/glm-4.7` (or any of the four models), provide their Z.AI API key, and chat normally.
- Context window percentage displays correctly in the TUI status bar for all four models.
- The provider follows the same patterns as the existing OpenAI provider — static context window table, keyring-backed API key, no new NuGet dependencies.

## Related code

- `src/Ur/Providers/IProvider.cs` — Interface the new provider must implement.
- `src/Ur/Providers/OpenAiProvider.cs` — Closest template: static context window table, keyring API key, OpenAI SDK client construction. The Z.AI provider is structurally identical but with a custom endpoint URI.
- `src/Ur/Providers/ProviderRegistry.cs` — Registry the new provider is added to at startup.
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — DI registration site (lines 140–167 for providers, lines 205–211 for settings schemas).
- `src/Ur/Ur.csproj` — Already has `Microsoft.Extensions.AI.OpenAI`; no new packages needed.
- `tests/Ur.Tests/ContextWindowTests.cs` — Tests for context window resolution per provider.
- `tests/Ur.Tests/ProviderRegistryTests.cs` — Tests for registry behavior.
- `tests/Ur.Tests/TestSupport/TestKeyring.cs` — In-memory keyring for tests.

## Current state

Four production providers exist: OpenRouter, OpenAI, Google, Ollama. The OpenAI provider is the simplest — it uses the OpenAI .NET SDK (`OpenAI.Chat.ChatClient`) with a static context window table and keyring-backed API key. Z.AI's API is OpenAI-protocol-compatible, so the same SDK works with a custom endpoint.

The `OpenAI.Chat.ChatClient` constructor accepts an `OpenAIClientOptions` parameter with an `Endpoint` property for overriding the base URL. The OpenAI provider currently uses the two-argument constructor (model + credential), so the Z.AI provider will use the three-argument form (model + credential + options) to pass `https://api.z.ai/api/coding/paas/v4` as the endpoint.

## Structural considerations

**Hierarchy**: The new provider sits at the same level as OpenAI, Google, etc. — a leaf implementation of `IProvider`. No new abstractions needed. Dependencies flow correctly: provider → keyring (for API key), registered into ProviderRegistry by DI.

**Abstraction**: The feature is at the right level. The Z.AI provider is a concrete provider implementation; the rest of the system interacts through `IProvider`. There's no temptation to create an "OpenAI-compatible base class" — the shared code is tiny (a few lines of SDK construction), and premature abstraction would add complexity for two call sites.

**Modularization**: One new file in `src/Ur/Providers/`. The provider is self-contained. Registration is a single line in DI. This does not bloat any existing module.

**Encapsulation**: The custom endpoint URI, keyring account name, and model context windows are all internal to the provider. Nothing leaks.

## Research

### Z.AI model context windows (from docs.z.ai)

| Model | Context Window | Max Output |
|-------|---------------|------------|
| `glm-5.1` | 200,000 tokens | 128,000 tokens |
| `glm-5-turbo` | 200,000 tokens | 128,000 tokens |
| `glm-4.7` | 200,000 tokens | 128,000 tokens |
| `glm-4.5-air` | 128,000 tokens | 96,000 tokens |

Source: Individual model pages at `docs.z.ai/guides/llm/glm-*`.

### API details

- **Coding API endpoint**: `https://api.z.ai/api/coding/paas/v4` (distinct from the general API at `/api/paas/v4`).
- **Protocol**: OpenAI-compatible. Standard chat completions endpoint with API key auth.
- **No model enumeration API**: Context windows must be hardcoded (same situation as OpenAI).

## Implementation plan

- [x] **Create `src/Ur/Providers/ZaiCodingProvider.cs`** — Implement `IProvider` following the OpenAI provider pattern:
  - `Name` returns `"zai-coding"`.
  - `RequiresApiKey` returns `true`.
  - Keyring account: `"zai-coding"` (service: `"ur"`, account: `"zai-coding"`).
  - `CreateChatClient(string model)`: Retrieve API key from keyring. Construct `OpenAI.Chat.ChatClient` with `OpenAIClientOptions { Endpoint = new Uri("https://api.z.ai/api/coding/paas/v4") }`. Call `.AsIChatClient()`.
  - `GetBlockingIssue()`: Return error message if no API key set, null otherwise.
  - `GetContextWindowAsync(string model, ...)`: Static `Dictionary<string, int>` lookup (case-insensitive), return null for unknown models. Table:
    - `"glm-5.1"` → 200,000
    - `"glm-5-turbo"` → 200,000
    - `"glm-4.7"` → 200,000
    - `"glm-4.5-air"` → 128,000
  - Include XML doc comments explaining the provider, the coding-specific endpoint, and why the context window table is static.

- [x] **Register the provider in DI** — In `src/Ur/Hosting/ServiceCollectionExtensions.cs`, add a new `services.AddSingleton<IProvider>(...)` line for `ZaiCodingProvider` alongside the existing four providers (after Ollama, before the fake-provider block). The provider only needs `IKeyring`.

- [x] **Add unit tests in `tests/Ur.Tests/ContextWindowTests.cs`** — Three tests following the existing OpenAI pattern:
  - `ZaiCoding_KnownModel_ReturnsContextWindow` — verify `glm-4.7` returns 200,000.
  - `ZaiCoding_UnknownModel_ReturnsNull` — verify an unknown model returns null.
  - `ZaiCoding_LookupIsCaseInsensitive` — verify `GLM-4.7` (uppercase) still resolves.

- [x] **Build and run tests** — `dotnet build` the solution, then `dotnet test` to verify all existing and new tests pass.

## Validation

- **Tests**: Three new unit tests for context window resolution (known model, unknown model, case insensitivity). Existing provider registry and context window tests must continue to pass.
- **Lint/format/typecheck**: `dotnet build` with no warnings (the project has `EnforceCodeStyleInBuild` and `AnalysisMode=Recommended`).
- **Manual verification**: Set `ur:model` to `zai-coding/glm-4.7`, provide a Z.AI API key, and verify the TUI displays the context percentage and accepts chat input. (Requires a valid Z.AI API key — skip if unavailable, but the structural correctness is covered by unit tests.)

## Open questions

- **Are there additional GLM models that should be included?** The docs mention variants like `glm-4.7-flash`, `glm-4.7-flashx`, `glm-4.5`, `glm-4.5-x`, `glm-4.5-airx`, `glm-4.5-flash`, `glm-5`. The user specified four models (`glm-5.1`, `glm-5-turbo`, `glm-4.7`, `glm-4.5-air`). If more are wanted, they can be added to the static table later — the structure supports it trivially.
