# Ur.Terminal: Phased Implementation Plan

## Goal

Implement the Ur.Terminal framework library — cell grid, buffer, layer/compositor with shadows, key input, diff-based screen output, and a frame-based render loop. Each phase is independently testable and builds on the previous.

## Desired outcome

A working `Ur.Terminal` project that the `Ur.Tui` chat app can build on. Every public type has unit test coverage. A demo app proves the framework works end-to-end (key input, composited rendering, shadow effects, frame loop).

## Related documents

- `docs/terminal-framework.md` — Architecture: data structures, compositor algorithm, diff engine, HAL, design decisions
- `docs/cli-tui.md` — The chat app that will consume this framework (informs what primitives we need)

## Related code

- `Ur.Tui/Program.cs` — The spike. Validates raw `/dev/tty` input, `stty` raw mode, `ArrayBufferWriter<byte>` for output, dual-task architecture. Reference implementation for the HAL.
- `Ur.Tests/Ur.Tests.csproj` — Test project template: xUnit 2.9.3, .NET 10.0, global `using Xunit`
- `Ur.Tests/HostSessionApiTests.cs` — Test naming convention: `Method_Scenario_Expected` with `[Fact]`
- `Ur/Ur.csproj` — Library project template: .NET 10.0, ImplicitUsings, Nullable, no OutputType
- `Ur.slnx` — Solution file to update with new projects

## Current state

- `Ur.Tui/` exists as a spike — raw async I/O demo, not structured as a framework
- No `Ur.Terminal/` project yet
- No test project for terminal code

## Constraints

- .NET 10.0, AoT compatible (no reflection in hot paths)
- macOS and Linux only (POSIX terminals, `/dev/tty`, `stty`)
- xUnit for tests, following existing `Method_Scenario_Expected` naming
- The framework must not reference any Ur library types — compile-time boundary via separate project

---

## Phase 1: Project Setup + Core Data Types

Create the project structure and implement the foundational data types that everything else builds on.

### Tasks

- [x] **Create `Ur.Terminal/Ur.Terminal.csproj`** — Class library, .NET 10.0, ImplicitUsings, Nullable. No NuGet dependencies.
- [x] **Create `Ur.Terminal.Tests/Ur.Terminal.Tests.csproj`** — xUnit test project referencing `Ur.Terminal`. Match the pattern in `Ur.Tests.csproj` (xunit 2.9.3, Microsoft.NET.Test.Sdk, coverlet, global `using Xunit`).
- [x] **Add both projects to `Ur.slnx`.**
- [x] **Implement `Color`** — `readonly record struct Color(byte R, byte G, byte B)`. Static members: `Color.White`, `Color.Black`, `Color.Default` (or whatever makes sense as defaults). Method: `Dim(float factor)` returns new Color with each channel multiplied and clamped to 0-255.
- [x] **Implement `Cell`** — `readonly record struct Cell(char Char, Color Fg, Color Bg)`. Static member: `Cell.Transparent` with `Char = '\0'`. Property: `IsTransparent` — checks `Char == '\0'`.
- [x] **Implement `Rect`** — `readonly record struct Rect(int X, int Y, int Width, int Height)`. Methods: `Contains(int x, int y)`, `Intersect(Rect other)` (returns the overlapping rect, or empty), computed properties `Right`, `Bottom`.
- [x] **Implement `Buffer`** — Width, Height, row-major `Cell[]`. Constructor fills with transparent cells. Methods:
  - `Set(int x, int y, Cell cell)` — clip out-of-bounds silently
  - `Get(int x, int y) → Cell` — return transparent for out-of-bounds
  - `Fill(Rect area, Cell cell)` — fill a rect region
  - `WriteString(int x, int y, ReadOnlySpan<char> text, Color fg, Color bg)` — one char per cell, clip at right edge
  - `DrawBox(Rect area, Color fg, Color bg)` — box-drawing characters for borders (`┌─┐│└┘`)
  - `Clear()` — reset all to transparent
- [x] **Implement `Key` enum** — Named keys: `A`-`Z`, `D0`-`D9`, `Enter`, `Escape`, `Tab`, `Backspace`, `Delete`, `Up`, `Down`, `Left`, `Right`, `Home`, `End`, `PageUp`, `PageDown`, `F1`-`F12`, `Space`, `Unknown`.
- [x] **Implement `Modifiers` flags enum** — `None = 0`, `Shift = 1`, `Ctrl = 2`, `Alt = 4`.
- [x] **Implement `KeyEvent`** — `readonly record struct KeyEvent(Key Key, Modifiers Mods, char? Char)`.
- [x] **Define `IComponent` interface** — `void Render(Buffer buffer, Rect area)`, `bool HandleKey(KeyEvent key)`.

### Tests

- [x] `Color_Dim_HalvesChannels` — `new Color(100, 200, 50).Dim(0.5f)` → `Color(50, 100, 25)`
- [x] `Color_Dim_ClampsToZero` — `Dim(0f)` → `Color(0, 0, 0)`
- [x] `Color_Dim_ClampsToMax` — `Dim(1f)` returns same values (no overflow)
- [x] `Cell_Transparent_IsTransparent` — `Cell.Transparent.IsTransparent` is true
- [x] `Cell_WithContent_IsNotTransparent` — `new Cell('A', ...).IsTransparent` is false
- [x] `Rect_Contains_InsidePoint` — point inside returns true
- [x] `Rect_Contains_OutsidePoint` — point outside returns false
- [x] `Rect_Intersect_Overlapping` — two overlapping rects produce correct intersection
- [x] `Rect_Intersect_NonOverlapping` — non-overlapping rects produce empty rect
- [x] `Buffer_Set_WithinBounds_StoresCell` — Set then Get returns same cell
- [x] `Buffer_Set_OutOfBounds_SilentlyClips` — no exception, cell not stored
- [x] `Buffer_Get_OutOfBounds_ReturnsTransparent`
- [x] `Buffer_Fill_SetsRegion` — fill a rect, verify all cells in region match
- [x] `Buffer_WriteString_WritesChars` — write "Hello", verify 5 cells
- [x] `Buffer_WriteString_ClipsAtEdge` — string extending past buffer width is truncated
- [x] `Buffer_DrawBox_DrawsBorder` — verify corner and edge characters at expected positions
- [x] `Buffer_Clear_AllTransparent` — after clear, every cell is transparent

### Validation

- `dotnet build Ur.Terminal` — compiles with no warnings
- `dotnet test Ur.Terminal.Tests` — all tests pass

---

## Phase 2: Layer + Compositor

Implement the compositing system: layers with shadow masks and the compositor that stacks them.

### Tasks

- [x] **Implement `Layer`** — Fields: `X`, `Y` (position), `Content` (Buffer), `ShadowMask` (`bool[]`, same dimensions as buffer). Constructor takes position + dimensions, creates internal Buffer and mask. Methods:
  - `Clear()` — clear content buffer + reset shadow mask
  - `MarkShadow(Rect region)` — set mask to true within region (clipped to layer bounds)
  - `Resize(int width, int height)` — recreate buffer and mask at new size
- [x] **Implement `Compositor`** — Constructor takes screen width + height. Maintains an ordered `List<Layer>` (bottom-to-top). Methods:
  - `AddLayer(Layer layer)` — append to stack
  - `RemoveLayer(Layer layer)` — remove from stack
  - `Compose() → Buffer` — produce final screen buffer using the algorithm from the architecture doc:
    1. Initialize output buffer (screen-sized, filled with default cell: space, default fg/bg)
    2. For each layer bottom-to-top, for each screen position:
       - Map to layer-local coordinates
       - If shadow mask is set → dim accumulated cell's Fg and Bg (use `Color.Dim` with layer's shadow factor)
       - Else if content cell is not transparent → replace accumulated cell
    3. Return output buffer
  - `Resize(int width, int height)` — update screen dimensions
- [x] **Add `ShadowDimFactor` property to `Layer`** — `float`, default `0.4`. Used by compositor when applying shadow.

### Tests

- [x] `Compositor_SingleLayer_OpaqueCell_AppearsInOutput` — one layer with a cell set → output has that cell
- [x] `Compositor_SingleLayer_TransparentCell_ShowsDefault` — transparent cell → output is the default (space)
- [x] `Compositor_TwoLayers_TopOpaqueOverridesBottom` — bottom has 'A', top has 'B' at same position → output is 'B'
- [x] `Compositor_TwoLayers_TopTransparentShowsBottom` — bottom has 'A', top transparent → output is 'A'
- [x] `Compositor_Shadow_PreservesCharacter` — bottom has 'X' with Color(200,200,200), top has shadow → output char is 'X' with dimmed colors
- [x] `Compositor_Shadow_DimsFgAndBg` — verify dimming math: Color(200,100,50).Dim(0.4) applied correctly
- [x] `Compositor_LayerOffset_PositionsCorrectly` — layer at (5, 3) with cell at local (0,0) → appears at screen (5, 3)
- [x] `Compositor_LayerPartiallyOffScreen_Clips` — layer positioned so part is off-screen → visible cells compose, off-screen ignored
- [x] `Compositor_ShadowAndContent_MutuallyExclusive` — cell with shadow mask set has its content ignored; shadow applies to whatever is below
- [x] `Layer_Clear_ResetsContentAndShadow` — after clear, all cells transparent, no shadow
- [x] `Layer_MarkShadow_SetsRegion` — mark a rect, verify shadow mask within region is true, outside is false

### Validation

- `dotnet test Ur.Terminal.Tests` — all Phase 1 + Phase 2 tests pass

---

## Phase 3: Screen (Diff Engine) + Terminal HAL

Implement the ANSI output layer: the diff engine that converts buffer changes to escape sequences, and the terminal abstraction.

### Tasks

- [x] **Define `ITerminal` interface** — The HAL contract:
  - `int Width { get; }`
  - `int Height { get; }`
  - `void EnterRawMode()`
  - `void ExitRawMode()`
  - `void EnterAlternateBuffer()`
  - `void ExitAlternateBuffer()`
  - `void HideCursor()`
  - `void ShowCursor()`
  - `void SetCursorPosition(int x, int y)`
  - `void Write(ReadOnlySpan<byte> data)`
  - `Stream OpenInput()` — for KeyReader to read from
- [x] **Implement `Screen`** — The diff engine. Stateless utility. Methods:
  - `WriteDiff(Buffer current, Buffer previous, IBufferWriter<byte> output)` — walk cell-by-cell, for each changed cell emit ANSI sequences:
    - Cursor positioning: `\e[{row};{col}H` (1-based). Skip if adjacent to last write (cursor auto-advances).
    - SGR for fg: `\e[38;2;{r};{g};{b}m` (true color). Skip if same as last emitted.
    - SGR for bg: `\e[48;2;{r};{g};{b}m`. Skip if same as last emitted.
    - Character byte(s) (UTF-8).
    - Track last-emitted position and colors to minimize escape sequences.
  - `WriteFullFrame(Buffer buffer, IBufferWriter<byte> output)` — write every cell (for first frame when there's no previous).
- [x] **Implement `AnsiTerminal`** — `ITerminal` implementation using POSIX + ANSI:
  - `EnterRawMode()` / `ExitRawMode()` — via `stty -echo -icanon min 1` / `stty sane` (as spike does)
  - `EnterAlternateBuffer()` — write `\e[?1049h`
  - `ExitAlternateBuffer()` — write `\e[?1049l`
  - `HideCursor()` — write `\e[?25l`
  - `ShowCursor()` — write `\e[?25h`
  - `Width` / `Height` — `Console.WindowWidth` / `Console.WindowHeight`
  - `Write(ReadOnlySpan<byte>)` — write to stdout
  - `OpenInput()` — open `/dev/tty` as `FileStream`
  - Implement `IDisposable`: exit raw mode, exit alternate buffer, show cursor on dispose. Also register `Console.CancelKeyPress` and `AppDomain.ProcessExit` for crash safety.
- [x] **Create `TestTerminal`** — Mock `ITerminal` for testing. Records all `Write` calls into a `List<byte[]>`. Configurable `Width`/`Height`. Allows tests to inspect output.

### Tests

- [x] `Screen_WriteDiff_UnchangedBuffer_NoOutput` — identical previous and current → zero bytes emitted
- [x] `Screen_WriteDiff_SingleCellChange_EmitsPositionAndCell` — change one cell → output contains cursor positioning + SGR + char
- [x] `Screen_WriteDiff_AdjacentChanges_SkipsCursorMove` — two horizontally adjacent cells changed → cursor position emitted once, chars emitted sequentially
- [x] `Screen_WriteDiff_SameColorAsLast_SkipsSGR` — two changed cells with same colors → SGR emitted once, not twice
- [x] `Screen_WriteDiff_DifferentColors_EmitsNewSGR` — cells with different colors → each gets its own SGR
- [x] `Screen_WriteDiff_TrueColorFormat` — verify `\e[38;2;R;G;Bm` format for fg and `\e[48;2;R;G;Bm` for bg
- [x] `Screen_WriteFullFrame_EmitsAllCells` — every non-transparent cell appears in output
- [x] `AnsiTerminal_Dispose_RestoresState` — (can test by checking that ExitRawMode/ExitAlternateBuffer/ShowCursor are called, or just verify the API exists)

### Validation

- `dotnet test Ur.Terminal.Tests` — all Phase 1-3 tests pass
- Manual: write a small program that creates an `AnsiTerminal`, enters raw/alt-buffer, writes a diff frame, exits. Verify terminal is restored cleanly.

---

## Phase 4: Input (Key Parser + KeyReader)

Implement keyboard input: the ANSI escape sequence parser that converts byte streams to `KeyEvent` values, and the background thread that reads from `/dev/tty`.

### Tasks

- [x] **Implement `KeyParser`** — Static/instance class. Method: `Parse(ReadOnlySpan<byte> input, out int consumed) → KeyEvent?`. Parses one key event from the start of the input span. Returns null if the bytes don't form a complete event yet (partial escape sequence). Sets `consumed` to the number of bytes used.
  - Single printable byte → `KeyEvent(Key.A..Z or Key.D0..D9, None, char)` (map ASCII)
  - `0x0D` (CR) → `KeyEvent(Key.Enter, None, null)`
  - `0x1B` alone (no more bytes, or next byte not `[`) → `KeyEvent(Key.Escape, None, null)`
  - `0x1B 0x5B ...` (CSI sequences):
    - `0x41` → `Key.Up`
    - `0x42` → `Key.Down`
    - `0x43` → `Key.Right`
    - `0x44` → `Key.Left`
    - `0x48` → `Key.Home`
    - `0x46` → `Key.End`
    - `0x35 0x7E` → `Key.PageUp`
    - `0x36 0x7E` → `Key.PageDown`
    - `0x33 0x7E` → `Key.Delete`
    - `0x31 0x35..0x39 0x7E` and `0x32 0x30..0x34 0x7E` → `Key.F5`-`Key.F12`
    - `0x31 0x31..0x34 0x7E` → `Key.F1`-`Key.F4` (where applicable)
  - `0x7F` or `0x08` → `Key.Backspace`
  - `0x09` → `Key.Tab`
  - `0x20` → `Key.Space` with `Char = ' '`
  - `0x01`-`0x1A` (Ctrl+A through Ctrl+Z) → map to `Key.A..Z` with `Modifiers.Ctrl`
  - Anything unrecognized → `Key.Unknown`
  - All `Modifiers` are `None` for now except Ctrl (which is detectable from byte value).
- [x] **Implement `KeyReader`** — Takes an `ITerminal` (for `OpenInput()`). Method: `Start(CancellationToken ct)` spawns a background thread that:
  - Opens the input stream
  - Reads bytes in a loop (blocking)
  - Feeds bytes to `KeyParser`
  - Places parsed `KeyEvent` values into a `ConcurrentQueue<KeyEvent>`
  - Method: `Drain(List<KeyEvent> output)` — dequeue all pending events into the caller's list. Called by the render loop each frame.
  - Method: `Stop()` — signal the background thread to exit.

### Tests

- [x] `KeyParser_PrintableChar_ReturnsKeyAndChar` — byte `0x61` ('a') → `KeyEvent(Key.A, None, 'a')`
- [x] `KeyParser_Enter_ReturnsEnter` — byte `0x0D` → `KeyEvent(Key.Enter, None, null)`
- [x] `KeyParser_Escape_ReturnsEscape` — byte `0x1B` alone → `KeyEvent(Key.Escape, None, null)`
- [x] `KeyParser_ArrowUp_ReturnsUp` — bytes `0x1B 0x5B 0x41` → `KeyEvent(Key.Up, None, null)`
- [x] `KeyParser_ArrowDown_ReturnsDown`
- [x] `KeyParser_ArrowLeft_ReturnsLeft`
- [x] `KeyParser_ArrowRight_ReturnsRight`
- [x] `KeyParser_PageUp_ReturnsPageUp` — bytes `0x1B 0x5B 0x35 0x7E`
- [x] `KeyParser_PageDown_ReturnsPageDown`
- [x] `KeyParser_Delete_ReturnsDelete` — bytes `0x1B 0x5B 0x33 0x7E`
- [x] `KeyParser_Backspace_ReturnsBackspace` — byte `0x7F`
- [x] `KeyParser_Tab_ReturnsTab` — byte `0x09`
- [x] `KeyParser_Space_ReturnsSpaceWithChar` — byte `0x20` → `KeyEvent(Key.Space, None, ' ')`
- [x] `KeyParser_CtrlC_ReturnsCtrlModifier` — byte `0x03` → `KeyEvent(Key.C, Ctrl, null)`
- [x] `KeyParser_MultipleKeysInBuffer_ParsesFirst` — buffer with two key sequences → first parsed, `consumed` indicates bytes used
- [x] `KeyParser_IncompleteEscape_ReturnsNull` — `0x1B 0x5B` with no following byte → null (need more data)
- [x] `KeyParser_UnknownSequence_ReturnsUnknown`

### Validation

- `dotnet test Ur.Terminal.Tests` — all Phase 1-4 tests pass
- Manual: modify the spike to use `KeyParser` + `KeyReader` instead of raw byte display. Verify arrow keys, escape, backspace, Ctrl+C all produce correct `KeyEvent` values.

---

## Phase 5: Render Loop + Integration

Tie everything together: the frame-based render loop, and a demo app that proves the framework works end-to-end.

### Tasks

- [x] **Implement `RenderLoop`** — Constructor takes `ITerminal`, `Compositor`, `KeyReader`, target FPS (`int`). Method:
  - `RunAsync(Func<ReadOnlySpan<KeyEvent>, bool> processFrame, CancellationToken ct)` — the main loop:
    1. Compute frame interval from target FPS.
    2. Initialize previous buffer (screen-sized, empty).
    3. Loop until cancelled:
       a. Drain key events from `KeyReader` into a temp list.
       b. Call `processFrame(keys)`. If it returns `false`, break (exit signal).
       c. Call `Compositor.Compose()` → current buffer.
       d. Call `Screen.WriteDiff(current, previous, outputWriter)`.
       e. Flush `outputWriter` to terminal.
       f. Swap buffers.
       g. Compute time remaining until next frame, `Task.Delay` for the remainder.
  - Handle terminal resize: check `terminal.Width`/`Height` each frame. If changed, resize compositor and layers.
- [x] **Build demo app** — Rewrite `Ur.Tui/Program.cs` on top of the framework:
  - Create `AnsiTerminal`, `Compositor`, `KeyReader`
  - Base layer with a simple status display (frame count, terminal size, last key pressed)
  - Overlay layer with a centered box (modal-shaped) with shadow
  - Toggle the overlay on/off with a key (e.g., 'm')
  - `q` to quit
  - This proves: HAL, key input, compositing, shadows, diff rendering, frame loop — all working together.

### Tests

- [x] `RenderLoop_CallsProcessFrameEachTick` — using `TestTerminal`, verify processFrame callback is invoked
- [x] `RenderLoop_DrainsKeyEvents` — enqueue keys into KeyReader's queue, verify processFrame receives them
- [x] `RenderLoop_FlushesComposedOutput` — verify `TestTerminal.Write` is called with non-empty data after compositing
- [x] `RenderLoop_RespectsExitSignal` — processFrame returns false → loop exits
- [x] `RenderLoop_HandlesCancellation` — cancellation token fires → loop exits cleanly

### Validation

- `dotnet test Ur.Terminal.Tests` — all tests across all phases pass
- `dotnet build Ur.Terminal` — no warnings
- Manual: run the demo app. Verify:
  - Frame counter increments at roughly the target FPS
  - Key presses are displayed correctly
  - Modal overlay appears/disappears with shadow effect
  - Terminal is restored cleanly on exit (q and Ctrl+C)
  - Resizing the terminal reflows the layout

---

## Risks and follow-up

- **ANSI escape sequence edge cases.** Different terminals may send different byte sequences for the same key (especially function keys). The parser may need terminal-specific adjustments. Mitigation: start with the common sequences (xterm-compatible), expand based on testing.
- **Ctrl+C handling.** Raw mode means Ctrl+C doesn't generate SIGINT — it's just byte `0x03`. The app needs to handle this explicitly. The `AnsiTerminal.Dispose` safety handlers need to cover process kill signals (`SIGTERM`, `SIGINT` if re-registered).
- **Escape key ambiguity.** Bare `0x1B` could be Escape *or* the start of a CSI sequence. The parser needs a timeout or lookahead strategy to disambiguate. Common approach: if `0x1B` is followed by `[` within a short window (~50ms), treat as CSI; otherwise treat as Escape. This can be deferred to Phase 4 implementation and refined based on testing.
- **`/dev/tty` availability.** Works on macOS and Linux. May not work in some CI environments or containers. Test project should handle this gracefully (skip tests that require a real terminal).
