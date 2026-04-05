# Subagent Event Relay

## Goal

Surface subagent events in the parent TUI so users can see what a subagent is doing while it runs, rather than only seeing the final result.

## Desired outcome

All events emitted by a subagent's internal loop are relayed to the TUI and visually distinguished from parent-agent events. Initially, all relayed events are prefixed with `>>>>` to prove the plumbing works. Future work will use the attached SubagentId to group, indent, or otherwise compose concurrent subagent streams cleanly.

## How we got here

The subagent transcript shows a UX problem: the TUI renders `[tool: run_subagent → ok]` and then the final accumulated text, with no visibility into what the subagent actually did — no intermediate tool calls, no streaming response. This will become worse when subagents run concurrently. The fix requires relaying subagent events up through the existing callback chain with a subagent identity attached.

There is also a specific bug: when the subagent requests permission for a tool (e.g., `write_file`), the permission prompt surfaces correctly — because `RequestPermissionAsync` in `TurnCallbacks` flows all the way down to SubagentRunner. But the `ToolCallStarted` and `ToolCallCompleted` events for that same tool call are silently consumed by SubagentRunner and never reach the TUI. The result is a permission approval prompt with no corresponding tool status lines — the user approves something that then appears to have never happened.

## Approaches considered

### Option A: Add `SubagentId` field to `AgentLoopEvent` base

- Summary: Every event gets an optional `string? SubagentId { get; init; }` field. SubagentRunner sets it before relaying. TUI checks for non-null value.
- Pros: Simple. No new types.
- Cons: Pollutes every event type with a field only relevant to ~20% of events. Tagging requires reconstructing each concrete event type (painful with `required` init-only properties and sealed classes).
- Failure modes: Drift — future events added without SubagentId support silently go untagged.

### Option B: `SubagentEvent` wrapper (recommended)

- Summary: A new `SubagentEvent : AgentLoopEvent` wraps an inner `AgentLoopEvent` and carries a `SubagentId`. SubagentRunner wraps each relayed event. TUI unwraps and renders with a prefix.
- Pros: Existing event types untouched. Wrapping is trivial (`new SubagentEvent { SubagentId = id, Inner = evt }`). Naturally composable — future rendering can peel layers or group by SubagentId without touching the existing switch.
- Cons: One extra indirection in the TUI rendering switch.
- Failure modes: If the TUI switch forgets to handle `SubagentEvent`, events are silently swallowed — but the compiler will warn about unhandled cases if pattern matching is exhaustive.

### Option C: Separate relay channel (out-of-band queue)

- Summary: SubagentRunner pushes events into a `Channel<AgentLoopEvent>` that the TUI drains concurrently.
- Pros: Non-blocking; naturally handles concurrent subagents.
- Cons: Overengineered for the current goal; threading complexity without benefit until concurrent subagents are actually supported.

## Recommended approach

**Option B — `SubagentEvent` wrapper + `TurnCallbacks` relay callback.**

`TurnCallbacks` already flows all the way down to `SubagentRunner` via the constructor. Adding one new optional delegate to `TurnCallbacks` requires no structural changes to any intermediate layer. `SubagentRunner` generates a short ID, wraps events, and invokes the callback. The TUI supplies the callback and handles `SubagentEvent` in its render switch.

- Why this approach: Zero changes to existing event types or the ToolInvoker. The callback flows through an already-established channel. The wrapper pattern composes cleanly with future grouping/indentation work.
- Key tradeoffs: The relay callback fires synchronously on the subagent's execution thread. For a single subagent this is fine; concurrent subagents will interleave writes. That is acceptable for a proof-of-concept and will be addressed in the follow-up concurrent-subagent plan.

## Related code

- `src/Ur/AgentLoop/AgentLoopEvent.cs` — Where the new `SubagentEvent` wrapper type is added
- `src/Ur/Permissions/TurnCallbacks.cs` — Where the new `SubagentEventEmitted` relay callback is added
- `src/Ur/AgentLoop/SubagentRunner.cs` — Where the SubagentId is generated and events are wrapped and relayed
- `src/Ur/AgentLoop/ISubagentRunner.cs` — Interface is unchanged (relay is via callbacks, not the return type)
- `src/Ur.Tui/Program.cs` — Where `TurnCallbacks` is constructed and `RenderEvent` handles `SubagentEvent`
- `src/Ur.Cli/Commands/ChatCommand.cs` — Where `TurnCallbacks` is constructed; needs matching `SubagentEvent` handling

## Current state

- `SubagentRunner.RunAsync` consumes all inner loop events silently (lines 62–83). Only `ResponseChunk.Text` is accumulated; everything else is dropped.
- `TurnCallbacks` has one callback: `RequestPermissionAsync`. It already flows to SubagentRunner via its constructor.
- `ToolInvoker.InvokeAllAsync` yields `ToolCallStarted` / `ToolCallCompleted` around tool execution; there is no mechanism for a tool to emit intermediate events during execution.
- TUI `RenderEvent` is a flat switch over the five existing event types — no concept of event source or nesting.

## Structural considerations

**Hierarchy:** The relay callback is in `Ur.Permissions.TurnCallbacks`, which is already a shared boundary type. Adding a callback here keeps the dependency arrows the same — SubagentRunner uses it, TUI provides it.

**Abstraction:** `SubagentEvent` is a thin envelope. It does not encode rendering decisions; the TUI decides how to present it. This keeps the event model neutral and the rendering layer in control.

**Modularization:** The relay logic lives entirely in SubagentRunner — the one place that knows a subagent is running. No other layer needs to know that events came from a child agent.

**Encapsulation:** `ISubagentRunner` stays unchanged. The relay flows through TurnCallbacks (already shared), not through the interface boundary.

## Implementation plan

- [x] Add `SubagentEvent : AgentLoopEvent` to `src/Ur/AgentLoop/AgentLoopEvent.cs` with `required string SubagentId { get; init; }` and `required AgentLoopEvent Inner { get; init; }`. Add a doc comment explaining it is a relay envelope.
- [x] Add `Func<AgentLoopEvent, ValueTask>? SubagentEventEmitted { get; init; }` to `src/Ur/Permissions/TurnCallbacks.cs`. Doc comment: fires for each event relayed from a running sub-agent, tagged with its SubagentId.
- [x] In `SubagentRunner.RunAsync`: generate a short ID (`Guid.NewGuid().ToString("N")[..8]`), and after calling `agentLoop.RunTurnAsync`, for each event before switching on it, wrap it in `SubagentEvent` and invoke `callbacks?.SubagentEventEmitted?.Invoke(wrapped)` (fire-and-forget via `.AsTask().GetAwaiter().GetResult()` for now, since RunAsync is not IAsyncEnumerable and ValueTask can't be awaited in a switch body without refactoring — or use `.ConfigureAwait(false)` in a local helper).
- [x] In `src/Ur.Tui/Program.cs`, add `SubagentEventEmitted` to the `TurnCallbacks` construction that calls `RenderEvent` with the wrapped event.
- [x] In TUI `RenderEvent`, add a `SubagentEvent` case that prepends `>>>> ` to all output from the inner event. The simplest approach: capture console output or just call a helper `RenderEvent(subagentEvent.Inner)` after printing the prefix on its own line. Since tool status lines use `Console.WriteLine`, and response chunks use `Console.Write`, the cleanest option is to print `>>>> ` as a prefix before delegating — implemented by printing the prefix then delegating to `RenderEvent(inner)`.
- [x] Add matching `SubagentEvent` handling in `src/Ur.Cli/Commands/ChatCommand.cs` event render switch (same pattern as TUI).
- [x] Update tests in `tests/Ur.Tests/BuiltinToolTests.cs` if any existing test asserts that subagent events are absent from the parent stream.

## Validation

- Tests: Run `make inspect` and fix any issues. Check `BuiltinToolTests.cs` for tests covering SubagentTool behavior — update any that need to account for relayed events.
- Manual verification: Run a subagent task (e.g., "read a file") and confirm that the subagent's tool calls and response appear in the TUI prefixed with `>>>>`, while the parent agent's events render normally without the prefix.

## Gaps and follow-up

- **Concurrent subagents:** The relay callback fires on the subagent thread. When two subagents run in parallel, their `>>>>` prefixed output will interleave. The SubagentId field is the foundation for future grouping — a follow-up plan will address rendering concurrent streams cleanly.
- **ValueTask relay in synchronous context:** `SubagentEventEmitted` returns `ValueTask`. The relay call in SubagentRunner's switch needs a clean await pattern. Since `RunAsync` is `async Task<string>`, we can `await` the callback inline — no blocking needed. Confirm this works without deadlock.
- **CLI rendering:** The CLI (`ChatCommand.cs`) will need the same `SubagentEvent` case added. It should match TUI behavior for now.
