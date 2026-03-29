# ADR-0006: Unified Settings File with Flat Dot-Namespaced Keys

- **Status:** accepted
- **Date:** 2026-03-29

## Context and Problem Statement

Ur has three sources of configuration: core settings, extension settings, and per-model settings from the provider registry. These all need to live somewhere. The user needs to edit settings in one place, and the configuration system needs to validate them against schemas declared by each source.

See [Configuration](../configuration.md).

## Decision Drivers

- **Single-developer simplicity.** One file to find, edit, and reason about beats multiple files.
- **Familiar to developers.** VS Code's settings model is widely understood.
- **Merge semantics must be trivial.** Workspace settings override user settings. Deep-merge of nested objects is ambiguous (does a workspace object replace or extend the user-level one?).
- **Schema validation is per-key.** Each setting has one owner with one schema. Validation should be a flat lookup, not a tree traversal.

## Considered Options

### Option 1: Nested JSON (grouped by owner)

**Description:** Settings grouped hierarchically — `{ "ur": { "compactThreshold": 0.8 }, "git-tools": { "defaultBranch": "main" } }`.

**Pros:**

- Visually structured — easy to see what belongs to what.
- Familiar from `package.json`, `tsconfig.json`, etc.

**Cons:**

- Deep-merge semantics for workspace/user layering are ambiguous. Does a workspace `"ur"` object replace or merge with the user-level `"ur"` object? Either choice surprises someone.
- Schema validation requires tree traversal or flattening internally anyway.
- Extension authors must understand the nesting structure to contribute settings.

**When this is the right choice:** When settings are deeply hierarchical with meaningful sub-grouping (e.g. webpack config).

### Option 2: Flat dot-namespaced keys (VS Code style)

**Description:** Single JSON object with flat keys — `{ "ur.compactThreshold": 0.8, "git-tools.defaultBranch": "main" }`.

**Pros:**

- Merge is trivial: workspace key replaces user key. No ambiguity.
- Schema validation is a flat map lookup.
- Familiar to VS Code users.
- Simple to implement — `Dictionary<string, JsonElement>` with `StringComparer.Ordinal`.

**Cons:**

- Keys can get long (`models.gemini-3-flash-preview.thinkingLevel`).
- No visual grouping in the file.
- Extension authors must choose a unique namespace prefix; collisions are detected at schema registration time, not earlier.

**When this is the right choice:** When settings are mostly scalar values, merging must be unambiguous, and the system is schema-validated per key.

## Decision

We chose **flat dot-namespaced keys** because merge semantics must be trivially correct (workspace key wins, no deep-merge ambiguity), validation is per-key against registered schemas, and the VS Code precedent makes this familiar. The settings in Ur are overwhelmingly scalar values, so the lack of visual grouping is a minor cost.

## Consequences

### Positive

- Workspace/user merging is a single-pass key-level replacement.
- Schema validation is O(1) per key.
- Extension authors can register settings with a single namespace prefix.

### Negative

- Long key names for deeply scoped settings (e.g. per-model overrides).
- No structural grouping — users must rely on naming conventions to find related settings.
- Namespace collision detection happens at runtime (schema registration), not at install time.

### Neutral

- The format is JSON, same as either option. Tooling compatibility is identical.

## Confirmation

- Extension authors can register and validate settings without encountering merge ambiguity.
- Users can override any setting at the workspace level by specifying the same key.
