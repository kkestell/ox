# Built-in file tools (read_file, write_file, update_file)

## Goal

Add three built-in tools implemented in C# (not via the Lua extension system) that give the LLM the ability to read, write, and update files within the workspace. These tools integrate with the existing permission system and tool registry.

## Desired outcome

- `read_file` — reads a file and returns its contents, truncating with a message if the output exceeds a line limit.
- `write_file` — creates or overwrites a file with the provided content.
- `update_file` — performs a single find-and-replace within a file. Errors if the old string is not found or appears more than once.
- All three tools are workspace-scoped: they reject paths outside the workspace boundary.
- All three tools are registered into the existing `ToolRegistry` with correct `PermissionMeta` so the permission system works without changes.
- The LLM sees these tools automatically on every turn — no extension enablement required.

## Related code

- `Ur/AgentLoop/ToolRegistry.cs` — central registry; built-in tools register here alongside extension tools
- `Ur/AgentLoop/AgentLoop.cs` — drives tool execution; no changes needed (tools are just AIFunctions)
- `Ur/AgentLoop/PermissionMeta.cs` — permission metadata record attached to tools at registration
- `Ur/Extensions/LuaToolAdapter.cs` — existing AIFunction subclass pattern to follow for the new tool classes
- `Ur/Permissions/PermissionPolicy.cs` — defines prompt behavior per OperationType (ReadInWorkspace is auto-allowed, WriteInWorkspace prompts)
- `Ur/Workspace.cs` — `Contains(path)` method used to enforce workspace boundary
- `Ur/UrHost.cs` — startup sequence; built-in tool registration goes here after ToolRegistry creation

## Current state

- Tools are only created through the Lua extension system today (`LuaToolAdapter : AIFunction`).
- The `ToolRegistry` is generic — it accepts any `AIFunction` and has no Lua dependency.
- `ToolRegistry.Register(AIFunction, OperationType, extensionId?, targetExtractor?)` already supports attaching permission metadata.
- `Workspace.Contains(path)` already exists for boundary checking.
- There is no concept of "built-in tools" yet — this plan introduces the first ones.

## Structural considerations

**Hierarchy** — Built-in tools sit at the same level as extension tools in the ToolRegistry. They are peers, not a separate layer. The AgentLoop doesn't need to know the difference.

**Modularization** — A new `Ur/Tools/` directory keeps built-in tool implementations separate from the extension system (`Ur/Extensions/`) and the agent loop (`Ur/AgentLoop/`). Each tool is its own class. A static registration helper ties them together.

**Encapsulation** — Tools receive a `Workspace` reference to enforce the workspace boundary. They don't need access to the permission system directly — that's handled by AgentLoop before invocation.

**Abstraction** — Each tool subclasses `AIFunction` directly (same pattern as `LuaToolAdapter`). No intermediate abstraction is needed for three tools.

## Implementation plan

- [ ] **Create `Ur/Tools/ReadFileTool.cs`** — `AIFunction` subclass. Parameters: `file_path` (string, required), `offset` (integer, optional, default 0 — zero-based line to start from), `limit` (integer, optional, default 2000 — max lines to return). Define `private const int DefaultLimit = 2000;` at the top of the class. Behavior: resolve path, validate it's inside the workspace, read the file, apply offset/limit, append a `[truncated: showing lines {offset+1}-{offset+returned} of {total} lines]` message when the output doesn't cover the entire file. Return file content as a string. JSON schema defined as a static `JsonElement` parsed once.

- [ ] **Create `Ur/Tools/WriteFileTool.cs`** — `AIFunction` subclass. Parameters: `file_path` (string, required), `content` (string, required). Behavior: resolve path, validate workspace boundary, create parent directories if needed, write content to file. Return a confirmation message like `"Wrote N bytes to path"`.

- [ ] **Create `Ur/Tools/UpdateFileTool.cs`** — `AIFunction` subclass. Parameters: `file_path` (string, required), `old_string` (string, required), `new_string` (string, required). Behavior: resolve path, validate workspace boundary, read file, count occurrences of `old_string`. If count == 0, throw with "old_string not found in file". If count > 1, throw with "old_string appears N times; must be unique". If count == 1, replace and write back. Return a confirmation message.

- [ ] **Create `Ur/Tools/BuiltinTools.cs`** — static class with `RegisterAll(ToolRegistry registry, Workspace workspace)`. Instantiates all three tools and registers them with appropriate permission metadata:
  - `read_file`: `OperationType.ReadInWorkspace`, target extractor returns `file_path` argument
  - `write_file`: `OperationType.WriteInWorkspace`, target extractor returns `file_path` argument
  - `update_file`: `OperationType.WriteInWorkspace`, target extractor returns `file_path` argument

- [ ] **Wire registration into `UrHost.StartAsync`** — call `BuiltinTools.RegisterAll(tools, workspace)` after the workspace is created and before extensions are loaded (so extensions can't shadow built-in tool names, though name collision handling is a future concern).

- [ ] **Add unit tests in `Ur.Tests/BuiltinToolTests.cs`** — test each tool in isolation:
  - `read_file`: reads a file, truncates long files, rejects paths outside workspace, handles missing files
  - `write_file`: writes content, creates directories, rejects paths outside workspace
  - `update_file`: replaces unique match, errors on zero matches, errors on multiple matches, rejects paths outside workspace
  - Verify tools appear in `ToolRegistry.All()` after `UrHost.StartAsync`

- [ ] **Verify build and all tests pass** — `dotnet build` and `dotnet test`

## Validation

- **Tests**: Unit tests for each tool's happy path and error cases. Integration test that built-in tools appear in the registry after host startup.
- **Build**: `dotnet build Ur.slnx` must succeed.
- **Test suite**: `dotnet test Ur.slnx` must pass.
- **Manual verification**: Start a chat session, confirm the LLM can see and invoke `read_file`, `write_file`, and `update_file`.

## Resolved decisions

- `read_file` accepts optional `offset` and `limit` parameters for reading specific line ranges.
- The default limit (2000) is a `const` at the top of `ReadFileTool.cs`, not configurable via settings for now.
