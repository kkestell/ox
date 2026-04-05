# Separate operation type from workspace containment

## Goal

Decouple `OperationType` from workspace boundaries. A tool declares *what kind* of operation it performs (read, write, execute) — nothing more. Whether a specific invocation targets something inside or outside the workspace is a sandbox/permission concern, determined at invocation time by the permission layer, not by the tool itself.

## Desired outcome

- `OperationType` is a simple three-value enum: `Read`, `Write`, `Execute`.
- Tools have no workspace containment checks. They operate on whatever path they're given.
- The permission layer (in `ToolInvoker`) resolves the target path, checks workspace containment, and uses both the operation kind and the location to determine whether a prompt is needed.
- The same behavioral matrix is preserved: reads in-workspace are auto-allowed, reads outside-workspace prompt, writes prompt with full scope options, execute always prompts.

## How we got here

The current `OperationType` enum (`ReadInWorkspace`, `ReadOutsideWorkspace`, `WriteInWorkspace`, `ExecuteCommand`) conflates two orthogonal concerns: what the operation does and where it targets. This means:

1. Tools declare a location-aware operation type at registration time, but the actual location depends on the arguments at invocation time.
2. Tools also enforce `workspace.Contains()` internally, duplicating a boundary check that should live in one place.
3. `ReadOutsideWorkspace` exists as an enum value but no built-in tool is ever registered with it — the tools just throw instead.

The user's mental model is clear: operation type is "read / write / execute." The workspace boundary is a policy input, not a tool property.

## Approaches considered

### Option A — Simplify enum, move containment to ToolInvoker

- Summary: Reduce `OperationType` to `Read | Write | Execute`. Remove `workspace.Contains()` from all tools. `ToolInvoker` gains a `Workspace` dependency, resolves the target to an absolute path, checks containment, and passes `(operationType, isInWorkspace)` to `PermissionPolicy`.
- Pros: Minimal new abstractions. Tools become simpler. Single enforcement point.
- Cons: `ToolInvoker` needs to understand path resolution (currently tool-specific). `AgentLoop` constructor gains a `Workspace` parameter.
- Failure modes: Target resolution in the invoker might not match what the tool actually does (e.g., glob's `path` argument vs. read_file's `file_path`). Mitigated by the existing `TargetExtractor` which already knows which argument key to read.

### Option B — Introduce a `Sandbox` abstraction

- Summary: Create a `Sandbox` class that encapsulates workspace, permission policy, and grant store. The invoker delegates all permission decisions to it.
- Pros: Clean single-responsibility boundary. Future extensibility (network sandbox, etc.).
- Cons: More abstraction for what is currently a simple check. The grant store is already managed by `UrSession`, so ownership gets murky.
- Failure modes: Over-engineering for current needs.

## Recommended approach

**Option A** — simplify enum, move containment to `ToolInvoker`.

- Why: It's the smallest change that achieves the goal. No new abstractions. The `TargetExtractor` already resolves the relevant argument per tool, so the invoker just needs to resolve that to an absolute path and check `workspace.Contains()`.
- Key tradeoff: `ToolInvoker` gains workspace awareness, but this is appropriate — it's the permission enforcement point, and workspace containment is a permission concern.

## Related code

- `src/Ur/Permissions/OperationType.cs` — the enum being simplified
- `src/Ur/Permissions/PermissionPolicy.cs` — gains `isInWorkspace` parameter
- `src/Ur/Permissions/PermissionRequest.cs` — uses `OperationType` (unchanged structurally)
- `src/Ur/Permissions/PermissionGrant.cs` — uses `OperationType` (changed values in serialized form)
- `src/Ur/Permissions/PermissionGrantStore.cs` — serialization format changes (old grants become unreadable)
- `src/Ur/Tools/BuiltinTools.cs` — registration uses simplified enum values
- `src/Ur/Tools/ToolRegistry.cs` — default changes from `WriteInWorkspace` to `Write`
- `src/Ur/Tools/PermissionMeta.cs` — unchanged structurally
- `src/Ur/Tools/ReadFileTool.cs` — remove `workspace.Contains()` check
- `src/Ur/Tools/WriteFileTool.cs` — remove `workspace.Contains()` check
- `src/Ur/Tools/UpdateFileTool.cs` — remove `workspace.Contains()` check
- `src/Ur/Tools/GlobTool.cs` — remove `workspace.Contains()` check
- `src/Ur/Tools/GrepTool.cs` — remove `workspace.Contains()` check
- `src/Ur/AgentLoop/ToolInvoker.cs` — gains `Workspace` dependency, performs containment check
- `src/Ur/AgentLoop/AgentLoop.cs` — passes `Workspace` to `ToolInvoker`
- `src/Ur/Sessions/UrSession.cs` — passes `Workspace` when constructing `AgentLoop`
- `src/Ur/Tools/ToolArgHelpers.cs` — `ResolvePath` used by invoker for path resolution
- `tests/Ur.Tests/PermissionPolicyTests.cs` — rewrite for new signature
- `tests/Ur.Tests/PermissionTests.cs` — update enum references
- `tests/Ur.Tests/BuiltinToolTests.cs` — remove "rejects path outside workspace" tests from tools, add sandbox-level tests

## Current state

The permission check flow today:

```
Tool registered with OperationType.ReadInWorkspace
  → ToolInvoker.IsPermissionDeniedAsync reads OperationType from metadata
  → PermissionPolicy.RequiresPrompt(ReadInWorkspace) → false → auto-allow
  → Tool.InvokeCoreAsync checks workspace.Contains() again internally
```

After this change:

```
Tool registered with OperationType.Read
  → ToolInvoker.IsPermissionDeniedAsync reads OperationType from metadata
  → ToolInvoker resolves target to absolute path, checks workspace.Contains()
  → PermissionPolicy.RequiresPrompt(Read, isInWorkspace: true) → false → auto-allow
  → Tool.InvokeCoreAsync just operates on the path (no containment check)
```

## Structural considerations

**Hierarchy**: `ToolInvoker` sits between the agent loop and tool execution — the right layer for enforcement. Moving containment here respects the existing layering.

**Abstraction**: Tools currently mix "do the operation" with "enforce the boundary." Separating these puts each concern at the right level: tools handle operations, the invoker handles policy.

**Encapsulation**: Tools no longer need to know about workspace boundaries. The `Workspace` dependency stays internal to the invoker — tools just receive paths and operate on them.

**Modularization**: No new modules. The existing permission module gains a small amount of location awareness that was already implicit in the enum values.

## Implementation plan

### Refactoring (separation of concerns)

- [ ] **Simplify `OperationType` enum** — Replace `ReadInWorkspace`, `ReadOutsideWorkspace`, `WriteInWorkspace`, `ExecuteCommand` with `Read`, `Write`, `Execute` in `src/Ur/Permissions/OperationType.cs`. Update the doc comment to reflect that operation type is about what, not where.

- [ ] **Update `PermissionPolicy`** — Change `RequiresPrompt` and `AllowedScopes` to accept `(OperationType operationType, bool isInWorkspace)`. Preserve the existing behavioral matrix:
  - `(Read, inWorkspace: true)` → no prompt, empty scopes
  - `(Read, inWorkspace: false)` → prompt, Once only
  - `(Write, inWorkspace: true)` → prompt, all scopes
  - `(Write, inWorkspace: false)` → prompt, Once only
  - `(Execute, _)` → prompt, Once only

- [ ] **Update `ToolInvoker`** — Add `Workspace` to the constructor. In `IsPermissionDeniedAsync`:
  1. Get operation type from metadata (default: `Write`)
  2. Resolve the target string to an absolute path using `ToolArgHelpers.ResolvePath(workspace.RootPath, target)`
  3. Check `workspace.Contains(resolvedPath)` to get `isInWorkspace`
  4. For `Execute` operations, treat as `isInWorkspace: false` (always prompts)
  5. Pass both to `PermissionPolicy`
  6. Use the resolved absolute path as the target in the `PermissionRequest`

- [ ] **Update `AgentLoop`** — Add `Workspace` parameter to the constructor. Pass it to `ToolInvoker`.

- [ ] **Update `UrSession`** — Pass `_host.Workspace` (or equivalent) when constructing `AgentLoop`. Verify `UrHost` exposes the workspace or that the session has access to it.

- [ ] **Remove `workspace.Contains()` from tools** — Delete the containment check and the associated `throw` from:
  - `ReadFileTool.InvokeCoreAsync`
  - `WriteFileTool.InvokeCoreAsync`
  - `UpdateFileTool.InvokeCoreAsync`
  - `GlobTool.InvokeCoreAsync`
  - `GrepTool.InvokeCoreAsync`
  
  Tools keep their `Workspace` dependency for path resolution (e.g., `workspace.RootPath` to resolve relative paths) but no longer enforce boundaries.

- [ ] **Update `BuiltinTools.RegisterAll`** — Change `OperationType.ReadInWorkspace` to `OperationType.Read`, and `OperationType.ExecuteCommand` to `OperationType.Execute`. The write tools already default, which will now be `OperationType.Write`.

- [ ] **Update `ToolRegistry`** — Change the default in `Register()` and the doc comment from `WriteInWorkspace` to `Write`.

- [ ] **Update `PermissionGrantStore` serialization** — The JSON enum converter will naturally serialize the new values (`read`, `write`, `execute`). Old persisted grants using the old enum names (`readInWorkspace`, `writeInWorkspace`, etc.) will fail to deserialize and be skipped — this is acceptable since the project is early-stage. Add a comment noting this intentional break.

### Tests

- [ ] **Rewrite `PermissionPolicyTests`** — Test the new two-parameter signatures. Cover all cells of the (OperationType, isInWorkspace) matrix.

- [ ] **Update `AgentLoopPermissionTests`** — Update enum references. The `ReadInWorkspace_NeverCallsCallback` test becomes `Read_InWorkspace_NeverCallsCallback`. Add a new test: `Read_OutsideWorkspace_CallsCallback` to verify that the same `Read` operation type prompts when the target is outside the workspace.

- [ ] **Update `PermissionGrantStoreTests`** — Replace `OperationType.WriteInWorkspace` / `ReadOutsideWorkspace` with `Write` / `Read`.

- [ ] **Update `UrSessionPermissionTests`** — Replace `writeInWorkspace` string assertions in serialized grant checks with `write`.

- [ ] **Update `BuiltinToolTests`** — Remove the "rejects path outside workspace" tests from individual tools (`ReadFile_RejectsPathOutsideWorkspace`, `WriteFile_RejectsPathOutsideWorkspace`, `UpdateFile_RejectsPathOutsideWorkspace`, `Glob_RejectsPathOutsideWorkspace`, `Grep_RejectsPathOutsideWorkspace`, and all path traversal tests). These boundary violations are now the sandbox's responsibility, not the tool's.

- [ ] **Add sandbox-level boundary tests** — Add tests to `AgentLoopPermissionTests` (or a new `ToolInvokerTests` class) that verify:
  - A `Read` tool targeting an in-workspace path is auto-allowed
  - A `Read` tool targeting an outside-workspace path triggers a permission prompt
  - A `Write` tool always triggers a permission prompt regardless of location
  - An `Execute` tool always triggers a permission prompt

### Validation

- [ ] Run `make inspect` and fix any issues in `inspection-results.txt`.
- [ ] Run `dotnet test` across all test projects — all tests pass.
- [ ] Manual verification: confirm that read_file on an in-workspace path runs without prompt, and read_file on an outside-workspace path triggers the permission callback.

## Impact assessment

- **Code paths affected**: Tool invocation pipeline (ToolInvoker → PermissionPolicy → tools). Registration (BuiltinTools, ToolRegistry). Grant persistence (serialized enum values change).
- **Data impact**: Persisted permission grants (`.ur/permissions.jsonl`) from before this change will not deserialize. They'll be silently skipped. Users will be re-prompted — acceptable for early-stage.
- **API impact**: `AgentLoop` constructor gains a `Workspace` parameter. `PermissionPolicy` methods gain an `isInWorkspace` parameter. Both are internal/public but not consumed externally yet.

## Open questions

- Should `Write` outside-workspace be `Once`-only (matching the current restrictiveness for out-of-workspace ops), or should it offer the same full scope options as in-workspace writes? The implementation plan above assumes `Once`-only for any outside-workspace operation, but this is a policy choice worth confirming.
