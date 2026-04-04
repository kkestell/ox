# Modal Dialog Support

## Goal

Add a `ModalDialog` widget that floats over existing content, traps keyboard focus, displays an arbitrary child widget, and presents OK/Cancel buttons. Wire up `Ur.Demo` to pop a demo modal (label + text input) on a keypress.

## Desired outcome

- `ModalDialog` can be shown over any application by setting `Application.Overlay`.
- While the modal is open, all keyboard input is routed exclusively to the modal (the background UI is frozen).
- The modal draws a dim scrim over the full screen, then a centered bordered box containing the title, child widget, and a button row.
- Pressing OK fires `Confirmed`; pressing Cancel or Escape fires `Cancelled`.
- `Ur.Demo` opens a demo modal with a label and `TextInput` when the user presses `F1` (or another agreed key).

## How we got here

The request was concrete and the codebase patterns were clear enough to go straight to architecture. The ScrollView precedent — a widget that manages its own private content layout and rendering outside the normal tree walk — became the foundation for ModalDialog's design.

## Approaches considered

### Option A: ModalDialog as a full-screen private-content widget (recommended)

Mirrors how `ScrollView` works. The modal is sized to the full screen by `Application`, draws the dim scrim in its own `Draw()`, then manually lays out and renders its private inner stack (title + content + buttons) at the center. `Application` gets an `Overlay: Widget?` property; when set, the overlay is laid out and rendered after `Root` and receives all keyboard input.

- **Pros:** Clean separation of concerns. Modal manages all of its own rendering. No changes to the layout engine. Follows the established ScrollView precedent. The dim scrim is free.
- **Cons:** The modal's internal widgets are invisible to `CollectFocusable`, so focus within the dialog (cycling between content and buttons) must be managed internally by `ModalDialog.HandleInput`.
- **Failure modes:** If ModalDialog forgets to call `LayoutWithConstraints` on its inner stack before rendering, widgets render at stale positions.

### Option B: ModalDialog as a last-child of Root with absolute positioning

The modal is appended to `Root.Children` as the final child. Because rendering is painter's-algorithm order, it appears on top. A "pinned" position is assigned by the application each frame.

- **Pros:** Requires no changes to `Application` or `Renderer`.
- **Cons:** The modal is in the normal layout flow — the layout engine would try to stack it below the other children in a vertical Stack, so its position would have to be manually overridden every frame after layout. No natural way to dim the background (the modal's canvas is only its own dialog box, not the full screen). Focus management (blocking Tab from reaching background widgets) requires application-level hacks. This is a structural bolt-on, not an integration.

## Recommended approach

**Option A.** It extends the architecture cleanly at the right layer, mirrors an existing pattern (ScrollView), and solves the dim scrim and focus trapping problems naturally.

Key tradeoffs:
- `Application.Run` gets slightly more complex: it must detect overlay changes, rebuild a second focus ring, and route input conditionally.
- `ModalDialog.Draw` calls `LayoutEngine.LayoutWithConstraints` and `Renderer.RenderTree` directly (same as ScrollView.Draw), making it a heavier widget. This is acceptable for a modal — it is laid out once per frame and there is at most one at a time.

## Related code

- `Ur.Widgets/Application.cs` — Owns the event loop, focus ring, and input dispatch. Needs `Overlay` property and overlay focus routing.
- `Ur.Widgets/Renderer.cs` — `Render()` needs to accept and composite an optional overlay after rendering root.
- `Ur.Widgets/ScrollView.cs` — The direct precedent: a widget that owns private content, calls `LayoutEngine.LayoutWithConstraints` in `Draw()`, and uses `Renderer.RenderTree` to render private children to the same canvas.
- `Ur.Widgets/Widget.cs` — Base class. No changes expected, but `Focusable`/`IsFocused`/`HandleInput` are the hooks used by the new `Button` widget.
- `Ur.Widgets/TextInput.cs` — Reference implementation for a focusable widget with focused/unfocused visual states.
- `Ur.Demo/Program.cs` — The demo application. Needs a keypress handler that opens the modal and an `Overlay` setter call.
- `Ur.Drawing/Canvas.cs` — `SubCanvas`, `DrawRect`, `DrawBorder`, `DrawText` are the drawing primitives used by `ModalDialog.Draw`.

## Current state

- No modal or overlay concept exists. The focus ring is built once at startup from `CollectFocusable(Root)` and is described in a comment as "stable because the tree is persistent — no widgets are added or removed during the loop." This assumption must be relaxed.
- `Application.Run` routes Tab to the app-level focus ring and all other input to the focused widget. This is the dispatch logic that needs conditional overlay routing.
- `Renderer.Render(root)` creates a `Screen` and renders the tree into it. It does not currently accept a second root.
- No `Button` widget exists. OK/Cancel will need one.

## Structural considerations

**Hierarchy:** The overlay sits above the main widget tree in render order but is a sibling of `Root` at the Application level, not a child. This respects the existing layer structure — Application owns the two roots (main + overlay), the layout engine and renderer know nothing about the distinction.

**Abstraction:** `ModalDialog` is at the right abstraction level — it encapsulates all dialog concerns (scrim, border, title, content layout, button row, focus cycling, event firing) behind a clean API. Callers just `new ModalDialog(content)`, wire events, set `Overlay`.

**Modularization:** `Button` is a general-purpose widget that belongs in `Ur.Widgets` alongside `TextInput` and `Label`. It will be useful beyond modals. `ModalDialog` is also a general widget in `Ur.Widgets`.

**Encapsulation:** `ModalDialog`'s inner stack (title label + content + button row) is stored as a private field, not in `Children`, so the normal renderer tree-walk skips it — exactly as ScrollView does with `_content`. This keeps the rendering contract intact.

## Implementation plan

### New: Button widget

- [ ] Create `Ur.Widgets/Button.cs`. Button is a focusable one-line widget displaying `[ label ]`. It exposes a `Clicked: event Action?` and fires it when Enter is pressed. Focused style: bright white on dark blue. Unfocused style: white on dark grey. `PreferredWidth = label.Length + 4` (for `[ ` and ` ]`). `PreferredHeight = 1`.

### New: ModalDialog widget

- [ ] Create `Ur.Widgets/ModalDialog.cs`. Constructor takes `Widget content`. Public properties: `Title: string` (default `""`), `DialogWidth: int` (default `60`). Events: `Confirmed: event Action?`, `Cancelled: event Action?`.
- [ ] In the constructor, build the private inner stack: a `Stack.Vertical` holding a title `Label`, the content widget, and a horizontal button row `Stack` containing `_okButton` and `_cancelButton`. Store these as private fields. Do NOT add them to `this.Children`.
- [ ] Set `Focusable = true` on the ModalDialog itself. It acts as a single focusable entry point for Application; focus cycling within the dialog is managed internally.
- [ ] Build an internal mini focus ring in the constructor: `[content (if focusable), _okButton, _cancelButton]`. Set `_innerFocusRing[0].IsFocused = true`.
- [ ] Implement `HandleInput`:
  - `Tab`: cycle through `_innerFocusRing` (update `IsFocused` on old and new).
  - `Enter`: if the focused widget is `_okButton`, fire `Confirmed`; if `_cancelButton`, fire `Cancelled`; otherwise forward to the focused widget.
  - `Escape`: fire `Cancelled`.
  - Everything else: forward to the currently focused inner widget via `HandleInput`.
- [ ] Implement `Draw(ICanvas canvas)`:
  1. Fill the entire canvas with a dim scrim style (`new Style(Color.BrightBlack, Color.Black, Modifier.Dim)` with space characters via `canvas.DrawRect`).
  2. Measure the inner stack: `LayoutEngine.LayoutWithConstraints(_inner, 0, 0, DialogWidth, 0)` (height unconstrained — 0 means fit).
  3. Compute the centered dialog position: `dialogX = (Width - DialogWidth) / 2`, `dialogY = (Height - _inner.Height - 2) / 2`. The `- 2` accounts for the border.
  4. Re-layout inner stack inside the border: `LayoutEngine.LayoutWithConstraints(_inner, dialogX + 1, dialogY + 1, DialogWidth - 2, _inner.Height)`.
  5. Fill dialog background: `canvas.DrawRect(new Rect(dialogX, dialogY, DialogWidth, _inner.Height + 2), ' ', dialogStyle)`.
  6. Draw dialog border: `canvas.DrawBorder(new Rect(dialogX, dialogY, DialogWidth, _inner.Height + 2), borderStyle, BorderSet.Single)`.
  7. If `Title` is non-empty, draw it centered in the top border: `canvas.DrawText(titleX, dialogY, $" {Title} ", titleStyle)`.
  8. Render inner stack: `Renderer.RenderTree(_inner, canvas)`.

### Modify: Application overlay support

- [ ] Add a private `_overlay: Widget?` backing field and a `bool _overlayDirty` flag to `Application`.
- [ ] Add a protected `Overlay: Widget?` property. Setter assigns `_overlay`, sets `_overlayDirty = true`.
- [ ] In `Application.Run`, after building the main `focusRing`, declare `overlayFocusRing: List<Widget> = []` and `overlayFocusIndex = 0`.
- [ ] At the top of the main loop (after draining actions, before layout), check `_overlayDirty`. If true: clear `IsFocused` on all widgets in the current overlay ring; rebuild `overlayFocusRing = _overlay != null ? CollectFocusable(_overlay) : []`; reset `overlayFocusIndex = 0`; set `IsFocused = true` on the first widget in the new ring (if any); clear `_overlayDirty`.
- [ ] Modify the input dispatch closure in the input thread: when `_overlay != null`, skip app-level Tab handling and route ALL non-Ctrl-C input to `overlayFocusRing[overlayFocusIndex].HandleInput(input)`. When `_overlay == null`, use the existing main `focusRing` logic unchanged.
- [ ] Modify the layout + render calls in the main loop:
  ```csharp
  LayoutEngine.LayoutWithConstraints(Root, 0, 0, driver.Width, driver.Height);
  if (_overlay != null)
      LayoutEngine.LayoutWithConstraints(_overlay, 0, 0, driver.Width, driver.Height);
  var screen = Renderer.Render(Root, _overlay);
  driver.Present(screen);
  ```
  Apply the same to the pre-loop initial render.

### Modify: Renderer overlay compositing

- [ ] Update `Renderer.Render` to accept an optional `Widget? overlay = null`. After rendering the root tree, if overlay is non-null, call `RenderWidget(overlay, canvas)`.

### Demo: Ur.Demo modal integration

- [ ] In `ChatDemoApp`, add a stored reference to a `ModalDialog` that contains a vertical Stack with a `Label("Enter a message:")` and a `TextInput`.
- [ ] Override `OnInput` — or, more precisely, add a key-dispatch check in the input thread's closure — to detect `F1` (or another key, see Open questions). On that key, set `Overlay = _demoModal` to open the modal.
- [ ] Wire `_demoModal.Confirmed` to: read the `TextInput.Value`, add it as a `UserMessage` to `_listView.Items`, clear the TextInput, set `Overlay = null`.
- [ ] Wire `_demoModal.Cancelled` to: clear the TextInput, set `Overlay = null`.

> **Note on `OnInput`**: Application currently has no `OnInput` virtual method. The cleanest way to give the demo access to raw input is to add a `protected virtual void OnInput(InputEvent input)` hook called from the input dispatch closure. This avoids the application subclass having to override `Run` and duplicates no logic.

- [ ] Add `protected virtual void OnInput(InputEvent input) {}` to `Application`. Call it from the input dispatch closure before routing Tab or dispatching to the focus ring.
- [ ] In `ChatDemoApp`, override `OnInput`: if `input is KeyEvent { Key: Key.F1 }` (or the chosen key), set `Overlay = _demoModal`.

> **Note**: `Key.F1` may not exist in the `Key` enum or be mapped by `ConsoleDriver`. Check and add it if needed (see Key enum and ConsoleDriver.ReadInput).

## Impact assessment

- **Code paths affected:** `Application.Run` (input dispatch and render loop), `Renderer.Render`, `LayoutEngine.LayoutWithConstraints` (called from new `ModalDialog.Draw`).
- **Dependency impact:** `Ur.Widgets` gains two new public types (`Button`, `ModalDialog`). `Ur.Demo` gains a dependency on these.
- **No schema or data impact.**

## Validation

- **Manual:** Run `Ur.Demo`. Press the trigger key. Confirm the modal appears centered over the dimmed chat UI. Tab cycles focus between the TextInput and OK/Cancel buttons (visible via focus highlight). Enter on OK adds a user message and dismisses. Escape or Enter on Cancel dismisses without adding. Resizing the terminal recenters the modal on the next frame.
- **Lint/build:** `dotnet build` in the repo root must succeed with no warnings.
- **Existing behavior:** The chat demo must function normally when no modal is open. Existing scroll, focus-Tab, and auto-scroll behaviors must be unaffected.

## Open questions

- **Trigger key for demo:** `F1` is the natural choice but may not be mapped in `ConsoleDriver`. What key should trigger the demo modal? Alternatives: `Ctrl-M`, `'m'` character key, or `F1` (with ConsoleDriver mapping added).
- **Modal width:** Default `DialogWidth = 60` is a guess. Should it be configurable per-instance, a fraction of screen width, or fixed?
- **Content scroll:** If the content widget is taller than the dialog box, it will be clipped. Should `ModalDialog` wrap its content in a `ScrollView` automatically, or leave that to the caller?
