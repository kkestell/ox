# Ur — Project State Assessment
_Generated 2026-04-03_

This document catalogs features that are designed but not wired, partially implemented, or only hinted at in the codebase. It is a punch list, not a roadmap.

---

## Critical: Not Wired

### Permission System
The permission architecture is complete and well-designed, but entirely disconnected from execution.

- `PermissionRequest`, `PermissionResponse`, `PermissionGrant`, `TurnCallbacks`, `PermissionPolicy`, `PermissionScope`, `OperationType` — all defined in `Ur/Permissions/`
- `TurnCallbacks.RequestPermissionAsync` callback exists as a parameter to `UrSession.RunTurnAsync`
- `AgentLoop.cs` never invokes it — the parameter is accepted and ignored
- `Workspace.PermissionsPath` is defined but nothing reads or writes to it
- No permission grant backing store exists
- `ChatCommand.cs` line 26 passes `null` for `TurnCallbacks`, with a comment that permission prompts are not yet wired
- **Effect:** Every tool call is silently permitted with no user oversight

### Error Events
- `AgentLoopEvent.Error` is defined in `AgentLoop/AgentLoopEvent.cs`
- `AgentLoop.cs` catches exceptions but never yields `Error` events
- Consumers receive no notification when a turn fails due to an exception

---

## Incomplete Features

### Session Resume in TUI
- `UrSession` supports loading existing sessions by ID
- The CLI `ur sessions list` / `ur sessions show` work
- `ur chat --session <id>` resumes a session from the CLI
- The TUI has no session picker or resume flow — it always starts a new session
- `ChatApp.cs` constructs a new session on startup with no way to load prior history

### TUI Slash Commands
Only three slash commands are dispatched in `ChatApp.cs`:
- `/quit`
- `/model`
- `/extensions`

The welcome message implies more exist. Missing:
- `/help` — referenced conceptually, not implemented
- `/clear` — no session-clear command
- `/new` or `/new-session` — no explicit new-session command
- `/load <session-id>` or any session navigation

### Extension Settings Configuration
- `ExtensionDescriptor` carries a `SettingsSchemas` dictionary (extension-defined JSON schemas)
- `ExtensionManagerModal.cs` renders extension info (name, version, description, status) but never displays or edits settings schemas
- No UI path to configure extension settings exists anywhere (TUI or CLI)

### Model List `--all` Flag
- `ur models list --all` is defined and parsed in `ModelCommands.cs`
- The flag is explicitly noted as "reserved for future use" — the library does not yet expose the unfiltered catalog
- `ModelCatalog` only exposes filtered (tool-capable) models publicly

---

## Structural Gaps

### No Logging Infrastructure
- Errors from extension loading are printed to `Console.Error`
- `SettingsLoader.cs` has a TODO to "hook into a logging/warning system" for unknown config keys
- No `ILogger` or logging framework is wired anywhere in the library
- Unknown settings keys are silently tolerated

### Windows Support
- `UrHost.CreatePlatformKeyring()` throws `PlatformNotSupportedException` on Windows
- Only `MacOSKeyring` and `LinuxKeyring` are implemented
- No Windows Credential Manager implementation

### Session Metadata
- Sessions only store ID and `CreatedAt`
- No title, summary, or tags
- `ur sessions list` shows raw GUIDs with timestamps, no human-readable labels

---

## Test Coverage Gaps

### TUI Components Untested
- No tests for `ChatInput`, `MessageList`, `ApiKeyModal`, `ModelPickerModal`, `ExtensionManagerModal`
- No tests for the compositor layer (base+overlay rendering)
- No tests for scroll behavior, modal state transitions, or the 30 FPS render loop
- No tests for the slash command dispatcher

### CLI Commands Untested
- All CLI commands are implemented but have no integration tests
- No tests for `System.CommandLine` argument parsing or error output formatting
- `HostSessionApiTests.cs` covers the underlying library but not the command layer

### Permission System Untested
- No tests for permission grant persistence (because there is no persistence)
- No tests for the `TurnCallbacks` integration (because it is not wired)

---

## Minor Gaps and TODOs

| Location | Issue |
|---|---|
| `ModelCatalog.cs` | New `HttpClient` created per `RefreshAsync` call; no retry on network failure |
| `ModelCatalog.cs` | Cache version detected by checking for `Architecture` field — fragile heuristic |
| `SessionStore.cs` | Malformed trailing lines silently skipped on load (intentional but lossy) |
| `SettingsLoader.cs` | Unknown settings keys silently tolerated; TODO to warn |
| `ExtensionLoader.cs` | Sandbox blocks known-dangerous Lua libs but has no explicit allowlist of safe ones |
| `Workspace.cs` | No validation that the workspace directory exists or is writable at boot |
| `UrHost.cs` | `RegisterCoreSchemas` registers only `ur.model`; other core settings not schema-validated |

---

## What Is Complete

For context, these areas are well-implemented and have solid test coverage:

- **Core agent loop** — streaming, tool call collection, multi-turn continuation
- **Session persistence** — JSONL append-only store with atomic writes and rollback
- **Extension system** — three-tier discovery, sandboxed Lua evaluation, enable/disable/reset lifecycle, workspace-scoped overrides
- **Configuration** — user/workspace scoped settings, JSON schema validation, atomic writes
- **Keyring** — macOS and Linux implementations
- **Model catalog** — OpenRouter fetch, disk cache, modality filtering
- **All CLI commands** — status, config, models, sessions, extensions, chat
- **All TUI components** — rendering, input, modals, state management
