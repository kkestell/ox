# TUI Renderable Abstraction

## Goal

Replace the TUI's append-only `RenderEvent()` switch with a retained-mode rendering model built around `IRenderable` — live objects whose content can change between redraws. Switch from raw console output to a full-screen alternate buffer viewport.

## Desired outcome

- A clean `IRenderable` interface that all display elements implement.
- Streaming assistant messages update a single `TextRenderable` by appending chunks.
- Tool calls use a single `ToolRenderable` that transitions through lifecycle states (started -> awaiting approval -> completed), rendering in-place.
- Subagent events are grouped in a `SubagentRenderable`, making it visually obvious which events come from which agent. No more `>>>>` prefix.
- The conversation is an `EventList` renderable — push new child renderables, and the display updates automatically.
- Full-screen alternate buffer with auto-scroll and a single-row input area at the bottom.

## How we got here

The current TUI writes events directly to the console as they arrive. This creates several pain points:

1. **Tool lifecycle is split**: `ToolCallStarted` and `ToolCallCompleted` are rendered as separate lines with no visual connection. There's no way to update a tool's display when approval is needed or when it completes.
2. **Subagent output is hard to follow**: The `>>>>` prefix is the only visual cue that events come from a subagent. When subagents run tools, it's easy to lose track of context.
3. **No retained state**: The TUI has no model of what's on screen — it only appends. This makes it impossible to update previously-rendered content.

Three rendering approaches were considered. The user chose the full-screen viewport (alternate screen buffer) for total layout control, with auto-scroll only (no manual scrollback for now) and a single-row input area.

## Approaches considered

### Option 1 — Smart tail (ANSI cursor rewrite)

- Summary: Only the most recent renderable is "live" — updated in-place using ANSI cursor-up + clear. Previous renderables finalize into scrollback.
- Pros: Lightweight, preserves terminal scroll history, no dependency.
- Cons: Can't update content that scrolled off. Only one renderable is live at a time. Complex cursor math for multi-line content.
- Failure modes: Terminal scrollback corruption when line counts change unexpectedly.

### Option 2 — Full-screen viewport (chosen)

- Summary: Switch to the alternate screen buffer. Maintain full display state, redraw on change. The TUI owns the entire screen.
- Pros: Total layout control. Any renderable can update at any time. Clean separation between conversation viewport and input area. Natural home for future features (scroll, status bar).
- Cons: Loses terminal scroll history. Must handle terminal cleanup on exit/crash. More code than the current approach.
- Failure modes: Terminal left in alternate buffer on unhandled crash (mitigated by cleanup handlers).

### Option 3 — Spectre.Console

- Summary: Use Spectre.Console's Live rendering for updatable display.
- Pros: Well-tested, handles terminal quirks.
- Cons: New dependency (~2MB), may fight with raw Console I/O, AOT compatibility uncertain.

## Recommended approach

Full-screen viewport (Option 2).

- Why: gives us the retained-mode rendering that the renderable abstraction requires. Any renderable can update at any time, the display engine redraws the visible tail, and the input area is cleanly separated.
- Key tradeoffs: we lose terminal scrollback (acceptable — the conversation model replaces it), and we take on more rendering code. The added code is well-structured and lives in its own module.

## Related code

- `src/Ur.Tui/Program.cs` — The entire current TUI. Contains `RenderEvent()`, the REPL loop, permission callback, key monitoring, and `CancellableReadLine`. All of this will be refactored.
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — The event types (`ResponseChunk`, `ToolCallStarted`, `ToolCallCompleted`, `SubagentEvent`, etc.) that renderables will consume. Not modified.
- `src/Ur/Permissions/TurnCallbacks.cs` — Callback wiring for permissions and subagent events. Not modified, but the TUI's callback implementations change.
- `src/Ur/AgentLoop/ToolInvoker.cs` — Yields `ToolCallStarted` then `ToolCallCompleted` per tool call. Defines the lifecycle that `ToolRenderable` must track.
- `src/Ur/AgentLoop/SubagentRunner.cs` — Relays subagent events via `TurnCallbacks.SubagentEventEmitted`. Defines the event flow that `SubagentRenderable` consumes.
- `src/Ur/Tools/SubagentTool.cs` — The `run_subagent` tool. Its `ToolCallStarted`/`ToolCallCompleted` on the main stream bracket the subagent's callback events.

## Current state

- The TUI is a single 464-line file (`Program.cs`) with no rendering abstraction.
- Events are dispatched via a `switch` statement in `RenderEvent()` that writes directly to `Console`.
- State is tracked with static bools (`_parentAtLineStart`, `_subagentAtLineStart`, `_inSubagentContext`).
- Subagent events arrive via `TurnCallbacks.SubagentEventEmitted` callback (not the main `await foreach` stream). The main stream sees `ToolCallStarted`/`ToolCallCompleted` for the `run_subagent` tool.
- Permission prompts pause the key monitor (`_pauseKeyReader`) and read from stdin inline.
- No ANSI cursor manipulation beyond color codes. No alternate screen buffer.
- `PublishAot` is enabled in the csproj — all code must be AOT-compatible (no reflection).

## Structural considerations

**Hierarchy**: Renderables form a clean tree: `EventList` (root) -> `[TextRenderable | ToolRenderable | SubagentRenderable]` -> (SubagentRenderable contains its own child renderables). The display engine (`Viewport`) sits above renderables — it calls `Render()` and writes to the terminal. `Terminal` sits below — raw ANSI operations. Three layers: `Terminal` <- `Viewport` <- `Program` (event routing).

**Abstraction**: `IRenderable` is purely a display contract — "give me lines, tell me when you change." Renderables don't know about the viewport or terminal. The viewport doesn't know about tool calls or subagents. Clean separation.

**Modularization**: The current monolithic `Program.cs` splits into focused modules: `Terminal` (ANSI primitives), `Viewport` (display engine), individual renderable types, and `Program` (orchestration/routing). Each has a single purpose.

**Encapsulation**: Each renderable owns its state and rendering logic. `ToolRenderable` encapsulates the lifecycle state machine. `SubagentRenderable` encapsulates child renderable management. The viewport only sees `IRenderable`.

## Refactoring

The existing `RenderEvent()` switch and static state variables are replaced entirely — this is a rewrite of the rendering path, not a modification. The REPL loop structure (`Main`) is preserved but adapted to use the viewport. `BuildCallbacks()` is rewritten to route events to renderables instead of calling `RenderEvent()`.

The core `Ur` library (`AgentLoopEvent`, `TurnCallbacks`, `ToolInvoker`, `SubagentRunner`) is not modified. The rendering change is entirely within `Ur.Tui`.

## Implementation plan

### Phase 1: Terminal abstraction

- [ ] Create `src/Ur.Tui/Rendering/Terminal.cs` — static class wrapping low-level ANSI operations:
  - `EnterAlternateBuffer()` / `ExitAlternateBuffer()` — `\e[?1049h` / `\e[?1049l`
  - `MoveCursor(row, col)` — `\e[{row};{col}H`
  - `ClearScreen()` — `\e[2J`
  - `ClearLine()` — `\e[2K`
  - `HideCursor()` / `ShowCursor()` — `\e[?25l` / `\e[?25h`
  - `Write(row, col, text)` — move + write in one call
  - `GetSize()` — returns `(width, height)` from `Console.WindowWidth/Height`
  - Comments should note the ANSI escape code being used and what terminal support is assumed.

### Phase 2: IRenderable and concrete types

- [ ] Create `src/Ur.Tui/Rendering/IRenderable.cs`:
  ```csharp
  // Core display contract. Every visual element in the conversation implements this.
  // Renderables are "live" — their content can change, and the Changed event signals
  // the display engine to redraw.
  interface IRenderable
  {
      // Returns lines to display, each pre-wrapped to fit within availableWidth.
      // Lines must not exceed availableWidth visible characters (ANSI codes excluded).
      IReadOnlyList<string> Render(int availableWidth);

      // Raised when content changes and the display should redraw.
      event Action? Changed;
  }
  ```

- [ ] Create `src/Ur.Tui/Rendering/TextRenderable.cs`:
  - Implements `IRenderable`. Used for streaming assistant messages and user messages.
  - `Append(string chunk)` — appends text and fires `Changed`.
  - `Render()` splits accumulated text into word-wrapped lines.
  - Optional: prefix/style (e.g., dim for user messages vs. normal for assistant).

- [ ] Create `src/Ur.Tui/Rendering/ToolRenderable.cs`:
  - Implements `IRenderable`. Represents the full lifecycle of a single tool call.
  - Internal state enum: `Started`, `AwaitingApproval`, `Completed`.
  - Constructed with `ToolCallStarted` data (tool name, formatted args).
  - `SetAwaitingApproval()` — transitions state, fires `Changed`.
  - `SetCompleted(bool isError)` — transitions to final state, fires `Changed`.
  - `Render()` returns a single line styled based on state:
    - Started: `{DarkGray}tool_name(arg: "val"){Reset}`
    - AwaitingApproval: `{DarkGray}tool_name(arg: "val"){Reset} {Yellow}[awaiting approval]{Reset}`
    - Completed: `{DarkGray}tool_name(arg: "val") -> ok{Reset}` or `-> error`
  - All rendered in dark gray like today, but with state-dependent suffixes.

- [ ] Create `src/Ur.Tui/Rendering/SubagentRenderable.cs`:
  - Implements `IRenderable`. Groups all events from a single subagent run.
  - Contains an inner list of child `IRenderable` objects (the subagent's own text, tools, etc.).
  - `AddChild(IRenderable child)` — appends and subscribes to child's `Changed`.
  - `Render()` returns:
    - A header line: `{DarkGray}--- subagent {id} ---{Reset}` (or similar visual marker)
    - All child renderables' lines, indented by 2 spaces
    - A footer line when completed: `{DarkGray}--- subagent complete ---{Reset}`
  - `SetCompleted()` — marks the subagent as done, fires `Changed`.
  - `SubagentId` property for event routing.

- [ ] Create `src/Ur.Tui/Rendering/EventList.cs`:
  - Implements `IRenderable`. The root container for the conversation.
  - `Add(IRenderable child)` — appends child, subscribes to its `Changed`, fires own `Changed`.
  - `Render()` concatenates all children's rendered lines.
  - This is what the viewport renders.

### Phase 3: Viewport / display engine

- [ ] Create `src/Ur.Tui/Rendering/Viewport.cs`:
  - Owns the `EventList` root renderable.
  - Manages the screen layout: conversation area (rows 1 through H-1), input row (row H).
  - `Redraw()` method:
    1. Calls `_root.Render(width)` to get all lines.
    2. Takes the last `viewportHeight` lines (auto-scroll behavior).
    3. Writes each line to its screen row using `Terminal.Write()`, clearing any leftover content.
    4. Positions cursor at the input row.
  - Debounced redraw: when a renderable fires `Changed`, set a dirty flag. A background loop calls `Redraw()` at ~30fps when dirty. This prevents per-character redraws during streaming.
  - `SetInputPrompt(string prompt)` — updates the input row text (e.g., `> `, `Allow write? y/n: `, `[running...]`).
  - `Start()` — enters alternate buffer, hides cursor, does initial draw.
  - `Stop()` — shows cursor, exits alternate buffer. Must be called on any exit path.

### Phase 4: Event routing (rewrite Program.cs)

- [ ] Replace `RenderEvent()` and static state variables with an event router:
  - Maintain a `Dictionary<string, SubagentRenderable>` mapping subagent IDs to their renderables.
  - Maintain a reference to the "current" `TextRenderable` (for streaming chunks) and `ToolRenderable` (for correlating started/completed events by `CallId`).
  - Maintain a `Dictionary<string, ToolRenderable>` mapping `CallId` to renderable for tool correlation.
  - Event routing logic:
    - `ResponseChunk` -> append to current `TextRenderable` (create one if needed, add to `EventList`).
    - `ToolCallStarted` -> create `ToolRenderable`, add to `EventList`, store in call map. Clear current `TextRenderable` reference (text block is done).
    - `ToolCallCompleted` -> look up `ToolRenderable` by `CallId`, call `SetCompleted()`.
    - `SubagentEvent { Inner: ResponseChunk }` -> route to the subagent's `TextRenderable` inside its `SubagentRenderable`.
    - `SubagentEvent { Inner: ToolCallStarted }` -> create `ToolRenderable` inside the `SubagentRenderable`.
    - `SubagentEvent { Inner: ToolCallCompleted }` -> update the subagent's `ToolRenderable`.
    - `SubagentEvent { Inner: TurnCompleted }` -> call `SetCompleted()` on the `SubagentRenderable`.
    - `TurnCompleted` -> no-op (the conversation just sits ready for next input).
    - `Error` -> create a `TextRenderable` with error styling, add to `EventList`.
  - For the `run_subagent` tool specifically: when `ToolCallStarted { ToolName: "run_subagent" }` arrives, create a `SubagentRenderable` (not a `ToolRenderable`) and store it keyed by the call ID. The subsequent `SubagentEvent` callbacks populate it. When `ToolCallCompleted` arrives for `run_subagent`, finalize the `SubagentRenderable`.

- [ ] Rewrite `BuildCallbacks()`:
  - `SubagentEventEmitted`: route the `SubagentEvent` to the appropriate `SubagentRenderable` (look up by `SubagentId`). The viewport auto-redraws via the `Changed` event chain.
  - `RequestPermissionAsync`: transition the relevant `ToolRenderable` to `AwaitingApproval` state. Update the viewport's input prompt to show the permission question. Read user input from the input area. Parse the response. Transition the `ToolRenderable` based on the decision. Restore the input prompt.

- [ ] Rewrite the REPL loop in `Main()`:
  - On startup: create `Viewport`, call `Start()`.
  - Register cleanup: `AppDomain.ProcessExit` and `Console.CancelKeyPress` both call `viewport.Stop()` to restore the terminal.
  - Idle state: input row shows `> `, user types, Enter sends.
  - During a turn: input row shows `[running... Esc to cancel]`. Event stream routes to renderables. Viewport auto-redraws.
  - After turn: restore `> ` prompt.

- [ ] Rewrite `CancellableReadLine` to work within the viewport:
  - Types characters directly into the input row (row H) rather than free-form console I/O.
  - Backspace clears within the input row.
  - The viewport's `SetInputPrompt()` positions the cursor correctly.

- [ ] Adapt `MonitorEscapeKeyAsync`:
  - Same logic, but Escape handling clears the input row and shows `[cancelled]` briefly.
  - Permission prompt pausing (`_pauseKeyReader`) may still be needed if the permission prompt reads from the input area.

### Phase 5: Cleanup and edge cases

- [ ] Remove all static state: `_parentAtLineStart`, `_subagentAtLineStart`, `_inSubagentContext`, `_pauseKeyReader`. These are replaced by renderable state and viewport state.
- [ ] Remove the `>>>>` prefix logic entirely.
- [ ] Handle terminal resize: detect `Console.WindowWidth`/`Console.WindowHeight` changes between redraws and adjust layout. The debounced redraw loop naturally picks up size changes.
- [ ] Ensure `viewport.Stop()` is called on all exit paths:
  - Normal exit (EOF / Ctrl+D)
  - Ctrl+C
  - Fatal error
  - Unhandled exception (`AppDomain.CurrentDomain.UnhandledException`)
- [ ] Verify AOT compatibility: no reflection in any new code. `IRenderable` uses no generics that would require runtime code generation.

## Impact assessment

- **Code paths affected**: Only `src/Ur.Tui/`. The core `Ur` library is not modified. The `Ur.Cli` project is not affected (it has its own rendering).
- **New files**: ~6 files in `src/Ur.Tui/Rendering/` (Terminal, IRenderable, TextRenderable, ToolRenderable, SubagentRenderable, EventList, Viewport).
- **Deleted code**: `RenderEvent()`, all static state variables, the `>>>>` prefix logic.
- **No dependency changes**: Pure ANSI escape sequences, no new packages.

## Validation

- **`make inspect`**: Run and fix any issues in `inspection-results.txt` before committing.
- **Manual verification**:
  - Start a conversation, verify the full-screen viewport renders correctly.
  - Send a message that triggers tool calls — verify the `ToolRenderable` transitions through states.
  - Trigger a permission prompt — verify it appears in the input row, and the tool renderable shows "awaiting approval."
  - Run a subagent — verify events are grouped in a `SubagentRenderable` with visual indentation, no `>>>>` prefix.
  - Press Escape during a turn — verify cancellation works and the display recovers.
  - Press Ctrl+C — verify the terminal is restored to normal.
  - Resize the terminal during a streaming response — verify the display adapts.
- **boo verification**: Use boo to run the TUI and confirm behavior end-to-end.

## Open questions

- What visual style should the `SubagentRenderable` use for grouping? Options: indented with dashed header/footer lines, box-drawing characters, color-coded background, or simple indent. The plan assumes dashed lines + 2-space indent — easy to change during implementation.
- Should user messages (the `> input` lines) also be renderables in the EventList, or just rendered statically before the turn starts? Making them renderables keeps the model uniform. The plan assumes they are renderables.
