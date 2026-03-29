# Tool Registry

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

Maintains the set of tools available to the LLM during the agent loop. Core provides the registry; extensions register tools into it. The agent loop queries the registry to resolve tool calls and to advertise available tools to the LLM.

### Non-Goals

- Does not execute tools — that is the [Agent Loop](agent-loop.md). The registry is a lookup table, not a dispatcher.
- Does not define built-in tools — core tools (if any) are registered the same way extension tools are.
- Does not enforce permissions — tool execution goes through the [Permission System](permission-system.md).

## Context

### Dependencies

| Dependency | What it provides | Interface |
|---|---|---|
| Microsoft.Extensions.AI | `AIFunction` and `AITool` abstractions | Tool type definitions |

### Dependents

| Dependent | What it needs | Interface |
|---|---|---|
| Agent Loop | Tool lookup by name, full tool list for LLM | `Get(name)`, `All()` |
| Extension System | Register/remove tools as extensions load/unload | `Register(tool)`, `Remove(name)` |

## Interface

### Register

- **Purpose:** Add or replace a tool.
- **Inputs:** `AIFunction` (name, description, parameters schema, handler).
- **Postconditions:** The tool is available via `Get` and included in `All`. If a tool with the same name already existed, it is replaced (last-write-wins).

### Remove

- **Purpose:** Remove a tool by name.
- **Inputs:** Tool name.
- **Outputs:** `bool` — whether a tool was removed.

### Get

- **Purpose:** Look up a tool by name.
- **Inputs:** Tool name.
- **Outputs:** `AIFunction` or `null` if not found.

### All

- **Purpose:** Return every registered tool for advertising to the LLM.
- **Outputs:** `IList<AITool>` snapshot of all registered tools.

## Data Structures

### Tool Store

- **Purpose:** Maps tool names to their definitions and handlers.
- **Shape:** `Dictionary<string, AIFunction>` with ordinal string comparison. Implemented in [`Ur/AgentLoop/ToolRegistry.cs`](../Ur/AgentLoop/ToolRegistry.cs).
- **Invariants:** Tool names are unique (enforced by dictionary key). Names are compared with `StringComparer.Ordinal` (case-sensitive).
- **Why this shape:** Flat dictionary is the simplest thing that works. Tools need O(1) lookup by name (the agent loop resolves every LLM tool call through `Get`). No ordering, no grouping, no namespacing — extensions own uniqueness through naming conventions.

## Design Decisions

### Last-write-wins registration (no conflict error)

- **Context:** Two extensions could register tools with the same name.
- **Options considered:** Error on conflict, first-write-wins, last-write-wins.
- **Choice:** Last-write-wins (silent replace).
- **Rationale:** Matches the three-tier extension loading model. A user extension can intentionally override a system extension's tool by registering the same name. An error would prevent this useful pattern. The extension loading order (system → user → workspace) makes last-write-wins predictable.
- **Consequences:** Accidental name collisions are silent. Mitigated by convention (namespace tool names: `git.status`, not `status`).

## Open Questions

- **Question:** Should tool registration support metadata beyond what `AIFunction` provides?
  **Context:** Extensions might want to declare permissions a tool needs (e.g. "this tool writes files"), so the permission system can pre-check before execution. Currently permissions are checked at execution time inside the tool handler.
  **Current thinking:** Not needed in v1. Permission checks at execution time work. If pre-declaration proves valuable (e.g. for UI display of "this tool will ask for write access"), it can be added as optional metadata on registration.
