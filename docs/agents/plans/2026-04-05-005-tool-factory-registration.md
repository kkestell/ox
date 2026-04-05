# Tool Factory Registration System

## Goal

Refactor the tool registration system from a scattered, multi-point approach (builtin sweep, host-level special cases, session-level wiring) to a unified factory-based architecture. All tools — builtin, extension, and cross-layer — will use the same machinery: a factory function that takes a `ToolContext` and returns an `AIFunction`.

This eliminates special-casing by dependency level, reduces cognitive load, and makes it trivial to add new tools or new context tiers.

## Desired outcome

- Single `ToolContext` record containing all context tools could need (workspace, session ID, chat client, tool registry, callbacks, system prompt)
- All tool registration uses factories: `Func<ToolContext, AIFunction>`
- One dumb registration loop that builds context once and calls all factories
- Extension tools (Lua-defined) use the same factory pattern as builtins
- Tests can construct `ToolContext` with mocks and instantiate tools in isolation
- No "special cases" — tools don't declare where they belong; they just declare what they use

## How we got here

Current registration is scattered across three call sites with different responsibilities:
1. `BuiltinTools.RegisterAll()` — assumes tools need only `Workspace`
2. `UrHost.BuildSessionToolRegistry()` — wires tools that need `sessionId` + `SkillRegistry`
3. `Extensions.RegisterActiveToolsInto()` — registers loaded extension tools

When SubagentTool was planned, it needed turn-level context (callbacks, system prompt) that isn't available until `UrSession.RunTurnAsync()`. This forced a choice: either move registration into `RunTurnAsync`, or restructure the whole system. The latter is what we're doing here.

## Approaches considered

### Option A — Multi-tier factories

Separate factory types for each tier:
- `Func<Workspace, AIFunction>` for host tools
- `Func<(Workspace, string), AIFunction>` for session tools  
- `Func<(Workspace, string, IChatClient, ...), AIFunction>` for turn tools

**Pros:** Tools declare exactly what they need; no unused context passed around.

**Cons:** Registration code has to know about three different factory shapes. Adding a new tier (e.g., request-scoped context) means a fourth factory type. High cognitive load — "which tier does my tool belong to?"

### Option B — Single unified context

One `ToolContext` record with all possible fields. All factories take it; tools use what they need.

**Pros:** One factory signature everywhere. Registration code is dumb — just one loop. No "which tier" reasoning. Adding new context is invisible to registration code; you just add a field to `ToolContext`. Tests trivial — construct whatever context you want.

**Cons:** Some tools get context they don't use. But this is a non-issue: unused fields are just ignored, and the cognitive simplicity win is huge.

## Recommended approach

**Option B** — Single unified `ToolContext`.

The reasoning: the cognitive load reduction is worth far more than unused context fields. The registration code becomes a simple loop with zero special cases. Tests become dead simple. Adding tools or context tiers requires zero changes to registration machinery.

## Structural considerations

**Hierarchy (dependency flow):**
- `ToolContext` is a pure data carrier; it doesn't depend on anything.
- Factories close over subsystem-specific dependencies (SkillRegistry for SkillTool, for example) and consume the passed context.
- Registration code (in `UrSession.RunTurnAsync`) orchestrates context building and factory invocation.
- No cycles: factories → ToolContext (data), registration code → factories.

**Abstraction:**
- `ToolContext` is at the right level: an orchestration contract that says "here's everything a tool might need." It's not a god object; it's explicitly a union of concerns needed at tool-instantiation time.
- Factories are thin: they take context, construct a tool, return it. No business logic.

**Modularization:**
- Tool definitions stay where they are (Ur.Tools, Ur.Skills, Ur.Extensions).
- Factories live next to their tools or in a dedicated factory registry.
- Registration orchestration stays in `UrSession` (where context is available) or a helper in `UrHost`.

**Encapsulation:**
- `ToolContext` is internal; tools don't expose it.
- The factory list is not exported; it's private to the registration orchestrator.
- Extension tools are wrapped in factories at their activation point; they don't know about the factory pattern.

## Implementation plan

### Step 1: Define `ToolContext`
- [ ] Create `src/Ur/ToolContext.cs` with a record:
  ```csharp
  internal record ToolContext(
      Workspace Workspace,
      string SessionId,
      IChatClient ChatClient,
      ToolRegistry Tools,
      TurnCallbacks? Callbacks,
      string? SystemPrompt);
  ```
- [x] Place in root `Ur` namespace (cross-cutting, similar to `UrConfiguration`, `Workspace`).

### Step 2: Create a factory registry type
- [x] Create `src/Ur/Tools/ToolFactory.cs` with a simple wrapper:
  ```csharp
  internal delegate AIFunction ToolFactory(ToolContext context);
  ```
  (Or a record if we want to add metadata later.)

### Step 3: Convert builtin tools to factories
- [x] Add static factory methods to each builtin tool class:
  ```csharp
  // In ReadFileTool
  public static AIFunction Create(ToolContext context) => new ReadFileTool(context.Workspace);
  ```
  (Or define them as lambda factories in a central `BuiltinToolFactories` class.)
- [x] Remove the static `RegisterAll()` method; move its registration logic into a factory list.
- [x] Create `src/Ur/Tools/BuiltinToolFactories.cs`:
  ```csharp
  internal static class BuiltinToolFactories
  {
      public static ToolFactory[] All => new ToolFactory[]
      {
          ctx => new ReadFileTool(ctx.Workspace),
          ctx => new WriteFileTool(ctx.Workspace),
          ctx => new UpdateFileTool(ctx.Workspace),
          ctx => new GlobTool(ctx.Workspace),
          ctx => new GrepTool(ctx.Workspace),
          ctx => new BashTool(ctx.Workspace),
      };
  }
  ```

### Step 4: Create a SkillTool factory
- [x] Add to `BuiltinToolFactories.All` (or a separate list):
  ```csharp
  ctx => new SkillTool(skillRegistry, ctx.SessionId)
  ```
  (Requires `skillRegistry` to be available at factory creation time — it's closed over from `UrHost`.)

### Step 5: Adapt extensions to use factories
- [x] Modify `Extension.RegisterToolsInto()` to return factories instead of registering directly:
  ```csharp
  internal ToolFactory[] GetToolFactories()
  {
      return _tools.Select(tool => (ToolContext _) => tool).ToArray();
  }
  ```
  (Each Lua tool becomes a factory that ignores context and returns itself.)
- [x] Update `ExtensionCatalog.GetActiveToolFactories()` to collect factories from active extensions.

### Step 6: Move registration into `UrSession.RunTurnAsync`
- [x] Build `ToolContext` once all components are ready (after wrappedCallbacks and systemPrompt are built).
- [x] Collect all factories: `BuiltinToolFactories.All` + `skillTool` + `Extensions.GetActiveToolFactories()`.
- [x] Call each factory with the context; register resulting tools into the registry.
- [x] Delete `UrHost.BuildSessionToolRegistry()` or simplify it to build only the base registry (without tools).

### Step 7: Handle permission metadata
- [x] Tools need to declare their `OperationType` and `TargetExtractor`. Options:
  - **Option A:** Factory returns `(AIFunction, PermissionMeta)` tuple.
  - **Option B:** `AIFunction` subclasses declare metadata via an interface (`IToolMeta`).
  - **Option C:** Registration code inspects tool type and assigns metadata (e.g., "SkillTool is always Read").
  
  **Recommend Option B:** `IToolMeta` interface on `AIFunction`. Tools that need special metadata implement it; registration code checks for it.
  ```csharp
  internal interface IToolMeta
  {
      OperationType OperationType { get; }
      ITargetExtractor? TargetExtractor { get; }
  }
  
  internal sealed class SkillTool : AIFunction, IToolMeta { ... }
  ```

### Step 8: Update tests
- [x] Tests can now be fixed with Haiku subagents after the refactor is complete (per user guidance).
- [ ] For now: manually update a few key tests to construct `ToolContext` with mocks:
  ```csharp
  var context = new ToolContext(
      mockWorkspace,
      "test-session-id",
      mockChatClient,
      mockRegistry,
      mockCallbacks: null,
      mockSystemPrompt: null);
  
  var tool = BuiltinToolFactories.All[0](context); // or skillTool(context)
  var result = await tool.InvokeAsync(...);
  ```

### Step 9: Run `make inspect`
- [x] Fix any PHAME violations or other lints.
- [x] Ensure all tests still pass (even if they're in temporary state).

## Impact assessment

**Code paths affected:**
- `UrHost.BuildSessionToolRegistry()` — removed or refactored
- `UrSession.RunTurnAsync()` — moved tool registration here; builds `ToolContext`
- `BuiltinTools.cs` — deleted or made private; logic moved to factories
- `Extension.RegisterToolsInto()` — adapted to return factories
- `ExtensionCatalog` — adds factory collection method
- All tool tests — will be updated by Haiku subagents post-refactor

**New files:**
- `src/Ur/ToolContext.cs`
- `src/Ur/Tools/BuiltinToolFactories.cs` (or factories co-located with tool classes)

**Data or schema impact:** None.

**Dependency or API impact:** 
- `ToolContext` is `internal`; no public API change.
- `BuiltinTools.RegisterAll()` becomes internal or disappears; it was never public.

## Validation

- **Compile:** No compiler errors; all types resolve.
- **Tests:** All builtin tool tests pass after update (Haiku handles this post-refactor).
- **Integration:** Create a session, run a turn, verify tools invoke correctly.
- **Extension loading:** Enable an extension, verify its tools appear in the registry and invoke.
- **Lint:** `make inspect` passes.
- **Manual test:** Run the TUI, invoke a few tools (read_file, skill, etc.) and confirm normal behavior.

## Related code

- `src/Ur/UrHost.cs:89-116` — current `BuildSessionToolRegistry()` method; logic moves to `RunTurnAsync()`
- `src/Ur/Sessions/UrSession.cs:85-169` — `RunTurnAsync()`; tool registration moves here
- `src/Ur/Tools/BuiltinTools.cs` — current builtin registration; logic moves to factories
- `src/Ur/Skills/SkillTool.cs:24` — constructor signature; factories will pass context
- `src/Ur/Extensions/Extension.cs:102-108` — current `RegisterToolsInto()`; adapted to return factories
- `src/Ur/Extensions/ExtensionCatalog.cs:65-72` — collects active extension tools; updated for factories
- `src/Ur/Tools/PermissionMeta.cs` — will add `IToolMeta` interface for tools to self-declare metadata
- `tests/Ur.Tests/BuiltinToolTests.cs` — tool tests; will be updated (deferred to Haiku subagent phase)
- `tests/Ur.Tests/Skills/SkillToolTests.cs` — same

## Open questions

None. Approach is settled; implementation is straightforward.
