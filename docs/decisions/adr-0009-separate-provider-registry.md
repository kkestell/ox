# ADR-0009: Separate Provider Registry from User Settings

- **Status:** accepted
- **Date:** 2026-03-29

## Context and Problem Statement

Ur needs to know two things about LLM providers and models: what exists (providers, models, context lengths, cost, settings schemas) and what the user has configured (which provider is enabled, what temperature to use). These could live in one place or two.

See [Provider Registry](../provider-registry.md) and [Configuration](../configuration.md).

## Decision Drivers

- **Schema before values.** Configuration validation needs to know what settings are valid for each model before the user's settings file is loaded.
- **Prevent accidental model creation.** If models are defined by their presence in settings, a typo creates a "model" that doesn't map to any real API endpoint.
- **Updatability.** Providers add and deprecate models frequently. The catalog must be updatable independently of user configuration.

## Considered Options

### Option 1: Everything in settings.json (models self-describe)

**Description:** No separate registry. Models are defined implicitly when the user adds settings for them (e.g. `"models.claude-sonnet-4.temperature": 0.7`). Model metadata (context length, cost) would also live in settings.

**Pros:**

- One data source. Simpler mental model for users.
- No registry file to maintain or update.

**Cons:**

- A typo in a model name creates a phantom model. No way to distinguish "user configured this model" from "user fat-fingered the name."
- Read-only properties (context length, cost) don't belong in a user-editable file — the user shouldn't be able to set `maxContextLength` to a wrong value.
- No schema source for model-specific settings validation. The system can't know that `temperature` is valid for model X but not model Y.
- Catalog updates (new models) require the user to manually add entries.

**When this is the right choice:** When the set of valid entries is open-ended and user-defined (e.g. custom API endpoints where there is no canonical catalog).

### Option 2: Separate registry (catalog) and settings (values)

**Description:** A static registry defines what providers and models exist, their read-only properties, and their settings schemas. The user's settings.json provides overrides for configurable values only. The registry is the schema; settings are the values.

**Pros:**

- Typos in model names are caught at validation time (unknown model = warning).
- Read-only properties are authoritative and not user-editable.
- Per-model settings schemas enable validation before the agent loop runs.
- Registry can ship embedded in the binary and be supplemented by a user override file (`~/.ur/providers.json`).

**Cons:**

- Two data sources to keep in sync.
- Registry must be updated when providers add models (embedded update requires a new build; override file is manual).

**When this is the right choice:** When there is a canonical set of valid entries with known schemas, and user configuration is overrides on top of that set.

## Decision

We chose **separate registry** because the provider/model catalog has a canonical shape (known providers, known models, known properties) and user settings are overrides on that shape. Mixing them would lose the ability to validate user input against the catalog and would allow accidental model creation via typos.

## Consequences

### Positive

- Configuration validation catches typos and invalid model settings at startup.
- Read-only model properties (context length, cost) are authoritative.
- Clear separation: registry owns "what exists," settings own "what the user chose."

### Negative

- Registry staleness when providers add models. Mitigated by user override file and potential future extension-provided models.
- Two places to look when debugging "why is my model setting not working."

### Neutral

- The registry format (embedded JSON + override file) is independent of the settings format.

## Confirmation

- Unknown model names in settings produce a clear warning at startup.
- Users cannot accidentally override read-only model properties.
