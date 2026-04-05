# Ur — Project State Assessment
_Generated 2026-04-03_

This document catalogs features that are designed but not wired, partially implemented, or only hinted at in the codebase. It is a punch list, not a roadmap.

---

## Critical: Not Wired


---

## Incomplete Features

### Extension Settings Discoverability
- `ExtensionDescriptor` carries a `SettingsSchemas` dictionary (extension-defined JSON schemas)
- Extension settings can be read and written via the generic `ur config get/set` commands
- No way to list what settings an extension supports or their schemas (e.g. `ur extensions settings <id>`)
- The schemas are registered and validated but never surfaced to the user

****### Model List `--all` Flag
- `ur models list --all` is defined and parsed in `ModelCommands.cs`
- The flag is explicitly noted as "reserved for future use" — the library does not yet expose the unfiltered catalog
- `ModelCatalog` only exposes filtered (tool-capable) models publicly via `UrConfiguration.AvailableModels`****

---

## Structural Gaps

### No Logging Infrastructure
- Errors from extension loading are printed to `Console.Error`
- `SettingsLoader.cs` has a TODO to "hook into a logging/warning system" for unknown config keys
- No `ILogger` or logging framework is wired anywhere in the library
- Unknown settings keys are silently tolerated

### Session Metadata
- Sessions only store ID and `CreatedAt`
- No title, summary, or tags
- `ur sessions list` shows raw timestamp-based IDs with no human-readable labels

---

## Test Coverage Gaps

### CLI Commands Untested
- All CLI commands are implemented but have no integration tests
- No tests for `System.CommandLine` argument parsing or error output formatting
- `HostSessionApiTests.cs` covers the underlying library but not the command layer

---

## Minor Gaps and TODOs

| Location | Issue |
|---|---|
| `Ur.csproj` | `Google_GenerativeAI.Microsoft` package referenced but never used in any source file |
| `ModelCatalog.cs` | New `HttpClient` created per `RefreshAsync` call; no retry on network failure |
| `ModelCatalog.cs` | Cache version detected by checking for `Architecture` field — fragile heuristic |
| `ModelCatalog.cs` | `LoadCache()` is synchronous despite being called during async startup |
| `SessionStore.cs` | Malformed trailing lines silently skipped on load (intentional but lossy) |
| `SettingsLoader.cs` | Unknown settings keys silently tolerated; TODO to warn |
| `Workspace.cs` | No validation that the workspace directory exists or is writable at boot |
| `UrHost.cs` | `RegisterCoreSchemas` registers only `ur.model`; other core settings not schema-validated |

---

## What Is Complete

For context, these areas are well-implemented and have solid test coverage:

- **Core agent loop** — streaming, tool call collection, multi-turn continuation, error handling
- **Session persistence** — JSONL append-only store with atomic writes and rollback
- **Extension system** — three-tier discovery, sandboxed Lua evaluation (allowlist: basic, string, table, math), enable/disable/reset lifecycle, workspace-scoped overrides
- **Configuration** — user/workspace scoped settings, JSON schema validation, atomic writes
- **Keyring** — macOS and Linux implementations
- **Model catalog** — OpenRouter fetch, disk cache, modality filtering
- **Permission system** — grant store with three lifetimes (session/workspace/always), prefix matching, TurnCallbacks integration, JSONL persistence
- **All CLI commands** — status, config, models, sessions, extensions, chat (with streaming output and permission prompts)
