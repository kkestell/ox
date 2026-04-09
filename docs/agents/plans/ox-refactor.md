# Ox TUI Rewrite

## Context

The Ox project (TUI layer) is a retained-mode widget tree where every node mixes state, layout, and cell production. A 731-line Viewport god-object owns everything. Width-math is scattered across every file. CellRow intermediaries shuttle data between renderables and the buffer. The IRenderable tree fires Changed events and manages its own mutation lifecycle.

The goal is to rewrite Ox using the Ratatui immediate-mode pattern: application state is pure data, rendering is a function from state to cells, widgets are stateless, layout is computed separately from rendering.

## Architecture

### Core types (added to Te/Rendering/)

These are standard TUI vocabulary from Ratatui, adapted to C# idioms:

- **`Rect`** — `readonly record struct(int X, int Y, int Width, int Height)` with `Inner(margin)`, `Inner(top, right, bottom, left)`, `Left/Top/Right/Bottom/Area/IsEmpty`. Goes in Te because it describes terminal regions, not application concepts.

- **`Style`** — `readonly record struct(Color? Fg, Color? Bg, TextDecoration? Decorations)` with nullable fields for composability. `Over(Style base)` fills nulls from a base style. `ResolvedFg/ResolvedBg/ResolvedDecorations` return non-null values. Factory methods: `Style.Of(fg)`, `Style.Of(fg, bg)`, etc.

- **`Span`** — `readonly record struct(string Content, Style Style)`. A run of uniformly styled text. `Width` property (returns `Content.Length` for now, future wide-char extension point).

- **`Line`** — A list of Spans. `Width` sums span widths. Factory: `Line.Empty`. Builder: `.Add(span)`.

- **`Layout`** — `static Layout.Split(Rect area, Direction direction, params Constraint[] constraints)` returns `Rect[]`. Constraints: `Fixed(int)`, `Min(int)`, `Percentage(int)`, `Fill()`. Two-pass allocation: fixed/min first, then distribute remainder to percentage/fill.

- **`TextWrap`** — `static List<Line> Wrap(string text, int width, Style style)` and `static List<Line> Wrap(ReadOnlySpan<Span> spans, int width)`. Word-aware wrapping with hard breaks for oversized words, `\n` for explicit breaks. Replaces the two duplicate implementations in TextRenderable and TodoSection.

- **`BufferExtensions`** — Extension methods on `ConsoleBuffer`:
  - `WriteString(x, y, text, Style)` — writes styled text, clipped to buffer width
  - `WriteSpan(x, y, Span)` — writes a span
  - `WriteLine(x, y, Line)` — writes a line of spans
  - `FillRect(Rect, char, Style)` — fills a rectangular region
  - `DrawBorder(Rect, BorderChars, Style)` — draws a box border

- **`BorderChars`** — `readonly record struct` with corner and edge characters. `BorderChars.Rounded` = `╭╮╰╯─│`.

- **`IWidget`** — `void Render(Rect area, ConsoleBuffer buffer)`. Stateless: receives data through constructor, writes cells directly to buffer.

### Application model (Ox/Model/)

Pure data, no events, no rendering logic:

- **`ConversationEntry`** — `EntryKind Kind`, `string Text` (mutable for streaming append), `Style TextStyle`, `ToolStatus` + `ToolResult` + `ToolIsError` (tool fields), `List<ConversationEntry>? Children` + `bool SubagentCompleted` (subagent fields). Computed `CircleColor` derived from Kind and status.

- **`EntryKind`** enum: `User`, `Assistant`, `Tool`, `Subagent`, `Error`, `Info`

- **`ToolStatus`** enum: `Running`, `AwaitingApproval`, `Completed`, `Failed`

- **`AppState`** — All mutable TUI state in one place:
  - `List<ConversationEntry> Entries`
  - `string InputText`, `string? CompletionSuffix`
  - `bool TurnRunning`, `long TurnStartedAtTick`, `string? ModelId`
  - `string? ContextUsageText`, `IReadOnlyList<TodoItem>? TodoItems`
  - Computed `bool SidebarVisible`

### Render pipeline (Ox/Rendering/)

- **`FrameRenderer.Render(AppState, ConsoleBuffer)`** — Static method. Clears buffer, computes layout (horizontal split for sidebar, vertical split for conversation/gap/composer), delegates to widgets. The entire render path is one function call.

### Widgets (Ox/Rendering/Widgets/)

All implement `IWidget`. Stateless — data passed via constructor.

- **`ConversationWidget(entries)`** — Renders circle-prefixed entries with word wrapping, blank separators, tool result subordination (`└─`), subagent children with inner clipping (20 rows max). Tail-clips to viewport height.

- **`ComposerWidget(inputText, completion, turnRunning, turnStartedAtTick, modelId)`** — Draws rounded border, input row with cursor/ghost-text, divider, status row with throbber + model ID.

- **`SidebarWidget(contextUsage, todoItems)`** — Renders "Context" header + usage, "Plan" header + status-prefixed todo items.

- **`SplashWidget()`** — Centers the ASCII art logo.

### Event routing (Ox/EventRouter.cs)

Rewritten to mutate `AppState` instead of creating IRenderable objects. Same correlation logic (CallId maps, subagent pairing, current-text-index tracking), but targets are `ConversationEntry` objects in `state.Entries` instead of retained renderables.

### Main loop (Ox/Program.cs)

Simplified. No Viewport object. The render cycle is:

```
state mutation -> FrameRenderer.Render(state, buffer) -> buffer.Render(Console.Out)
```

Called behind a lock from: keystroke callbacks, event routing, throbber timer, resize timer. Same lifecycle (BackgroundService, signal handlers, EnsureReadyAsync) but with the retained-mode machinery removed.

### What survives unchanged

| File | Notes |
|------|-------|
| `Terminal.cs` | Pure ANSI helpers, no coupling to rendering model |
| `InputReader.cs` | Already callback-based, no changes needed |
| `AutocompleteEngine.cs` | Pure function, no rendering coupling |

### What survives with minor changes

| File | Changes |
|------|---------|
| `PermissionHandler.cs` | Signature changes from `(EventRouter, InputReader, Viewport, string)` to `(EventRouter, InputReader, Action<string>, Action, string)` — replaces `viewport.SetInputPrompt` and `viewport.RedrawIfDirty` with lambdas |

### What gets deleted (12 files)

`IRenderable.cs`, `ISidebarSection.cs`, `CellRow.cs`, `EventList.cs`, `TextRenderable.cs`, `ToolRenderable.cs`, `SubagentRenderable.cs`, `TreeChrome.cs`, `Sidebar.cs`, `ContextSection.cs`, `TodoSection.cs`, `Viewport.cs`

## Implementation phases

### Phase 1: Core types in Te

Add to `Te/Rendering/`: Rect, Style, Span, Line, TextWrap, Layout, BufferExtensions, BorderChars, IWidget.

Unit tests for: Rect.Inner, Style.Over, TextWrap (word wrap, hard break, newlines, spans crossing lines), Layout.Split (fixed, fill, percentage, mixed), BufferExtensions (WriteString clipping, DrawBorder).

Existing Ox unchanged — new types not referenced yet.

### Phase 2: Application model + widgets + FrameRenderer

Add `Ox/Model/AppState.cs`, `Ox/Model/ConversationEntry.cs`, `Ox/Rendering/FrameRenderer.cs`, and the four widget files.

Test by constructing AppState with sample entries, rendering to a ConsoleBuffer, asserting specific cell contents.

### Phase 3: New EventRouter

Rewrite EventRouter to mutate AppState. Port/rewrite the EventRouter tests from TuiRenderingTests.

### Phase 4: Rewire Program.cs + PermissionHandler

Replace the current wiring (EventList + Viewport + retained renderables) with (AppState + FrameRenderer + RenderAndFlush). Update PermissionHandler signature. This is the cutover — old rendering code stops being called.

### Phase 5: Delete old code + update tests

Remove the 12 deleted files. Rewrite TuiRenderingTests to test new types. Full test suite green.

## Verification

- `dotnet test` after every phase
- After Phase 4: manual smoke test — start TUI, send messages, observe streaming text, tool calls with lifecycle colors, subagent blocks, sidebar context/todos, throbber animation, ghost-text autocomplete, resize, Escape cancel, Ctrl+C exit, permission prompts
- After Phase 5: `grep -rn "IRenderable\|CellRow\|EventList" src/Ox/` returns nothing
