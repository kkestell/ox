# Add glob, grep, and bash built-in tools

## Goal

Add three new built-in tools — `glob`, `grep`, and `bash` — that give the LLM the ability to search for files by pattern, search file contents, and execute shell commands. Together with the existing `read_file`, `write_file`, and `update_file`, these complete the core tool set that every modern AI coding assistant converges on.

## Desired outcome

- `glob` finds files by glob pattern within the workspace (auto-allowed, no prompt).
- `grep` searches file contents using ripgrep when available, falling back to .NET regex (auto-allowed, no prompt).
- `bash` executes shell commands with timeout and output truncation (requires per-invocation user approval via a new `ExecuteCommand` permission type).
- All three tools follow the same `AIFunction` pattern as the existing file tools, are registered in `BuiltinTools.RegisterAll`, and have comprehensive test coverage.

## How we got here

The user asked for search, grep, and command execution tools. A review of [tools-comparison.md](/home/kyle/src/research/analysis/tools-comparison.md) confirmed that every major harness (Cline, OpenCode, Roo-Code, Continue) provides these three categories. Key design decisions:

- **Permission model**: `ExecuteCommand` as a new `OperationType` with `Once`-only scope, rather than reusing `WriteInWorkspace` (which would let blanket grants approve arbitrary commands) or building a complex classification system (YAGNI).
- **Tool naming**: `glob` / `grep` / `bash` — terse names following OpenCode conventions, user preference.
- **Grep strategy**: Prefer ripgrep for performance, fall back to .NET for portability. The performance difference matters for content search across large repos; it doesn't matter for glob (pure filesystem enumeration).

## Related code

- `Ur/Tools/ReadFileTool.cs` — Template for new tools: AIFunction subclass, JSON schema, workspace boundary check, path resolution
- `Ur/Tools/BuiltinTools.cs` — Registration point; new tools added here
- `Ur/Tools/ToolArgHelpers.cs` — Shared argument extraction; may need `GetOptionalString` added
- `Ur/AgentLoop/ToolRegistry.cs` — Registry API (no changes needed)
- `Ur/AgentLoop/AgentLoop.cs` — Invocation loop (no changes needed)
- `Ur/Permissions/OperationType.cs` — Add `ExecuteCommand` variant
- `Ur/Permissions/PermissionPolicy.cs` — Add `ExecuteCommand` scope/prompt rules
- `Ur/Workspace.cs` — `Contains()` for boundary checks, `RootPath` for working directory
- `Ur.Tests/BuiltinToolTests.cs` — Test patterns to follow

## Current state

- Three built-in tools exist (`read_file`, `write_file`, `update_file`), all following the same pattern: `AIFunction` subclass with a static JSON schema, workspace-scoped, registered in `BuiltinTools.RegisterAll` with `PermissionMeta`.
- `ToolArgHelpers` handles `string` (tests) and `JsonElement` (LLM) transparently for required strings and optional ints. No `GetOptionalString` helper yet.
- `OperationType` has five variants; `PermissionPolicy` maps each to allowed scopes and whether a prompt is required.
- No external process execution exists anywhere in the codebase today.

## Structural considerations

**Hierarchy**: All three tools sit in `Ur/Tools/` alongside the existing file tools. They use the same `AIFunction` base, same `Workspace` dependency, same registration path. No new layers introduced.

**Abstraction**: `glob` and `grep` are read operations at the same level as `read_file`. `bash` is a fundamentally different capability (arbitrary execution) but the same abstraction (AIFunction with schema → invoke → string result) fits cleanly. The permission system already handles the risk differentiation.

**Modularization**: Each tool is a single file. `grep` has a dual-backend (rg/fallback) but that complexity is internal to the class — no new interfaces or strategy patterns needed. A private method per backend is sufficient.

**Encapsulation**: `bash` needs `System.Diagnostics.Process`, which is new to the tools layer but doesn't leak. The workspace root becomes the working directory; timeout and output limits are tool-internal concerns.

**NuGet dependency**: `glob` needs `Microsoft.Extensions.FileSystemGlobbing` for proper `**` support. This is a lightweight, zero-transitive-dependency package from Microsoft — appropriate for a .NET project already using `Microsoft.Extensions.AI`.

## Implementation plan

### Permission system changes

- [x] Add `ExecuteCommand` to `OperationType` enum in `Ur/Permissions/OperationType.cs`
- [x] Add `ExecuteCommand` case to `PermissionPolicy.AllowedScopes` → returns `[PermissionScope.Once]`
- [x] Add `ExecuteCommand` case to `PermissionPolicy.RequiresPrompt` → returns `true` (or leave the existing `!= ReadInWorkspace` logic, which already handles this correctly since `ExecuteCommand != ReadInWorkspace`)

### Helper additions

- [x] Add `GetOptionalString` to `ToolArgHelpers.cs` — same pattern as `GetOptionalInt` but returns `string?`. Needed for optional `path` and `include` parameters on glob/grep.

### glob tool

- [x] Add `Microsoft.Extensions.FileSystemGlobbing` package to `Ur/Ur.csproj`
- [x] Create `Ur/Tools/GlobTool.cs`:
  - Name: `glob`
  - Description: "Find files by glob pattern within the workspace."
  - Schema: `pattern` (required string, e.g. `**/*.cs`), `path` (optional string, subdirectory to scope the search, defaults to workspace root)
  - Implementation:
    1. Resolve `path` against workspace root (same `ResolvePath` logic as ReadFileTool)
    2. Workspace boundary check on resolved path
    3. Use `Microsoft.Extensions.FileSystemGlobbing.Matcher` to match `pattern` against the directory
    4. Return matching paths as newline-separated list, relative to workspace root
    5. Truncate output if matches exceed a limit (e.g. 1000 files), with a `[truncated]` notice
  - Permission: `ReadInWorkspace` (auto-allowed)

### grep tool

- [x] Create `Ur/Tools/GrepTool.cs`:
  - Name: `grep`
  - Description: "Search file contents by regex pattern. Uses ripgrep if available, otherwise falls back to .NET regex."
  - Schema: `pattern` (required string — regex), `path` (optional string — subdirectory to scope), `include` (optional string — glob filter, e.g. `*.cs`), `context_lines` (optional int — lines of context before/after, default 0)
  - Implementation — ripgrep path:
    1. On first invocation, check if `rg` is on PATH (cache the result in a static field)
    2. If available: spawn `rg` with `--line-number`, `--no-heading`, `--color never`, `--context {n}`, `--glob {include}`, pattern, search_path
    3. Capture stdout, enforce timeout (30s), truncate output at ~100KB or ~2000 lines
    4. Return raw rg output (already in `file:line:content` format)
  - Implementation — .NET fallback:
    1. Enumerate files in the search path (optionally filtered by `include` glob)
    2. For each file, read lines, apply `Regex.IsMatch` to each line
    3. Collect matches in `file:line:content` format
    4. Same truncation limits
  - Workspace boundary check on the resolved search path
  - Permission: `ReadInWorkspace` (auto-allowed)

### bash tool

- [x] Create `Ur/Tools/BashTool.cs`:
  - Name: `bash`
  - Description: "Execute a shell command in the workspace directory."
  - Schema: `command` (required string), `timeout_ms` (optional int, default 120000 — 2 minutes)
  - Implementation:
    1. Create `ProcessStartInfo` for `/bin/bash` with arguments `-c {command}`
    2. Set `WorkingDirectory` to workspace root
    3. Redirect stdout and stderr
    4. Start process, read output with a combined timeout
    5. If process exceeds timeout, kill it and return partial output + timeout notice
    6. Truncate combined output at ~100KB or ~2000 lines, with `[truncated]` notice
    7. Return formatted result: exit code + stdout + stderr (if non-empty)
  - Permission: `ExecuteCommand` with target extractor that shows the command string
  - Edge cases: handle processes that produce no output, processes that write to stderr only, zombie processes

### Registration

- [x] Update `BuiltinTools.RegisterAll` in `Ur/Tools/BuiltinTools.cs` to register all three new tools:
  - `glob` → `ReadInWorkspace`, target extractor pulls `pattern`
  - `grep` → `ReadInWorkspace`, target extractor pulls `pattern`
  - `bash` → `ExecuteCommand`, target extractor pulls `command`

### Tests

- [x] Add glob tests to `Ur.Tests/BuiltinToolTests.cs`:
  - Matches files by pattern
  - Respects subdirectory path scoping
  - Returns paths relative to workspace root
  - Rejects path outside workspace
  - Returns empty result for no matches (not an error)
  - Truncates large result sets

- [x] Add grep tests to `Ur.Tests/BuiltinToolTests.cs`:
  - Finds matching lines in files
  - Respects include filter
  - Returns line numbers in output
  - Rejects path outside workspace
  - Returns empty result for no matches (not an error)
  - Handles context lines

- [x] Add bash tests to `Ur.Tests/BuiltinToolTests.cs`:
  - Executes simple command and returns output
  - Returns exit code
  - Captures stderr
  - Times out long-running commands (use a short timeout in test)
  - Truncates large output
  - Sets working directory to workspace root

- [x] Add registration test: verify all six tools appear after `RegisterAll`

- [x] Run full test suite: `dotnet test` from repo root

## Validation

- **Tests**: `dotnet test` — all existing tests must still pass; new tests cover each tool's happy path, boundary checks, error cases, and truncation behavior
- **Manual verification**: start the CLI, use each tool in conversation:
  - `glob` to find `.cs` files
  - `grep` to search for a known string
  - `bash` to run `git status` (should prompt for permission)
- **Build**: `dotnet build` with no warnings

## Open questions

- Should `glob` exclude hidden files/directories (e.g. `.git/`, `.ur/`) by default, or leave that to the caller? Excluding `.git/` is probably sensible since it's never useful to the LLM — but this can also be a follow-up.
- Should `bash` work on Windows (using `cmd.exe` or `pwsh`)? The current design assumes `/bin/bash`. Cross-platform support can be a follow-up if needed.
