# Buffer-Based TUI Rendering

## Goal

Replace the string-with-embedded-ANSI-codes rendering model with a cell-based buffer. Every visual element produces rows of `Cell` values (rune + foreground + background + style flags). The viewport assembles these into a `ScreenBuffer` and flushes it to the terminal by positioning the cursor at each cell — intentionally simple, intentionally slow, easy to get right. Optimization comes later.

## Desired outcome

- A `Cell` struct that carries a character, foreground color, background color, and style flags.
- A `Color` value type supporting default, basic 16, and 256-color palettes.
- A `CellStyle` flags enum (None, Bold, Dim, Italic, Underline, Reverse).
- A `ScreenBuffer` (width × height grid of cells) with convenience methods for writing text and filling regions.
- `IRenderable.Render()` returns `IReadOnlyList<CellRow>` instead of `IReadOnlyList<string>`.
- All renderables (TextRenderable, ToolRenderable, SubagentRenderable, EventList) produce cell rows — no ANSI escape codes anywhere in renderable code.
- `Viewport` assembles cell rows into a `ScreenBuffer` and calls `Terminal.Flush(buffer)`.
- `Terminal.Flush()` iterates every cell, positions the cursor, emits the appropriate ANSI color/style codes, and writes the character. This is the **only** place ANSI codes are generated.
- The `VisibleLength()` ANSI-stripping helper in EventList is eliminated — cell-based rendering makes it unnecessary.

## Approaches considered

### Option 1 — Cell rows returned by renderables (recommended)

- Summary: Change `IRenderable.Render()` to return `IReadOnlyList<CellRow>` instead of `IReadOnlyList<string>`. Each renderable produces typed cell data. Viewport collects cell rows, writes them into a `ScreenBuffer`, and flushes.
- Pros: Direct evolution of the current architecture. Renderables remain declarative ("here is my content"). Composability preserved — `EventList` wraps child cell rows with bubble-styled cells. Trivially testable: assert on cell colors/content without parsing ANSI codes.
- Cons: Every renderable must be updated. Larger diff.
- Failure modes: Missed a renderable during migration → compile error (good — the interface change forces updates).

### Option 2 — Renderables draw to buffer slices

- Summary: Change `IRenderable.Render()` to accept a rectangular buffer region and write cells into it imperatively.
- Pros: More natural for 2D drawing. Renderables can do arbitrary positioning within their region.
- Cons: Renderables need to know their dimensions upfront, complicating the current "render then measure" flow. Harder to compose — parent must allocate child regions before children render, but child height depends on content. Breaks the current model where renderables produce variable-height output.
- Failure modes: Layout calculation becomes a two-pass problem (measure then render), adding complexity for no benefit at this stage.

### Option 3 — Keep string renderables, parse ANSI at viewport level

- Summary: Renderables continue returning strings with embedded ANSI codes. Viewport parses ANSI sequences and converts to cells when building the buffer.
- Pros: Minimal changes to renderables.
- Cons: Violates the user's requirement that "event list and chat input drawing code should only know about cells." ANSI parsing is fragile. Doesn't actually simplify the rendering pipeline — it adds a parsing step.
- Failure modes: ANSI parser mishandles edge cases (partial sequences, nested resets, 256-color codes).

## Recommended approach

Option 1 — cell rows returned by renderables.

- Why: It's a direct evolution of the current architecture. The change is mechanical (swap string for CellRow everywhere), the compiler catches missed updates, and the result is a clean pipeline where ANSI codes exist in exactly one place.
- Key tradeoffs: Large diff touching every renderable, but each change is straightforward. The cell-by-cell flush is deliberately inefficient — correctness first, optimization later.

## Related code

- `src/Ur.Tui/Rendering/IRenderable.cs` — The interface that changes from `IReadOnlyList<string>` to `IReadOnlyList<CellRow>`.
- `src/Ur.Tui/Rendering/Terminal.cs` — Gains a `Flush(ScreenBuffer)` method; existing `Write(row, col, text)` may be kept for the input area or removed.
- `src/Ur.Tui/Rendering/TextRenderable.cs` — Word-wrapping logic stays, but output changes from styled strings to cell rows. Prefix/suffix ANSI codes become cell colors.
- `src/Ur.Tui/Rendering/ToolRenderable.cs` — State-dependent ANSI styling becomes cell colors.
- `src/Ur.Tui/Rendering/SubagentRenderable.cs` — Indent and header/footer become cell operations.
- `src/Ur.Tui/Rendering/EventList.cs` — Bubble styling (background, bar, padding) applied as cell colors instead of ANSI wrapping. `VisibleLength()` is eliminated.
- `src/Ur.Tui/Rendering/Viewport.cs` — Builds a `ScreenBuffer` from cell rows, calls `Terminal.Flush()`. Input area rendering also moves to cells.
- `src/Ur.Tui/Program.cs` — Minimal changes — event routing is unaffected since it manipulates renderables, not strings.

## Current state

- Renderables return `IReadOnlyList<string>` with embedded ANSI escape codes (e.g., `\e[90m`, `\e[48;5;236m`).
- `EventList.Render()` builds bubble-styled strings by prepending/appending ANSI color codes and using `VisibleLength()` to calculate padding while skipping CSI sequences.
- `Viewport.Redraw()` calls `Terminal.Write(row, col, line)` for each visible line — Terminal positions the cursor, writes the pre-formatted string, and clears to end of line.
- Colors used today: default, black (30), red (31), yellow (33), blue (34), cyan (46 bg), dark gray (90), 256-color dark gray (48;5;236 bg). Styles: dim (2), reverse (7).
- The input area (decoration row, text row, bottom padding) is rendered separately in Viewport using direct `Terminal.Write()` calls with ANSI codes.

## Structural considerations

**Hierarchy**: The cell types (`Color`, `CellStyle`, `Cell`, `CellRow`) sit at the bottom — they are value types with no dependencies. `ScreenBuffer` depends only on cell types. Renderables depend on cell types (to produce `CellRow`). `Viewport` depends on `ScreenBuffer` and renderables. `Terminal` depends on `ScreenBuffer` and cell types (to flush). Clean bottom-up dependency flow.

**Abstraction**: The key improvement is that renderables move from a mixed abstraction (text + ANSI control codes interleaved) to a uniform one (cells). This eliminates the `VisibleLength()` workaround and makes the rendering pipeline honest — what you see in a `CellRow` is exactly what appears on screen.

**Modularization**: New cell types go in their own files under `src/Ur.Tui/Rendering/`. `ScreenBuffer` is a self-contained class. No existing module boundaries change.

**Encapsulation**: ANSI escape code generation is pushed entirely into `Terminal.Flush()`. No other code needs to know about escape sequences. This is a strict improvement over the current state where every renderable embeds ANSI codes.

## Implementation plan

### Phase 1: Cell types

- [x] Create `src/Ur.Tui/Rendering/Color.cs`:
  - `ColorKind` enum: `Default`, `Basic`, `Bright`, `Color256`
  - `Color` readonly struct with `Kind` and `Value` (byte) fields
  - Static factory properties: `Default`, `Black`, `Red`, `Green`, `Yellow`, `Blue`, `Magenta`, `Cyan`, `White`
  - Static factory properties for bright variants: `BrightBlack` (dark gray), etc.
  - Static factory method: `FromIndex(byte index)` for 256-color
  - Comments documenting which SGR parameter each color maps to

- [x] Create `src/Ur.Tui/Rendering/CellStyle.cs`:
  - `[Flags] enum CellStyle`: `None = 0`, `Bold = 1`, `Dim = 2`, `Italic = 4`, `Underline = 8`, `Reverse = 16`

- [x] Create `src/Ur.Tui/Rendering/Cell.cs`:
  - Readonly struct: `Rune` (char), `Foreground` (Color), `Background` (Color), `Style` (CellStyle)
  - Static property `Empty` → space character, default colors, no style
  - Constructor or init-property pattern for easy creation

- [x] Create `src/Ur.Tui/Rendering/CellRow.cs`:
  - Wraps `List<Cell>` with `IReadOnlyList<Cell> Cells` property
  - `static CellRow FromText(string text, Color fg, Color bg, CellStyle style = CellStyle.None)` — converts a plain string into a row of uniformly-styled cells
  - `static CellRow Empty` — a row with no cells (for blank separator lines)
  - `void Append(char rune, Color fg, Color bg, CellStyle style)` — for building rows incrementally
  - `void Append(string text, Color fg, Color bg, CellStyle style)` — append multiple cells
  - `void PadRight(int totalWidth, Color bg)` — extend with space cells to fill available width

### Phase 2: ScreenBuffer

- [x] Create `src/Ur.Tui/Rendering/ScreenBuffer.cs`:
  - Constructor: `ScreenBuffer(int width, int height)` — allocates `Cell[height, width]` initialized to `Cell.Empty`
  - `int Width`, `int Height` properties
  - `Cell this[int row, int col]` indexer (get/set)
  - `void WriteRow(int row, CellRow cellRow)` — writes a cell row into the buffer at the given row, truncating if wider than buffer, padding with empty cells if narrower
  - `void Clear()` — reset all cells to `Cell.Empty`
  - This is the object that `Terminal.Flush()` consumes

### Phase 3: Terminal.Flush()

- [x] Add `Terminal.Flush(ScreenBuffer buffer)` method:
  - Iterate row 0..height-1, col 0..width-1
  - For each cell: position cursor at `(row+1, col+1)` (Terminal uses 1-based), emit SGR codes for foreground/background/style, write the character
  - Optimization: track "current" fg/bg/style state; only emit SGR codes when they change from the previous cell. This is a trivial optimization that dramatically reduces output volume without adding complexity.
  - At the end, emit a final SGR reset (`\e[0m`)
  - Use `Console.Out.Write()` for all output, then `Console.Out.Flush()` once at the end
  - Add helper: `EmitSgr(Color fg, Color bg, CellStyle style)` that builds the appropriate escape sequence

- [x] Add `Terminal.SgrForColor(Color color, bool background)` private helper:
  - `Default` → `39` (fg) or `49` (bg)
  - `Basic` → `30+value` (fg) or `40+value` (bg)
  - `Bright` → `90+value` (fg) or `100+value` (bg)
  - `Color256` → `38;5;{value}` (fg) or `48;5;{value}` (bg)

### Phase 4: Update IRenderable

- [x] Change `IRenderable.Render(int availableWidth)` return type from `IReadOnlyList<string>` to `IReadOnlyList<CellRow>`

### Phase 5: Update TextRenderable

- [x] Replace `_linePrefix` / `_lineSuffix` string fields with `Color Foreground`, `Color Background`, `CellStyle Style` fields
- [x] Update `Render()`: word-wrapping logic stays the same, but instead of building strings with ANSI prefixes, build `CellRow` objects using `CellRow.FromText(line, fg, bg, style)`
- [x] Update constructor calls in Program.cs to pass colors instead of ANSI prefix/suffix strings:
  - User messages: `Color.Default` fg, `Color.Default` bg, `CellStyle.Dim` (was `\e[2m` prefix)
  - Assistant messages: default colors, no style
  - Error messages: `Color.Red` fg (was `\e[31m` prefix)

### Phase 6: Update ToolRenderable

- [x] Replace ANSI string building with `CellRow` construction:
  - Base text in `Color.BrightBlack` (dark gray)
  - Awaiting approval suffix in `Color.Yellow`
  - Completed suffix in `Color.BrightBlack`
- [x] Build cell rows by appending segments with different colors (e.g., tool signature in gray, then status suffix in yellow)

### Phase 7: Update SubagentRenderable

- [x] Header/footer lines: `CellRow.FromText("--- subagent {id} ---", Color.BrightBlack, Color.Default)`
- [x] Indentation: prepend 2 space cells to each child cell row (create new `CellRow` with spaces + child cells)
- [x] Reduce `availableWidth` by indent size when rendering children (already done today)

### Phase 8: Update EventList

- [x] Replace string-based bubble styling with cell-based bubble styling:
  - Define bubble colors as `Color` values:
    - User: `Color.FromIndex(236)` background, `Color.Blue` bar
    - Assistant: `Color.Black` background, `Color.Yellow` bar
    - System: `Color.Black` background, `Color.Black` bar
  - For each child row, build a new `CellRow`:
    - 1 space cell (bubble bg)
    - 1 bar cell ('▎', bar color fg, bubble bg)
    - 1 space cell (bubble bg)
    - child cells (with background overridden to bubble bg if cell bg is default)
    - 1 space cell (bubble bg)
    - Pad remaining width with bubble bg
  - Top/bottom padding rows: full-width cells with bubble bg, space character
  - Blank separator rows between bubbles: `CellRow` of empty cells
- [x] Remove `VisibleLength()` method — no longer needed
- [x] Remove all ANSI escape code string constants

### Phase 9: Update Viewport

- [x] Create a `ScreenBuffer` in `Redraw()` sized to terminal dimensions
- [x] Write visible cell rows (tail of `_root.Render()`) into the buffer using `buffer.WriteRow()`
- [x] Write input area (decoration row, text row, bottom padding) into the buffer as cell rows:
  - Decoration row: `▆` character in blue-on-cyan, then spaces in blue bg
  - Text row: prompt + typed text in blue bg
  - Bottom padding: spaces in blue bg
- [x] Call `Terminal.Flush(buffer)` instead of individual `Terminal.Write()` calls
- [x] Remove direct `Terminal.Write()` calls from `Redraw()` and `DrawInputArea()`

### Phase 10: Cleanup

- [x] Remove `Terminal.Write(row, col, text)` if no longer used (or keep as a convenience if the input area still uses it during intermediate steps)
- [x] Remove `Terminal.ClearToEndOfLine()` if no longer needed (buffer flush writes every cell, so no clearing necessary)
- [x] Remove any remaining ANSI string constants from renderable files
- [x] Run `make inspect`, read `inspection-results.txt`, fix any issues

## Validation

- **`make inspect`**: Run and fix all issues before committing.
- **Manual verification**:
  - Start a conversation — verify the full-screen viewport renders correctly with bubble styling.
  - Send a message that triggers tool calls — verify colors and state transitions look correct.
  - Trigger a permission prompt — verify it renders in the input area.
  - Run a subagent — verify indented grouping with header/footer.
  - Verify user message dim styling, error red styling, tool call dark gray styling all appear correctly.
  - Resize the terminal during streaming — verify the display adapts.
  - Press Ctrl+C / close terminal — verify cleanup restores terminal state.

## Open questions

- Should `CellRow` carry its own background color for "fill the rest of the row" semantics, or should the viewport always pad explicitly? The plan assumes explicit padding via `PadRight()`, but a row-level default background could simplify EventList bubble styling.
- Should `Cell.Rune` be `char` or `System.Text.Rune`? Using `char` is simpler and sufficient for the current character set (ASCII + a few Unicode box-drawing characters). `System.Text.Rune` handles surrogate pairs correctly but adds complexity. The plan assumes `char` for now.
