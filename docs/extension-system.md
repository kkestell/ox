# Extension System

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Discovers, loads, and manages Lua extensions. Provides the Lua runtime (via Lua-CSharp), exposes C# APIs to Lua, and manages extension lifecycle (enable/disable). Extensions can provide tools, hook into the agent loop as middleware, declare settings, and interact with the filesystem and network through Ur's permission-gated APIs.

### Non-Goals

- Does not execute tools directly — that is the [Agent Loop](agent-loop.md) via the Tool Registry.
- Does not enforce permissions — that is the [Permission System](permission-system.md). The extension system loads extensions; the permission system gates what they can do.
- Does not manage settings storage or validation — that is [Configuration](configuration.md). Extensions declare their settings schemas; configuration validates and stores values.

## Context

### Dependencies

| Dependency          | What it provides                       | Interface                          |
| ------------------- | -------------------------------------- | ---------------------------------- |
| Lua-CSharp          | Lua runtime, `LuaPlatform` sandboxing  | `LuaState`, `LuaPlatform`         |
| Permission System   | Gates sensitive operations from Lua     | Permission check callbacks         |
| Configuration       | Resolved extension settings values      | Settings read API                  |
| Tool Registry       | Accepts tool registrations              | Tool registration API              |
| Workspace           | Provides workspace extension directory  | Filesystem path                    |

### Dependents

| Dependent    | What it needs                              | Interface                  |
| ------------ | ------------------------------------------ | -------------------------- |
| Agent Loop   | Middleware pipeline, loaded tool functions  | Middleware + tool APIs     |
| Tool Registry| Tool definitions registered by extensions  | Tool registration          |

## Interface

### Load Extensions

- **Purpose:** Discover and load extensions from the three-tier directory structure.
- **Inputs:** System dir (`~/.ur/extensions/system`), user dir (`~/.ur/extensions/user`), workspace dir (`$WORKSPACE/.ur/extensions`).
- **Outputs:** Set of discovered extensions with manifest metadata, plus the subset of extensions whose effective state is enabled and therefore active.
- **Errors:** Malformed manifest (warning, skip extension). Lua parse/runtime error on load (warning, skip extension).
- **Preconditions:** Directories may or may not exist.
- **Postconditions:** System and user extensions are enabled by default. Workspace extensions are disabled by default. Effective state is resolved by [Extension Management](extension-management.md): tier defaults plus persisted overrides. Only effective-enabled extensions are initialized. Loading order: system, then user, then workspace. Within a tier, order is unspecified.

### Enable / Disable Extension

- **Purpose:** Toggle an extension's active state at runtime.
- **Inputs:** Extension identifier, desired state.
- **Outputs:** Confirmation of state change.
- **Errors:** Unknown extension ID.
- **Postconditions:** Disabled extensions have no active runtime state; their tools are removed from the tool registry and their middleware is absent from the pipeline. Enabling an inactive extension initializes its runtime on demand.

## Data Structures

### Extension

- **Purpose:** Represents a loaded extension.
- **Shape:** Name, description, version, source tier (system/user/workspace), enabled state, list of registered tools, list of registered middleware, settings schema (JSON schema), Lua state reference.
- **Invariants:** An extension's source tier is immutable after load. An extension's name must be unique across all tiers (if two extensions share a name, the higher-trust tier wins — system > user > workspace).
- **Why this shape:** The source tier drives trust defaults (enabled/disabled). The settings schema is needed for validation at the configuration layer.

### Extension Manifest

- **Purpose:** Declares metadata and capabilities of an extension.
- **Shape:** A Lua table returned from `manifest.lua`. Contains: name, version, description, and declared settings (with JSON schemas). Permission declarations can be added later without changing the discovery model.
- **Invariants:** Name is required. Version is required (semver).
- **Why this shape:** Manifest discovery stays homogeneous with the rest of the extension toolchain and gives the system metadata before `main.lua` executes.

## Internal Design

Each extension is a directory containing a manifest and a main Lua script. Startup is split into discovery and activation:

1. Read `manifest.lua` to get metadata and settings schema.
2. Resolve effective enabled state from tier defaults plus persisted overrides in [Extension Management](extension-management.md).
3. For each effective-enabled extension, create a sandboxed `LuaState`, execute `main.lua`, and let the script register tools and middleware.
4. If activation fails, record the failure, leave the extension inactive, and continue startup. Do not abort host creation.

Extensions share no Lua state with each other. Each gets its own `LuaState`.

## Error Handling and Failure Modes

| Failure Mode               | Detection           | Recovery                    | Impact on Dependents       |
| -------------------------- | ------------------- | --------------------------- | -------------------------- |
| Malformed manifest         | Parse error on load | Skip extension, log warning | Extension's tools/middleware unavailable |
| Lua runtime error on load  | Exception from Lua-CSharp | Skip extension, log warning | Same |
| Lua runtime error during tool call | Exception from Lua-CSharp | Return error to agent loop as tool result | LLM sees error, can retry or adjust |
| Lua runtime error in middleware | Exception from Lua-CSharp | Skip this middleware, log warning, continue pipeline | Degraded but functional |

## Design Decisions

### Lua over .NET plugins

See [ADR-0001](decisions/adr-0001-lua-for-extensions.md) for full analysis.

- **Choice:** Lua via Lua-CSharp.
- **Rationale:** AoT rules out dynamic assembly loading. Lua is lightweight, sandboxable via `LuaPlatform`, and Lua-CSharp provides good .NET interop.
- **Consequences:** Extension authors must write Lua, not C#. The API surface exposed to Lua must be carefully designed and maintained.

### Isolated Lua states per extension

- **Context:** Should extensions share a Lua runtime or each get their own?
- **Options considered:** Shared state (simpler, extensions can cooperate), isolated states (safer, extensions can't interfere).
- **Choice:** Isolated states.
- **Rationale:** Security and reliability. A buggy extension cannot corrupt another's state. Aligns with the security quality attribute.
- **Consequences:** Inter-extension communication must go through Ur APIs, not shared Lua globals. Slight memory overhead per extension.

### Three-tier loading with trust defaults

See [ADR-0004](decisions/adr-0004-three-tier-extension-loading.md) for full analysis.

- **Choice:** System and user extensions enabled by default; workspace extensions disabled by default.
- **Rationale:** Workspace extensions live in the repo and could be contributed by anyone. A `git pull` shouldn't silently activate new agent capabilities.
- **Consequences:** Users must explicitly enable workspace extensions. This adds friction but prevents supply-chain-style attacks via repo contributions.

### Disabled extensions are discovered but not initialized

See [ADR-0012](decisions/adr-0012-extension-enablement-state-and-lazy-activation.md) for full analysis.

- **Context:** Discovery metadata is needed for listing and management, but "disabled" should mean the extension has not executed code in the current process.
- **Options considered:** Eager initialization for all discovered extensions; lazy initialization only for effective-enabled ones.
- **Choice:** Only effective-enabled extensions are initialized.
- **Rationale:** This keeps the security meaning of workspace-default-disabled intact and leaves room for richer extension APIs later.
- **Consequences:** Enabling an extension is an activation path, not just a registry flip. Disabled extensions remain visible in the management UI because their manifest metadata is still discovered.

## Open Questions

- **Question:** How do extensions declare dependencies on other extensions?
  **Context:** A "git-tools" extension might want to use a utility function from a "process" extension.
  **Current thinking:** Not needed in v1. Keep extensions independent. If deps are needed later, the manifest can declare them and the loader can topologically sort.

- **Question:** Can extensions be installed/managed from a registry (like npm or VS Code marketplace)?
  **Context:** Currently extensions are just directories. A package manager is a big scope addition.
  **Current thinking:** Out of scope for v1. Extensions are manually placed in directories. A future `ur install <extension>` command could fetch from a registry.
