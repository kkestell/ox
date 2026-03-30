# Terminal Framework (Ur.Terminal)

> Part of: [Ur Architecture](index.md)

## Purpose and Scope

A minimal TUI framework that provides the building blocks for terminal-based UIs. Owns the terminal HAL (raw mode, ANSI output, key input), a cell-based rendering model, a layer/compositor system with shadow support, a component contract, and a frame-based render loop. Purpose-built for Ur's needs but decoupled from any Ur domain types — the framework knows nothing about chat, LLMs, or sessions.

### Non-Goals

- Not a general-purpose widget toolkit. No built-in text input, list view, or dialog widgets. The framework provides the contract (`IComponent`); applications implement their own components.
- No layout engine. Applications compute their own rects (region sizes, positions). The framework renders what it's told.
- No focus system. Applications route key events to components based on their own state.
- No component tree, dirty tracking, or lifecycle management. Components are stateless renderers from the framework's perspective — the application owns their state and decides when to call them.
- No text shaping, Unicode grapheme segmentation, or bidirectional text. Cells are one character wide. Full-width characters and combining marks are out of scope for v1.
- No mouse input.

## Context

### Dependencies

| Dependency | What it provides | Interface |
|---|---|---|
| .NET runtime | Console I/O, threading, async | `Console`, `Thread`, `Task` |
| POSIX terminal | Raw mode, `/dev/tty` | `stty`, file I/O |

### Dependents

| Dependent | What it needs | Interface |
|---|---|---|
| Ur.Tui (chat app) | Terminal setup, rendering, input, compositing | All public types in this project |

## Data Structures

### Color

- **Purpose:** 24-bit true color value.
- **Shape:** `readonly record struct Color(byte R, byte G, byte B)`
- **Operations:** `Dim(float factor)` — returns a new color with each channel multiplied by factor. Used by the compositor for shadow blending.
- **Why true color:** Shadow compositing requires arithmetic color modulation. ANSI-256 palette indices don't compose — you can't dim index 42 by 50%. True color is supported by all modern terminals (iTerm2, kitty, WezTerm, GNOME Terminal, Windows Terminal).

### Cell

- **Purpose:** A single terminal cell — the atomic unit of the display.
- **Shape:** `readonly record struct Cell(char Char, Color Fg, Color Bg)`
- **Sentinel:** `Cell.Transparent` — a static value with `Char = '\0'`. Used to indicate "nothing drawn here" in a buffer. The compositor treats transparent cells as pass-through.
- **Invariants:** One `Cell` = one terminal column. No wide character support in v1.
- **Why this shape:** Char + Fg + Bg is the complete state of a terminal cell. The struct is small enough to copy freely. The transparent sentinel avoids nullable value types.

### Rect

- **Purpose:** A rectangular region within a buffer.
- **Shape:** `readonly record struct Rect(int X, int Y, int Width, int Height)`
- **Operations:** `Contains(int x, int y)`, `Intersect(Rect other)`, `Clamp(int x, int y)`.
- **Why this shape:** Components need to know their drawable area. Rects are passed to `IComponent.Render` and used by the compositor for layer bounds.

### Buffer

- **Purpose:** A rectangular grid of cells. The fundamental drawing surface.
- **Shape:** Width, Height, and a row-major `Cell[]` array.
- **Operations:**
  - `Set(int x, int y, Cell cell)` — write a single cell
  - `Get(int x, int y) → Cell` — read a single cell
  - `Fill(Rect area, Cell cell)` — fill a region
  - `WriteString(int x, int y, ReadOnlySpan<char> text, Color fg, Color bg)` — write text horizontally, one char per cell
  - `DrawBox(Rect area, Color fg, Color bg)` — draw a box-drawing border around a region
  - `Clear()` — reset all cells to transparent
- **Invariants:** All coordinates are bounds-checked. Out-of-bounds writes are silently clipped (not exceptions) — this simplifies component rendering near edges.
- **Why this shape:** The buffer is the core abstraction between components (which write cells) and the compositor (which reads them). Drawing primitives live here because they're pure data operations on the cell grid, not rendering/output concerns.

### Layer

- **Purpose:** A buffer positioned on screen with a shadow mask for compositing.
- **Shape:**
  - `X, Y: int` — position offset on the screen
  - `Content: Buffer` — the cell grid (same size as the layer)
  - `ShadowMask: bool[]` — per-cell flag; `true` means "dim whatever is below, don't draw content here"
- **Operations:**
  - `Clear()` — reset content to transparent, shadow mask to false
  - `MarkShadow(Rect region)` — set shadow mask within a region
- **Invariants:**
  - Shadow and content are mutually exclusive per cell: if `ShadowMask[i]` is true, the cell's content is ignored by the compositor.
  - Position can be negative (layer partially off-screen left/top) — the compositor clips.
- **Why this shape:** Separating the shadow mask from the content buffer means components never need to know about shadows. A component renders to `layer.Content` within its rect. The application stamps the shadow region on the mask separately. The compositor combines both.

### KeyEvent

- **Purpose:** An abstracted keyboard event.
- **Shape:** `readonly record struct KeyEvent(Key Key, Modifiers Mods, char? Char)`
  - `Key`: enum covering named keys (A-Z, Digits, Enter, Escape, Tab, Backspace, Delete, Up/Down/Left/Right, Home, End, PageUp, PageDown, F1-F12)
  - `Modifiers`: flags enum (`None`, `Shift`, `Ctrl`, `Alt`)
  - `Char`: the printable character if applicable, null for non-printable keys
- **Why this shape:** The `Key` enum + `Modifiers` flags provide a stable API that works whether the backend parses basic ANSI sequences (v1) or Kitty keyboard protocol (later). Code that checks `key.Mods.HasFlag(Modifiers.Shift)` compiles today — it just returns false until Kitty support lands. The `Char` field gives components direct access to printable input without mapping through the enum.

### IComponent

- **Purpose:** The minimal contract for renderable, interactive UI elements.
- **Shape:**
  ```
  interface IComponent
  {
      void Render(Buffer buffer, Rect area);
      bool HandleKey(KeyEvent key);
  }
  ```
- **`Render`:** Draw into `buffer` within `area`. May be called every frame. Must not access terminal I/O directly. Must stay within `area` bounds (out-of-bounds writes are clipped by Buffer, but components should respect their area).
- **`HandleKey`:** Process a key event. Return `true` if consumed, `false` to let the application route it elsewhere.
- **What the framework does NOT enforce:** The application decides which component gets `Render` calls (all of them, every frame? only dirty ones?), which component gets key events (focus routing), and how components compose spatially. The interface is just the calling convention.

## Internal Design

### Module Structure

```
Ur.Terminal/
  Core/
    Color.cs
    Cell.cs
    Rect.cs
    Buffer.cs
  Rendering/
    Layer.cs
    Compositor.cs
    Screen.cs           — diff engine: previous Buffer + current Buffer → ANSI byte output
  Input/
    Key.cs
    Modifiers.cs
    KeyEvent.cs
    KeyReader.cs        — background thread, reads /dev/tty, enqueues KeyEvents
  Terminal/
    ITerminal.cs        — HAL interface
    AnsiTerminal.cs     — ANSI implementation
  Components/
    IComponent.cs
  App/
    RenderLoop.cs       — frame timer at configurable FPS
```

### Compositor

The compositor takes an ordered list of layers (bottom-to-top) and produces a single `Buffer` the size of the screen.

For each screen position `(sx, sy)`:
1. Start with a default cell (space, default fg/bg).
2. Walk layers bottom-to-top:
   - Map screen position to layer-local coordinates: `(lx, ly) = (sx - layer.X, sy - layer.Y)`
   - If out of layer bounds → skip
   - If `layer.ShadowMask[lx, ly]` → dim the accumulated cell's Fg and Bg
   - Else if `layer.Content[lx, ly]` is not transparent → replace accumulated cell
3. The accumulated cell is the final output for this position.

Shadow dimming applies a configurable factor (e.g., 0.4) to both Fg and Bg RGB channels. The character is preserved from whatever was below — that's the "show through" effect.

### Screen (Diff Engine)

`Screen` takes the current composed `Buffer` and the previous frame's `Buffer`, and produces the minimal ANSI byte sequence to update the terminal.

Strategy: walk cell-by-cell. For each changed cell:
1. Move cursor to position (skip if adjacent to last write — cursor auto-advances).
2. Emit SGR sequence for fg/bg if colors changed from the last emitted cell.
3. Emit the character byte(s).

Batches output into a single write syscall per frame. Uses `ArrayBufferWriter<byte>` (as the spike already does) for efficient buffer management.

This is where **all ANSI escape code knowledge lives**. Nothing above `Screen` knows about `\e[` sequences.

### KeyReader

Runs on a dedicated background thread (not async — blocking reads from `/dev/tty`). Parses incoming bytes into `KeyEvent` values.

v1 parsing: basic ANSI escape sequences for arrow keys, function keys, and single-byte keys. Modifiers are not reliably detectable from basic ANSI, so `Modifiers` is always `None`.

Future: Kitty keyboard protocol parsing. Same `KeyEvent` output, but `Modifiers` is populated. App code doesn't change.

Parsed events are placed into a concurrent queue. The render loop (or app) drains the queue each frame.

### RenderLoop

Drives the frame cycle at a configurable target FPS:

1. Calculate next frame deadline from target interval (e.g., 33ms for 30 FPS).
2. Drain pending key events from `KeyReader`'s queue.
3. Yield keys to the application for processing (callback or return).
4. Invoke the compositor → composed `Buffer`.
5. Diff against previous frame via `Screen` → ANSI output.
6. Flush to terminal.
7. Swap buffers (current becomes previous).
8. Sleep until next frame deadline.

The render loop provides the heartbeat. State changes between frames (from key events, from app-side async operations like LLM streaming) are batched automatically — the next frame picks up whatever changed.

### Terminal HAL (ITerminal / AnsiTerminal)

Abstracts the physical terminal:

- `EnterRawMode()` / `ExitRawMode()` — via `stty` (as the spike does)
- `EnterAlternateBuffer()` / `ExitAlternateBuffer()` — `\e[?1049h` / `\e[?1049l`
- `HideCursor()` / `ShowCursor()` — `\e[?25l` / `\e[?25h`
- `Width` / `Height` — current terminal dimensions
- `Write(ReadOnlySpan<byte> data)` — raw output
- `OpenInput() → Stream` — open `/dev/tty` for reading

`ITerminal` exists so tests can substitute a fake terminal. The real implementation (`AnsiTerminal`) manages `IDisposable` cleanup to ensure the terminal is always restored on exit (even on crash — via `Console.CancelKeyPress` and `AppDomain.ProcessExit` handlers).

## Quality Attributes

| Attribute | Requirement | Implication for design |
|---|---|---|
| Performance | Render at configurable FPS (up to 60) without visible lag or flicker | Diff-based output (only changed cells). Single write syscall per frame. No allocations in the hot path (render/diff). |
| Simplicity | Framework fits in one person's head | No widget tree, no layout engine, no reactive data binding. Just buffers, layers, and a render loop. |
| Portability | macOS and Linux terminals | ANSI escape sequences only. `/dev/tty` for input. `stty` for raw mode. No Windows support (matches Ur's non-goal). |
| Extensibility | Kitty keyboard protocol can be added without breaking app code | `KeyEvent` abstraction with `Modifiers` flags. The HAL implementation changes; the event contract is stable. |

## Design Decisions

### Cell grid with transparent sentinel, not nullable Cell

- **Context:** Layers need a way to represent "nothing drawn here."
- **Options considered:**
  - `Cell?` (nullable value type) — idiomatic C# but adds 1 byte overhead per cell (nullable flag) and complicates hot-path code with `.Value` access.
  - Sentinel value (`Char = '\0'`) — no overhead, simple equality check.
- **Choice:** Sentinel.
- **Rationale:** The cell grid is the hot path. Thousands of cells per frame. The sentinel is zero-cost and the convention is easy to follow. `'\0'` will never appear as actual terminal content.

### Shadow as a separate mask, not a blend mode in Cell

- **Context:** Modal overlays need shadow regions where the character below shows through but colors are dimmed.
- **Options considered:**
  - Blend mode enum per cell (`Opaque | Transparent | Shadow`) — puts compositing concerns into the cell value type. Every cell carries a field most cells never use.
  - Separate shadow mask on the layer — clean separation. Cells are just content. Compositing behavior is a layer-level concern.
- **Choice:** Separate mask.
- **Rationale:** Components write cells without knowing about shadows. The application stamps the shadow region on the layer mask. The compositor handles the blending. This keeps the `Cell` type minimal and the component contract simple.

### True color (24-bit RGB), not ANSI-256

- **Context:** Need arithmetic color modulation for shadow dimming.
- **Options considered:**
  - ANSI-256 palette — smaller, more compatible. But palette indices don't compose: you can't dim index 42 by 50%.
  - True color (RGB) — allows `Dim(factor)` = multiply each channel. Supported by all target terminals.
- **Choice:** True color.
- **Rationale:** Shadow compositing is a hard requirement. True color makes it trivial. All terminals Ur targets support it.

### Frame-based render loop, not event-driven rendering

- **Context:** When to render — on every state change, or on a fixed timer?
- **Options considered:**
  - Event-driven — render immediately after any state change. Lowest latency for single events. But streaming LLM responses produce hundreds of events per second, causing excessive redraws.
  - Frame-based — render at a fixed rate (configurable FPS). State changes between frames are batched automatically.
- **Choice:** Frame-based.
- **Rationale:** LLM streaming produces a high event rate. Frame-based rendering naturally batches rapid updates. Configurable FPS lets the app trade latency for CPU usage. This matches the proven game-loop architecture.

### Background thread for input, not async

- **Context:** How to read keyboard input without blocking the render loop.
- **Options considered:**
  - Async I/O on `/dev/tty` — would integrate with the async render loop. But .NET's `Console.ReadKey` is synchronous, and async file reads on `/dev/tty` are unreliable across platforms.
  - Dedicated background thread — blocking reads in a tight loop. Simple, reliable, cross-platform.
- **Choice:** Background thread (as validated by the spike).
- **Rationale:** The spike proved this works. A dedicated thread for blocking reads is the standard approach for terminal input in non-Windows systems. Events flow to the render loop via a concurrent queue.

## Open Questions

- **Question:** Should `Buffer` drawing primitives include text wrapping (word wrap within a rect)?
  **Context:** The message list component needs to wrap long messages. Should wrapping logic live in `Buffer.WriteString` (framework) or in the component (app)?
  **Current thinking:** Wrapping is presentation logic that depends on content semantics (word boundaries, markdown structure). The framework provides `WriteString` (one line, clips at rect edge). The app handles wrapping before calling `WriteString` per line.

- **Question:** What's the right default shadow dim factor?
  **Context:** The compositor needs a factor for dimming colors in shadow regions. Could be hardcoded, could be configurable per-layer, could be configurable globally.
  **Current thinking:** Configurable on the `Layer` (a `ShadowDimFactor` property, default 0.4). Different layers might want different shadow intensities.

- **Question:** Should the `RenderLoop` provide a hook for app-side frame processing, or should the app own the loop and call framework methods?
  **Context:** Two models: (a) framework owns the loop, calls an app callback each frame; (b) app owns the loop, calls `compositor.Compose()` and `screen.Flush()` at its own pace.
  **Current thinking:** Framework owns the loop with a callback. The app provides a `ProcessFrame(ReadOnlySpan<KeyEvent> keys)` callback. This keeps frame timing precise and avoids the app reimplementing the sleep/deadline logic.
