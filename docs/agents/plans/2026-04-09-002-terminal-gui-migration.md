# Migrate Ox from Te to Terminal.Gui

## Goal

Replace Te (the custom terminal primitives library) with Terminal.Gui v2 as Ox's TUI framework. Remove Te from the solution entirely. Adopt Terminal.Gui's application model, layout system, input handling, and drawing API so that Ox benefits from a mature, community-maintained widget library and stops paying the maintenance cost of custom terminal infrastructure.

## Desired outcome

Ox is a Terminal.Gui application. The solution contains Ox and Ur (no Te, no Fe). The UI looks and behaves the same as today -- conversation stream, input area with autocomplete, sidebar, throbber, tool call rendering, subagent rendering -- but is built on Terminal.Gui Views, Pos/Dim layout, and the Terminal.Gui main loop. Future UI work (dialogs, menus, more complex layouts) can leverage Terminal.Gui's 70+ built-in views.

## How we got here

Kyle wants both the richer widget set Terminal.Gui provides and to stop maintaining Te's low-level terminal primitives. The Fe framework plan (custom retained-mode widget framework on top of Te) is superseded by this migration -- Terminal.Gui fills the same role with a mature, tested implementation.

Key decisions:

- **Go native Terminal.Gui**: Rebuild the UI using Terminal.Gui's idioms, not a shim layer.
- **Ur stays pure**: No TUI dependency in Ur. Only Ox references Terminal.Gui.
- **Remove Te entirely**: No thin wrapper, no compatibility layer. Clean break.
- **Fe plan is dead**: `2026-04-09-001-fe-tui-framework.md` is obsolete.

## Research

### Terminal.Gui v2 API (beta)

- **Latest**: `2.0.0-beta.149` (April 2026). Targets net10.0. API mostly stable; breaking changes possible before GA. Fine for personal projects.
- **Application model**: Instance-based. `Application.Create().Init()` returns `IApplication`. `app.Run<T>()` pushes a Runnable onto the session stack. `app.Dispose()` restores terminal.
- **Views**: Tree of Views with SubViews. Each View has Frame (position/size relative to parent), Content Area (logical content), Viewport (visible portion for scrolling).
- **Layout**: Pos/Dim relational system. `Pos.Absolute()`, `Pos.Percent()`, `Pos.Center()`, `Pos.Left(view)`, `Dim.Fill()`, `Dim.Percent()`, `Dim.Auto()`, `Dim.Func()`. More like WinForms anchoring than flexbox.
- **Custom drawing**: Override `OnDrawingContent()` with `Move(col, row)`, `AddStr(text)`, `SetAttribute(attr)`. Deferred rendering -- drawing goes to back buffer, written to terminal each iteration.
- **Colors**: `Attribute(foreground, background, style)`. Supports TrueColor with 16-color fallback. `Scheme` maps `VisualRole` (Normal, Focus, Active, etc.) to Attributes.
- **Input**: `Key` class with EventType/ModifierKey. Three-tier command binding (Command → KeyBinding → Application). Event routing: Driver → Application → focused View hierarchy → OnKeyDown → KeyBindings.
- **Threading**: Single-threaded UI. `App.Invoke()` marshals from background threads. `await` continuations resume on main thread. `App.AddTimeout()` for timers.
- **Scrolling**: Built into every View. Set content size, enable scroll bars, done.
- **Mouse**: `MouseFlags` enum, `MouseBindings` for declarative binding, `OnMouseEvent()` for direct handling.

### Ox architecture (current)

- **Rendering pipeline**: `IRenderable.Render(width) → CellRow[] → ConsoleBuffer.SetCell() → Console.Out` (ANSI diffs).
- **Input pipeline**: `TerminalInputSource → InputCoordinator (channel) → InputReader → key dispatch`.
- **Layout**: Hard-coded in `Viewport.BuildFrame()` -- conversation area (top, flexible), input area (bottom, 5 rows), sidebar (right, optional).
- **Event routing**: `AgentLoopEvent → EventRouter → create/update renderables → Changed event → dirty flag → redraw`.
- **Key files**: Program.cs (354 lines), Viewport.cs (732 lines), InputReader.cs (205 lines), EventRouter.cs (243 lines), plus renderables (TextRenderable, ToolRenderable, SubagentRenderable, EventList, Sidebar, etc.).

## Structural considerations

### PHAME analysis

- **Hierarchy**: Terminal.Gui sits where Te + Ox's rendering layer currently live. Ur remains independent. The dependency chain becomes: Terminal.Gui (NuGet) ← Ox → Ur. Clean.
- **Abstraction**: Terminal.Gui's View is the right abstraction level. Custom drawing via `OnDrawingContent()` gives us the same cell-level control Te's ConsoleBuffer provided, but within the framework's layout and event systems.
- **Modularization**: Ox's Rendering/ directory gets replaced by Views/ (or similar). EventRouter stays -- it translates agent events into UI mutations, independent of rendering technology. InputReader is absorbed by Terminal.Gui's input system.
- **Encapsulation**: Terminal.Gui manages the terminal (alternate buffer, cursor, raw mode). Ox no longer needs Terminal.cs or any direct ANSI escape sequences.

### Key architectural mapping

| Ox/Te Concept                 | Terminal.Gui Equivalent                                        |
| ----------------------------- | -------------------------------------------------------------- |
| `ConsoleBuffer`               | Terminal.Gui driver (managed internally)                       |
| `Viewport`                    | `Toplevel` / `Window` with child Views + Pos/Dim layout        |
| `IRenderable`                 | Custom `View` subclass with `OnDrawingContent()`               |
| `CellRow`                     | `Move()` + `AddStr()` + `SetAttribute()` in draw override      |
| `EventList`                   | Custom scrollable `View` containing conversation content       |
| `InputReader`                 | `TextField` (or custom `View`) with key bindings               |
| `Terminal` (escape sequences) | Managed by Terminal.Gui driver                                 |
| `Color` / `TextDecoration`    | `Attribute` / `Style`                                          |
| `InputCoordinator`            | Terminal.Gui input routing (+ `App.Invoke()` for agent events) |
| Resize polling timer          | Automatic in Terminal.Gui                                      |
| Throbber timer                | `App.AddTimeout()`                                             |

### The conversation View design

The conversation area is the most complex part of the UI. It needs streaming text, dynamic tool call colors, tree connectors, word wrapping, and auto-scroll. Two options:

**Option A: Single custom-drawn View** -- One View for the entire conversation, managing its own content size and drawing all messages in `OnDrawingContent()`. Word wrapping, scroll position, and content layout are computed internally. Terminal.Gui handles the View's position/size within the window and provides scrollbar infrastructure.

**Option B: View-per-message** -- Each message, tool call, and subagent block is a separate View in a container. Terminal.Gui handles stacking and scrolling.

**Recommendation: Option A.** The conversation has unique rendering requirements (streaming text growth, tree connectors between tool calls, dynamic color updates on tool state changes). A single View with built-in scrolling and custom drawing keeps this logic centralized and avoids the overhead of hundreds of child Views in long conversations. This is idiomatic Terminal.Gui -- custom drawing is a first-class feature, not a workaround.

## Related code

### Files to replace/remove

- `src/Te/` -- Entire Te project. Removed from solution and deleted.
- `src/Ox/Rendering/Viewport.cs` -- Replaced by Terminal.Gui layout + custom Views.
- `src/Ox/Rendering/IRenderable.cs` -- Replaced by Terminal.Gui View subclasses.
- `src/Ox/Rendering/CellRow.cs` -- Replaced by Terminal.Gui drawing API.
- `src/Ox/Rendering/EventList.cs` -- Replaced by `ConversationView` (custom View).
- `src/Ox/Rendering/TextRenderable.cs` -- Logic moves into ConversationView's drawing.
- `src/Ox/Rendering/ToolRenderable.cs` -- Logic moves into ConversationView's drawing.
- `src/Ox/Rendering/SubagentRenderable.cs` -- Logic moves into ConversationView's drawing.
- `src/Ox/Rendering/TreeChrome.cs` -- Drawing helpers reimplemented using Terminal.Gui API.
- `src/Ox/Rendering/Sidebar.cs` -- Replaced by a Terminal.Gui View.
- `src/Ox/Rendering/ContextSection.cs` -- Replaced by a Terminal.Gui View.
- `src/Ox/Rendering/TodoSection.cs` -- Replaced by a Terminal.Gui View.
- `src/Ox/Rendering/Terminal.cs` -- Terminal.Gui manages the terminal directly.
- `src/Ox/InputReader.cs` -- Replaced by Terminal.Gui input Views + key handling.

### Files to modify significantly

- `src/Ox/Ox.csproj` -- Remove Te reference, add Terminal.Gui NuGet package.
- `src/Ox/Program.cs` -- Rewrite to use Terminal.Gui application model. Keep agent loop integration.
- `src/Ox/EventRouter.cs` -- Keep routing logic but target new View types instead of IRenderables.
- `src/Ox/AutocompleteEngine.cs` -- Keep; wire to new input View.
- `src/Ox/PermissionHandler.cs` -- Adapt to use Terminal.Gui dialogs or inline prompts.

### Files that stay unchanged

- `src/Ur/` -- Entire Ur project. No changes.
- `src/Ox/Configuration/` -- Configuration logic unrelated to rendering.

## Implementation plan

### Phase 1: Project setup

- [x] Add Terminal.Gui v2 NuGet package to Ox.csproj: `dotnet add src/Ox/Ox.csproj package Terminal.Gui --prerelease`
- [x] Remove Te project reference from Ox.csproj.
- [x] Remove Te project from the solution file (`Ox.slnx`).
- [x] Delete the `src/Te/` directory entirely.
- [x] Verify the solution builds (it won't yet -- this creates the compilation errors we'll fix in subsequent phases).

### Phase 2: Application shell

- [x] Create `src/Ox/Views/OxApp.cs` -- a `Runnable` (or `Toplevel` subclass) that serves as the root window. Layout:
  - Left/main area: `ConversationView` (fills available space vertically, with `Dim.Fill() - inputAreaHeight`)
  - Bottom area: `InputAreaView` (fixed height, ~5 rows, full width minus sidebar)
  - Right area: `SidebarView` (fixed width up to 36 cols, optional/collapsible, full height)
  - Use Pos/Dim to express this layout declaratively.
- [x] Rewrite `Program.cs` / `TuiService`:
  - Bootstrap with `Application.Create().Init()`.
  - Handle the two-phase lifecycle: configuration phase uses plain `Console.ReadLine()` before Terminal.Gui starts; then `app.Run<OxApp>()` for the TUI phase.
  - The REPL loop moves into `OxApp` -- it receives user input from the input View, runs the agent turn on a background task, and routes events back via `App.Invoke()`.
  - Wire Ctrl+C / process exit for clean shutdown via `app.RequestStop()` or `Application.Shutdown()`.
- [x] Wire signal handling (SIGINT, ProcessExit) to cleanly stop Terminal.Gui.

### Phase 3: Conversation View

- [x] Create `src/Ox/Views/ConversationView.cs` -- a custom `View` that renders the conversation stream.
  - Override `OnDrawingContent()` to draw all messages, tool calls, and subagent blocks.
  - Maintain an internal list of conversation entries (replacing EventList's list of IRenderables). Each entry is a data model (not a View) holding: text content, entry type (user message, assistant text, tool call, subagent), styling info, tool state/color.
  - Implement word-wrapping logic (port from `TextRenderable.WrapText()`).
  - Implement tree chrome rendering (port circle prefixes, `└─` connectors from `TreeChrome` and `ToolRenderable`).
  - Use `SetContentSize()` to report total content height. Terminal.Gui's built-in scrolling handles the rest.
  - Auto-scroll to bottom on new content: set `Viewport` to bottom after appending.
  - Expose methods: `AddUserMessage(string)`, `AddTextBlock(...)`, `AddToolCall(...)`, `AddSubagent(...)`, `UpdateToolState(...)`, `AppendText(...)`.
  - Call `SetNeedsDraw()` when content changes.

- [x] Port color mapping from Te's `Color` to Terminal.Gui's `Color`/`Attribute`:
  - Te `Color.Blue` → Terminal.Gui `Color.Blue` (same concept, different type)
  - Te `Color.FromIndex(n)` → Terminal.Gui `Color.FromIndex(n)` or `Color.FromRgb(...)` if v2 uses TrueColor
  - Te `TextDecoration.Bold` → Terminal.Gui `Style.Bold`
  - Te `TextDecoration.Reverse` → Terminal.Gui `Style.Reverse` (for cursor indicator)

- [x] Port splash art rendering (centered ASCII art when conversation is empty).

### Phase 4: Input area

- [x] Create `src/Ox/Views/InputAreaView.cs` -- a container View for the composer panel.
  - Contains a `TextField` (or custom text input View) for user input.
  - Draws a rounded border (╭ ╮ ╰ ╯) using `OnDrawingContent()` or Terminal.Gui's `Border` adornment with `LineStyle.Rounded`.
  - Status line below the input: throbber display + model ID. Use a child `View` or draw directly.
  - Ghost text for autocomplete: render grayed-out completion suffix after the cursor position. May need a custom input View if `TextField` doesn't support ghost text natively.

- [x] Wire autocomplete:
  - On text change in the input field, query `AutocompleteEngine`.
  - Display ghost text (grayed-out completion suffix).
  - Tab accepts the completion.

- [x] Wire submission:
  - Enter submits the current text (fires an event that OxApp handles to start a turn).
  - Ctrl+C / Ctrl+D sends EOF signal.

- [x] Implement throbber:
  - Use `App.AddTimeout(TimeSpan.FromSeconds(1), ...)` to advance the throbber counter.
  - Port `Viewport.BuildThrobberCells()` and `ComputeThrobberCounter()` logic.
  - Start timer when turn begins, remove when turn ends.

### Phase 5: Sidebar

- [x] Create `src/Ox/Views/SidebarView.cs` -- a container View positioned on the right side of the window.
  - Width: up to 36 columns or 1/3 of terminal width, whichever is smaller.
  - Visibility: hidden when no sections have content (reclaim space for conversation).
  - Contains child Views for each section.

- [x] Port `ContextSection` as a child View showing token usage (context window utilization).

- [x] Port `TodoSection` as a child View showing the task list from the `TodoStore`.

- [x] Wire sidebar visibility: when sections gain/lose content, toggle the sidebar View's `Visible` property and trigger relayout (the Pos/Dim system handles the conversation area reclaiming space automatically).

### Phase 6: Event routing adaptation

- [x] Update `EventRouter.cs` to target the new `ConversationView` instead of `EventList` + IRenderables.
  - Replace `EventList.Add(renderable, style, colorFunc)` calls with `ConversationView.AddXxx()` methods.
  - Replace `TextRenderable` creation/mutation with `ConversationView.AppendText()` calls.
  - Replace `ToolRenderable` creation/mutation with `ConversationView.AddToolCall()` / `UpdateToolState()` calls.
  - Replace `SubagentRenderable` creation/mutation with `ConversationView.AddSubagent()` and nested event routing.
  - All mutations must go through `App.Invoke()` since agent events arrive on background threads.

- [x] Port turn lifecycle:
  - Turn start: enable throbber, wire escape cancellation via Terminal.Gui key handling.
  - Turn end: disable throbber, finalize any open text/tool blocks.
  - Escape key during turn: use a key binding on the application or OxApp level to cancel the CTS.

### Phase 7: Permission prompts

- [x] Adapt `PermissionHandler.cs` to work within Terminal.Gui.
  - Option A: Use a Terminal.Gui `Dialog` for permission prompts (modal, blocks input to conversation).
  - Option B: Inline permission prompt in the input area (replace the text field temporarily). This matches the current UX more closely.
  - Whichever approach: the handler must return a `PermissionDecision` asynchronously. Use `TaskCompletionSource` wired to button clicks or key presses.

### Phase 8: Configuration phase (pre-TUI)

- [x] Handle the pre-viewport configuration phase (API key setup, model selection).
  - This currently uses `InputReader.ReadLineAsync()` which does plain console I/O.
  - Simplest approach: keep this as plain `Console.ReadLine()` / `Console.WriteLine()` before Terminal.Gui starts. Terminal.Gui is not initialized until config is complete.
  - Alternative: use Terminal.Gui for config too (a separate Runnable). Probably overkill for now.

### Phase 9: Cleanup

- [x] Delete `src/Ox/Rendering/` directory entirely (Viewport.cs, IRenderable.cs, CellRow.cs, EventList.cs, TextRenderable.cs, ToolRenderable.cs, SubagentRenderable.cs, TreeChrome.cs, Sidebar.cs, ContextSection.cs, TodoSection.cs, Terminal.cs).
- [x] Delete the Fe plan: `docs/agents/plans/2026-04-09-001-fe-tui-framework.md`.
- [x] Verify no remaining `using Te.*` references anywhere in the solution.
- [x] Update solution file to remove Te project entry if not already done.

### Phase 10: AOT compatibility check

- [x] Terminal.Gui v2 may or may not be AOT-compatible. Test `dotnet publish -c Release` with `PublishAot=true`.
  - If it works, keep AOT.
  - If it doesn't, either disable AOT in Ox.csproj or add necessary trimming/AOT annotations. Terminal.Gui uses reflection in some places (serialization, type resolution) which may require `rd.xml` or `DynamicDependency` attributes.

## Validation

- **Build**: `dotnet build` succeeds with no Te references.
- **Run**: `dotnet run --project src/Ox` launches the TUI, displays the conversation, accepts input, streams responses, shows tool calls, displays sidebar.
- **Throbber**: Animates during turns, stops when turn completes.
- **Autocomplete**: Slash commands show ghost text, Tab accepts.
- **Permission prompts**: Tool approval/denial works.
- **Escape cancellation**: Pressing Escape during a turn cancels it.
- **Ctrl+C**: Clean shutdown (terminal restored, no garbled output).
- **Resize**: Terminal resize reflows the layout correctly.
- **Long conversations**: Scrolling works, no performance degradation.
- **Subagents**: Nested event blocks render correctly with tree chrome.
- **Sidebar**: Shows/hides based on content. Context usage and todo list update.

## Open questions

- **AOT compatibility**: Terminal.Gui v2 beta may not support PublishAot. If it doesn't, is dropping AOT acceptable, or should we investigate trimming workarounds?

- **Ghost text in TextField**: Terminal.Gui's `TextField` may not natively support rendering ghost text (grayed-out autocomplete suffix). If not, do we build a custom input View, or accept a simpler autocomplete UX (e.g., dropdown suggestions instead of inline ghost text)?

- **Permission prompt UX**: Should permission prompts be modal dialogs (Terminal.Gui `Dialog`) or inline in the input area (closer to current behavior)? Dialogs are simpler to implement; inline is more seamless.
