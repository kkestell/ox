# Widget Offset + Layout Refactor

Supersedes `2026-04-04-001-modal-dialogs.md` and `2026-04-04-002-widget-measure-method.md`.

## Goal

Replace the global layout engine + offscreen-buffer scroll pattern with a cleaner
architecture:

- Each widget owns its layout via a virtual `Layout(int w, int h)` method.
- Scrolling is handled by `OffsetX`/`OffsetY` properties on Widget; the Renderer
  applies them as a translate+clip when drawing children.
- `Flex` encapsulates the current 7-pass flex algorithm as a widget.
- `ScrollView` becomes trivial — just a scrollbar painter and an OffsetY manager.
- `ModalDialog` (from the superseded plan) can be implemented cleanly after this.

## Desired outcome

- No widget ever calls `LayoutEngine` or `Renderer` from inside `Draw()`.
- `ScrollView` has no offscreen buffer, no `_contentHeight` cache, no layout in `Draw`.
- A `ModalDialog` can be added afterward without inventing new patterns.
- All existing demo app behavior (chat scroll, text input, auto-scroll) is preserved.

## How we got here

`ScrollView.Draw()` calls `LayoutEngine.LayoutWithConstraints` to measure content
height and render into an offscreen buffer — a phase violation. The modal dialogs
plan was about to copy this pattern verbatim. The smell was spreading, not contained.

The fix: give the Renderer native clip+translate support (via `OffsetX`/`OffsetY` on
Widget), move layout responsibility into each widget, and confine the flex algorithm
to a `Flex` widget. This eliminates the motivation for the offscreen buffer entirely.

## Approaches considered

### Option A: OffsetX/OffsetY + virtual Layout (recommended)

- Widgets own their layout; the Renderer clips children to the parent's viewport using
  `OffsetX`/`OffsetY` as a translation.
- `Flex` widget runs the current 7-pass algorithm for its children.
- `ScrollView` sets `OffsetY`, draws a scrollbar, done.
- Pros: Clean separation. No layout in Draw. No offscreen buffer. ModalDialog becomes
  straightforward.
- Cons: Requires changes across Drawing, Renderer, all widgets, and Application. Bigger
  scope than a targeted fix.

### Option B: Add `Measure()` to Widget (the superseded plan)

- Pure measurement method eliminates the `_contentHeight` staleness bug.
- `LayoutWithConstraints` stays in `Draw()` because rendering still needs arranged layout.
- Pros: Minimal change.
- Cons: The phase violation and the offscreen buffer survive. ModalDialog repeats the
  pattern. This is a painkiller, not a cure.

## Related code

- `Ur.Drawing/Geometry.cs` — `Rect` uses `ushort`; must move to `int` to support
  negative positions (content scrolled above viewport)
- `Ur.Drawing/Screen.cs` — `ushort` coordinates; same migration
- `Ur.Drawing/Canvas.cs` — `SubCanvas` must handle negative rect origins after `ushort → int`
- `Ur.Drawing/ICanvas.cs` — interface signatures change with `ushort → int`
- `Ur.Widgets/Widget.cs` — receives `OffsetX`, `OffsetY`, and `virtual Layout()`
- `Ur.Widgets/Renderer.cs` — key change: switches from absolute root-canvas rendering
  to parent-canvas rendering with per-widget offset translation
- `Ur.Widgets/LayoutEngine.cs` — gutted; algorithm moves into `Flex`
- `Ur.Widgets/Stack.cs` — replaced by `Flex`; can be deleted or made a thin alias
- `Ur.Widgets/Flex.cs` (new) — contains the 7-pass flex layout algorithm
- `Ur.Widgets/ListView.cs` — implements `Layout()` as a vertical flex container
- `Ur.Widgets/Label.cs` — implements `Layout()` (set own size from preferred dims)
- `Ur.Widgets/TextInput.cs` — implements `Layout()`
- `Ur.Widgets/ScrollView.cs` — radically simplified
- `Ur.Widgets/Application.cs` — calls `Root.Layout(w, h)` instead of `LayoutEngine`
- `Ur.Widgets/Calculations.cs`, `GrowShrink.cs`, `Traversal.cs` — become Flex internals

## Structural considerations

**Coordinates become parent-relative.** Currently all `X`/`Y` values are absolute
screen positions. After this change, `X`/`Y` are relative to the parent widget's
content origin. The Renderer accumulates the transform as it descends the tree, creating
each widget's sub-canvas from its parent's canvas. This is the standard model used by
every mature retained-mode UI toolkit.

**OffsetX/OffsetY semantics.** When the Renderer draws widget W's children, each child
is positioned at `(child.X - W.OffsetX, child.Y - W.OffsetY)` within W's canvas.
Children that land above or left of the origin are clipped to zero size by SubCanvas.
`ScrollView` sets `OffsetY` to the scroll amount; the Renderer handles the rest.

**Layout propagation.** `Application` calls `Root.Layout(screenW, screenH)`. Each
widget's `Layout(w, h)` sets its own `Width`/`Height`, positions its children (setting
their `X`/`Y` in parent-relative coords), and calls each child's `Layout()` with the
assigned dimensions. `Flex` implements this with the existing 7-pass algorithm.
`ScrollView.Layout(w, h)` calls `_content.Layout(w - 1, 0)` — 0 means unconstrained
height, same convention as current `LayoutWithConstraints`. `_content.Height`
afterward gives the natural content height for scrollbar calculations.

**Flex replaces Stack.** `Stack` is currently an empty container — the layout engine
does all the work. After this refactor, `Flex` does the same job with the algorithm
baked in. `Stack` can be deleted (or kept as `class Stack : Flex` for one release).

## Implementation plan

### Phase 1 — Drawing layer: `ushort → int`

- [x] In `Ur.Drawing/Geometry.cs`: change `Rect` record parameters and computed
  properties (`Right`, `Bottom`, `Contains`, `Create`) from `ushort` to `int`.
- [x] In `Ur.Drawing/Screen.cs`: change `Width`, `Height`, constructor parameters,
  and `Set`/`Get` coordinates from `ushort` to `int`. Update the bounds check
  (`x >= Width || y >= Height`) to also guard `x < 0 || y < 0`.
- [x] In `Ur.Drawing/ICanvas.cs`: change all `ushort` parameters to `int`.
- [x] In `Ur.Drawing/Canvas.cs`:
  - Change all `ushort` parameters and locals to `int`.
  - Update `SubCanvas` to handle negative `rect.X`/`rect.Y`: if the rect origin is
    negative, shrink the width/height by the overlap and clamp the origin to 0, so
    partially-offscreen children are clipped rather than wrapping.
- [x] Fix any resulting compile errors in widgets that call canvas methods with
  `ushort` literals or casts — remove the casts.

### Phase 2 — Widget base: OffsetX/OffsetY + virtual Layout

- [x] In `Ur.Widgets/Widget.cs`:
  - Add `public int OffsetX { get; set; }` and `public int OffsetY { get; set; }`.
  - Add `public virtual void Layout(int availableWidth, int availableHeight) {}`.
    The default is a no-op — widgets that don't need layout (leaf widgets with
    `PreferredWidth`/`Height` already set) are fine with the default. Containers
    override this.

### Phase 3 — Renderer: parent-canvas + offset translation

- [x] Rewrite `Renderer.RenderWidget` to pass the **parent's sub-canvas** to children
  instead of the root canvas. When rendering widget W's children, create each child's
  sub-canvas as:
  ```
  sub = widgetCanvas.SubCanvas(new Rect(
      child.X - W.OffsetX,
      child.Y - W.OffsetY,
      child.Width, child.Height))
  ```
  SubCanvas clipping (from Phase 1) handles children outside the viewport automatically.
- [x] Update `Renderer.Render(Widget root)` to create the root canvas from the screen
  and call `RenderWidget(root, screenCanvas)` where root's sub-canvas is the full screen.
- [ ] Remove `Renderer.RenderTree` (kept as internal bridge for ScrollView until Phase 7).

### Phase 4 — Flex widget (replaces Stack + LayoutEngine)

- [x] Create `Ur.Widgets/Flex.cs`. `Flex` extends `Widget`. Constructor accepts
  `LayoutDirection direction = LayoutDirection.Vertical` (same as current `Stack`).
- [x] Move the 7-pass layout algorithm from `LayoutEngine` into `Flex.Layout(int w, int h)`:
  - Set `this.Width`/`Height` based on `HorizontalSizing`/`VerticalSizing` and `w`/`h`.
  - Run Passes 1–7 on `Children` using the existing helpers in `Calculations`,
    `GrowShrink`, and `Traversal` (these files stay as internal helpers, now only
    consumed by Flex).
  - Pass 7 (Position) sets children's `X`/`Y` in **parent-relative** coordinates
    (origin 0,0 = top-left of this widget's content area). This is the key change
    from the current absolute-coordinate pass.
  - After positioning, call `child.Layout(child.Width, child.Height)` for each child
    so containers recurse.
- [x] `Flex.Draw(ICanvas canvas)` remains empty (same as current `Stack.Draw`).
- [x] Add `static Flex Vertical()` and `static Flex Horizontal()` factory methods
  matching current `Stack.Vertical()` / `Stack.Horizontal()`.
- [x] Delete `Ur.Widgets/LayoutEngine.cs` once Flex uses the algorithm.
- [x] Delete `Ur.Widgets/Stack.cs` and update all references to `Stack` → `Flex`.

### Phase 5 — Leaf widgets: implement Layout

- [x] `Label.Layout(int w, int h)`: set `Width = w > 0 ? w : PreferredWidth`,
  `Height = PreferredHeight`. (Width may be constrained by Flex; height is always
  natural line count.)
- [x] `TextInput.Layout(int w, int h)`: set `Width = w > 0 ? w : PreferredWidth`,
  `Height = 1`.

### Phase 6 — ListView: implement Layout

- [x] `ListView<T>.Layout(int w, int h)`: same logic as `Flex.Layout` for a vertical
  container. Position children at parent-relative coords, call each child's `Layout`.
  This is identical to `Flex.Layout(w, h)` with `Direction = Vertical` — consider
  making `ListView` extend `Flex` or delegating to shared vertical-layout logic.

### Phase 7 — ScrollView: simplify

- [x] Rewrite `ScrollView`:
  - Keep `_content` in `Children` (the Renderer now handles offset+clip natively).
  - `Layout(int w, int h)`: set `Width = w`, `Height = h`, then call
    `_content.Layout(w - 1, 0)` (0 = unconstrained height). Set `_content.X = 0`,
    `_content.Y = 0`. After this call `_content.Height` is the natural content height.
  - `Draw(ICanvas canvas)`: call `DrawScrollbar(canvas, _content.Height)`. That's it —
    no offscreen buffer, no `LayoutWithConstraints`, no blit loop.
  - `HandleInput`: `OffsetY` replaces `_scrollOffset`. Auto-scroll logic stays the same
    but reads `_content.Height` directly (available after `Layout` runs).
  - Delete `_contentHeight`, `_scrollOffset` (replaced by base `OffsetY`), and all
    offscreen-buffer code.

### Phase 8 — Application: call Layout instead of LayoutEngine

- [x] In `Application.cs`, replace:
  ```csharp
  LayoutEngine.LayoutWithConstraints(Root, 0, 0, driver.Width, driver.Height);
  ```
  with:
  ```csharp
  Root.X = 0; Root.Y = 0;
  Root.Layout(driver.Width, driver.Height);
  ```
  Apply this change in both the pre-loop initial render and the main loop.

## Validation

- `dotnet build` must succeed with no warnings.
- Run `Ur.Demo`: chat messages render correctly, scroll works, auto-scroll pins to
  bottom, user scrolling up pauses auto-scroll, scrolling back to bottom re-enables it.
- Resize the terminal while the demo is running — layout should reflow on the next frame.
- Delete `docs/agents/plans/2026-04-04-001-modal-dialogs.md` and
  `docs/agents/plans/2026-04-04-002-widget-measure-method.md` once this plan is
  complete (they are superseded). The modal dialog feature can be re-planned against
  the new architecture.
