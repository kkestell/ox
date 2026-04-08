# Parallel tool execution

## Goal

When the LLM returns multiple tool calls in a single message, execute them concurrently instead of sequentially. Permission-required tools are approved one at a time in the UI but start executing immediately on approval, running concurrently with everything else already in flight.

## Desired outcome

- Auto-allowed tools (in-workspace reads, previously granted operations) start executing concurrently as soon as the LLM response finishes.
- Tools that require approval are presented to the user one at a time. Each begins execution immediately on approval, running concurrently with all other in-flight tools.
- The UI correctly renders interleaved `ToolCallStarted`/`ToolCallCompleted` events from concurrent tools.
- No change to the public API of `ToolInvoker.InvokeAllAsync` — it still returns `IAsyncEnumerable<AgentLoopEvent>`.

## How we got here

Observed that two `run_subagent` calls in a single LLM response executed sequentially — the first ran to completion before the second appeared in the UI. Confirmed this is the current design: `InvokeAllAsync` uses a `foreach` with `await InvokeOneAsync(...)`. The user wants concurrent execution with a specific permission UX: auto-allowed tools fire immediately, approval-required tools are serialized for prompting but parallelize on grant.

## Related code

- `src/Ur/AgentLoop/ToolInvoker.cs` — Focal point. `InvokeAllAsync` (sequential foreach), `InvokeOneAsync` (permission check + invoke), `IsPermissionDeniedAsync` (permission gate). All changes to dispatch logic live here.
- `src/Ur/AgentLoop/AgentLoop.cs` — Consumer of `InvokeAllAsync` via `await foreach`. Lines 114-119 construct `toolResultMessage` and add it to history after all tools complete. No change needed — it already awaits the full enumerable.
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — Event types. Need a new `ToolAwaitingApproval` event (see below).
- `src/Ur.Tui/EventRouter.cs` — Routes events to renderables. `_lastStartedTool` (line 61) assumes sequential execution and must be replaced. `SetLastToolAwaitingApproval()` (line 228) is called by the permission callback as a side-channel — must switch to event-driven.
- `src/Ur/AgentLoop/SubagentRunner.cs` — Subagents are just tools to the invoker. No changes needed — they'll naturally run concurrently.
- `src/Ur/Permissions/PermissionPolicy.cs` — `RequiresPrompt()` determines the auto-allow vs needs-approval categorization. Read-only dependency.
- `src/Ur/Permissions/PermissionGrantStore.cs` — `IsCovered()` is called during categorization, `StoreAsync()` during approval. Both are called from the serial approval path so no thread-safety concern.
- `src/Ur/Permissions/TurnCallbacks.cs` — `RequestPermissionAsync` callback shape. No change needed.

## Current state

- `InvokeAllAsync` iterates tool calls sequentially. Each call goes through permission check → invoke → result, fully completing before the next starts.
- `InvokeOneAsync` couples permission checking with invocation — these need to be separable for the parallel design.
- `EventRouter._lastStartedTool` is a single-slot field set by `ToolCallStarted` and read by `SetLastToolAwaitingApproval()`. This works because only one tool is in flight at a time. With parallelism, multiple tools could have `ToolCallStarted` events in the channel before the approval prompt fires.
- `resultMessage.Contents` is an `IList<AIContent>` — not safe for concurrent mutation. Results must be collected separately and assembled after all tasks complete.
- `System.Threading.Channels` is not currently used in the codebase.

## Structural considerations

**Hierarchy**: The change is contained within `ToolInvoker`. `AgentLoop.RunTurnAsync` still just does `await foreach` over the events. The parallelism is invisible to the layer above.

**Abstraction**: `InvokeAllAsync`'s public signature (`IAsyncEnumerable<AgentLoopEvent>`) does not change. The channel is an internal implementation detail. The one new surface is the `ToolAwaitingApproval` event type, which is needed to replace the `_lastStartedTool` side-channel in EventRouter.

**Modularization**: `InvokeOneAsync` currently does two things (permission check + invocation). Splitting it makes each concern independently addressable — the permission check feeds the categorization/approval pipeline, the invocation feeds the concurrent execution pipeline.

**Encapsulation**: The current `SetLastToolAwaitingApproval()` method on EventRouter is a side-channel from the permission callback into the UI layer. It assumes sequential execution. Replacing it with a `ToolAwaitingApproval` event preserves the event-driven contract and removes the assumption.

## Refactoring

### Split `InvokeOneAsync` into permission check and invocation

`InvokeOneAsync` currently handles both permission checking and tool invocation. These need to be separate methods because the parallel design categorizes all calls by permission status first, then executes them on different schedules.

Extract `CheckPermissionAsync(call, callbacks, ct) → PermissionResult` (auto-allowed, granted, denied) and keep `InvokeOneAsync` as the invocation-only path (handler lookup → invoke → result).

### Replace `_lastStartedTool` side-channel with `ToolAwaitingApproval` event

`EventRouter.SetLastToolAwaitingApproval()` is called directly by the permission callback as a side-channel. With parallel execution, we can't rely on "the last started tool" being the one awaiting approval. Instead:

- Add a `ToolAwaitingApproval { CallId }` event type.
- Emit it from the approval pipeline in `ToolInvoker` right before calling `RequestPermissionAsync`.
- `EventRouter` handles it by looking up the renderable by `CallId` and calling `SetAwaitingApproval()`.
- Remove `_lastStartedTool` field and `SetLastToolAwaitingApproval()` method.

## Implementation plan

### Phase 1: Refactor ToolInvoker internals

- [x] Extract `CheckPermissionAsync` from `IsPermissionDeniedAsync` — returns an enum `{ AutoAllowed, Granted, Denied }` instead of a bool. The existing `IsPermissionDeniedAsync` combines policy check + prompt + grant storage; the new method should separate the "does this need a prompt?" check from the "ask the user" step.
- [x] Split `InvokeOneAsync` into two methods: one for the permission gate (categorization), one for invocation only (`ExecuteToolAsync`). The invocation method assumes permission is already resolved.

### Phase 2: Channel-based parallel dispatch in `InvokeAllAsync`

- [x] Add `using System.Threading.Channels` to the codebase.
- [x] Rewrite `InvokeAllAsync` with the following structure:
  1. **Categorize**: iterate all calls, run `CheckPermissionAsync` to determine auto-allowed vs needs-approval. This is fast (in-memory grant store lookup + policy check, no user prompt).
  2. **Create channel**: `Channel.CreateUnbounded<AgentLoopEvent>()`.
  3. **Producer task**: a background `Task.Run` that:
     - Fires off auto-allowed tools concurrently (each writes `ToolCallStarted`/`ToolCallCompleted` to the channel).
     - Runs the approval pipeline serially: for each needs-approval call, writes `ToolCallStarted` and `ToolAwaitingApproval` to the channel, calls `RequestPermissionAsync`, and on approval spawns a concurrent execution task (on denial writes `ToolCallCompleted` with "Permission denied.").
     - Awaits all execution tasks via `Task.WhenAll`, then calls `channel.Writer.Complete()`.
  4. **Consumer**: `await foreach` over `channel.Reader.ReadAllAsync(ct)`, yielding each event.
- [x] Collect results in a `ConcurrentDictionary<string, FunctionResultContent>`. After the channel drains, populate `resultMessage.Contents` in the original call order.

### Phase 3: New `ToolAwaitingApproval` event

- [x] Add `ToolAwaitingApproval` event type to `AgentLoopEvent.cs` with `CallId` property.
- [x] Emit `ToolAwaitingApproval` from the approval pipeline in `InvokeAllAsync`, right before calling `RequestPermissionAsync`.
- [x] Remove the `SetLastToolAwaitingApproval()` call from the permission callback in the TUI layer (wherever it currently lives — likely `UrSession.BuildWrappedCallbacks` or the REPL).

### Phase 4: Update EventRouter

- [x] Handle `ToolAwaitingApproval` in `RouteMainEvent`: look up the `ToolRenderable` by `CallId` from `_toolCallMap` and call `SetAwaitingApproval()`.
- [x] Handle `ToolAwaitingApproval` in `RouteSubagentEvent` if subagent tools can also require approval (they share the parent's callbacks).
- [x] Remove `_lastStartedTool` field and `SetLastToolAwaitingApproval()` method.
- [x] Verify that interleaved `ToolCallStarted` events from concurrent tools render correctly — each gets its own `ToolRenderable` added to the `EventList`, which should already work since they're keyed by `CallId`.

### Phase 5: Validation

- [ ] Manual test: two `run_subagent` calls in one message — verify both appear in the UI simultaneously and run concurrently.
- [ ] Manual test: a mix of auto-allowed and approval-required tools — verify auto-allowed start immediately, approval-required appear one at a time, and approved ones start before remaining approvals finish.
- [ ] Manual test: deny a permission-required tool — verify it shows as denied and other tools continue.
- [x] Verify `resultMessage` contains all results in correct call order after completion.
- [x] Run `dotnet build` and `dotnet test` (if tests exist).
