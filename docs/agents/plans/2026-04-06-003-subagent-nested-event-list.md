# Subagent Nested Event List

## Goal

Render subagent output as a fixed-height, bordered event list nested inside the main event list — indented from the left margin, with a horizontal-rule header and footer, and inner content that uses the same bubble-chrome rendering as the outer conversation.

## Desired outcome

A running subagent is displayed as:

```
     ─── subagent 075a3168 ───────────────────────────────────────────────────────────
     ▎
     ▎   write_file(file_path: "foo.txt", content: "foo") → ok
     ▎
     
     ▎   - File written: foo.txt
     ▎   - Content: foo
     ▎   - Size: 3 bytes
     ▎   The file has been created/overwritten successfully.
     ─────────────────────────────────────────────────────────────────────────────────
```

As the inner event list grows beyond the fixed height, it tail-clips (shows only the most recent rows):

```
     ─── subagent 075a3168 ───────────────────────────────────────────────────────────
     ▎   - Size: 3 bytes
     ▎   The file has been created/overwritten successfully.

     ▎   Blah blah blah blah blah.
     ─────────────────────────────────────────────────────────────────────────────────
```

The inner `▎` bars are the standard EventList bubble chrome — the subagent box is literally a clipped, bordered EventList nested inside the outer EventList.

## How we got here

The request was to generalize as much as possible since this is "an event list within an event list." The existing code already uses `EventList` for the outer conversation and `Viewport` to tail-clip it to screen height. The cleanest generalization is to give `SubagentRenderable` its own inner `EventList`, let it clip that list's rows to a fixed height in `Render()`, and surround the clipped rows with bordered header/footer rows.

## Approaches considered

### Option A: Modify EventList with a `maxHeight` parameter

Add a `clipToHeight` parameter to `EventList.Render()`. `SubagentRenderable` passes this when rendering its inner list.

- Pros: No new classes; clipping lives close to the rows it trims.
- Cons: Mixes layout concerns (bubble chrome) with display concerns (clipping) inside `EventList`. Clipping is a one-liner (`rows.TakeLast(n)`); it doesn't need to live in `EventList`.
- Failure modes: Future callers passing `clipToHeight` to the outer EventList by accident.

### Option B: Standalone `TailClipRenderable` wrapper

A new `TailClipRenderable(IRenderable inner, int maxRows)` that wraps any renderable, calls its `Render()`, and returns only the last `maxRows` rows. `SubagentRenderable` wraps its inner `EventList` with this.

- Pros: Composable; EventList stays unchanged; clipping is reusable.
- Cons: A whole class for a one-liner. Adds `Changed` forwarding boilerplate. Overkill for the only current use case.

### Option C: Clip inside SubagentRenderable.Render() — recommended

`SubagentRenderable` contains an inner `EventList` (same type as the outer one). In `Render()`, it calls `_innerList.Render(innerWidth)`, takes the last `MaxInnerRows` rows, prefixes each with indent cells, and wraps the whole thing in header/footer border rows. No changes to `EventList`.

- Pros: Minimal changes; `EventList` stays unchanged; the architecture maps directly onto the user mental model (event list inside event list). The tail-clip is a trivial `Math.Max` + loop, same as `Viewport.Redraw()`.
- Cons: Clipping logic is duplicated between `Viewport` and `SubagentRenderable`, but it's two lines — not worth abstracting.

## Recommended approach

**Option C.** `SubagentRenderable` owns an inner `EventList` and handles its own clipping in `Render()`. `EventList` is untouched. The pattern mirrors how `Viewport` tail-clips the outer `EventList` — same idea, one level deeper.

## Related code

- `src/Ur.Tui/Rendering/SubagentRenderable.cs` — The class being refactored; currently holds a flat `List<IRenderable>` with simple indent.
- `src/Ur.Tui/Rendering/EventList.cs` — The bubble-chrome container reused as the inner list; `BubbleStyle.None` added here.
- `src/Ur.Tui/Rendering/Viewport.cs` — Reference implementation of tail-clip logic (`startIndex = Max(0, rows.Count - height)`); SubagentRenderable follows the same pattern.
- `src/Ur.Tui/Program.cs` — `EventRouter.RouteSubagentEvent()` (lines ~529–597) calls `subRenderable.AddChild()`; updated to pass `BubbleStyle`.
- `tests/Ur.Tests/TuiRenderingTests.cs` — Existing `SubagentRenderable` tests; update and extend.

## Current state

- `SubagentRenderable` holds `List<IRenderable> _children`, renders them with a 2-space indent prefix, emits `--- subagent {id} ---` header and `--- subagent complete ---` footer as plain text rows.
- `EventRouter.RouteSubagentEvent()` calls `subRenderable.AddChild(renderable)` with no BubbleStyle parameter.
- `SubagentRenderable` is added to the outer `EventList` with `BubbleStyle.System` (black background, invisible bar).
- `Viewport.Redraw()` already performs tail-clip: `startIndex = Max(0, allRows.Count - viewportHeight)`.

## Structural considerations

**Hierarchy**: `Viewport → EventList` is now mirrored by `SubagentRenderable → EventList`. Both are container/viewport relationships. Dependencies still flow in the same direction.

**Abstraction**: `SubagentRenderable` level-shifts: it accepts agent events (through `AddChild`) and produces bordered, indented, clipped terminal rows. The inner `EventList` handles bubble-chrome; `SubagentRenderable` handles the box frame. This is appropriate layering.

**Modularization**: `EventList` is not polluted with subagent-specific concerns. `SubagentRenderable` is not polluted with bubble-chrome concerns. Responsibilities remain separate.

**Encapsulation**: The inner `EventList` is private to `SubagentRenderable`. `EventRouter` interacts with `SubagentRenderable.AddChild(child, style)` — same call site as today, just with an added `style` parameter.

## Refactoring

**Add `BubbleStyle.None` to `EventList`** (small, before the feature work):

The outer `EventList` currently wraps every child in bubble chrome. With the new `SubagentRenderable` providing its own borders and indentation, we still need the outer EventList to treat it like any other child (for blank-row separation between bubbles), but we don't want the outer bubble's padding rows or bar glyph to stack with the inner box. Adding `BubbleStyle.None` renders the child rows directly (no bar, no background fill, no top/bottom padding rows) while preserving the blank-row separator between bubbles.

This removes the visual double-indentation that would occur if both the outer System bubble chrome (3 chars) and `SubagentRenderable`'s own 4-space indent were applied simultaneously.

## Implementation plan

- [ ] **Add `BubbleStyle.None` to `EventList`**
  - Add `None` to the `BubbleStyle` enum in `EventList.cs`.
  - In `EventList.Render()`, skip `MakePaddingRow()` calls and add child rows directly (no bar glyph, no background fill) when style is `None`. Blank-row separator between bubbles is still emitted.
  - Update `StyleColors()` or the per-bubble branch to handle `None`.

- [ ] **Refactor `SubagentRenderable` to use an inner `EventList`**
  - Replace `List<IRenderable> _children` with `EventList _innerList` (private, created in the constructor).
  - Change `AddChild(IRenderable child)` to `AddChild(IRenderable child, BubbleStyle style)`. Delegate to `_innerList.Add(child, style)`. Forward `Changed` from `_innerList` upward (subscribe in constructor).
  - Add `private const int IndentWidth = 4` and `private const int MaxInnerRows = 10`.
  - Rewrite `Render(int availableWidth)`:
    1. Compute `innerWidth = Math.Max(1, availableWidth - IndentWidth)`.
    2. Call `var innerRows = _innerList.Render(innerWidth)`.
    3. Clip: `var startIndex = Math.Max(0, innerRows.Count - MaxInnerRows)`.
    4. Build header row: `[IndentWidth spaces]─── subagent {SubagentId} [─ fill to innerWidth]`.
    5. For each clipped inner row, prepend `IndentWidth` space cells (inherit `Color.Default` for all indent cells).
    6. Build footer row (always visible, even before completion): `[IndentWidth spaces][─ fill to innerWidth]`.
    7. Return header + indented inner rows + footer.
  - Keep `SetCompleted()` for the defensive-finalization contract. It no longer needs to add a footer row (the footer is structural, always rendered). It may optionally change the header style (e.g., dim the subagent ID) — leave this as future polish; for now `SetCompleted()` just sets `_completed = true` and fires `Changed`.

- [ ] **Update `EventRouter.RouteSubagentEvent()` in `Program.cs`**
  - Change `subRenderable.AddChild(subText)` → `subRenderable.AddChild(subText, BubbleStyle.Assistant)`.
  - Change `subRenderable.AddChild(subTool)` → `subRenderable.AddChild(subTool, BubbleStyle.System)`.
  - Change `subRenderable.AddChild(subErrText)` → `subRenderable.AddChild(subErrText, BubbleStyle.System)`.
  - Change outer `eventList.Add(subRenderable, BubbleStyle.System)` → `eventList.Add(subRenderable, BubbleStyle.None)`.

- [ ] **Update and extend tests in `TuiRenderingTests.cs`**
  - Update existing `SubagentRenderable` tests to pass `BubbleStyle` to `AddChild`.
  - Add test: header row is always present; footer row is always present (even before `SetCompleted`).
  - Add test: when inner rows exceed `MaxInnerRows`, only the last `MaxInnerRows` rows appear between header and footer.
  - Add test: all inner rows are prefixed with `IndentWidth` space cells.
  - Add test: `EventList` with `BubbleStyle.None` renders child rows without bar glyph or background, but still emits blank-row separator between bubbles.

- [ ] **Run `make inspect`, read `inspection-results.txt`, fix any issues**

## Impact assessment

- **Code paths affected**: `SubagentRenderable.Render()`, `SubagentRenderable.AddChild()`, `EventList.Render()` (new branch for `BubbleStyle.None`), `EventRouter.RouteSubagentEvent()`.
- **No data or schema impact.**
- **API impact within the module**: `SubagentRenderable.AddChild` gains a required `BubbleStyle` parameter. The only caller is `EventRouter.RouteSubagentEvent()` (three call sites), updated in the same PR.

## Validation

- **Tests**: Run `dotnet test` — all existing tests should still pass; new tests (listed above) should pass.
- **Lint**: `make inspect` → read `inspection-results.txt` → fix findings.
- **Manual**: Run `ur` and trigger a subagent. Verify:
  - Subagent box shows horizontal-rule header and footer.
  - Inner content uses bubble chrome (`▎` bar).
  - When inner content exceeds `MaxInnerRows`, older rows scroll off above the header visually (the box stays fixed height).
  - Outer conversation content (assistant text, tools) is unaffected.
  - No visual regression with `BubbleStyle.None` on the outer bubble.
