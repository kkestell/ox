# Modal Dialog System

## Goal

Add a modal dialog abstraction to the Ur TUI framework: a `Dialog` widget that renders as a bordered, titled, centered overlay on top of the main application content, with OK/Cancel buttons and event-based result communication.

## Desired outcome

- `Dialog` base class that consumers subclass to build custom dialogs (form fields, confirmations, etc.)
- Dialogs render centered and floating over a dimmed background
- Input is scoped to the dialog while it's open (modal behavior)
- OK/Cancel buttons in a bottom row, Escape key closes with Cancel
- `Closed` event with `DialogResult` enum lets callers retrieve form data after dismissal
- Demo app updated: clicking [Send] opens an "OpenRouter API Key" dialog

## How we got here

Three interrelated design challenges were explored:

1. **Input routing (modality)** — Considered replacing Root, filtering focus ring, or adding a modal stack. Chose modal stack because it cleanly separates concerns, extends to nested modals, and follows the WinForms `ShowDialog` pattern.

2. **Overlay rendering** — Considered dialog-as-child, separate screen compositing, or two-pass rendering. Chose two-pass because the Renderer already knows how to draw a widget subtree into a canvas region — we just add a second pass after Root.

3. **Sizing** — Considered content-fitting (multi-pass measurement), fixed proportional, or explicit size. Chose fixed proportional (60% width, auto height capped at 80%) as a pragmatic first iteration. Content-fitting can be layered on later.

4. **Result communication** — Considered callbacks, async/await, or events. Chose events + `DialogResult` enum for WinForms familiarity and extensibility.

## Related code

- `Ur.Widgets/Application.cs` — Main loop, focus ring, input dispatch. Gets modal stack, ShowModal/CloseModal, Escape handling.
- `Ur.Widgets/Renderer.cs` — Single-pass tree walk. Gets two-pass rendering with dim + centered overlay.
- `Ur.Widgets/Widget.cs` — Base class. Dialog inherits from this.
- `Ur.Widgets/Button.cs` — OK/Cancel buttons reuse this. Pattern for Clicked event.
- `Ur.Widgets/Flex.cs` — Used internally by Dialog for content + button row layout.
- `Ur.Widgets/ScrollView.cs` — Pattern for single-child container with chrome (border, scrollbar).
- `Ur.Drawing/Canvas.cs` — SubCanvas, DrawBorder, DrawText used for dialog chrome rendering.
- `Ur.Demo/Program.cs` — Demo app, gets dialog trigger on [Send] click.

## Current state

- **Widget tree**: Retained-mode, mutable. Single `Root` widget, depth-first focus ring.
- **Rendering**: Single-pass depth-first tree walk. No overlay or z-order concept.
- **Input**: Tab cycles focus ring, Ctrl-C quits, all other input goes to focused widget.
- **Layout**: Flex-based 7-pass algorithm with Fit/Fixed/Grow sizing modes.
- **Canvas**: Hierarchical clipping via SubCanvas. `DrawBorder()` already supports Single/Double/Rounded border sets.

### Existing patterns to follow

- ScrollView pattern: single `_content` child, custom chrome in `Draw()`, delegates layout to content.
- Button pattern: `Clicked` event with `Action` delegate. Dialog uses same for `Closed` event.
- Application pattern: `Invoke(Action)` for thread-safe mutation. Modal operations use same queue.

## Structural considerations

**Hierarchy**: Dialog is a peer to other widgets in the type hierarchy (inherits Widget directly), but lives outside the Root tree — managed by Application's modal stack. This is a new concept: widgets that exist outside the main tree but participate in layout and rendering.

**Abstraction**: Dialog sits at the right level — it's a container widget with chrome (like ScrollView) plus Application-level orchestration (modal stack). The split is clean: Dialog handles its own layout/rendering, Application handles modality.

**Modularization**: New files (`Dialog.cs`, `DialogResult.cs`) keep the dialog code isolated. Changes to `Application.cs` and `Renderer.cs` are additive (new methods, new code paths) rather than restructuring existing logic.

**Encapsulation**: Dialog exposes `Content` (protected) for subclasses and `Closed` event for callers. Internal layout structure (button row, border math) is private. Application's modal stack is private; only `ShowModal`/`CloseModal` are public.

## Implementation plan

### Phase 1: Infrastructure

- [ ] **Create `Ur.Widgets/DialogResult.cs`** — `enum DialogResult { OK, Cancel }`. Simple enum, separate file for discoverability.

- [ ] **Create `Ur.Widgets/Dialog.cs`** — The dialog widget:
  - Constructor takes `string title`, optional `bool showCancelButton = true`
  - Internal structure: `Flex.Vertical` layout containing `_content` (Flex.Vertical, Grow) + `_buttonRow` (Flex.Horizontal)
  - Button row: a grow-mode spacer `Flex` to push buttons right, then OK and Cancel `Button`s
  - `protected Flex Content` property for subclasses to add widgets to
  - `public event Action<DialogResult>? Closed`
  - `protected void Close(DialogResult result)` invokes the event
  - `Layout(availableWidth, availableHeight)`:
    - `Width = Min(availableWidth * 0.6, availableWidth - 4)` (at least 2-cell margin each side)
    - Lay out internal flex at `(Width - 2, 0)` to get natural content height (unconstrained)
    - `Height = Min(contentHeight + borderTop + borderBottom + separatorRow, availableHeight * 0.8)`
    - If content exceeds available height, clamp (scrolling content is a follow-up)
    - Position children: content at (1, 1), button row at (1, Height - 2) inside border
  - `Draw(ICanvas canvas)`:
    - `DrawBorder(fullRect, style, BorderSet.Rounded)` (or Single — Rounded looks nice for dialogs)
    - Draw title centered in top border: overwrite border chars with ` Title ` text flanked by border horizontal chars
    - Draw horizontal separator above button row: `DrawHLine` with `├` and `┤` at the edges
    - Fill interior background

### Phase 2: Application integration

- [ ] **Modify `Ur.Widgets/Application.cs`** — Add modal dialog support:
  - Private field: `private readonly Stack<Widget> _modalStack = new()`
  - `public void ShowModal(Dialog dialog)` — pushes dialog onto stack, subscribes to `dialog.Closed` to auto-close, rebuilds focus ring from dialog
  - `public void CloseModal()` — pops topmost modal, rebuilds focus ring from next modal or Root
  - Modify focus ring logic: `var focusRoot = _modalStack.Count > 0 ? _modalStack.Peek() : Root`
  - Modify input handling: when modal is active and Escape is pressed, close topmost modal with `DialogResult.Cancel`
  - Modify main loop: after `Root.Layout()`, also call `modal.Layout(width, height)` for the topmost modal
  - Pass modal to Renderer: `Renderer.Render(Root, topModal)`

### Phase 3: Renderer changes

- [ ] **Modify `Ur.Widgets/Renderer.cs`** — Two-pass rendering:
  - Change `Render(Widget root)` signature to `Render(Widget root, Widget? modal = null)`
  - After rendering Root, if modal is non-null:
    - Dim the background: iterate all screen cells, apply `Modifier.Dim` or darken colors
    - Calculate centered position: `x = (root.Width - modal.Width) / 2`, `y = (root.Height - modal.Height) / 2`
    - Create SubCanvas at centered rect and call `RenderWidget(modal, subCanvas)`

### Phase 4: Demo

- [ ] **Create dialog subclass in `Ur.Demo/Program.cs`** — `ApiKeyDialog : Dialog`:
  - Contains a `Label("Enter your OpenRouter API Key:")` and a `TextInput`
  - Exposes `TextInput ApiKeyInput` property so caller can read the value after close

- [ ] **Wire up dialog in `ChatDemoApp`** — When [Send] button is clicked:
  - Create `ApiKeyDialog`, subscribe to `Closed` event
  - On `DialogResult.OK`, add a system message showing the key was set
  - Call `ShowModal(dialog)`

### Phase 5: Validation

- [ ] **Build and verify** — `dotnet build` succeeds with no warnings
- [ ] **Manual test** — Run `Ur.Demo`, click Send, verify dialog appears centered over dimmed background, Tab between fields, OK/Cancel/Escape all work correctly

## Impact assessment

- **Code paths affected**: Application main loop (layout + input dispatch), Renderer (new overlay pass), new widget files. No changes to existing widgets.
- **API surface**: New public types `Dialog`, `DialogResult`. New public methods `Application.ShowModal()`, `Application.CloseModal()`.
- **Backwards compatibility**: Fully additive. No existing behavior changes. Applications that don't use dialogs are unaffected.

## Validation

- **Build**: `dotnet build` from repo root — must compile clean
- **Manual verification**:
  - Dialog renders centered with border, title, separator, and buttons
  - Background is dimmed when dialog is open
  - Tab cycles only between dialog's focusable widgets (TextInput, OK, Cancel)
  - OK button and Enter close with DialogResult.OK
  - Cancel button closes with DialogResult.Cancel
  - Escape closes with DialogResult.Cancel
  - After close, focus returns to main application widgets
  - Background widgets are not interactable while dialog is open

## Open questions

- **Border style**: Should dialogs use `BorderSet.Rounded` (softer, modern) or `BorderSet.Single` (classic TUI)? Leaning Rounded but easy to change. Will use Rounded as default.
- **Background dim**: Should dimming darken colors or apply `Modifier.Dim`? The Dim modifier is simpler but may not look great on all terminals. Darkening colors (halving RGB values) is more reliable. Will try `Modifier.Dim` first and iterate.
- **Button right-alignment**: The mockup shows buttons right-aligned. Plan uses a Grow-mode spacer Flex in the button row to push buttons right. If this doesn't work cleanly with the current Flex algorithm, buttons can be left-aligned for v1.
