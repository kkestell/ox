# Migrate Ox to Use Te

## Goal

Replace Ox's hand-rolled terminal rendering primitives and input polling with the
equivalent types from the Te library, which was extracted specifically to serve
this role. After the migration, Ox has no duplicated terminal I/O code — it
delegates all low-level concerns to Te.

## Desired outcome

- `Ox.Rendering.Color`, `Ox.Rendering.ColorKind`, `Ox.Rendering.Cell`,
  `Ox.Rendering.CellStyle`, and `Ox.Rendering.ScreenBuffer` are deleted.
- `Ox.Rendering.Terminal` is stripped to lifecycle/cursor helpers only
  (`EnterAlternateBuffer`, `ExitAlternateBuffer`, `HideCursor`, `ShowCursor`,
  `ClearScreen`, `GetSize`). The `Flush` / `BuildSgr` / `SgrForColor` methods
  are deleted.
- `Ox.Rendering.Viewport` owns a persistent `Te.Rendering.ConsoleBuffer` field
  and calls `ConsoleBuffer.Render(Console.Out)` instead of `Terminal.Flush(buffer)`.
- `Ox.InputReader` is replaced by Te's `TerminalInputSource` + `InputCoordinator`.
  All keyboard events — line reading and Escape monitoring — flow through one
  unified path.

## How we got here

User requested a full migration (rendering + input) in one plan. Te was recently
extracted from Ox-adjacent code with this exact integration in mind; the primitives
are nearly identical and the delta is well-understood.

## Related code

- `src/Ox/Ox.csproj` — project references; needs Te added
- `src/Ox/Rendering/Color.cs`, `ColorKind.cs` — deleted; Te's equivalents used directly
- `src/Ox/Rendering/Cell.cs` — deleted; Te's `Cell` used directly
- `src/Ox/Rendering/CellStyle.cs` — deleted; replaced by `Te.Rendering.TextDecoration`
- `src/Ox/Rendering/CellRow.cs` — updated to use Te's `Cell` and `TextDecoration`
- `src/Ox/Rendering/ScreenBuffer.cs` — deleted; replaced by `Te.Rendering.ConsoleBuffer`
- `src/Ox/Rendering/Terminal.cs` — stripped; `Flush`/SGR helpers removed
- `src/Ox/Rendering/Viewport.cs` — major update; owns persistent `ConsoleBuffer`
- `src/Ox/InputReader.cs` — rewritten to use Te's `InputCoordinator`
- `src/Ox/Program.cs` — wires up `TerminalInputSource` + `InputCoordinator` at startup
- `src/Te/Rendering/ConsoleBuffer.cs` — replacement for `ScreenBuffer`
- `src/Te/Rendering/Cell.cs`, `Color.cs`, `TextDecoration.cs` — replacement primitives
- `src/Te/Input/TerminalInputSource.cs` — replacement for polling Console.ReadKey
- `src/Te/Input/InputCoordinator.cs` — event queue; replacement for `_readingLine` flag
- `tests/Ur.Tests/TuiRenderingTests.cs` — updated for new buffer API

## Current state

- Ox's `Cell` uses `char Rune`, `Color Foreground`, `Color Background`, `CellStyle Style`
- Te's `Cell` uses `char Rune`, `Color Foreground`, `Color Background`, `TextDecoration Decorations`
  (`Decorations` is a superset: adds `Blink` and `Strikethrough`)
- Ox's `ScreenBuffer` is write-only, created fresh per frame; Te's `ConsoleBuffer` is
  persistent and dirty-tracks cells so `Render()` only emits changed cells
- `ScreenBuffer.WriteCell(row, col, cell)` uses row-major order; `ConsoleBuffer.SetCell(x, y, cell)`
  uses x=col, y=row — a coordinate transposition is required throughout Viewport
- `InputReader` polls `Console.KeyAvailable` in a tight loop with `Thread.Sleep(20)`;
  it uses a `_readingLine` volatile flag to prevent `MonitorEscapeKeyAsync` from racing
  over the same keystrokes. Te's `TerminalInputSource` runs a background reader thread
  and pushes events into `InputCoordinator`'s queue, eliminating the race entirely.

## Structural considerations

**Hierarchy:** Te sits below Ox in the dependency graph. Adding a project reference
from Ox → Te is correct — the dependency flows toward the lower-level library.

**Abstraction:** `CellRow` stays in Ox. It is the boundary type between renderables
and the Viewport, not a primitive. After the migration, `CellRow` uses Te's `Cell`
internally but remains an Ox concept.

**Modularization:** `Terminal.cs` survives in stripped form. The alternate-buffer
and cursor-visibility escape sequences are legitimate Ox responsibilities (they
control the TUI lifecycle, not individual cells). These do not belong in Te, which
is intentionally minimal.

**Encapsulation:** `ConsoleBuffer` must become a persistent `Viewport` field
(not created per-frame) so dirty-cell tracking works. `BuildFrame` changes from
returning a `ScreenBuffer` to writing into `_buffer` directly. Tests that currently
inspect the returned `ScreenBuffer` must switch to reading from `Viewport`'s buffer.

## Implementation plan

### Setup

- [x] Add a project reference from `src/Ox/Ox.csproj` to `src/Te/Te.csproj`

### Rendering — Phase 1: Replace primitive types

- [x] Delete `src/Ox/Rendering/Color.cs` and `src/Ox/Rendering/ColorKind.cs`.
      Add `using Te.Rendering;` wherever `Color` was used. Te's `Color` API is identical
      (`Color.Default`, `Color.BrightBlack`, `Color.FromIndex(n)`, etc.).

- [x] Delete `src/Ox/Rendering/Cell.cs`. Add `using Te.Rendering;` at usage sites.
      Note: Te's `Cell` property is `Decorations`, not `Style` — update any site that
      reads `cell.Style`.

- [x] Delete `src/Ox/Rendering/CellStyle.cs`. Replace every use of `CellStyle` with
      `TextDecoration` (from `Te.Rendering`). Rename flag references:
  - `CellStyle.None` → `TextDecoration.None`
  - `CellStyle.Bold` → `TextDecoration.Bold`
  - `CellStyle.Dim` → `TextDecoration.Dim`
  - `CellStyle.Italic` → `TextDecoration.Italic`
  - `CellStyle.Underline` → `TextDecoration.Underline`
  - `CellStyle.Reverse` → `TextDecoration.Reverse`

- [x] Update `src/Ox/Rendering/CellRow.cs`:
  - Remove `using Ox.Rendering;` for the deleted types, add `using Te.Rendering;`
  - Change `CellStyle` → `TextDecoration` in all method signatures
  - `Cell` constructor arg: `style:` → no change needed since `Cell` is now Te's struct
    (Te's Cell takes `TextDecoration decorations = TextDecoration.None` as 4th arg)

- [x] Update all renderables (`EventList.cs`, `TextRenderable.cs`, `ToolRenderable.cs`,
      `SubagentRenderable.cs`, `TodoSection.cs`, `ContextSection.cs`, `Sidebar.cs`,
      `TreeChrome.cs`) to use `TextDecoration` instead of `CellStyle`.

### Rendering — Phase 2: Replace ScreenBuffer with ConsoleBuffer

- [x] Add a `private ConsoleBuffer _buffer` field to `Viewport`. Initialize it in the
      constructor with `new ConsoleBuffer(Console.WindowWidth, Console.WindowHeight)`.
      This replaces the per-frame `new ScreenBuffer(width, height)` in `BuildFrame`.

- [x] Rewrite `Viewport.BuildFrame(int width, int height)`:
  - Change return type from `ScreenBuffer` to `void`
  - At the start of the method, call `_buffer.Resize(width, height)` if dimensions
    changed, then `_buffer.Clear()` to reset the back buffer for this frame
  - Replace every `buffer.WriteRow(row, cellRow)` call with a local helper that
    iterates `cellRow.Cells` and calls `_buffer.SetCell(col, row, cells[col])`
    (note: ConsoleBuffer is x=col, y=row)
  - Replace every `buffer.WriteCell(row, col, cell)` call with `_buffer.SetCell(col, row, cell)`

- [x] Rewrite `Viewport.Redraw()`:
  - Remove the `var buffer = BuildFrame(width, height);` line
  - Call `BuildFrame(width, height)` (void return)
  - Replace `Terminal.Flush(buffer)` with `_buffer.Render(Console.Out)`

- [x] Delete `src/Ox/Rendering/ScreenBuffer.cs`.

- [x] Strip `src/Ox/Rendering/Terminal.cs`:
  - Delete `Flush(ScreenBuffer buffer)`, `BuildSgr(...)`, and `SgrForColor(...)` — all
    replaced by `ConsoleBuffer.Render()`
  - Keep `EnterAlternateBuffer()`, `ExitAlternateBuffer()`, `HideCursor()`, `ShowCursor()`,
    `ClearScreen()`, and `GetSize()`
  - Update the class comment to reflect its narrower scope

- [x] Update `tests/Ur.Tests/TuiRenderingTests.cs`:
  - `BuildFrame` no longer returns a buffer; expose `Viewport._buffer` as `internal`
    (the test assembly already has `InternalsVisibleTo` access) and assert against
    `_buffer.GetCell(x, y)` instead of `buffer[row, col]`
  - Note the coordinate convention flip: `GetCell(x=col, y=row)`

### Input — Phase 3: Replace InputReader with Te input system

- [x] In `src/Ox/Program.cs`, create `TerminalInputSource` and `InputCoordinator` once
      at startup (before the viewport loop). Pass the coordinator into `InputReader`
      (or wherever the line-reading logic lives after this migration).

- [x] Rewrite `InputReader.ReadLineInViewport()`:
  - Remove the `Console.KeyAvailable` + `Console.ReadKey` polling loop
  - Subscribe to `InputCoordinator.KeyReceived` (or call `ProcessPendingInput` in a
    polling loop) and dispatch to the existing switch logic
  - Map Te `KeyEventArgs` fields to the current key-handling cases:
    - `e.Code == KeyCode.Enter` → Enter
    - `e.Code == KeyCode.Backspace` → Backspace
    - `e.Code == KeyCode.Tab` → Tab
    - `e.Code == KeyCode.Escape` → Escape (handled here, see below)
    - `e.Code == KeyCode.D && e.Ctrl` → Ctrl+D (EOF)
    - `!char.IsControl(e.Character)` → printable character → `buffer.Append(e.Character)`
  - Remove the `_readingLine` flag — it is no longer needed because all key events flow
    through one source

- [x] Fold `MonitorEscapeKeyAsync()` into `ReadLineInViewport()`:
  - When an Escape key event arrives during line reading, cancel `turnCts` and exit.
    (Escape during line reading means "cancel current turn", same as before.)
  - Delete `MonitorEscapeKeyAsync()` from `InputReader`.

- [x] Rewrite `InputReader.ReadLine()` (pre-viewport, direct echo):
  - `TerminalInputSource` puts the terminal in raw mode immediately on construction,
    which disables automatic echo. The pre-viewport `ReadLine()` must now echo
    characters manually (write each printable char to `Console.Out`, write "\b \b"
    for Backspace) — the same approach as the in-viewport reader but with explicit echo.
  - Alternatively, if the pre-viewport phase is short and low-stakes, the simplest fix
    is to disable raw mode for that phase and re-enable it when the viewport starts.
    Prefer the explicit-echo approach to avoid mode switching.

- [x] Dispose `TerminalInputSource` and `InputCoordinator` on exit in `Program.cs`.

### Validation

- [x] Run `dotnet build` — zero errors
- [x] Run `dotnet test` — all tests pass, including updated `TuiRenderingTests`
- [x] Run Ox interactively:
  - Verify the splash screen renders correctly
  - Type a message — confirm the input row echoes keystrokes and autocomplete works
  - Send a message — confirm the conversation area updates and the throbber animates
  - Press Escape mid-turn — confirm the turn is cancelled
  - Ctrl+D on an empty input — confirm clean exit
  - Resize the terminal window — confirm the layout reflows correctly
  - Toggle the sidebar (`/todo` or equivalent) — confirm sidebar appears and the
    separator column renders correctly
  - Verify on both macOS (Unix raw mode path) and, if available, Windows (managed fallback)

## Open questions

- Should `TerminalInputSource` enable mouse support (`TerminalInputSourceOptions.EnableMouse`)
  in Ox? Currently Ox has no mouse event consumers, but Te supports it. Leave disabled for
  now unless there is a concrete use case.

- The `ReadLine()` pre-viewport echo mode: verify whether explicit echo or mode-switching
  is the cleaner approach once `TerminalInputSource` construction is seen in context of
  `Program.cs`.
