# Extension Management

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Provides the library-owned surface for listing discovered extensions, resolving their effective enabled state, persisting user overrides, and activating/deactivating extensions without exposing loader or tool-registry internals to UIs. It sits between the [Extension System](extension-system.md), the public [Host & Session API](host-session-api.md), and frontend surfaces such as the [TUI Chat Client](cli-tui.md).

Implementation plan: [extension-management-implementation-plan.md](extension-management-implementation-plan.md).

### Non-Goals

- Does not install, update, or uninstall extensions from a registry or marketplace. Extensions are still directories on disk in v1.
- Does not parse manifests or execute Lua directly. Discovery and activation remain extension-system responsibilities.
- Does not store extension settings or schemas. Those still belong to [Configuration](configuration.md).
- Does not let a repository opt users into workspace extensions. Workspace trust decisions are per-user state, not repo state.

## Context

### Dependencies

| Dependency | What it provides | Interface |
|------------|------------------|-----------|
| Extension System | Manifest discovery, activation/deactivation hooks, runtime instances | Internal activation API |
| Workspace | Stable workspace root path for per-workspace state lookup | Absolute root path |
| Filesystem | Persistence for override state | JSON files under `~/.ur/` |

### Dependents

| Dependent | What it needs | Interface |
|-----------|---------------|-----------|
| Host & Session API | Public workspace-scoped extension management surface | `ExtensionCatalog` |
| TUI Chat Client | List/search/toggle extensions | `List()`, `SetEnabledAsync(...)`, `ResetAsync(...)` |
| Future CLI/GUI frontends | Same as above | Same |

## Interface

### List Extensions

- **Purpose:** Return a UI-friendly snapshot of all discovered extensions for the current workspace.
- **Inputs:** None.
- **Outputs:** Ordered list of `ExtensionInfo` values.
- **Errors:** None. Discovery/activation failures are reflected in the returned info rather than failing the list operation.
- **Preconditions:** Host startup has completed manifest discovery.
- **Postconditions / Invariants:** The list includes disabled and faulted extensions, not just active ones. Order is stable: system, user, workspace; then by name within each tier.

### Set Enabled State

- **Purpose:** Change whether an extension should be active for this user in this scope.
- **Inputs:** `extensionId`, `enabled`.
- **Outputs:** Updated `ExtensionInfo`.
- **Errors:** Unknown extension ID; persistence failure while writing override state.
- **Preconditions:** Caller should invoke this between turns in v1. Mutating extension state during an active turn is out of scope.
- **Postconditions / Invariants:** If the requested state differs from the tier default, a persisted override exists. If the requested state matches the default, any persisted override is removed. Disabling unregisters capabilities and tears down runtime state. Enabling initializes the runtime and registers capabilities; activation failure leaves the extension inactive and records `LoadError`.

### Reset To Default

- **Purpose:** Remove a persisted override and return an extension to its tier default.
- **Inputs:** `extensionId`.
- **Outputs:** Updated `ExtensionInfo`.
- **Errors:** Unknown extension ID; persistence failure while clearing override state.
- **Preconditions:** Same as `Set Enabled State`.
- **Postconditions / Invariants:** The effective state becomes the tier default: system/user enabled, workspace disabled.

## Quality Attributes

| Attribute | Requirement | Implication for design |
|-----------|-------------|------------------------|
| Security | Workspace extensions must never become enabled because of repo state alone | Workspace opt-ins are persisted outside the repo in per-user workspace state |
| Usability | Users need to see what exists, what is active, and why something failed | The public snapshot includes status, tier, override presence, and load error |
| Simplicity | One developer should be able to reason about the rules quickly | Persist deltas from defaults only; native scope rules instead of a full policy matrix |

## Data Structures

### `UrExtensionId`

- **Purpose:** Stable identifier for one discovered extension.
- **Shape:** `{ Tier, Name }`, serialized as `<tier>:<name>` for persistence and UI commands.
- **Invariants:** Unique within a host instance. Tier is part of the identity even though lower-trust collisions are currently skipped.
- **Why this shape:** It is human-readable, stable across restarts, and unambiguous in persisted state.

### `ExtensionInfo`

- **Purpose:** Public snapshot exposed to UIs.
- **Shape:** `{ Id, Name, Tier, Description, Version, DefaultEnabled, DesiredEnabled, IsActive, HasOverride, LoadError? }`.
- **Invariants:** `IsActive` implies `DesiredEnabled`. `LoadError` is null unless `DesiredEnabled` is true and activation failed in the current process.
- **Why this shape:** UIs need to distinguish "disabled by choice" from "should be enabled but failed to activate."

### `ExtensionOverride`

- **Purpose:** Persist a user decision that differs from the tier default.
- **Shape:** Map entry from `UrExtensionId` to `bool enabled`.
- **Invariants:** Only stored when the value differs from the default for that tier. Global state contains only system/user IDs; per-workspace state contains only workspace IDs.
- **Why this shape:** Delta storage keeps the persisted files small and makes "what did the user explicitly choose?" obvious.

### `ExtensionOverrideStore`

- **Purpose:** Persist override maps.
- **Shape:** Two JSON files:
  - Global: `~/.ur/extensions-state.json` for system/user overrides
  - Per-workspace: `~/.ur/workspaces/<workspace-hash>/extensions-state.json` for workspace-tier overrides
- **Invariants:** The workspace file is keyed by a hash of the absolute workspace path and stores that original path for diagnostics. No extension enablement state is written under `$WORKSPACE/.ur/`.
- **Why this shape:** Workspace opt-ins are trust decisions by a person on a machine, not project configuration to commit into a repo.

## Internal Design

Startup becomes a three-phase process:

1. Discover manifests from all three tiers and build descriptors only.
2. Read override state, compute each extension's `DesiredEnabled` from `tier default + override`.
3. Initialize only `DesiredEnabled == true` extensions. Disabled extensions remain manifest-only entries in the catalog.

At runtime, enable/disable flows go through the catalog:

- **Enable:** persist override if needed, create sandboxed runtime, execute `main.lua`, register tools/middleware, clear `LoadError` on success.
- **Disable:** unregister tools/middleware, dispose runtime state, clear transient load errors, persist override if needed.
- **Reset:** clear the persisted override, then reconcile the live runtime to the tier default.

This design deliberately separates three concerns that were previously blurred together:

- Discovery: "This extension exists."
- Desired state: "The user wants this active/inactive in this scope."
- Active runtime: "Its code has been executed and its capabilities are currently registered."

## Error Handling and Failure Modes

| Failure Mode | Detection | Recovery | Impact on Dependents |
|--------------|-----------|----------|----------------------|
| Override state file is malformed | JSON parse failure on host startup | Warn, ignore overrides, fall back to tier defaults | UI still lists extensions; some toggles may revert to defaults |
| Persisting an override fails | File I/O exception during toggle | Return failure to caller; keep in-memory state unchanged | UI shows error and leaves prior state intact |
| Extension activation fails (`main.lua` parse/runtime error) | Exception from extension system | Mark `LoadError`, leave extension inactive | UI can surface failure and offer retry/disable |
| Unknown extension ID | Lookup miss in catalog | Return error immediately | Caller bug or stale UI selection |

## Design Decisions

### Enablement state is dedicated management state, not settings

See [ADR-0012](decisions/adr-0012-extension-enablement-state-and-lazy-activation.md).

- **Context:** Extension enablement is a trust and lifecycle concern, not just a typed configuration key.
- **Options considered:** Settings-based persistence; dedicated state store; ephemeral-only toggles.
- **Choice:** Dedicated state store with separate global and per-workspace files.
- **Rationale:** Putting workspace enablement in repo-scoped settings would let the repo try to opt users into untrusted code, which breaks the trust model.
- **Consequences:** Extension management has its own persistence path and public API surface instead of living under `UrConfiguration`.

### Disabled extensions are not initialized

See [ADR-0012](decisions/adr-0012-extension-enablement-state-and-lazy-activation.md).

- **Context:** A "disabled" extension that still executes `main.lua` is only cosmetically disabled.
- **Options considered:** Eager initialization for all discovered extensions; lazy initialization only for effective-enabled extensions.
- **Choice:** Initialize only effective-enabled extensions.
- **Rationale:** This preserves the security meaning of "disabled" and keeps future extension APIs from accidentally executing untrusted code during discovery.
- **Consequences:** Enabling an extension may do work on demand; disabling tears down runtime state.

### Overrides follow native scope in v1

- **Context:** Users may eventually want "disable this user extension only in this workspace."
- **Options considered:** Full two-dimensional policy matrix; native-scope-only rules.
- **Choice:** Native scope only in v1. System/user toggles are global. Workspace toggles are per-user per-workspace.
- **Rationale:** The simple rule matches user expectations and avoids inventing a complex precedence lattice before there is proven demand.
- **Consequences:** Per-workspace suppression of global extensions is deferred. If it becomes important later, it can be added as another override layer.

## Open Questions

- **Question:** Should malformed-manifest extensions appear in the management UI, or only in logs?
  **Context:** Today a bad manifest prevents discovery entirely, which means the user cannot fix it from the TUI because it never appears in the list.
  **Current thinking:** Probably yes eventually, as a read-only error row. Not necessary for the first management pass.

- **Question:** Do we need per-workspace overrides for user/system extensions?
  **Context:** Some users may want to keep a global extension installed but suppress it in one noisy repo.
  **Current thinking:** Defer. Native-scope-only rules are the right starting point.
