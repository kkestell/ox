# Configuration & Settings

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Manages a unified settings system (core + extension + model settings) with JSON schema validation, user/workspace layering at startup, runtime mutation via API, and secret storage via the system keyring. Provides the single source of truth for all configuration in Ur.

### Non-Goals

- Does not define what settings exist — core, extensions, and the provider registry each declare their own schemas. This component validates and stores values against those schemas.
- Does not manage the provider/model registry itself — that is [Provider Registry](provider-registry.md). But it does store and validate model settings overrides.

## Context

### Dependencies

| Dependency        | What it provides                           | Interface                     |
| ----------------- | ------------------------------------------ | ----------------------------- |
| Extension System  | Extension settings schemas                 | JSON schemas per extension    |
| Provider Registry | Per-model settings schemas                 | JSON schemas per model        |
| System keyring    | Secure secret storage                      | OS keyring API                |
| Workspace         | Workspace-level settings file path         | Filesystem path               |

### Dependents

| Dependent         | What it needs                              | Interface                     |
| ----------------- | ------------------------------------------ | ----------------------------- |
| Agent Loop        | Core settings (e.g. default model)         | Settings read API             |
| Extension System  | Resolved extension settings                | Settings read API             |
| Provider Registry | Model settings overrides, provider API keys| Settings read API, keyring API|

## Interface

### Load Settings

- **Purpose:** Read, merge, and validate settings from user and workspace files.
- **Inputs:** User settings path (`~/.ur/settings.json`), workspace settings path (`$WORKSPACE/.ur/settings.json`), combined schema (core + loaded extensions + provider registry).
- **Outputs:** Merged, validated settings object.
- **Errors:** File parse error (fatal). Unknown setting key (warning, ignored). Type mismatch against schema (error, fatal with clear message).
- **Preconditions:** Schema sources (core, extensions, provider registry) must have registered their schemas before settings are loaded.
- **Postconditions:** Every setting in the merged result conforms to its declared schema.

### Get Setting

- **Purpose:** Read a single setting value.
- **Inputs:** Dot-namespaced key (e.g. `"ur.defaultModel"`, `"git-tools.defaultBranch"`).
- **Outputs:** The resolved value (workspace overrides user), or null if unset. Schema-declared defaults will be supported when the extension system lands — until then, callers provide their own fallbacks.
- **Errors:** Unknown key (warning).

### Get Secret

- **Purpose:** Retrieve a secret (e.g. API key) from the system keyring.
- **Inputs:** Secret identifier (e.g. provider name).
- **Outputs:** Secret value, or null if not set.

### Set Secret

- **Purpose:** Store a secret in the system keyring.
- **Inputs:** Secret identifier, secret value.
- **Postconditions:** Secret is persisted in the OS keyring. Never written to settings files.

### Delete Secret

- **Purpose:** Remove a secret from the system keyring.
- **Inputs:** Secret identifier.

## Data Structures

### Settings File

- **Purpose:** Stores user or workspace configuration.
- **Shape:** JSON object with flat, dot-namespaced keys. Example:
  ```json
  {
    "ur.defaultModel": "claude-sonnet-4",
    "git-tools.defaultBranch": "main",
    "models.claude-sonnet-4.temperature": 0.7,
    "models.gemini-3-flash-preview.thinkingLevel": "medium"
  }
  ```
- **Invariants:** Keys are dot-separated, namespaced by owner. No nesting — the file is a flat key-value map. Values must conform to the schema declared by the key's owner.
- **Why this shape:** VS Code-style flat keys. Simpler merging (workspace key wins over user key, no deep merge ambiguity). Simpler schema validation (one schema per key). Familiar to developers.

### Settings Schema Registry

- **Purpose:** Aggregates JSON schemas from all sources (core, extensions, provider registry).
- **Shape:** Map of setting key to JSON schema. Built at startup as schemas are registered.
- **Invariants:** A key can only be registered once. If two sources try to register the same key, it is an error.

## Internal Design

Settings loading order:

1. Core registers its settings schemas.
2. Extensions are loaded; each registers its settings schemas (from manifest).
3. Provider registry registers per-model settings schemas.
4. User `settings.json` is read and parsed.
5. Workspace `settings.json` is read and parsed (if it exists).
6. Workspace values override user values (simple key-level replacement, no deep merge).
7. Merged result is validated against the combined schema registry.
8. Unknown keys: log warning, ignore.
9. Type mismatches: log error with details (key, expected type, actual value), abort startup.

## Design Decisions

### Flat dot-namespaced keys (not nested JSON)

See [ADR-0006](decisions/adr-0006-unified-settings-file.md) for full analysis.

- **Choice:** Flat dot-namespaced keys.
- **Rationale:** Schema validation is per-key — one schema lookup per setting, no nested path resolution. Familiar to VS Code users. Nested JSON would require defining deep-merge semantics for the workspace/user layering, which is complexity for no benefit when settings are mostly scalar values.
- **Consequences:** Keys can get long (`models.gemini-3-flash-preview.thinkingLevel`). No structural grouping in the file. Extension authors must choose a unique namespace; collisions are an error at schema registration time.

### Unknown keys warn, type mismatches error

- **Context:** Need to handle settings that don't match any known schema.
- **Options considered:** Strict (all unknowns are errors), lenient (all unknowns ignored), mixed.
- **Choice:** Mixed — unknowns warn, type mismatches error.
- **Rationale:** Unknown keys are likely typos or stale settings from uninstalled extensions — annoying but not dangerous. Type mismatches indicate a real problem (wrong value type will cause runtime errors downstream).
- **Consequences:** Users see warnings for leftover settings after uninstalling an extension. They must fix type errors before Ur will start.

### Settings are mutable at runtime, persisted through the API (planned — not yet implemented)

- **Context:** Should settings reload when the file changes, or only at startup?
- **Options considered:** File watch (VS Code style), load once at startup (simplest), mutable via API with write-through to disk.
- **Choice:** Settings are loaded at startup. Changes at runtime go through the settings API, which updates both in-memory state and the settings file on disk. No file watcher.
- **Current state:** `SettingsLoader.Load()` implements the startup path (read, merge, validate). The runtime mutation API (set + write-through) is not yet implemented — `Settings` is currently read-only after load.
- **Rationale:** All settings mutations originate from within the application (e.g. UI, extensions). The API is the single writer — it updates memory and persists to disk in one operation. File watching adds complexity for a scenario (external edits during a session) that doesn't need to be supported. External edits take effect on the next session.
- **Consequences:** Components read from the in-memory settings object, which is always current. The settings file on disk stays in sync for the next session or for external tooling to inspect.

### Schema-declared defaults are an extension system feature (deferred)

- **Context:** Should `Settings.Get` return schema-declared defaults when a key isn't explicitly set?
- **Options considered:** (A) Bake defaults into the merged values dictionary at load time — simple but lossy, can't distinguish "user set this" from "default." (B) Separate values and defaults dictionaries — preserves the distinction cheaply, clean for runtime mutation later. (C) Defer entirely.
- **Choice:** Defer until the extension system lands.
- **Rationale:** The only current consumer of defaults would be extensions (e.g. `"git-tools.defaultBranch"` defaulting to `"main"`). Core settings either don't benefit from schema defaults (`ur.defaultModel` has runtime fallback logic) or turned out not to be settings at all (compact threshold is computed from model context length). Building the machinery now has no consumer. When it is built, Option B is the right shape.
- **Consequences:** Callers that need a fallback use `settings.Get<T>(key) ?? fallback` for now. This is fine for the small number of core settings.

### Compact threshold is not a user setting

- **Context:** Originally listed as a core setting (`ur.compactThreshold`). Is it user-configurable?
- **Choice:** No. It is computed from the current model's context length. It belongs in the agent loop's internal logic.
- **Rationale:** The compact threshold is a function of how much context you have, which varies per model. Exposing it as a user-tunable knob invites misconfiguration with no real benefit.
- **Consequences:** Removed from core settings schemas.

### LibraryImport P/Invoke for cross-platform keyring access

See [ADR-0008](decisions/adr-0008-libraryimport-keyring.md) for full analysis.

- **Choice:** `LibraryImport`-based P/Invoke wrapper per platform behind an `IKeyring` interface.
- **Rationale:** Small surface area (~150 lines per platform), full AoT compatibility via source-generated marshalling, no external dependencies, complete control.
- **Consequences:** Depends on `libsecret-1.so.0` and `libglib-2.0.so.0` at runtime on Linux (standard on GNOME/KDE desktops). Works with any `org.freedesktop.secrets` provider (gnome-keyring-daemon, KWallet with the secrets portal, etc.).

## Open Questions

None currently.
