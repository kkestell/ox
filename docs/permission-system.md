# Permission System

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Mediates access to sensitive operations (filesystem writes, network access, out-of-workspace reads). When an extension or tool attempts a gated operation, the permission system checks for an existing grant or prompts the user for approval with scope-appropriate options.

### Non-Goals

- Does not implement the UI for permission prompts — it defines the contract; the UI layer renders the prompt.
- Does not sandbox Lua execution — that is the [Extension System](extension-system.md) via `LuaPlatform`. The permission system is called when sandboxed code attempts a gated operation.

## Context

### Dependencies

| Dependency  | What it provides                        | Interface                  |
| ----------- | --------------------------------------- | -------------------------- |
| UI Layer    | Renders permission prompts to the user  | Permission prompt callback |
| Workspace   | Workspace root path (for in/out check)  | Filesystem path            |

### Dependents

| Dependent        | What it needs                            | Interface               |
| ---------------- | ---------------------------------------- | ----------------------- |
| Extension System | Permission checks for Lua API calls      | `CheckPermission(op)`   |
| Agent Loop       | Permission checks for tool execution     | `CheckPermission(op)`   |

## Interface

### Check Permission

- **Purpose:** Determine if an operation is allowed, prompting the user if needed.
- **Inputs:** Operation descriptor (type: read/write/network, target path or URL, requesting extension name).
- **Outputs:** Allowed or denied.
- **Errors:** UI layer unavailable (e.g. headless mode with no prompt handler) — deny by default.
- **Postconditions:** If the user grants permission with a persistent scope, the grant is recorded.

## Data Structures

### Permission Grant

- **Purpose:** Records a user's approval for a specific operation class.
- **Shape:** Operation type, target pattern (path prefix or domain), scope (once/session/workspace/always), granting extension.
- **Invariants:** Scope restrictions by operation:

| Operation                      | Allowed scopes                          |
| ------------------------------ | --------------------------------------- |
| File read (in workspace)       | Always allowed — no grant needed        |
| File read (outside workspace)  | Once, No                                |
| File write (in workspace)      | Once, Session, Workspace, Always        |
| File write (outside workspace) | Once, No                                |
| Network access                 | Once, No                                |

- **Why this shape:** The scope restrictions are the core security invariant. "Always allow writes outside workspace" and "always allow network" are dangerous — restricting them to "once" prevents users from creating permanent footguns.

### Grant Persistence

- **Once:** Not persisted. Expires immediately after the operation.
- **Session:** In-memory only. Dies with the process.
- **Workspace:** Persisted in `$WORKSPACE/.ur/permissions`.
- **Always:** Persisted in `~/.ur/permissions`.

## Design Decisions

### Scope restrictions on dangerous operations

See [ADR-0005](decisions/adr-0005-scoped-permission-restrictions.md) for full analysis.

- **Choice:** Out-of-workspace writes and network access are restricted to "once" scope only.
- **Rationale:** A user who clicks "always allow network" has effectively disabled the sandbox for all current and future extensions. "Once" forces deliberate approval each time.
- **Consequences:** Extensions that frequently write outside the workspace or use the network will generate many prompts. This is intentional friction.

### Permission prompt as a UI-layer callback

- **Context:** The library can't assume terminal I/O. GUI and IDE frontends need their own prompt UI.
- **Choice:** The permission system defines an interface/callback. The UI layer implements it. See [UI Contract](ui-contract.md).
- **Implementation:** `PermissionRequest(OperationType, Target, RequestingExtension, AllowedScopes)` → `PermissionResponse(Granted, Scope)`. Defined in [`Ur/Permissions/PermissionRequest.cs`](../Ur/Permissions/PermissionRequest.cs) and [`Ur/Permissions/PermissionResponse.cs`](../Ur/Permissions/PermissionResponse.cs).
- **Rationale:** Same pattern as the agent loop's event model. The library asks "should this be allowed?"; the UI layer decides how to present the question and returns the answer.
- **Consequences:** Every UI layer must implement permission prompting. The callback must support the scope options appropriate for the operation type.

## Open Questions

- **Question:** How are persisted grants matched to operations?
  **Context:** A grant for "write to $WORKSPACE/src/" — does it cover `$WORKSPACE/src/foo/bar.cs`? Path prefix matching? Glob patterns?
  **Current thinking:** Path prefix matching for filesystem operations. Exact domain matching for network. Keep it simple in v1.

- **Question:** Can the user revoke a persisted grant?
  **Context:** If a user granted "always allow in-workspace writes" and wants to take it back.
  **Current thinking:** Yes — a settings/management UI should list active grants and allow revocation. But the mechanism is TBD.
