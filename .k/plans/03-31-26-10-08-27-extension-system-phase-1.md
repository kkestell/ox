# Extension System — Phase 1: Discovery, Loading, and Tool Registration

## Goal

Implement the core extension system infrastructure: discover Lua extensions from the three-tier directory structure, evaluate manifests, create sandboxed Lua runtimes, and let extensions register tools that the agent loop can invoke. This is the foundation that middleware, permissions, and richer Lua APIs will build on.

## Desired outcome

After this work:
- Ur discovers extension directories under `~/.ur/extensions/system/`, `~/.ur/extensions/user/`, and `$WORKSPACE/.ur/extensions/`.
- Each extension has a `manifest.lua` that declares metadata and settings schemas.
- Each enabled extension's `main.lua` runs in an isolated, locked-down `LuaState`.
- Extensions can call `ur.tool.register(...)` from Lua to register tools in the global `ToolRegistry`.
- System/user extensions are enabled by default; workspace extensions are disabled by default.
- Enable/disable toggles add/remove an extension's tools from the registry.
- Extension settings schemas are validated alongside core schemas at startup.
- A sample extension (e.g., a simple string-processing tool) demonstrates the full round-trip.

## How we got here

The extension system is described in detail in `docs/extension-system.md` and several ADRs, but has no runtime implementation beyond metadata types (`ExtensionInfo`, `ExtensionTier`).

Three scoping decisions narrow this plan:
1. **Manifest format: `manifest.lua`** — evaluated in a minimal sandbox (no I/O). Keeps the toolchain homogeneous with the rest of the extension.
2. **Middleware: deferred** — the C# middleware pipeline doesn't exist in `AgentLoop` yet. Building it is a separate plan.
3. **Permissions: deferred** — extensions run in a fully locked-down sandbox. Tool handlers can do pure computation only. Filesystem/network/exec APIs come when the permission system is wired in.

## Related code

- `Ur/Extensions/ExtensionInfo.cs` — Current metadata-only type. Will be replaced/expanded.
- `Ur/Extensions/ExtensionTier.cs` — Tier enum. Stays as-is.
- `Ur/UrHost.cs` — Startup orchestration. Line 96 has `// TODO: Load extensions (metadata/schemas only)`. Two-phase loading wires in here.
- `Ur/AgentLoop/ToolRegistry.cs` — Where extension tools are registered. Interface is ready (`Register`, `Remove`, `Get`, `All`).
- `Ur/Configuration/SettingsSchemaRegistry.cs` — Where extension settings schemas are registered. Interface is ready (`Register`, `TryGetSchema`).
- `Ur/Configuration/Settings.cs` — Read-only after load. Extensions may read settings via `ur.settings.get` (future, not this plan).
- `Ur/Configuration/SettingsLoader.cs` — Orchestrates merge/validate. No changes needed if schemas are registered before `loader.Load()`.
- `Ur/Workspace.cs` — Provides `ExtensionsDirectory` for workspace extensions. Works as-is.
- `Ur/Ur.csproj` — Needs `LuaCSharp` package reference.

## Current state

- **Existing types:** `ExtensionInfo` (metadata record), `ExtensionTier` (enum). No behavior.
- **ToolRegistry:** Fully functional. `Register(AIFunction)`, `Remove(name)`, `Get(name)`, `All()`. Last-write-wins on name collision.
- **SettingsSchemaRegistry:** Fully functional. `Register(key, JsonElement)` with duplicate-key error.
- **UrHost.Start:** Linear startup: workspace → model catalog → schemas → settings → sessions. The TODO at line 96 marks where extension loading belongs.
- **Workspace:** `ExtensionsDirectory` points to `$WORKSPACE/.ur/extensions/`. `EnsureDirectories` creates it.
- **No Lua dependency:** `Ur.csproj` has no reference to `LuaCSharp` yet.
- **AIFunction:** Abstract class. Subclass it, override `InvokeCoreAsync(AIFunctionArguments, CancellationToken) → ValueTask<object?>`. Properties: `Name`, `Description`, `JsonSchema` (string, JSON Schema for parameters).

## Structural considerations

### Hierarchy

The extension system sits between `UrHost` (which orchestrates startup and holds the extension collection) and `ToolRegistry` (which receives tool registrations). Extensions do not depend on the agent loop — they register tools into the registry, and the agent loop consumes the registry independently. This keeps the dependency direction clean: `UrHost → ExtensionSystem → ToolRegistry ← AgentLoop`.

### Abstraction

The rest of the system should not know about Lua. `UrHost` sees extensions as objects that contribute tools and schemas. The agent loop sees `AIFunction` instances in the `ToolRegistry`. The Lua runtime, LuaPlatform sandboxing, and Lua↔C# marshalling are encapsulated within the extension module.

### Modularization

The natural module boundary is the `Ur/Extensions/` directory. It currently has two files (`ExtensionInfo.cs`, `ExtensionTier.cs`). The new types all belong here. No other modules need new files — only `UrHost.cs` and `Ur.csproj` need modification outside this directory.

### Encapsulation

- `LuaState` never leaks outside `Ur/Extensions/`.
- The Lua API surface (`ur.tool.register`) is an internal implementation detail.
- `Extension` exposes metadata and enable/disable; its Lua internals are private.
- `LuaToolAdapter` (the `AIFunction` subclass) is internal — consumers see it as `AIFunction`.

## Research

### Lua-CSharp (NuGet: `LuaCSharp`, v0.5.3)

- **AoT safe.** No IL generation, no reflection emit. Source generators for `[LuaObject]` bindings.
- **Sandboxing via `LuaPlatform`:** Constructor takes `FileSystem`, `OsEnvironment`, `StandardIO`, `TimeProvider`. Pass restricted/null implementations to lock down an extension. For manifest evaluation: no filesystem, no OS, no I/O. For main script execution: same (until permission system lands).
- **Per-state isolation:** Each `LuaState` is independent, not thread-safe. One state per extension.
- **Async execution:** `DoStringAsync`, `DoFileAsync`, `RunAsync` are all async.
- **Exposing C# to Lua:** Set `state.Environment["name"] = new LuaFunction(...)` for callbacks. Or use `[LuaObject]` source generator for richer type bindings.
- **Calling Lua from C#:** `state.DoFileAsync(path)` returns `LuaValue[]`. Tables accessible via `result.Read<LuaTable>()`.
- **Data marshalling:** `LuaValue` discriminated union struct. Implicit conversions from C# primitives. `LuaTable` for tables. `context.GetArgument<T>(index)` in function callbacks.
- **Gotchas:** UTF-16 strings (non-standard). Not thread-safe. Incomplete stdlib.

### Verified LuaCSharp API details (discovered during implementation)

These correct/expand the research above based on actual reflection of the v0.5.3 DLL:

- **Namespaces:**
  - `Lua` — core types: `LuaState`, `LuaTable`, `LuaValue`, `LuaFunction`, `LuaFunctionExecutionContext`
  - `Lua.IO` — `ILuaFileSystem`, `ILuaStandardIO`, `ILuaStream`, `LuaFileOpenMode`
  - `Lua.Platforms` — `LuaPlatform` (record), `ILuaOsEnvironment`
  - `Lua.Standard` — `OpenLibsExtensions` (extension methods: `OpenBasicLibrary`, `OpenStringLibrary`, etc.)
- **Exception types (all in `Lua` namespace, all extend `System.Exception` directly):**
  - `LuaRuntimeException`, `LuaAssertionException` (extends `LuaRuntimeException`), `LuaParseException`, `LuaCompileException`, `LuaModuleNotFoundException`, `LuaUndumpException`, `LuaCanceledException` (extends `OperationCanceledException`)
  - **There is no base `LuaException` class.** The plan's original code used `LuaException` which doesn't exist.
  - `LuaRuntimeException` constructor signature needs verification — it does NOT take a single `string` argument. Check actual constructors before using `throw new LuaRuntimeException(msg)`.
- **`ILuaFileSystem` (in `Lua.IO`):** Async methods — `Open`, `Rename`, `Remove`, `OpenTempFileStream` all take `CancellationToken` and return `ValueTask`. Also has `string DirectorySeparator { get; }`.
- **`ILuaOsEnvironment` (in `Lua.Platforms`):** `Exit` returns `ValueTask` (async). `GetTotalProcessorTime` returns `double` (not `TimeSpan`). `GetEnvironmentVariable` returns `string` (not `string?`).
- **`ILuaStream` (in `Lua.IO`):** High-level async interface — `ReadAllAsync`, `ReadLineAsync`, `WriteAsync(string)`, `FlushAsync`, `CloseAsync`, etc. Has static factories: `CreateFromString`, `CreateFromStream`, `CreateFromMemory`.
- **`LuaFunction` constructor:** `new LuaFunction(string name, Func<LuaFunctionExecutionContext, CancellationToken, ValueTask<int>> func)` — the int return value is the number of Lua return values pushed via `context.Return(...)`.
- **`LuaState.RunAsync` signature:** Needs verification — the exact parameter types for passing arguments to a LuaFunction need to be checked.

### AIFunction subclassing

`AIFunction` (in `Microsoft.Extensions.AI.Abstractions` v10.4.1) inherits `AIFunctionDeclaration` inherits `AITool`:
- **`JsonSchema` is `JsonElement`** (not `string` as originally written below). Defined on `AIFunctionDeclaration`.
- Only abstract member: `protected abstract ValueTask<object?> InvokeCoreAsync(AIFunctionArguments, CancellationToken)`
- Virtual overridable: `Name`, `Description` (from `AITool`), `JsonSchema`, `ReturnJsonSchema` (from `AIFunctionDeclaration`)
- Parameterless protected constructor.
- `AIFunctionArguments` implements `IDictionary<string, object?>` — iterate with foreach.

## Refactoring

### Replace `ExtensionInfo` with `Extension`

`ExtensionInfo` is a metadata-only record. The new `Extension` class subsumes it — it holds the same metadata plus the LuaState, registered tools, and lifecycle behavior. `ExtensionInfo` is deleted; any code referencing it uses `Extension` instead (currently nothing references it beyond the type definition).

## Implementation plan

### 1. Add Lua-CSharp dependency

- [x] Add `<PackageReference Include="LuaCSharp" Version="0.5.3" />` to `Ur/Ur.csproj`.
- [x] Verify the project builds (`dotnet build`). AoT publish not yet tested.

### 2. Define the Extension type

- [x] Deleted `Ur/Extensions/ExtensionInfo.cs`.
- [x] Created `Ur/Extensions/Extension.cs` — internal constructor (not private), all metadata + mutable state + Enable/Disable/RegisterTool as planned.

### 3. Create `LuaToolAdapter` (AIFunction subclass)

- [x] Created `Ur/Extensions/LuaToolAdapter.cs`. Note: `JsonSchema` is `JsonElement` (not `string` as the research section stated). Includes full marshalling: C# → Lua (primitives, JsonElement, nested objects/arrays) and Lua → C# (string, double, bool, table → JSON string).

### 4–9. ExtensionLoader, ur.tool.register, phase 2 init, UrHost wiring, enable/disable, Lua→JSON

- [x] Created `Ur/Extensions/ExtensionLoader.cs` — contains all of tasks 4–6 and 9 in a single file:
  - `DiscoverAllAsync` (three-tier discovery, manifest eval, name-collision rule)
  - `InitializeAsync` (phase 2: sandboxed LuaState, `ur.tool.register` injection, main.lua execution, auto-enable for system/user)
  - `LuaTableToJsonElement` (generic Lua table → JsonElement conversion)
  - Sandboxed platform implementations (`NoOpFileSystem`, `NoOpOsEnvironment`, `NoOpStandardIO`)
  - Only safe Lua stdlib opened: basic, string, table, math (no IO, OS, module, debug)
- [x] Enable/disable already implemented on `Extension` class (task 8).
- [x] **Wire into host startup** — implemented as `UrHost.StartAsync` end-to-end with no sync shim. Startup now discovers extensions before settings load, registers extension schemas, initializes runtimes after settings load, and stores the loaded extension list on `UrHost`.

**BUILD STATUS: Green.** `dotnet build` succeeds after fixing the remaining Lua-CSharp API mismatches:

1. **Sandbox exceptions** — disallowed filesystem/OS operations now throw `InvalidOperationException` from the host-side sandbox implementations instead of trying to construct `LuaRuntimeException` directly.

2. **Lua tool invocation** — `LuaToolAdapter.InvokeCoreAsync` now uses `LuaStateExtensions.CallAsync(..., ReadOnlySpan<LuaValue>, ...)`, which matches the actual v0.5.3 API.

3. **Host-side script loading** — `manifest.lua` and `main.lua` are now read by C# and evaluated via `DoStringAsync(...)`, preserving the Lua sandbox while still allowing the host to load extension entry files.

### 10. Fix remaining build errors

- [x] Fix `LuaRuntimeException` constructor usage in `NoOp*` sandbox classes by switching to host-thrown `InvalidOperationException`.
- [x] Fix Lua tool invocation by switching from `RunAsync` to the correct `CallAsync` API.
- [x] Verify full solution builds with `dotnet build`.

### 11. Wire into `UrHost.StartAsync`

- [x] Make host startup async as `UrHost.StartAsync`.
- [x] Insert extension discovery between schema registration and settings loading.
- [x] Insert phase 2 initialization after settings load.
- [x] Store the loaded extensions list on `UrHost`.

### 12. Tests

- [x] **Discovery tests** (`Ur.Tests/ExtensionSystemTests.cs`):
  - Discovers extensions from a temp directory structure.
  - Respects three-tier ordering (system → user → workspace).
  - Skips directories without `manifest.lua`.
  - Skips extensions with malformed manifest (Lua parse error, missing name/version).
  - Higher-tier wins on name collision.
- [x] **Manifest evaluation tests:**
  - Parses name, version, description.
  - Extracts settings schemas.
  - Manifest cannot access filesystem or OS.
- [x] **Tool registration tests:**
  - `ur.tool.register` from Lua creates an `AIFunction` in the extension's tool list.
  - Registered tool appears in `ToolRegistry` when extension is enabled.
  - Tool invocation marshals arguments and returns result.
- [x] **Enable/disable tests:**
  - Disabling removes tools from registry.
  - Re-enabling adds them back.
  - Workspace extensions start disabled.
- [x] **Integration test:**
  - End-to-end: create a temp workspace with a sample extension, start UrHost, verify the tool is registered and invocable.

### 13. Sample extension for validation

- [x] Create a sample extension in test data (`Ur.Tests/TestData/Extensions/sample-echo/`) and use it from the extension-system tests:
  ```
  sample-ext/
    manifest.lua
    main.lua
  ```
  - `manifest.lua`: declares name, version, one setting.
  - `main.lua`: registers a simple tool (e.g., `sample.echo` that returns its input).

## Impact assessment

- **Code paths affected:** `UrHost.StartAsync` (startup sequence), `Ur/Extensions/` (new code), CLI/TUI startup call sites, and host/session tests.
- **Dependency impact:** New NuGet dependency: `LuaCSharp`. Native AOT publish succeeds locally; remaining follow-up is the existing `System.Text.Json` trim/AOT warnings.
- **API impact:** `ExtensionInfo` is deleted. `UrHost.Start` was replaced with `UrHost.StartAsync`, and `UrHost` now exposes the loaded `Extensions` collection.

## Validation

- **Tests:** Implemented in `Ur.Tests/ExtensionSystemTests.cs` for discovery, manifest parsing, sandboxing, tool registration, workspace-default disablement, and host startup integration. `dotnet test` passes.
- **Build:** `dotnet build` succeeds.
- **AoT:** Verified locally by publishing both CLI and TUI with Native AOT:
  - `dotnet publish Ur.Cli/Ur.Cli.csproj -c Release -r osx-arm64 -p:PublishAot=true`
  - `dotnet publish Ur.Tui/Ur.Tui.csproj -c Release -r osx-arm64 -p:PublishAot=true`
  Both succeeded. Remaining warnings are pre-existing `System.Text.Json` trim/AOT warnings in configuration and session serialization code, not extension-system failures.
- **Manual verification:** Create a sample extension in `~/.ur/extensions/user/`, start Ur, verify the tool appears in the tool list.

## Gaps and follow-up

These are capabilities the docs describe that this plan cannot deliver. Each should become its own plan.

### Middleware pipeline

The agent loop has no middleware infrastructure. Extensions cannot hook into pre-LLM or post-tool phases. This is the second most important extension capability after tools.

**Scope:** Design and build the C# middleware pipeline in `AgentLoop`. Define the Lua middleware API (`ur.middleware.add`). Wire extension-registered middleware into the agent loop.

### Permission system integration

Extensions run in a fully locked-down sandbox — no filesystem, no network, no shell. Tool handlers can only do pure computation. To be useful, extensions need permission-gated APIs (`ur.fs.read`, `ur.fs.write`, `ur.exec`, `ur.net.fetch`) that check with the permission system before executing.

**Scope:** Build the runtime permission checker. Implement custom `LuaPlatform` components that route through the permission system. Design and expose the Lua API for gated operations.

### Extension management UI

No way for users to list, enable, disable, or inspect extensions from the UI. The `Enable`/`Disable` methods exist in the library but no UI exposes them.

**Scope:** TUI commands or modal for extension management. Workspace extension opt-in flow.

## Open questions

- **Lua table → JSON Schema fidelity:** How complete does the conversion need to be? Full JSON Schema spec is vast. Starting with `type`, `properties`, `required`, `description`, `items`, and `enum` should cover tool parameter schemas. Can expand later.
- **Extension error reporting:** When an extension fails to load, should UrHost expose this to the UI (e.g., via a warnings list), or is logging sufficient? Leaning toward a warnings list so the TUI can display it.
- **`ur.log` API:** Should this plan include a minimal `ur.log(message)` API so extension authors can debug during development? It's trivial to add (write to stderr or a log file) and very useful. Not in scope per the current plan, but could be added cheaply.
