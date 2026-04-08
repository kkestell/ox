# Todo tool and sidebar

## Goal

Add a `todo_write` tool that lets the LLM track task progress during a conversation, and a generic right sidebar in the TUI that displays the current todo list. The sidebar is designed to host future sections beyond todos.

## Desired outcome

- The LLM can call `todo_write` with a full replacement list of `{ content, status }` items.
- Todos are session-scoped and in-memory (lost on process exit).
- The TUI displays a persistent right sidebar when there's content to show.
- The sidebar's top section renders the current todo list with status indicators.
- When there are no todos, the sidebar is hidden and the conversation uses the full terminal width.

## Related code

- `src/Ur/Tools/BuiltinToolFactories.cs` — registration list for all built-in tools; new tool entry goes here
- `src/Ur/Tools/ReadFileTool.cs` — cleanest existing tool example; `TodoWriteTool` follows the same `AIFunction` subclass pattern
- `src/Ur/Tools/ToolRegistry.cs` — where tools register with permission metadata
- `src/Ur/ToolContext.cs` — context record passed to tool factories; needs a new `TodoStore` field
- `src/Ur/Sessions/UrSession.cs` — session lifecycle; owns the `TodoStore` instance and passes it via `ToolContext`
- `src/Ur/UrHost.cs` — `BuildSessionToolRegistry` builds `ToolContext` and calls factories
- `src/Ur/AgentLoop/AgentLoopEvent.cs` — event types; needs a new `TodoUpdated` event
- `src/Ur/AgentLoop/AgentLoop.cs` — agent loop; needs to detect `todo_write` results and emit `TodoUpdated`
- `src/Ox/Rendering/Viewport.cs` — layout engine; needs sidebar column allocation
- `src/Ox/Rendering/EventList.cs` — conversation tree; its render width shrinks when the sidebar is visible
- `src/Ox/EventRouter.cs` — routes agent events to renderables; needs to handle `TodoUpdated`
- `src/Ox/Program.cs` — wires Viewport to session; needs to pass the `TodoStore` for sidebar binding

## Structural considerations

**Hierarchy** — `TodoStore` and `TodoItem` live in the `Ur` layer (they're agent state, not UI). The `TodoWriteTool` lives alongside other tools in `Ur/Tools/`. The sidebar and its sections live in `Ox/Rendering/` (they're pure display). The `TodoUpdated` event bridges the two layers through the existing event channel.

**Abstraction** — The sidebar is generic: it renders a list of `ISidebarSection` instances, each responsible for its own content. The todo section is the first concrete implementation. Future sections (e.g., session info, file watches) plug in without changing the sidebar infrastructure.

**Modularization** — The `TodoStore` is a standalone class with a `Changed` event, not embedded in `UrSession`. The sidebar is a standalone `IRenderable`, not embedded in `Viewport`. This keeps each unit testable in isolation.

**Encapsulation** — `UrSession` exposes `TodoStore` as a read-only public property (same pattern as `LastInputTokens`). The TUI reads it to bind the sidebar. The tool writes to it via `ToolContext`. No internal session state is leaked.

## Implementation tasks

### 1. Data model (`Ur/`)

- [x] Create `src/Ur/Todo/TodoItem.cs` — a simple record:

  ```csharp
  // The status enum uses snake_case string values to match the JSON schema
  // the LLM sends (e.g., "in_progress"), avoiding a custom converter.
  enum TodoStatus { Pending, InProgress, Completed }
  record TodoItem(string Content, TodoStatus Status);
  ```

- [x] Create `src/Ur/Todo/TodoStore.cs` — session-scoped in-memory store:
  ```csharp
  // Observable store so the TUI sidebar can react to changes without polling.
  // Thread-safe: the agent loop writes from its async pipeline, the TUI timer
  // reads on a ThreadPool thread for rendering.
  sealed class TodoStore
  {
      IReadOnlyList<TodoItem> Items { get; }
      event Action? Changed;
      void Update(IReadOnlyList<TodoItem> items);  // full replacement
  }
  ```
  `Update` replaces the entire list atomically (same semantics as OpenCode's `todowrite`). No add/remove/patch — the LLM always sends the full list.

### 2. Tool (`Ur/Tools/`)

- [x] Create `src/Ur/Tools/TodoWriteTool.cs` — `AIFunction` subclass:
  - Name: `todo_write`
  - Description: concise, tells the LLM when and how to use it (modeled after OpenCode's prompt guidance)
  - Schema:
    ```json
    {
      "type": "object",
      "properties": {
        "todos": {
          "type": "array",
          "description": "The complete todo list. Every call replaces the entire list.",
          "items": {
            "type": "object",
            "properties": {
              "content": {
                "type": "string",
                "description": "Brief task description in imperative form."
              },
              "status": {
                "type": "string",
                "enum": ["pending", "in_progress", "completed"],
                "description": "pending = not started, in_progress = currently working, completed = done."
              }
            },
            "required": ["content", "status"],
            "additionalProperties": false
          }
        }
      },
      "required": ["todos"],
      "additionalProperties": false
    }
    ```
  - Constructor takes `TodoStore` (from `ToolContext`)
  - `InvokeCoreAsync`: parse the JSON array, build `List<TodoItem>`, call `store.Update(items)`, return a short confirmation string (e.g., `"Todo list updated (3 items)."`)
  - Permission: `OperationType.Read` (no filesystem or execution side effects — auto-allowed, never prompts)
  - Target extractor: `null` (the tool call format `todo_write(...)` is self-describing enough)
  - Implements `IToolMeta` so permission metadata is self-declared (same pattern as `SubagentTool`)

### 3. Wiring into the tool registry

- [x] Add `TodoStore` field to `ToolContext`:

  ```csharp
  internal record ToolContext(Workspace Workspace, string SessionId, TodoStore Todos);
  ```

- [x] Add `TodoStore` property to `UrSession` — constructed in the `UrSession` constructor, passed through `ToolContext`:

  ```csharp
  public TodoStore Todos { get; } = new();
  ```

- [x] Update `UrHost.BuildSessionToolRegistry` to pass the `TodoStore` through `ToolContext`. Since `BuildSessionToolRegistry` doesn't have access to the session's `TodoStore` (it's a host-level method called before the session is fully wired), two options:
  - **Option A:** Move tool registry building into `UrSession.RunTurnAsync` where the session is available. But it's already partially there (SubagentTool is registered in `RunTurnAsync`).
  - **Option B (recommended):** Add a `TodoStore?` parameter to `BuildSessionToolRegistry` that defaults to null, and have `UrSession.RunTurnAsync` pass `this.Todos`. The `ToolContext` record already uses a union-of-concerns design, so a nullable field is fine.

- [x] Add `TodoWriteTool` entry to `BuiltinToolFactories.All`:
  ```csharp
  (ctx => new TodoWriteTool(ctx.Todos), OperationType.Read, null)
  ```
  Note: `ctx.Todos` may be null when `BuildSessionToolRegistry` is called from tests without a session. `TodoWriteTool` should handle this gracefully (return an error string rather than throw).

### 4. Agent loop event

- [x] Add `TodoUpdated` event to `AgentLoopEvent.cs`:

  ```csharp
  public sealed class TodoUpdated : AgentLoopEvent
  {
      public required IReadOnlyList<TodoItem> Items { get; init; }
  }
  ```

  This event is the bridge between Ur and Ox — the EventRouter uses it to update the sidebar.

- [x] Emit `TodoUpdated` from the agent loop after `todo_write` completes. The cleanest place is in `ToolInvoker` or in `AgentLoop.RunTurnAsync` after tool results are collected. Since the `TodoStore.Changed` event already notifies the TUI directly, the `TodoUpdated` event is an optional enhancement — the sidebar can subscribe to `TodoStore.Changed` instead. **Decision: skip the event for now.** The sidebar binds directly to `TodoStore.Changed`. This avoids changes to `AgentLoop` and `ToolInvoker` entirely.

### 5. Sidebar infrastructure (`Ox/Rendering/`)

- [x] Create `src/Ox/Rendering/ISidebarSection.cs`:

  ```csharp
  // A section within the sidebar. Each section renders independently and
  // reports whether it currently has content to display. The sidebar hides
  // itself when all sections report HasContent = false.
  interface ISidebarSection : IRenderable
  {
      bool HasContent { get; }
  }
  ```

- [x] Create `src/Ox/Rendering/Sidebar.cs`:

  ```csharp
  // Generic right sidebar that renders a vertical stack of ISidebarSection
  // instances. Hidden (zero width) when no section has content. Raises
  // Changed when any child section changes so the Viewport redraws.
  sealed class Sidebar : IRenderable
  {
      void AddSection(ISidebarSection section);
      bool IsVisible { get; }  // true when any section has content
      IReadOnlyList<CellRow> Render(int availableWidth);
      event Action? Changed;
  }
  ```

  - Renders sections top-to-bottom, skipping sections where `HasContent == false`.
  - Subscribes to each section's `Changed` event and propagates to its own `Changed`.
  - `IsVisible` is checked by `Viewport` to decide whether to allocate sidebar width.

### 6. Todo sidebar section (`Ox/Rendering/`)

- [x] Create `src/Ox/Rendering/TodoSection.cs`:

  ```csharp
  // Renders the current todo list as a sidebar section. Subscribes to
  // TodoStore.Changed so it re-renders when the LLM updates the list.
  sealed class TodoSection : ISidebarSection
  {
      TodoSection(TodoStore store);
      bool HasContent => store.Items.Count > 0;
  }
  ```

  - Renders a header line: `Plan` (or `Tasks`) in BrightBlack
  - Each item rendered with a status indicator:
    - Completed: `  ✓ content` in green (or BrightBlack/strikethrough)
    - In progress: `  ● content` in yellow
    - Pending: `  ○ content` in white (or BrightBlack)
  - Content is word-wrapped to the available sidebar width.
  - Progress summary at the bottom: `2/5 completed` in BrightBlack.

### 7. Viewport layout changes (`Ox/Rendering/Viewport.cs`)

The terminal is split into two full-height columns: left (header + conversation + input + status) and right (sidebar). The sidebar occupies the full height of the terminal, not just the conversation region. A thin `│` separator in BrightBlack divides them.

```
┌─────────────────────────────┐┌──────────────────┐
│ Session: 2026...    50,000  ││ Plan              │
│ ━━━━━━━━━━━━━━━━━━━━━━━━━━━ ││                   │
│ ├─ ● User message           ││   ✓ Read config   │
│ │  ├─ ● read_file(...)      ││   ● Implement it  │
│ │  └─ ● Response text       ││   ○ Write tests   │
│ ━━━━━━━━━━━━━━━━━━━━━━━━━━━ ││                   │
│ ❯ _                         ││                   │
│ ─────────────────────────── ││                   │
│ ■ ■ ■        ollama/gemma3  ││ 2/5 completed     │
│                             ││                   │
└─────────────────────────────┘└──────────────────┘
```

- [x] Add `Sidebar` field to `Viewport` — passed in via constructor or a setter.

- [x] Update `BuildFrame` to allocate width:

  ```
  if sidebar.IsVisible:
      sidebarWidth = min(36, terminalWidth / 3)  // cap at 1/3 of terminal
      separatorWidth = 1                          // thin vertical separator (│, BrightBlack)
      leftWidth = terminalWidth - sidebarWidth - separatorWidth
  else:
      leftWidth = terminalWidth
  ```

  The sidebar width is capped to prevent it from dominating narrow terminals.

- [x] Update all left-column render methods (`RenderHeader`, `RenderConversation`, `RenderInputArea`, `RenderStatusBar`) to use `leftWidth` instead of `width`. The header, input area, and status bar all live inside the left column — they do not span the full terminal width.

- [x] Add `RenderSidebar` method:
  - Renders the sidebar's rows into the right columns of the `ScreenBuffer`, spanning the full terminal height.
  - Draws a thin vertical separator (`│` in BrightBlack) in the separator column for every row.
  - Sidebar content is top-aligned (not tail-clipped like the conversation).

- [x] Subscribe to `Sidebar.Changed` to set the dirty flag (same pattern as `EventList.Changed`).

### 8. Wiring in Program.cs

- [x] After creating/opening the `UrSession`, create the sidebar:
  ```csharp
  var todoSection = new TodoSection(session.Todos);
  var sidebar = new Sidebar();
  sidebar.AddSection(todoSection);
  ```
  Pass `sidebar` to the `Viewport`.

### 9. System prompt guidance

- [x] Add todo usage guidance to the system prompt (in `SystemPromptBuilder` or a dedicated builder). The LLM needs to know:
  - Use `todo_write` for multi-step tasks (3+ steps)
  - Always send the full list (replacement semantics)
  - Mark items `in_progress` before starting, `completed` when done
  - Keep at most one item `in_progress` at a time
  - Don't use for trivial single-step tasks
  - Remove completed items once all work is done (clears the sidebar)

### 10. Tests

- [x] `TodoStore` unit tests: update, replace, empty list, Changed event fires
- [x] `TodoWriteTool` unit tests: valid input, missing fields, empty list, status parsing
- [x] `TodoSection` render tests: empty (hidden), single item, mixed statuses, word wrap
- [x] `Sidebar` render tests: no sections visible → zero width, one section visible → renders, multiple sections
- [x] `Viewport` layout tests: with and without sidebar, narrow terminal edge case
