# Remove Lua Extension System

## Goal

Strip the entire Lua-based extension system from Ur. The extension system adds significant architectural complexity — a discovery pipeline, trust tiers, Lua sandboxing, value marshalling, override persistence, and CLI commands — all for a feature that will be replaced by a simpler Claude Code–inspired hook system. Removing it now simplifies the codebase and clears the path for that future work.

## Desired outcome

- Zero Lua-related code remains in the repo.
- The `LuaCSharp` NuGet dependency is removed.
- `UrHost`, `ToolRegistry`, `PermissionMeta`, `PermissionRequest`, `Workspace`, and DI registration are simplified.
- All existing non-extension tests still pass.
- The project compiles cleanly with no dead references.

## Related code

### Files to delete entirely

- `src/Ur/Extensions/Extension.cs` — Runtime lifecycle state machine for individual extensions
- `src/Ur/Extensions/ExtensionCatalog.cs` — Public API for querying and managing extensions
- `src/Ur/Extensions/ExtensionDescriptor.cs` — Immutable metadata from manifest
- `src/Ur/Extensions/ExtensionId.cs` — Tier-qualified ID struct
- `src/Ur/Extensions/ExtensionInfo.cs` — Public DTO for UI/CLI
- `src/Ur/Extensions/ExtensionLoader.cs` — Discovery and activation pipeline
- `src/Ur/Extensions/ExtensionOverrideStore.cs` — Persistent enablement state (JSON files)
- `src/Ur/Extensions/ExtensionTier.cs` — Enum: System, User, Workspace
- `src/Ur/Extensions/LuaJsonHelpers.cs` — Lua table ↔ JSON serialization
- `src/Ur/Extensions/LuaTableHelpers.cs` — Typed field extraction from Lua tables
- `src/Ur/Extensions/LuaToolAdapter.cs` — AIFunction wrapper for Lua functions
- `src/Ur/Extensions/LuaValueMarshaller.cs` — .NET ↔ Lua value marshalling
- `src/Ur/Extensions/ManifestParser.cs` — Manifest table → descriptor mapping
- `src/Ur.Cli/Commands/ExtensionCommands.cs` — CLI `ur extensions` command group
- `tests/Ur.Tests/ExtensionSystemTests.cs` — All extension-specific tests
- `tests/Ur.Tests/TestData/Extensions/` — Sample extension templates (manifest.lua, main.lua)
- `.ur/extensions/` — Workspace-local test extension directory (if present)

### Files to modify

- `src/Ur/Ur.csproj` — Remove `LuaCSharp` package reference
- `src/Ur/UrHost.cs` — Remove `ExtensionCatalog` constructor param, `Extensions` property, and extension tool registration loop in `BuildSessionToolRegistry`
- `src/Ur/Hosting/ServiceCollectionExtensions.cs` — Remove extension discovery, `ExtensionCatalog` creation, `ExtensionOverrideStore`, `RegisterExtensionSchemas` helper, and extension-related DI registrations
- `src/Ur/Hosting/UrStartupOptions.cs` — Remove `SystemExtensionsPath` and `UserExtensionsPath` properties
- `src/Ur/Tools/ToolRegistry.cs` — Remove `extensionId` parameter from `Register`, `FilteredCopy`, and `MergeInto`
- `src/Ur/Tools/PermissionMeta.cs` — Remove `ExtensionId` field from the record
- `src/Ur/Permissions/PermissionRequest.cs` — Rename `RequestingExtension` to `ToolName` (it already falls back to tool name for non-extension tools; the field name is a vestige)
- `src/Ur/AgentLoop/ToolInvoker.cs` — Simplify `extensionId` resolution at line 259 to just use `call.Name`
- `src/Ur/Workspace.cs` — Remove `ExtensionDirectory` property, `StateHash` property, and extension directory creation in `EnsureDirectories`
- `src/Ur.Cli/Commands/StatusCommand.cs` — Remove extension statistics display (lines 47-49)
- `src/Ox/PermissionHandler.cs` — Update permission prompt string that references `RequestingExtension`
- `src/Ur.Cli/Commands/ChatCommand.cs` — Update permission prompt string that references `RequestingExtension`
- `tests/Ur.Tests/TestSupport/TestEnvironment.cs` — Remove `TempExtensionEnvironment` class entirely or simplify to remove extension paths
- `tests/Ur.Tests/TestSupport/TestHostBuilder.cs` — Remove extension-related setup
- `tests/Ur.Tests/HostSessionApiTests.cs` — Remove extension-specific test cases (the `Extensions_*` tests), keep remaining session API tests
- `tests/Ur.Tests/PermissionMetaTests.cs` — Remove `ExtensionId` from all `PermissionMeta` constructor calls
- `tests/Ur.Tests/BuiltinToolTests.cs` — Replace `TempExtensionEnvironment` usage with simplified test setup
- `tests/Ur.Tests/ParallelToolExecutionTests.cs` — Update `RequestingExtension` reference

## Structural considerations

The extension system's complexity is distributed across several architectural layers:

1. **Discovery & lifecycle** — `ExtensionLoader`, `ExtensionCatalog`, `Extension`, `ExtensionOverrideStore` form a self-contained subsystem. Removing them is clean — nothing else depends on extension discovery.

2. **Tool registration** — The `extensionId` parameter threads through `ToolRegistry.Register` → `PermissionMeta` → `PermissionRequest` → UI prompt. This is the deepest tentacle. After removal, the `extensionId` concept collapses: `PermissionRequest.RequestingExtension` becomes just the tool name (which it already was for non-extension tools). Renaming it to `ToolName` makes the code honest about what it contains.

3. **DI container** — Four singleton registrations (discovered extensions list, `SettingsSchemaRegistry` with extension schemas, `ExtensionOverrideStore`, `ExtensionCatalog`) and their wiring into `UrHost`. These can be deleted with the remaining registrations left intact.

4. **Workspace** — `ExtensionsDirectory` and `StateHash` are extension-only. `StateHash` exists solely for workspace-scoped extension override persistence. Both can be removed.

5. **Settings** — `RegisterExtensionSchemas` injects extension-declared settings into `SettingsSchemaRegistry`. Without extensions, the registry only contains core schemas. The registry itself stays; only the extension registration call goes away.

6. **Test infrastructure** — `TempExtensionEnvironment` is used by extension tests *and* by several non-extension tests (e.g., `BuiltinToolTests`) as a convenient workspace fixture. Those non-extension tests need a simpler replacement that just provides workspace and DI setup without extension paths.

## Implementation plan

### Phase 1: Delete extension source files

- [x] Delete the entire `src/Ur/Extensions/` directory (13 files)
- [x] Delete `src/Ur.Cli/Commands/ExtensionCommands.cs`
- [x] Delete `tests/Ur.Tests/ExtensionSystemTests.cs`
- [x] Delete `tests/Ur.Tests/TestData/Extensions/` directory
- [x] Delete `.ur/extensions/` directory if present in the workspace root
- [x] Remove `LuaCSharp` package reference from `src/Ur/Ur.csproj`

### Phase 2: Simplify UrHost and DI registration

- [x] **`src/Ur/UrHost.cs`**: Remove `ExtensionCatalog` constructor parameter and `Extensions` property. Remove the extension tool registration loop in `BuildSessionToolRegistry` (the `foreach` over `Extensions.GetActiveToolFactories()`). Remove `using Ur.Extensions` import. Update startup log message to drop `extensions={ExtensionCount}`.
- [x] **`src/Ur/Hosting/ServiceCollectionExtensions.cs`**: Remove the `List<Extension>` singleton registration (extension discovery). Remove `ExtensionOverrideStore` and `ExtensionCatalog` singleton registrations. Remove the `RegisterExtensionSchemas` call from `SettingsSchemaRegistry` registration. Delete the `RegisterExtensionSchemas` helper method entirely. Remove `ExtensionCatalog` from `UrHost` construction. Remove `using Ur.Extensions` import.
- [x] **`src/Ur/Hosting/UrStartupOptions.cs`**: Remove `SystemExtensionsPath` and `UserExtensionsPath` properties and their doc comments.

### Phase 3: Simplify ToolRegistry and permissions

- [x] **`src/Ur/Tools/ToolRegistry.cs`**: Remove `extensionId` parameter from `Register` method. Update `FilteredCopy` and `MergeInto` to not pass `extensionId`. Update all internal call sites.
- [x] **`src/Ur/Tools/PermissionMeta.cs`**: Remove `ExtensionId` field from the record. Update the doc comment.
- [x] **`src/Ur/Permissions/PermissionRequest.cs`**: Rename `RequestingExtension` to `ToolName`. This is a semantic rename — the field already contained the tool name for all non-extension tools.
- [x] **`src/Ur/AgentLoop/ToolInvoker.cs`**: At line 259, simplify `var extensionId = meta?.ExtensionId ?? call.Name` to just `call.Name`. Update the `PermissionRequest` constructor call to use the renamed field.
- [x] **`src/Ur.Cli/Commands/ChatCommand.cs`**: Update `req.RequestingExtension` to `req.ToolName` in the permission prompt string.
- [x] **`src/Ox/PermissionHandler.cs`**: Update `req.RequestingExtension` to `req.ToolName` in the permission prompt string.

### Phase 4: Simplify Workspace

- [x] **`src/Ur/Workspace.cs`**: Remove `ExtensionsDirectory` property. Remove `StateHash` property (only used by `ExtensionOverrideStore`). Remove `Directory.CreateDirectory(ExtensionsDirectory)` from `EnsureDirectories`. Remove the doc comment reference to "workspace extensions".

### Phase 5: Update CLI

- [x] **`src/Ur.Cli/Commands/StatusCommand.cs`**: Remove the extension statistics block (lines 47-49 that display "Extensions: N enabled / M total").
- [x] Verify that the `ExtensionCommands` class was the only thing registering the `extensions` subcommand — ensure no dangling command registration exists.

### Phase 6: Fix tests

- [x] **`tests/Ur.Tests/TestSupport/TestEnvironment.cs`**: Remove `TempExtensionEnvironment` entirely (or refactor it into a simpler `TempWorkspaceEnvironment` that just provides workspace + DI without extension paths). Remove `using Ur.Extensions`. Remove all extension-writing helper methods.
- [x] **`tests/Ur.Tests/TestSupport/TestHostBuilder.cs`**: Remove any extension-related setup references.
- [x] **`tests/Ur.Tests/HostSessionApiTests.cs`**: Delete all `Extensions_*` test methods. Keep the remaining session API tests. Update any test setup that referenced extension paths.
- [x] **`tests/Ur.Tests/PermissionMetaTests.cs`**: Remove `ExtensionId: null` from all `PermissionMeta` constructor calls (the field no longer exists).
- [x] **`tests/Ur.Tests/BuiltinToolTests.cs`**: Replace `TempExtensionEnvironment` with the simplified test workspace fixture.
- [x] **`tests/Ur.Tests/ParallelToolExecutionTests.cs`**: Update `req.RequestingExtension` to `req.ToolName`.
- [x] **`tests/Ur.Tests/SettingsLoaderTests.cs`**: Comment about unknown keys is accurate as-is (not extension-specific), left unchanged.
- [x] Update any other test files that fail to compile after the above changes.

### Phase 7: Validate

- [x] Run `dotnet build` across the solution — zero errors, zero warnings related to removed types.
- [x] Run `dotnet test` — all remaining tests pass.
- [x] Grep the entire repo for `Extension`, `Lua`, `NLua`, `LuaCSharp`, `manifest.lua`, `main.lua`, `extension` to confirm no stale references remain (excluding this plan document and any historical plan documents).

## Open questions

- **Test fixture replacement**: `TempExtensionEnvironment` is used by non-extension tests as a convenient way to get a workspace + DI container. Should the replacement be a renamed `TempWorkspaceEnvironment` that preserves the workspace/DI setup but drops extension paths? Or should those tests be refactored to use a different setup pattern? (Recommendation: rename and simplify — the workspace + DI pattern is good, it just shouldn't carry extension baggage.)
