# Provider Registry

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Declares the available LLM providers, their models, per-model read-only properties (context length, cost), and per-model configurable settings with JSON schemas. Provides the data needed by the agent loop to talk to the right API, by configuration to validate model settings, and by the core to make decisions like session compaction.

This is the "catalog" — it says what exists and what is valid. The user's actual choices (which provider is enabled, which model is default, what temperature to use) live in [Configuration](configuration.md).

### Non-Goals

- Does not store API keys — that is [Configuration](configuration.md) via the system keyring.
- Does not make LLM API calls — that is the [Agent Loop](agent-loop.md) via Microsoft.Extensions.AI.
- Does not implement provider-specific HTTP clients — Microsoft.Extensions.AI handles that.

## Context

### Dependencies

| Dependency               | What it provides                  | Interface                        |
| ------------------------ | --------------------------------- | -------------------------------- |
| Microsoft.Extensions.AI  | Provider-specific client impls    | `IChatClient` per provider       |
| Static registry data     | Provider/model definitions        | Embedded JSON or override file   |

### Dependents

| Dependent     | What it needs                                      | Interface                       |
| ------------- | -------------------------------------------------- | ------------------------------- |
| Configuration | Per-model settings schemas (for validation)        | JSON schemas                    |
| Agent Loop    | `IChatClient` for the selected model               | Client factory                  |
| Core (compaction) | Model context length                           | Read-only property lookup       |

## Interface

### Get Providers

- **Purpose:** List all known providers.
- **Outputs:** List of providers with name, description, available models.

### Get Model

- **Purpose:** Get a specific model's metadata.
- **Inputs:** Model identifier (e.g. `"claude-sonnet-4"`, `"gpt-4o"`).
- **Outputs:** Model properties (context length, cost per token, etc.), settings schema (JSON schema for configurable settings like temperature, thinking level), provider reference.
- **Errors:** Unknown model ID.

### Get Model Settings Schema

- **Purpose:** Get the JSON schema for a model's configurable settings.
- **Inputs:** Model identifier.
- **Outputs:** JSON schema. Example for a model supporting temperature and thinking:
  ```json
  {
    "temperature": { "type": "number", "minimum": 0, "maximum": 1, "default": 0.7 },
    "thinkingLevel": { "type": "string", "enum": ["low", "medium", "high"], "default": "medium" }
  }
  ```
- **Errors:** Unknown model ID.

### Create Chat Client

- **Purpose:** Create a configured `IChatClient` for a given provider/model.
- **Inputs:** Provider name, model ID, API key (from keyring), model settings (from configuration).
- **Outputs:** `IChatClient` ready to use.
- **Preconditions:** Provider must be enabled. API key must be available.

## Data Structures

### Provider

- **Purpose:** Represents an LLM API backend.
- **Shape:** Name (e.g. `"openai"`, `"anthropic"`), display name, list of model IDs.
- **Invariants:** Provider name is unique. Provider name is the key used in the keyring for API key storage.

### Model

- **Purpose:** Represents a specific LLM.
- **Shape:**
  - `id`: Unique identifier (e.g. `"claude-sonnet-4"`, `"gpt-4o"`).
  - `provider`: Reference to parent provider.
  - `properties`: Read-only attributes:
    - `maxContextLength`: Token count.
    - `maxOutputLength`: Token count.
    - `costPerInputToken`: Decimal.
    - `costPerOutputToken`: Decimal.
    - `supportsTool Calling`: Boolean.
    - `supportsStreaming`: Boolean.
  - `settingsSchema`: JSON schema defining what the user can configure for this model.
- **Invariants:** Model ID is unique across all providers. Every model has a settings schema (even if empty — meaning no configurable settings).
- **Why this shape:** Properties are used by the core (compaction decisions, cost tracking). Settings schema is used by configuration for validation. Separating read-only properties from configurable settings prevents users from trying to set `maxContextLength` in their settings file.

### Registry Data

- **Purpose:** The static catalog of all providers and models.
- **Shape:** JSON file at `~/.ur/providers.json`. Ships with Ur. Example structure:
  ```json
  {
    "providers": {
      "anthropic": {
        "displayName": "Anthropic",
        "models": {
          "claude-sonnet-4": {
            "properties": { "maxContextLength": 200000, "maxOutputLength": 8192, ... },
            "settingsSchema": { "temperature": { "type": "number", "minimum": 0, "maximum": 1 } }
          }
        }
      }
    }
  }
  ```
- **Invariants:** Ur owns this file. Users can hand-edit it (e.g. to add a newly released model), but changes while Ur is running may be overwritten. Edits take effect on restart.
- **Why this shape:** Pure JSON, no code execution needed to parse. Single file, no embedded resources, no merge logic. Simpler than the embedded-plus-override approach.

### Model Identifiers

Models are always referenced by a **qualified identifier**: `{provider}/{model-id}`. Split on the first `/` to get provider and model.

- `anthropic/claude-sonnet-4` → provider `anthropic`, model `claude-sonnet-4`
- `openrouter/google/gemini-3-pro-preview` → provider `openrouter`, model `google/gemini-3-pro-preview`

The same underlying model can exist on multiple providers (e.g. Gemini on Google's API vs OpenRouter). These are distinct entries with distinct qualified IDs, properties, and pricing.

Settings keys use the qualified form: `"models.anthropic/claude-sonnet-4.temperature": 0.7`. The slash is unambiguous because the dot is the namespace separator.

## Internal Design

The registry is loaded early in startup, before configuration validation (since configuration needs the model settings schemas).

Load order:
1. Read `~/.ur/providers.json`.
2. Register per-model settings schemas with the configuration system.

## Error Handling and Failure Modes

| Failure Mode              | Detection           | Recovery                       | Impact on Dependents         |
| ------------------------- | ------------------- | ------------------------------ | ---------------------------- |
| Embedded registry missing | Startup check       | Fatal — binary is corrupt      | Cannot start                 |
| Override file malformed   | Parse error         | Log warning, use embedded only | User overrides unavailable   |
| Unknown model in settings | Configuration validation | Warning (unknown setting key) | Setting ignored              |
| API key missing for enabled provider | Client creation | Error with clear message  | Cannot use that provider     |

## Design Decisions

### Separate registry from user settings

See [ADR-0009](decisions/adr-0009-separate-provider-registry.md) for full analysis.

- **Choice:** Separate registry.
- **Rationale:** The registry is the schema — it defines what is valid. Settings are the user's values. Mixing them means the user could accidentally "create" a model by adding settings for a model ID that doesn't exist. Separation enforces that you can only configure models that the registry knows about.
- **Consequences:** Two data sources to keep in sync. The registry must be updated when providers add models.

### Provider enabled = API key in keyring (no separate flag)

- **Context:** Need to know which providers are active.
- **Options considered:** Explicit `enabled` flag in settings, derive from API key presence.
- **Choice:** A provider is enabled if and only if it has an API key in the system keyring. No separate flag.
- **Rationale:** Eliminates a redundant state. You can't use a provider without a key. Enabling = enter key, disabling = delete key. One action, one state.
- **Consequences:** The provider management UI reads the keyring to determine state. No settings entry for provider enablement.

### Provider management as a UI flow (not settings)

- **Context:** Enabling a provider requires an API key. Disabling means deleting the key.
- **Options considered:** Purely declarative in settings (`"providers.openai.enabled": true`), interactive UI flow.
- **Choice:** Interactive UI flow — the user sees a list of providers (from `providers.json`), enables/disables them (keyring), configures model settings (`settings.json`). See [UI Contract](ui-contract.md).
- **Rationale:** API key entry is inherently interactive (you don't want keys in settings files). The enable/disable flow naturally pairs with key management.
- **Consequences:** The library must expose a provider management interface that UI layers implement. This is another case (like permissions) where the library defines the interaction contract and the UI layer renders it.

### Model change starts a new session

See [ADR-0010](decisions/adr-0010-model-change-new-session.md) for full analysis.

- **Choice:** Switching models ends the current session and starts a new one.
- **Rationale:** Sessions store raw `ChatMessage` with provider-specific `AdditionalProperties`. Different providers' metadata is incompatible — mixing them in one session causes errors or silent data loss.
- **Consequences:** The UI warns "changing models will start a new session." V1 uses one model per session. Multi-model conversations are a future concern.

### Extensions cannot add providers

- **Context:** Could extensions register new providers at runtime (e.g. corporate proxy, self-hosted endpoint)?
- **Choice:** No. Extensions can register tools and hook into the agent loop. The provider registry is static.
- **Rationale:** Keeping the registry static simplifies startup (no chicken-and-egg with extension loading). The user file `~/.ur/providers.json` covers the "add a custom endpoint" case without runtime complexity.
- **Consequences:** A user who wants a custom provider edits `providers.json` and implements an `IChatClientFactory` (or we add support for OpenAI-compatible endpoints in the shipped factory).

## Open Questions

None currently.
