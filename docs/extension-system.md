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
- **Outputs:** Set of loaded extensions, each with metadata and registered capabilities (tools, middleware).
- **Errors:** Malformed manifest (warning, skip extension). Lua parse/runtime error on load (warning, skip extension).
- **Preconditions:** Directories may or may not exist.
- **Postconditions:** System and user extensions are enabled by default. Workspace extensions are disabled by default. Loading order: system, then user, then workspace. Within a tier, order is unspecified.

### Enable / Disable Extension

- **Purpose:** Toggle an extension's active state at runtime.
- **Inputs:** Extension identifier, desired state.
- **Outputs:** Confirmation of state change.
- **Errors:** Unknown extension ID.
- **Postconditions:** Disabled extensions' tools are removed from the tool registry; their middleware is bypassed.

## Data Structures

### Extension

- **Purpose:** Represents a loaded extension.
- **Shape:** Name, description, version, source tier (system/user/workspace), enabled state, list of registered tools, list of registered middleware, settings schema (JSON schema), Lua state reference.
- **Invariants:** An extension's source tier is immutable after load. An extension's name must be unique across all tiers (if two extensions share a name, the higher-trust tier wins — system > user > workspace).
- **Why this shape:** The source tier drives trust defaults (enabled/disabled). The settings schema is needed for validation at the configuration layer.

### Extension Manifest

- **Purpose:** Declares metadata and capabilities of an extension.
- **Shape:** TBD — likely a table returned from a `manifest.lua` or a JSON file. Contains: name, version, description, declared settings (with JSON schemas), required permissions.
- **Invariants:** Name is required. Version is required (semver).
- **Why this shape:** Manifest is read before the extension's main code executes, so the system knows what to expect (settings, permissions) before granting any capabilities.

## Internal Design

Each extension is a directory containing a manifest and a main Lua script. On load:

1. Read the manifest to get metadata, settings schema, and declared permissions.
2. Create a sandboxed `LuaState` with a `LuaPlatform` configured per the extension's tier and permissions.
3. Execute the main Lua script. The script calls Ur APIs to register tools and middleware.
4. If any step fails, log a warning and skip the extension. Do not abort startup.

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

## Open Questions

- **Question:** What does the extension manifest look like concretely? `manifest.lua` (a Lua table) or `extension.json`?
  **Context:** Lua is more natural for a Lua extension system, but JSON is language-agnostic and easier to parse without running code.
  **Current thinking:** Leaning toward `manifest.lua` that returns a table. Keeps the toolchain homogeneous. The manifest is evaluated in a minimal sandbox (no I/O, no network).

- **Question:** How do extensions declare dependencies on other extensions?
  **Context:** A "git-tools" extension might want to use a utility function from a "process" extension.
  **Current thinking:** Not needed in v1. Keep extensions independent. If deps are needed later, the manifest can declare them and the loader can topologically sort.

- **Question:** What happens when two extensions in different tiers have the same name?
  **Context:** E.g., a system extension "git" and a workspace extension "git".
  **Current thinking:** Higher-trust tier wins (system > user > workspace). The lower-tier extension is skipped with a warning.

- **Question:** Can extensions be installed/managed from a registry (like npm or VS Code marketplace)?
  **Context:** Currently extensions are just directories. A package manager is a big scope addition.
  **Current thinking:** Out of scope for v1. Extensions are manually placed in directories. A future `ur install <extension>` command could fetch from a registry.
