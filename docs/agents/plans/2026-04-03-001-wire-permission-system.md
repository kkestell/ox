# Wire the Permission System

## Goal

Connect the permission architecture — which is fully designed but entirely disconnected from execution — so that every tool call requires explicit user approval before running.

## Desired outcome

- No tool call executes silently. Every sensitive operation is gated on either a cached grant or a live approval callback.
- The library exposes a clean, host-agnostic hook (`TurnCallbacks`) that any consumer (CLI, future TUI, GUI) implements in terms of its own UI pattern.
- Permission grants at `Session`, `Workspace`, and `Always` scope persist so users aren't re-asked for the same operation.

## How we got here

The Ur.Tui project has been deleted. The only active consumer of `UrSession.RunTurnAsync` is `Ur.Cli/Commands/ChatCommand.cs`, which currently passes `null` for `TurnCallbacks`. The library's permission types (`PermissionRequest`, `PermissionResponse`, `PermissionGrant`, `PermissionScope`, `OperationType`, `PermissionPolicy`, `TurnCallbacks`) are all defined but `AgentLoop` never calls the callback.

The central design question is where to attach the callback — per-turn or per-session — since the session lifetime is the natural scope for grant tracking.

## Approaches considered

### Option 1: Keep TurnCallbacks as a per-turn parameter

`UrSession.RunTurnAsync(input, TurnCallbacks? callbacks, ct)` remains unchanged. Hosts pass the same delegate on every call.

- Pro: zero API surface change; already designed this way
- Con: every call site must remember to pass callbacks (currently all pass `null`); per-turn feels wrong for something that belongs to the session's lifetime
- Failure mode: future host authors accidentally leave callbacks null and get silent approval

### Option 2: Move TurnCallbacks to session creation (recommended)

`UrHost.CreateSession(TurnCallbacks? callbacks = null)` captures the callbacks at session birth. `UrSession.RunTurnAsync` drops the parameter.

- Pro: set-once ergonomics; callbacks and grant store share the same session lifetime; no per-turn parameter that hosts can forget
- Pro: consistent with how session is already the unit of identity and persistence
- Con: small breaking change to `UrSession.RunTurnAsync` (remove parameter), but that parameter was always unused
- Failure mode: none significant — the callback is optional, `null` means auto-deny

### Option 3: Mutable property on UrSession

`UrSession.PermissionCallbacks { get; set; }` set by the host after creation.

- Pro: flexible, no API changes
- Con: mutable session state; easy to forget; violates "configure then use" principle

## Recommended approach

**Option 2** — session-creation callbacks.

The grant store and the approval callback both belong to the session's lifetime. Tying them together at `CreateSession` makes the relationship explicit and removes a per-turn parameter that was semantically wrong from the start. Any future host (GUI, TUI, headless) provides its approval delegate once:

```csharp
var session = host.CreateSession(new TurnCallbacks
{
    RequestPermissionAsync = MyApprovalDialogAsync
});
```

## Related code

- `Ur/Permissions/TurnCallbacks.cs` — defines the callback contract; currently accepted but ignored
- `Ur/Permissions/PermissionRequest.cs` — what the library passes to the host when a tool needs permission
- `Ur/Permissions/PermissionResponse.cs` — what the host returns (granted, scope)
- `Ur/Permissions/PermissionGrant.cs` — persisted grant record checked before re-prompting
- `Ur/Permissions/PermissionPolicy.cs` — maps `OperationType` → allowed scopes; `RequiresPrompt` gate
- `Ur/Permissions/PermissionScope.cs` — Once / Session / Workspace / Always
- `Ur/Permissions/OperationType.cs` — ReadInWorkspace / ReadOutsideWorkspace / WriteInWorkspace / WriteOutsideWorkspace / Network
- `Ur/AgentLoop/AgentLoop.cs` — tool execution loop; needs to call the callback before each tool; currently ignores `TurnCallbacks`
- `Ur/AgentLoop/ToolRegistry.cs` — holds `AIFunction` instances; needs per-tool `OperationType` metadata
- `Ur/Sessions/UrSession.cs` — accepts `TurnCallbacks?` but doesn't pass it to `AgentLoop`; will own the `PermissionGrantStore`
- `Ur/UrHost.cs` — session factory; `CreateSession()` gains optional `TurnCallbacks` parameter
- `Ur/Workspace.cs` — `PermissionsPath` is defined but never read/written
- `Ur.Cli/Commands/ChatCommand.cs` — passes `null` for `TurnCallbacks`; needs a real CLI approval prompt

## Current state

- `AgentLoop.RunTurnAsync` accepts only `List<ChatMessage>` and `CancellationToken` — no `TurnCallbacks` parameter
- `UrSession.RunTurnAsync` accepts `TurnCallbacks?` but does not forward it to `AgentLoop`
- `ToolRegistry.Register` takes an `AIFunction` with no permission metadata
- `Workspace.PermissionsPath` (`.ur/permissions`) is defined but never used
- No `PermissionGrantStore` exists
- `ChatCommand.cs` passes `null` for `TurnCallbacks` (line 105)

## Structural considerations

**Hierarchy:** The grant-checking logic belongs in `UrSession` (session layer), not in `AgentLoop` (execution layer). `AgentLoop` should only call a callback — it has no business knowing about grant persistence. `UrSession` wraps the host-provided callback with a grant-checking decorator before handing it to `AgentLoop`.

**Abstraction:** `TurnCallbacks` is the right abstraction level. It keeps the library free of UI concerns. The host provides whatever approval mechanism its UI supports.

**Encapsulation:** `PermissionGrantStore` is internal to `Ur`; its file paths and format are not exposed. The host sees only `TurnCallbacks`.

**Tool metadata:** `ToolRegistry` must know the `OperationType` for each registered tool so `AgentLoop` can build a `PermissionRequest` before invoking it. This requires a small extension to `Register`.

## Implementation plan

### 1. Extend ToolRegistry with permission metadata

- [x] Add an overload `ToolRegistry.Register(AIFunction tool, OperationType operationType, string? extensionId = null, Func<AIFunctionArguments, string>? targetExtractor = null)`
- [x] Store `(OperationType, extensionId, targetExtractor)` in a parallel `Dictionary<string, PermissionMeta>` keyed by tool name
- [x] Add `ToolRegistry.GetPermissionMeta(string toolName) → PermissionMeta?` (returns null for unknown tools)
- [x] Update `Extension.MarkActivated` to pass `OperationType.WriteInWorkspace` when registering Lua tools (conservative default; Lua tools can write files)
- [x] Update `Extension.ResetRuntimeState` — no change needed (tools are removed by name)

### 2. Add PermissionGrantStore

- [x] Create `Ur/Permissions/PermissionGrantStore.cs`
- [x] Store session grants in-memory: `List<PermissionGrant>` (scope `Session`)
- [x] Read/write workspace grants from `{workspace}/.ur/permissions.jsonl` (scope `Workspace`)
- [x] Read/write always grants from `{userDataDir}/permissions.jsonl` (scope `Always`)
- [x] `bool IsCovered(PermissionRequest)` — checks if any active grant matches `operationType` + target prefix
- [x] `Task StoreAsync(PermissionGrant, CancellationToken)` — persists by scope; `Once` is not stored
- [x] Load workspace and always grants lazily (read on first `IsCovered` check, not at construction)

### 3. Wire AgentLoop to call the callback

- [x] Add `TurnCallbacks?` parameter to `AgentLoop.RunTurnAsync` (before `CancellationToken`)
- [x] Before executing each tool call:
  - Look up `PermissionMeta` from registry; default to `WriteInWorkspace` if not found
  - If `PermissionPolicy.RequiresPrompt(operationType)` and `RequestPermissionAsync` is not null:
    - Extract target: call `meta.TargetExtractor(call.Arguments)` or fall back to tool name
    - Build `PermissionRequest(operationType, target, extensionId ?? call.Name, PermissionPolicy.AllowedScopes(operationType))`
    - Await `turnCallbacks.RequestPermissionAsync(request, ct)`
    - If denied: add a `FunctionResultContent` with "Permission denied." and continue to next tool call (do not `yield break`)
  - If `RequestPermissionAsync` is null and `RequiresPrompt`: deny silently (no callback = no approval)

### 4. UrSession: own the grant store, wrap the callback, create with TurnCallbacks

- [x] Add `PermissionGrantStore _grantStore` field to `UrSession`
- [x] Change `UrHost.CreateSession()` signature to `UrSession CreateSession(TurnCallbacks? callbacks = null)`; pass callbacks + workspace/userDataDir paths to `UrSession`
- [x] Change `UrHost.OpenSessionAsync` similarly
- [x] Remove `TurnCallbacks?` parameter from `UrSession.RunTurnAsync` (it's now session-level state)
- [x] In `UrSession.RunTurnAsync`, build a wrapped `TurnCallbacks`:
  1. Check `_grantStore.IsCovered(request)` → if yes, return `Granted: true, Scope: null`
  2. If no stored callback (host passed `null`): return `Granted: false, Scope: null`
  3. Delegate to the host's `RequestPermissionAsync`
  4. On grant: call `_grantStore.StoreAsync(new PermissionGrant(...), ct)` for non-Once scopes
- [x] Pass the wrapped `TurnCallbacks` to `AgentLoop.RunTurnAsync`

### 5. CLI: implement the approval prompt

- [x] In `ChatCommand.cs`, build `TurnCallbacks` before calling `CreateSession`:
  ```
  RequestPermissionAsync = async (req, ct) =>
  {
      var scopeList = req.AllowedScopes.Count > 0
          ? $" [{string.Join(", ", req.AllowedScopes)}]"
          : "";
      Console.Error.Write(
          $"\nAllow {req.OperationType} on '{req.Target}' by '{req.RequestingExtension}'? (y/n{scopeList}): ");
      var input = Console.ReadLine()?.Trim().ToLowerInvariant();
      return input switch
      {
          "y" or "yes"           => new PermissionResponse(true, PermissionScope.Once),
          "session"              => new PermissionResponse(true, PermissionScope.Session),
          "workspace"            => new PermissionResponse(true, PermissionScope.Workspace),
          "always"               => new PermissionResponse(true, PermissionScope.Always),
          _                      => new PermissionResponse(false, null),
      };
  }
  ```
- [x] Pass `TurnCallbacks` to `host.CreateSession(callbacks)`
- [x] Note: only scope options valid for the operation type should be presented; filter `req.AllowedScopes`

### 6. Ensure Workspace.EnsureDirectories() creates the permissions directory

- [x] `Workspace.EnsureDirectories()` should not need to change since `PermissionsPath` is a file, not a directory — but verify the parent `.ur` directory is created (it already is)

### 7. Tests

- [x] Unit test: `PermissionGrantStore.IsCovered` — session grant covers exact match and prefix match; workspace grant loaded from file; `Once` is never stored
- [x] Unit test: `AgentLoop` with a callback that denies — tool call produces a "Permission denied" function result, loop continues
- [x] Unit test: `AgentLoop` with a callback that grants — tool executes normally
- [x] Unit test: `UrSession` grant-wrapping decorator — covered grant skips the host callback; uncovered grant invokes callback; `Workspace`-scope grant is stored
- [x] Integration test: `HostSessionApiTests` or a new `PermissionTests` — end-to-end: tool registered with `WriteInWorkspace`, callback invoked once, `Session`-scope grant prevents second prompt

## Impact assessment

- **Code paths affected:** `AgentLoop.RunTurnAsync` (tool execution), `UrSession.RunTurnAsync` (turn orchestration), `UrHost.CreateSession`/`OpenSessionAsync` (session factory), `ChatCommand` (CLI consumer)
- **Data or schema impact:** New `permissions.jsonl` file at `{workspace}/.ur/permissions.jsonl` and `~/.ur/permissions.jsonl`; no changes to session JSONL format
- **Dependency or API impact:** `UrHost.CreateSession` gains an optional parameter (non-breaking); `UrSession.RunTurnAsync` loses the `TurnCallbacks?` parameter (breaking for any callers, but currently only `ChatCommand` passes `null`)

## Validation

- `dotnet test` passes (all existing tests green)
- Manual: `ur chat "write a file"` with a registered write tool — CLI prompts, user approves/denies, tool runs or is skipped accordingly
- Manual: approve with `session` scope — second tool call in same session executes without a prompt
- Manual: approve with `workspace` scope — kill process, run again in same workspace — no prompt

## Gaps and follow-up

- **No built-in write/read tools yet:** The permission gate is correct once extension tools register with `OperationType`, but there are no built-in tools to test against beyond extension Lua tools. This is expected — the system tests against extension-provided tools.
- **Lua tool OperationType declaration:** Lua extensions currently cannot declare their own `OperationType` — the default `WriteInWorkspace` is used. A future extension API could let Lua tools self-declare (e.g., `ur.register_tool({ operation_type = "read_in_workspace", ... })`). Tracked separately.
- **Future TUI/GUI approval UI:** For a future interactive host, implement `RequestPermissionAsync` as a dialog callback. The `PermissionModal` pattern (or equivalent) is the natural approach: create a `TaskCompletionSource<PermissionResponse>`, marshal the request to the UI thread, present a modal, complete the TCS when the user responds.
