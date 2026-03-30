# Provider Registry

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Manages provider configuration and model discovery. Currently OpenRouter-only — model metadata is fetched from the OpenRouter API and cached to disk. Provides the data needed by the agent loop to create chat clients, and by the core for compaction decisions (context length) and cost display.

### Non-Goals

- Does not store API keys — that is the system keyring via [Configuration](configuration.md).
- Does not make LLM chat calls — that is the [Agent Loop](agent-loop.md) via Microsoft.Extensions.AI.
- Does not handle model selection UI — that is the TUI's `/model` command. The registry provides the data; the UI renders it.
- Does not support multiple providers yet. OpenRouter covers the broadest model set. Multi-provider support is deferred.

## Context

### Dependencies

| Dependency              | What it provides                  | Interface                        |
| ----------------------- | --------------------------------- | -------------------------------- |
| Microsoft.Extensions.AI | Chat client abstraction           | `IChatClient`                    |
| OpenAI SDK              | OpenAI-compatible client impl     | `ChatClient.AsIChatClient()`     |
| IKeyring                | API key retrieval                 | `GetSecret(service, account)` — library-managed, not host-provided |
| OpenRouter Models API   | Model catalog (context, pricing)  | `GET /api/v1/models`             |

### Dependents

| Dependent         | What it needs                                      | Interface                       |
| ----------------- | -------------------------------------------------- | ------------------------------- |
| Agent Loop        | `IChatClient` for the selected model               | `CreateChatClient(modelId)`     |
| Core (compaction) | Model context length                               | Read-only property lookup       |
| TUI `/model`      | Searchable model list with cost/context             | Model catalog query             |

## Interface

### Get Models

- **Purpose:** Return all available models for display in the TUI model selector.
- **Outputs:** List of models with id, display name, context length, pricing, supported parameters.
- **Behavior:** Returns cached data if available. If no cache exists, fetches from the OpenRouter API. Callers can request a refresh.

### Get Model

- **Purpose:** Get a specific model's metadata by ID.
- **Inputs:** Model ID (e.g. `"anthropic/claude-sonnet-4.6"`). This is the OpenRouter model ID, not a qualified `provider/model` form — since OpenRouter is the only provider, the OpenRouter ID is the canonical ID.
- **Outputs:** Model metadata (context length, pricing, supported parameters) or null if unknown.

### Create Chat Client (internal)

- **Purpose:** Create a configured `IChatClient` for a given model. Internal to the library — consumed by the agent loop, not exposed to frontends. See [ADR-0011](decisions/adr-0011-library-owns-chat-client-and-keyring.md).
- **Inputs:** Model ID. API key resolved from keyring. Endpoint is the hardcoded OpenRouter chat endpoint.
- **Outputs:** `IChatClient` ready to use.
- **Preconditions:** API key must be in keyring.

## Data Structures

### Model (from OpenRouter API)

The OpenRouter `GET /api/v1/models` endpoint returns `{ "data": [...] }`. Each model entry:

```json
{
  "id": "anthropic/claude-sonnet-4.6",
  "name": "Anthropic: Claude Sonnet 4.6",
  "context_length": 1000000,
  "pricing": {
    "prompt": "0.000003",
    "completion": "0.000015"
  },
  "top_provider": {
    "max_completion_tokens": 64000
  },
  "supported_parameters": ["temperature", "tools", "tool_choice", ...]
}
```

We store a subset of this data in our model type:

- `Id` — OpenRouter model ID (e.g. `"anthropic/claude-sonnet-4.6"`).
- `Name` — Human-readable display name.
- `ContextLength` — Max input tokens.
- `MaxOutputTokens` — From `top_provider.max_completion_tokens`.
- `InputCostPerToken` — Parsed from `pricing.prompt` (decimal).
- `OutputCostPerToken` — Parsed from `pricing.completion` (decimal).
- `SupportedParameters` — List of strings. Used to know whether the model supports tools, temperature, etc.

**Invariants:** Model ID is unique. Pricing strings from the API are parsed to decimal. Models without pricing data (free models) have zero cost.

### Model Cache

- **Purpose:** Avoid hitting the OpenRouter API on every startup.
- **Shape:** JSON file at `~/.ur/cache/models.json`. Contains the full model list plus a fetch timestamp.
- **Invariants:** The cache is advisory — if corrupt or missing, re-fetch. The cache file is never the source of truth; the API is. Cache is refreshed when the user runs `/model` (to ensure they see the latest).

### User's Model Selection

The user's selected model ID and any custom settings (e.g. temperature) are stored in `settings.json`:

```json
{
  "ur.model": "anthropic/claude-sonnet-4.6",
  "ur.temperature": 0.7
}
```

This is the user's choice, not registry data. The registry provides what's available; settings record what's chosen.

## Internal Design

### Startup Flow

1. Read API key from keyring. If missing, the TUI prompts the user (first-run flow).
2. Load model cache from `~/.ur/cache/models.json`. If cache is missing or stale, fetch from `GET https://openrouter.ai/api/v1/models` and write cache.
3. Read `ur.model` from settings. If missing, the TUI forces model selection (first-run flow).

### Client Creation

`CreateChatClient(string modelId)`:
1. Get API key from keyring.
2. Create OpenAI SDK `ChatClient` with endpoint `https://openrouter.ai/api/v1` and the model ID.
3. Return as `IChatClient`.

No model validation against the catalog — if the user has a model ID in settings, we trust it. OpenRouter will return an error if the model doesn't exist.

## Error Handling and Failure Modes

| Failure Mode              | Detection           | Recovery                         | Impact                        |
| ------------------------- | ------------------- | -------------------------------- | ----------------------------- |
| API key missing           | Keyring returns null | TUI prompts for key (first run)  | Cannot start until key entered |
| Models API unreachable    | HTTP error/timeout  | Use disk cache if available      | Stale model list              |
| Models API + no cache     | HTTP error + no file | Start anyway, skip model lookup  | No model metadata (compaction degrades gracefully) |
| Cache file corrupt        | JSON parse error    | Delete and re-fetch              | Transparent to user           |

## Design Decisions

### OpenRouter-only for now

- **Context:** Ur needs LLM access. Could support multiple providers (OpenAI, Anthropic, Google, OpenRouter) or start with one.
- **Choice:** OpenRouter only. It provides access to all major models through a single API and has a models discovery endpoint.
- **Rationale:** One provider means one endpoint, one API key, one auth flow, one model catalog. OpenRouter covers the broadest model set. Multi-provider adds complexity (different auth flows, different model discovery mechanisms, model ID disambiguation) with no functional gain for v1.
- **Consequences:** Users need an OpenRouter account and API key. Direct-provider pricing advantages are unavailable. Acceptable for v1.

### API-based model discovery (not static catalog)

- **Context:** Need model metadata (context length, pricing) for compaction and cost display. OpenRouter has 345+ models that change frequently.
- **Options considered:** Static `providers.json` with model catalog, API-based discovery with caching.
- **Choice:** Fetch from `GET https://openrouter.ai/api/v1/models` and cache to disk.
- **Rationale:** A static catalog of 345+ models is unmaintainable. The API is public (no auth required), returns everything we need, and is the source of truth.
- **Consequences:** Network dependency for model discovery (mitigated by disk cache). Cache staleness is acceptable — models don't change minute-to-minute.

### No model validation on client creation

- **Context:** Should `CreateChatClient` verify the model ID exists in the catalog before creating the client?
- **Choice:** No. Pass the model ID through to OpenRouter. If it's invalid, OpenRouter returns an error.
- **Rationale:** The catalog may be stale. A model could be valid on OpenRouter but not yet in our cache. Validation would create false negatives. Let the API be the authority.

### Separate registry from user settings

See [ADR-0009](decisions/adr-0009-separate-provider-registry.md).

- **Choice:** Separate. The model catalog (what's available) is distinct from settings (what the user chose).
- **Rationale:** The catalog is API-fetched truth. Settings are user choices. Mixing them conflates "what exists" with "what I want."

## Open Questions

- **Question:** What should the cache TTL be? Or should we only refresh on explicit user action (`/model` command)?
  **Current thinking:** Refresh when `/model` is opened. Don't refresh on every startup — it adds latency and a network dependency to launch. The cache is "good enough" for compaction; exact context lengths rarely change for existing models.
