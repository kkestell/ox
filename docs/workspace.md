# Workspace

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Represents the directory Ur was launched in. Provides path computation for all workspace-scoped state (sessions, extensions, settings, permissions) and a boundary check for the permission system to distinguish in-workspace vs out-of-workspace operations.

### Non-Goals

- Does not manage the contents of `.ur/` subdirectories — that is the responsibility of [Session Storage](session-storage.md), [Extension System](extension-system.md), [Configuration](configuration.md), and [Permission System](permission-system.md) respectively.
- Does not discover or select the workspace. The caller (CLI or host) provides the root path; `Workspace` just wraps it.

## Context

### Dependencies

None. `Workspace` is a leaf — it depends only on `System.IO.Path`.

### Dependents

| Dependent | What it needs | Interface |
|---|---|---|
| Session Storage | `SessionsDirectory` path | Property |
| Extension System | `ExtensionsDirectory` path | Property |
| Configuration | `SettingsPath` for workspace settings | Property |
| Permission System | `Contains(path)` for in/out-of-workspace checks, `PermissionsPath` for grant persistence | Method, property |
| UrHost | All of the above during startup | Constructor + properties |

## Interface

### Constructor

- **Inputs:** `rootPath` (string) — the directory Ur was launched in.
- **Postconditions:** `RootPath` is the fully resolved absolute path.

### Path Properties

| Property | Value |
|---|---|
| `RootPath` | Absolute path to the workspace root |
| `UrDirectory` | `{RootPath}/.ur` |
| `SessionsDirectory` | `{UrDirectory}/sessions` |
| `ExtensionsDirectory` | `{UrDirectory}/extensions` |
| `SettingsPath` | `{UrDirectory}/settings.json` |
| `PermissionsPath` | `{UrDirectory}/permissions` |

### EnsureDirectories

- **Purpose:** Create the `.ur/sessions` and `.ur/extensions` directories if they don't exist.
- **Postconditions:** Both directories exist on disk.

### Contains

- **Purpose:** Check whether a given path is inside the workspace.
- **Inputs:** `path` (string).
- **Outputs:** `true` if the fully resolved path starts with `RootPath + /` or equals `RootPath`.
- **Used by:** Permission system to determine whether a file operation is in-workspace (always allowed) or out-of-workspace (requires permission).

## Data Structures

### Workspace

- **Shape:** Immutable value object. Single `RootPath` field; all other properties are computed. Implemented in [`Ur/Workspace.cs`](../Ur/Workspace.cs).
- **Invariants:** `RootPath` is always an absolute path (resolved via `Path.GetFullPath` in the constructor). Path properties are deterministic — no I/O on read.
- **Why this shape:** The workspace is a path anchor, not a stateful object. Every component that needs workspace-scoped paths gets them from the same source, preventing path-construction bugs from scattered `Path.Combine` calls.

## Design Decisions

### Contains uses prefix matching, not symlink resolution

- **Context:** `Contains(path)` checks `fullPath.StartsWith(RootPath + separator)`. This does not resolve symlinks — a symlink inside the workspace pointing outside would pass the check.
- **Choice:** Prefix matching on string paths.
- **Rationale:** Symlink resolution requires I/O and can fail. The permission system is the security boundary, not `Contains` — `Contains` is a fast heuristic for the common case. Symlink-based escapes are an edge case that can be addressed later if needed.
- **Consequences:** A symlink at `$WORKSPACE/link -> /etc` would make `Contains("$WORKSPACE/link/passwd")` return `true`. The permission system still gates the actual write/read operation, so this is defense-in-depth, not the sole check.
