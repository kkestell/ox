# Ox TUI Rewrite

## Context

The Ox project (TUI layer) is a retained-mode widget tree where every node mixes state, layout, and cell production. A 731-line Viewport god-object owns everything. Width-math is scattered across every file. CellRow intermediaries shuttle data between renderables and the buffer. The IRenderable tree fires Changed events and manages its own mutation lifecycle.

The goal is to rewrite Ox using the Ratatui immediate-mode pattern: application state is pure data, rendering is a function from state to cells, widgets are stateless, layout is computed separately from rendering.

The key architectural decision: **Te becomes the immediate-mode framework**, not just a low-level terminal I/O library with Ratatui abstractions bolted on top. We own Te. Rather than adding extension methods and bridge types that paper over Te's current API, we refactor Te itself to natively speak the immediate-mode vocabulary: Style, Rect, BufferView (clipped regions), Span/Line (styled text), Layout, and IWidget. Ox then shrinks to pure application code — data model, four widgets, event routing, and wiring.

## Architecture

### Te refactor (Te/Rendering/)

#### Style — replaces loose color/decoration fields

`readonly record struct Style(Color? Fg, Color? Bg, TextDecoration? Decorations)`

- Nullable fields for composability — a `Style` can represent "override just foreground."
- `Over(Style base)` fills nulls from a base style (overlay semantics).
- `ResolvedFg` / `ResolvedBg` / `ResolvedDecorations` return non-null values with defaults.
- Factory methods: `Style.Of(fg)`, `Style.Of(fg, bg)`, `Style.Of(fg, bg, deco)`.
- `Style.Default` — all nulls, resolves to terminal defaults.

#### Cell — refactored to use Style

`readonly struct Cell(char Rune, Style Style)` — replaces the current `Cell(char, Color, Color, TextDecoration)`.

- `Cell.Empty` remains `new Cell(' ', Style.Default)`.
- All existing ConsoleBuffer methods that take `(Color fg, Color bg, TextDecoration deco)` change to take `Style`.
- The Render() SGR emission logic reads `cell.Style.ResolvedFg` etc. instead of `cell.Foreground` etc.

This is a breaking change to Te's public API. The old Ox renderables (which we're about to delete) will need mechanical fixes to compile during the transition — just wrapping loose args in `Style.Of(...)`.

#### Rect — terminal region geometry

`readonly record struct Rect(int X, int Y, int Width, int Height)`

- Properties: `Left`, `Top`, `Right` (X+Width), `Bottom` (Y+Height), `Area`, `IsEmpty`.
- `Inner(int margin)` — shrinks all sides uniformly.
- `Inner(int top, int right, int bottom, int left)` — shrinks each side independently.
- `Intersect(Rect other)` — returns the overlapping region, or `Rect.Empty` if none.

#### BufferView — clipped, offset view of a buffer region

`readonly struct BufferView`

The core abstraction that makes widgets safe. A BufferView knows its bounds — writes outside are silently clipped. Widgets receive a BufferView and cannot accidentally draw outside their region.

```
Fields (private):
  ConsoleBuffer _buffer    // underlying buffer
  Rect _clip               // absolute region within the buffer

Properties:
  int Width  => _clip.Width
  int Height => _clip.Height
  Rect Area  => new Rect(0, 0, Width, Height)  // local coordinates

Methods:
  // Cell-level writes (x, y are local to this view)
  void SetCell(int x, int y, Cell cell)
  Cell GetCell(int x, int y)

  // Styled text writes
  void WriteString(int x, int y, string text, Style style)
  void WriteSpan(int x, int y, Span span)
  void WriteLine(int x, int y, Line line)

  // Region fills
  void Fill(char rune, Style style)              // fills entire view
  void FillRect(Rect rect, char rune, Style style)  // fills sub-region
  void DrawBorder(BorderChars chars, Style style)    // draws border at view edges
  void DrawBorder(Rect rect, BorderChars chars, Style style)

  // Sub-region slicing
  BufferView SubRegion(Rect rect)    // nested clipping, composes correctly
  BufferView[] Split(Direction dir, params Constraint[] constraints)  // layout shortcut
```

`SubRegion` composes: if this view clips to absolute (10, 5, 30, 20) and you request SubRegion(Rect(2, 3, 10, 10)), the result clips to absolute (12, 8, 10, 10) intersected with the parent bounds. Widgets that call SubRegion for child layout cannot escape their own bounds.

`Split` is a convenience that calls `Layout.Split(Area, dir, constraints)` and returns `SubRegion(rect)` for each resulting Rect. This is the primary way widgets subdivide their space.

#### ConsoleBuffer — gains View() entry point

The existing ConsoleBuffer keeps its double-buffer dirty-diff rendering. New additions:

- `BufferView View()` — returns a BufferView covering the entire buffer. This is the entry point from the main loop.
- `BufferView View(Rect region)` — returns a clipped view for a sub-region.
- Internal: `SetCell`/`GetCell` continue to work in absolute coordinates (called by BufferView).

The old `SetCell(x, y, char, Color, Color, TextDecoration)` overload changes to `SetCell(x, y, Cell)` and `SetCell(x, y, char, Style)`. `FillCells` becomes `FillCells(x, y, width, char, Style)`.

#### Span and Line — styled text primitives

- **`Span`** — `readonly record struct(string Content, Style Style)`. A run of uniformly styled text. `Width` property (returns `Content.Length`; future wide-char extension point).

- **`Line`** — Wraps a `List<Span>`. `Width` sums span widths. `Line.Empty` factory. `.Add(span)` builder. `Line.From(string text, Style style)` convenience.

These are terminal vocabulary, not application types. They live in Te because BufferView.WriteLine needs them.

#### TextWrap — word-aware wrapping

`static class TextWrap`

- `static List<Line> Wrap(string text, int width, Style style)` — wraps plain text into Lines.
- `static List<Line> Wrap(ReadOnlySpan<Span> spans, int width)` — wraps styled spans, splitting at word boundaries. Handles `\n` explicit breaks and hard-breaks for oversized words.

Replaces the duplicate implementations in TextRenderable.WrapText and TodoSection.WordWrap.

#### Layout — constraint-based space division

`static class Layout`

- `static Rect[] Split(Rect area, Direction direction, params Constraint[] constraints)` — returns sub-rects.
- Constraints: `Fixed(int)`, `Min(int)`, `Percentage(int)`, `Fill()`.
- Two-pass allocation: fixed/min first, then distribute remainder to percentage/fill.

Layout returns `Rect[]` for pure calculations. In practice, widgets use `BufferView.Split(...)` which wraps this and returns `BufferView[]` directly.

#### BorderChars — box-drawing character sets

`readonly record struct BorderChars(char TopLeft, char TopRight, char BottomLeft, char BottomRight, char Horizontal, char Vertical)`

- `BorderChars.Rounded` = `╭╮╰╯─│`
- `BorderChars.Plain` = `┌┐└┘─│`

#### IWidget — the widget protocol

```csharp
public interface IWidget
{
    void Render(BufferView view);
}
```

Stateless. Receives data through the constructor. Writes cells to the BufferView. Cannot draw outside its view's bounds. This lives in Te as the framework contract.

### Application model (Ox/Model/)

Pure data, no events, no rendering logic.

#### ConversationEntry

```
Kind: EntryKind             // determines circle color and rendering behavior
Text: string                // mutable — appended during streaming
TextStyle: Style            // resolved at creation (bold for user, etc.)

// Tool fields (only used when Kind == Tool)
ToolStatus: ToolStatus?     // Running → AwaitingApproval → Completed / Failed
ToolResult: string?         // collapsed result text, shown with └─ prefix
ToolIsError: bool           // red styling for error results

// Subagent fields (only used when Kind == Subagent)
Children: List<ConversationEntry>?
SubagentCompleted: bool     // when true, shows "completed" footer

// Computed
CircleColor: Color          // derived from Kind + ToolStatus
```

#### EntryKind enum

`User`, `Assistant`, `Tool`, `Subagent`, `Error`, `Info`

#### ToolStatus enum

`Running`, `AwaitingApproval`, `Completed`, `Failed`

#### AppState

All mutable TUI state in one place:

```
// Conversation
List<ConversationEntry> Entries
int? ScrollOffset              // null = pinned to bottom, 0 = top of history

// Input
string InputText
int CursorIndex                // position within InputText for cursor rendering
string? CompletionSuffix       // ghost-text from autocomplete

// Turn state
bool TurnRunning
long TurnStartedAtTick         // for throbber animation
string? ModelId

// Sidebar
string? ContextUsageText
IReadOnlyList<TodoItem>? TodoItems

// Computed
bool SidebarVisible            // true when ContextUsageText or TodoItems present
```

### Render pipeline (Ox/Rendering/)

#### FrameRenderer

`static void Render(AppState state, ConsoleBuffer buffer)`

Single static method. The entire render path:

1. Clear buffer.
2. Get root view via `buffer.View()`.
3. Split horizontally for sidebar: `view.Split(Horizontal, Fill(), Fixed(sidebarWidth))` if sidebar visible, otherwise just `[view]`.
4. Split main area vertically: `mainView.Split(Vertical, Fill(), Fixed(1), Fixed(composerHeight))` → conversation, gap, composer views.
5. Construct and render widgets into their views.
6. `buffer.Render(Console.Out)` — dirty-diff flush.

#### Render triggering

Mutations set a `_dirty` flag. A 16ms timer calls `RenderIfDirty()` which runs `FrameRenderer.Render` under the lock. This naturally batches streaming ResponseChunks (many arrive per frame interval) into single redraws. The throbber tick, resize detection, and keystroke callbacks all just set `_dirty = true` — the timer handles the actual render.

This replaces the current `Changed` event + `RedrawIfDirty()` pattern with something explicit and predictable.

### Widgets (Ox/Rendering/Widgets/)

All implement Te's `IWidget`. Stateless — data passed via constructor.

- **`ConversationWidget(entries, scrollOffset)`** — Renders circle-prefixed entries with word wrapping (via `TextWrap`), blank separators, tool result subordination (`└─`), subagent children with inner clipping (constant `MaxSubagentInnerRows = 20`). Applies scroll offset: `null` = show tail, integer = offset from top.

- **`ComposerWidget(inputText, cursorIndex, completion, turnRunning, turnStartedAtTick, modelId)`** — Uses `view.DrawBorder(BorderChars.Rounded, ...)` for the rounded frame. Input row with cursor positioning and ghost-text. Divider line. Status row with throbber + model ID.

- **`SidebarWidget(contextUsage, todoItems)`** — Renders "Context" header + usage text, "Plan" header + status-prefixed todo items (using `TextWrap` for long items).

- **`SplashWidget()`** — Centers the ASCII art logo within its view.

### Event routing (Ox/EventRouter.cs)

Rewritten to mutate `AppState` instead of creating IRenderable objects. Same correlation logic (CallId maps, subagent pairing, current-text-index tracking), but targets are `ConversationEntry` objects in `state.Entries` instead of retained renderables.

### Main loop (Ox/Program.cs)

Simplified. No Viewport object. The render cycle is:

```
state mutation → set _dirty → timer → FrameRenderer.Render(state, buffer) → buffer.Render(Console.Out)
```

Same lifecycle (BackgroundService, signal handlers, EnsureReadyAsync) but with the retained-mode machinery removed.

### What survives unchanged

| File                    | Notes                                             |
| ----------------------- | ------------------------------------------------- |
| `Terminal.cs`           | Pure ANSI helpers, no coupling to rendering model |
| `InputReader.cs`        | Already callback-based, no changes needed         |
| `AutocompleteEngine.cs` | Pure function, no rendering coupling              |

### What survives with minor changes

| File                   | Changes                                                                                                                                                                                                              |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `PermissionHandler.cs` | Signature changes from `(EventRouter, InputReader, Viewport, string)` to `(EventRouter, InputReader, Action<string>, Action, string)` — replaces `viewport.SetInputPrompt` and `viewport.RedrawIfDirty` with lambdas |

### What gets deleted (12 files from Ox)

`IRenderable.cs`, `ISidebarSection.cs`, `CellRow.cs`, `EventList.cs`, `TextRenderable.cs`, `ToolRenderable.cs`, `SubagentRenderable.cs`, `TreeChrome.cs`, `Sidebar.cs`, `ContextSection.cs`, `TodoSection.cs`, `Viewport.cs`

## Diagrams

### Te before and after

```
BEFORE — Te is a low-level terminal I/O library:

  Te/Rendering/
  ├── Cell(char, Color, Color, TextDecoration)   loose fields
  ├── Color                                       color enum
  ├── TextDecoration                              decoration flags
  └── ConsoleBuffer                               raw 2D cell grid
        SetCell(x, y, char, Color, Color, TextDecoration)
        FillCells(x, y, width, char, Color, Color, TextDecoration)
        Render(TextWriter)

  Ox must build its own framework on top:
    CellRow, IRenderable, EventList, Viewport (731 lines), TreeChrome...

────────────────────────────────────────────────────────────

AFTER — Te is an immediate-mode TUI framework:

  Te/Rendering/
  ├── Style(Color? Fg, Color? Bg, TextDecoration? Decorations)
  ├── Cell(char Rune, Style Style)                 uses Style natively
  ├── Rect(X, Y, Width, Height)                    region geometry
  ├── Span(string Content, Style Style)            styled text run
  ├── Line(List<Span>)                             line of styled text
  ├── TextWrap                                     word-aware wrapping
  ├── Layout                                       constraint-based splitting
  ├── BorderChars                                  box-drawing character sets
  ├── IWidget                                      void Render(BufferView)
  ├── BufferView                                   clipped region with write methods
  │     SetCell, WriteString, WriteSpan, WriteLine
  │     Fill, FillRect, DrawBorder
  │     SubRegion(Rect), Split(Direction, Constraint[])
  └── ConsoleBuffer                                double-buffered + dirty-diff
        View() → BufferView                        entry point for rendering
        Render(TextWriter)                         emits only changed cells

  Ox is just application code:
    AppState, ConversationEntry, 4 widgets, FrameRenderer, EventRouter
```

### Current architecture (retained-mode)

In the current design, every renderable owns its own state and fires `Changed` events. The Viewport subscribes to these events and redraws. Data flows through CellRow intermediaries.

```
┌─────────────────────────────────────────────────────────────────┐
│ Program.cs (TuiService)                                        │
│                                                                 │
│  ┌──────────┐    AgentLoopEvent    ┌─────────────┐             │
│  │ UrSession│───────────────────▶│ EventRouter │             │
│  └──────────┘                      └──────┬──────┘             │
│                                           │ creates / mutates  │
│                          ┌────────────────┼───────────────┐    │
│                          ▼                ▼               ▼    │
│                   ┌──────────┐    ┌────────────┐  ┌──────────┐ │
│                   │  Text    │    │    Tool    │  │ Subagent │ │
│                   │Renderable│    │ Renderable │  │Renderable│ │
│                   └────┬─────┘    └─────┬──────┘  └────┬─────┘ │
│                        │ Changed        │ Changed      │Changed│
│                        └───────┬────────┴──────────────┘       │
│                                ▼                                │
│                        ┌──────────────┐                        │
│                        │  EventList   │  (root container)      │
│                        │  .Render()   │                        │
│                        └──────┬───────┘                        │
│                               │ List<CellRow>                  │
│                               ▼                                │
│  ┌─────────┐          ┌──────────────┐         ┌───────────┐  │
│  │ Sidebar │──cells──▶│   Viewport   │◀──────│  Throbber  │  │
│  └─────────┘          │  (731 lines) │  timer  │  / Resize  │  │
│                        └──────┬───────┘         └───────────┘  │
│                               │                                │
│                               ▼                                │
│                       ┌───────────────┐                        │
│                       │ ConsoleBuffer │                        │
│                       │  (Te layer)   │                        │
│                       └───────────────┘                        │
└─────────────────────────────────────────────────────────────────┘

Problems:
  - Each renderable owns mutable state + fires events = hard to reason about
  - CellRow intermediary shuttles data between renderables and buffer
  - Viewport is a 731-line god-object: layout, scrolling, input prompt, sidebar
  - Width-math duplicated in every renderable's Render(availableWidth)
  - Word-wrap implemented twice (TextRenderable and TodoSection)
```

### Proposed architecture (immediate-mode)

State is plain data. Te provides the framework. Ox is thin application code.

```
┌─────────────────────────────────────────────────────────────────┐
│ Program.cs (TuiService)                                        │
│                                                                 │
│  ┌──────────┐    AgentLoopEvent    ┌─────────────┐             │
│  │ UrSession│───────────────────▶│ EventRouter │             │
│  └──────────┘                      └──────┬──────┘             │
│                                           │                    │
│                                    mutates│plain data          │
│                                           ▼                    │
│                                   ┌──────────────┐             │
│                                   │   AppState   │             │
│                                   │              │             │
│                                   │ Entries      │             │
│                                   │ InputText    │             │
│                                   │ CursorIndex  │             │
│                                   │ ScrollOffset │             │
│                                   │ TurnRunning  │             │
│                                   │ TodoItems    │             │
│                                   │ ...          │             │
│                                   └──────┬───────┘             │
│                                          │                     │
│          ┌───────────────────────────────┐│                     │
│          │ any mutation source:          ││                     │
│          │  • keystroke callback         ││                     │
│          │  • event routing              ││ sets _dirty         │
│          │  • throbber timer             ││                     │
│          │  • resize timer               ││                     │
│          └───────────────────────────────┘│                     │
│                                          │                     │
│                              16ms timer: RenderIfDirty()       │
│                                          │                     │
│                                          ▼                     │
│                               ┌────────────────────┐           │
│                               │ FrameRenderer      │           │
│                               │ .Render(state, buf) │           │
│                               └─────────┬──────────┘           │
│                                         │                      │
│              buffer.View() → root BufferView                   │
│                       │                                        │
│                .Split(Horizontal, ...)                          │
│                    ┌──────┴──────┐                              │
│                    ▼             ▼                              │
│               mainView     sidebarView                         │
│                    │             │                              │
│           .Split(Vertical, ...) │                              │
│            ┌───┬───┐            │                              │
│            ▼   ▼   ▼            ▼                              │
│     ┌──────┐ gap ┌────────┐ ┌────────┐                        │
│     │Convo │     │Composer│ │Sidebar │   (all IWidget)        │
│     │Widget│     │Widget  │ │Widget  │                        │
│     └──┬───┘     └───┬────┘ └───┬────┘                        │
│        │             │          │                              │
│        └─────────────┴──────────┘                              │
│                      │                                         │
│            each widget receives a BufferView                   │
│            and writes cells directly — clipped automatically   │
│                      │                                         │
│                      ▼                                         │
│              ┌───────────────┐                                 │
│              │ ConsoleBuffer │  (Te)                            │
│              │ dirty-diff    │                                  │
│              │ → ANSI out    │                                  │
│              └───────────────┘                                 │
└─────────────────────────────────────────────────────────────────┘

Key differences from current:
  - No events, no CellRow, no retained renderables
  - Te provides BufferView with automatic clipping — widgets can't escape bounds
  - Layout via BufferView.Split() — no manual Rect passing
  - Render batched by timer — streaming chunks coalesce naturally
```

### Render pipeline detail

```
FrameRenderer.Render(AppState state, ConsoleBuffer buffer)
│
├─ 1. buffer.Clear()
│
├─ 2. var root = buffer.View()
│
├─ 3. Top-level split (sidebar)
│     var [main, sidebar] = root.Split(Horizontal, Fill(), Fixed(36))
│     ┌──────────────────────────┬──────────┐
│     │       main               │ sidebar  │
│     └──────────────────────────┴──────────┘
│     (or just [root] if sidebar not visible)
│
├─ 4. Split main vertically
│     var [convo, gap, composer] = main.Split(Vertical,
│         Fill(), Fixed(1), Fixed(composerHeight))
│     ┌──────────────────────────┐
│     │    convo (BufferView)    │  ← Fill
│     ├──────────────────────────┤
│     │    gap   (BufferView)    │  ← Fixed(1)
│     ├──────────────────────────┤
│     │    composer (BufferView) │  ← Fixed(composerHeight)
│     └──────────────────────────┘
│
├─ 5. Render widgets — each receives its own clipped BufferView
│
│     new ConversationWidget(state.Entries, state.ScrollOffset)
│       .Render(convo)          // writes cells, clipped to convo bounds
│
│     new ComposerWidget(state.InputText, state.CursorIndex, ...)
│       .Render(composer)       // draws border, input, status — clipped
│
│     if sidebar visible:
│       new SidebarWidget(state.ContextUsageText, state.TodoItems)
│         .Render(sidebar)      // clipped to sidebar column
│
│     if no entries:
│       new SplashWidget()
│         .Render(convo)        // centered in conversation area
│
└─ 6. buffer.Render(Console.Out)  ← dirty-diff, emit only changed ANSI
```

### BufferView clipping

This is the key safety property. Widgets cannot draw outside their region:

```
root BufferView (0,0 → 120,40)
  │
  ├── main = root.SubRegion(Rect(0, 0, 84, 40))
  │     writes to absolute cols 0-83 — anything beyond is clipped
  │     │
  │     ├── convo = main.SubRegion(Rect(0, 0, 84, 33))
  │     │     writes to absolute rows 0-32 — can't touch composer
  │     │
  │     ├── gap = main.SubRegion(Rect(0, 33, 84, 1))
  │     │
  │     └── composer = main.SubRegion(Rect(0, 34, 84, 6))
  │           writes to absolute rows 34-39 — can't touch convo
  │           │
  │           └── inner = composer.SubRegion(Rect(1, 1, 82, 4))
  │                 nested clip for content inside border
  │                 absolute region (1, 35, 82, 4) — doubly clipped
  │
  └── sidebar = root.SubRegion(Rect(84, 0, 36, 40))
        writes to absolute cols 84-119 — can't touch main area
```

### Screen layout

```
┌─ convo BufferView ─────────────────────────┬─ sidebar BufferView ─┐
│                                            │                      │
│  ● User message text that may             │  Context              │
│    wrap across multiple lines              │  ████░░ 67%           │
│                                            │                      │
│  ● Assistant response streaming            │  Plan                 │
│    in real time with word wrap             │  ✓ Phase 1            │
│                                            │  ◐ Phase 2            │
│  ● Tool call: bash                        │  ○ Phase 3            │
│    └─ exit code 0 (collapsed)              │                      │
│                                            │                      │
│  ● Subagent: research                     │                      │
│    │ ● searching files...                 │                      │
│    │ ● found 3 matches                    │                      │
│    └─ completed                            │                      │
│                                            │                      │
├─ gap BufferView ───────────────────────────┤                      │
├─ composer BufferView ──────────────────────┤                      │
│ ╭──────────────────────────────────────╮   │                      │
│ │ > type here...        ghost-text     │   │                      │
│ ├──────────────────────────────────────┤   │                      │
│ │ ◐ model-name                         │   │                      │
│ ╰──────────────────────────────────────╯   │                      │
└────────────────────────────────────────────┴──────────────────────┘

Every labeled region is a BufferView. Widgets write into their view
using local coordinates (0,0 = top-left of their region).
BufferView translates to absolute buffer coordinates and clips.
```

### ConversationEntry data model

```
ConversationEntry
├── Kind: EntryKind ──────────── determines circle color + rendering
│   ├── User       → blue ●
│   ├── Assistant  → blue ●
│   ├── Tool       → yellow ● (running) / green ● (done) / red ● (error)
│   ├── Subagent   → blue ● with nested children
│   ├── Error      → red ●
│   └── Info       → dim ●
│
├── Text: string ─────────────── mutable, appended during streaming
├── TextStyle: Style ─────────── resolved at creation (bold for user, etc.)
│
├── ToolStatus: ToolStatus? ──── Running → AwaitingApproval → Completed/Failed
├── ToolResult: string? ──────── collapsed result text (shown with └─ prefix)
├── ToolIsError: bool ────────── red styling for error results
│
├── Children: List<CE>? ──────── subagent's inner entries (max 20 rows visible)
└── SubagentCompleted: bool ──── when true, shows "completed" footer
```

### State mutation and render cycle

```
                        ┌──────────────┐
       keystroke ──────▶│              │──┐
    event routing ─────▶│   AppState   │  │ sets _dirty = true
   throbber timer ─────▶│              │  │
    resize timer ──────▶│              │──┘
                        └──────────────┘
                               ▲
                               │
                          lock { }
                     (all mutations serialized)

              ┌─────────────────────────────┐
              │  16ms timer (≈60 redraws/s) │
              │                             │
              │  if (_dirty) {              │
              │    _dirty = false;          │
              │    lock {                   │
              │      FrameRenderer.Render(  │
              │        state, buffer);      │
              │    }                        │
              │    buffer.Render(out);      │
              │  }                          │
              └─────────────────────────────┘

Benefits:
  - Streaming chunks (100s/sec) batch into ~60 redraws/sec
  - Throbber just sets _dirty, no special render path
  - Single render entry point, easy to reason about
```

### Phase dependency graph

```
Phase 1: Te framework refactor
  │  Style, Cell refactor, Rect, BufferView, Span, Line,
  │  TextWrap, Layout, BorderChars, IWidget
  │  + mechanical fixes to old Ox code for Cell API change
  │
  ▼
Phase 2: App model + widgets + FrameRenderer
  │  AppState, ConversationEntry, FrameRenderer,
  │  ConversationWidget, ComposerWidget, SidebarWidget, SplashWidget
  │
  ▼
Phase 3: New EventRouter
  │  Rewrite to mutate AppState instead of creating renderables
  │
  ▼
Phase 4: Rewire Program.cs + PermissionHandler  ← cutover point
  │  Old rendering code stops being called
  │  16ms render timer replaces Changed event wiring
  │
  ▼
Phase 5: Delete old code + update tests
     Remove 12 files, rewrite TuiRenderingTests
```

## Implementation phases

### Phase 1: Te framework refactor

Refactor Te from a low-level terminal I/O library into an immediate-mode TUI framework.

**New files in Te/Rendering/:**

- `Style.cs` — Style record struct with Over(), factories, resolved accessors
- `Rect.cs` — Rect record struct with Inner(), Intersect(), edge properties
- `BufferView.cs` — Clipped buffer region with all write/split methods
- `Span.cs` — Styled text run
- `Line.cs` — List of Spans
- `TextWrap.cs` — Word-aware wrapping producing Lines
- `Layout.cs` — Constraint-based Rect splitting (Direction enum, Constraint types)
- `BorderChars.cs` — Box-drawing character sets
- `IWidget.cs` — `void Render(BufferView view)` interface

**Modified files in Te/Rendering/:**

- `Cell.cs` — Refactored to `Cell(char Rune, Style Style)`. Old constructor removed.
- `ConsoleBuffer.cs` — SetCell/FillCells signatures updated for Style. `View()` and `View(Rect)` added. Render() SGR emission reads from `cell.Style`.

**Mechanical fixes in Ox (to keep build green):**

- Every `new Cell(rune, fg, bg, deco)` → `new Cell(rune, Style.Of(fg, bg, deco))`
- Every `cell.Foreground` → `cell.Style.ResolvedFg`, etc.
- Every `SetCell(x, y, rune, fg, bg, deco)` → `SetCell(x, y, rune, Style.Of(fg, bg, deco))`
- These are throwaway changes — the old Ox files get deleted in Phase 5.

**Unit tests:**

- Rect: Inner(), Intersect(), edge properties, IsEmpty
- Style: Over() composability, resolved defaults, factories
- BufferView: SetCell clipping, WriteString clipping, SubRegion composition, Split()
- TextWrap: word wrap, hard break, newlines, multi-span wrapping
- Layout.Split: Fixed, Fill, Percentage, mixed constraints, both directions
- BorderChars: DrawBorder via BufferView

### Phase 2: Application model + widgets + FrameRenderer

**New files in Ox/Model/:**

- `AppState.cs` — All fields listed above (Entries, ScrollOffset, CursorIndex, etc.)
- `ConversationEntry.cs` — Entry class with Kind, Text, tool/subagent fields, computed CircleColor

**New files in Ox/Rendering/:**

- `FrameRenderer.cs` — Static Render method: clear → view → split → widgets → flush
- `Widgets/ConversationWidget.cs`
- `Widgets/ComposerWidget.cs`
- `Widgets/SidebarWidget.cs`
- `Widgets/SplashWidget.cs`

**Tests:**

- Construct AppState with sample entries, render to ConsoleBuffer, assert cell contents at specific coordinates
- Test each widget independently: construct with known data, render into a BufferView, verify output
- Test FrameRenderer layout: verify sidebar/conversation/composer regions get correct dimensions

### Phase 3: New EventRouter

Rewrite EventRouter to mutate `AppState` instead of creating IRenderable objects.

Same correlation logic:

- `_currentTextIndex` tracks which entry is being streamed to (index into `state.Entries`)
- `_toolCallMap` maps CallId → entry index
- `_subagentCallIds`, `_pendingSubagentCalls`, `_callIdToSubagentId` — same pairing logic
- Subagent children are `ConversationEntry.Children` instead of `SubagentRenderable._innerList`

Port/rewrite EventRouter tests from TuiRenderingTests to verify mutations on AppState.

### Phase 4: Rewire Program.cs + PermissionHandler

Replace the current wiring:

- Delete: `EventList` creation, `Viewport` creation, `Sidebar`/`ContextSection`/`TodoSection` creation
- Add: `AppState` creation, 16ms render timer, `RenderIfDirty()` method
- `EventRouter` now takes `AppState` instead of `EventList`
- PermissionHandler signature: replace `Viewport` with lambdas for `SetInputPrompt` and `RequestRedraw`
- Context usage updates write to `state.ContextUsageText` instead of `ContextSection.SetUsage`
- TodoStore changes write to `state.TodoItems` instead of firing through `TodoSection`

This is the cutover — old rendering code stops being called.

### Phase 5: Delete old code + update tests

**Delete from Ox/Rendering/ (12 files):**
`IRenderable.cs`, `ISidebarSection.cs`, `CellRow.cs`, `EventList.cs`, `TextRenderable.cs`, `ToolRenderable.cs`, `SubagentRenderable.cs`, `TreeChrome.cs`, `Sidebar.cs`, `ContextSection.cs`, `TodoSection.cs`, `Viewport.cs`

**Update tests:**

- Remove old TuiRenderingTests that test deleted types (ViewportBufferTests, TextRenderableTests, ToolRenderableTests, SubagentRenderableTests, EventListTests, ContextSectionTests, TodoSectionTests, ViewportSidebarTests, ViewportSplashTests)
- New tests already written in Phases 1-3 cover the same functionality
- Verify no references to `IRenderable`, `CellRow`, `EventList` remain

**Update Te.Tests:**

- ConsoleBufferTests updated for new Cell/Style API
- Add tests for new Te types (covered in Phase 1)

## Verification

- `dotnet test` after every phase
- After Phase 4: manual smoke test — start TUI, send messages, observe streaming text, tool calls with lifecycle colors, subagent blocks, sidebar context/todos, throbber animation, ghost-text autocomplete, resize, Escape cancel, Ctrl+C exit, permission prompts, scroll-back through history
- After Phase 5: `grep -rn "IRenderable\|CellRow\|EventList" src/Ox/` returns nothing
