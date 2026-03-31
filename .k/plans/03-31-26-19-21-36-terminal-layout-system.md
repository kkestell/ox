# Terminal Layout System

## Goal

Implement layout primitives and a unified `Widget` base class for Ur.Terminal, then migrate all existing components from `IComponent` to `Widget`. Eliminate manual rect arithmetic, duplicated chrome rendering, and duplicated scroll/selection logic from the TUI layer.

## Desired outcome

- `Widget` abstract base class with automatic chrome (border, background, padding) and a Render/RenderContent split.
- `VerticalStack` and `Center` layout containers as Widgets.
- `ScrollableList<T>` reusable widget extracting the duplicated list logic from modals.
- All five existing components (`ChatInput`, `MessageList`, `ApiKeyModal`, `ModelPickerModal`, `ExtensionManagerModal`) migrated to Widget subclasses.
- `ChatApp.RenderFrame()` uses `VerticalStack` for the base layer and `Center` for modals on the overlay layer, with no manual layout math.
- `IComponent` removed.
- All existing tests pass (updated as needed); new tests cover framework types.

## Related code

- `Ur.Terminal/Components/IComponent.cs` — the interface being replaced by Widget
- `Ur.Terminal/Core/Rect.cs` — value type used for layout rects
- `Ur.Terminal/Core/Buffer.cs` — rendering target; `Fill`, `DrawBox`, `WriteString` used by chrome
- `Ur.Terminal/Core/Cell.cs` — cell value type
- `Ur.Terminal/Core/Color.cs` — color value type
- `Ur.Terminal/Rendering/Layer.cs` — layers hold Buffers; ChatApp renders to layers
- `Ur.Terminal/Rendering/Compositor.cs` — composites layers; provides Width/Height
- `Ur.Tui/ChatApp.cs` — owns layout math, key routing, modal rendering, shadow logic
- `Ur.Tui/Components/ChatInput.cs` — has its own border rendering and `GetInputHeight` measurement
- `Ur.Tui/Components/MessageList.cs` — leaf component, fill-sized
- `Ur.Tui/Components/ApiKeyModal.cs` — self-centering modal with manual chrome
- `Ur.Tui/Components/ModelPickerModal.cs` — self-centering modal with duplicated scroll/selection logic
- `Ur.Tui/Components/ExtensionManagerModal.cs` — self-centering modal with duplicated scroll/selection logic
- `Ur.Tui.Tests/` — all component tests need updating for Widget base class

## Current state

- `IComponent` is a two-method interface: `Render(Buffer, Rect)` and `HandleKey(KeyEvent) → bool`.
- Chrome is scattered: `ChatInput` draws its own border via a private `RenderBorder()`. All three modals manually center themselves, fill background, and draw a box border — identical boilerplate in each.
- Layout is manual: `ChatApp.RenderFrame()` computes `inputHeight = _chatInput.GetInputHeight(w)`, then `messageHeight = h - inputHeight`, and constructs rects by hand.
- Modal dimensions are hardcoded as `const` fields on each modal class (`ModalWidth`, `ModalHeight`). `ChatApp` pattern-matches on modal type to extract these for shadow positioning — fragile and ugly.
- `ModelPickerModal` and `ExtensionManagerModal` duplicate ~80 lines of scroll/selection logic: `_selectedIndex`, `_scrollOffset`, `EnsureVisible()`, scroll indicators (▲/▼), up/down/home/end key handling, and the visible-window render loop. The code is nearly identical.
- Modals combine filter input + scrollable list + detail area in a single flat `Render` method. After refactoring, the scrollable list portion becomes a `ScrollableList<T>` child; filter and detail remain in the modal's own `RenderContent`.

## Structural considerations

**Hierarchy:** Widget lives in `Ur.Terminal/Components/`, same level as the current `IComponent`. Layout containers (`VerticalStack`, `Center`) go in `Ur.Terminal/Layout/`. `ScrollableList<T>` is a framework widget in `Ur.Terminal/Components/`. All app-level components in `Ur.Tui/Components/` become Widget subclasses. Dependencies flow one way: Ur.Tui → Ur.Terminal.

**Abstraction:** The Render/RenderContent split puts chrome at the right level — Widget handles mechanical chrome drawing, subclasses handle content. `MeasureHeight`/`MeasureContentHeight` follows the same split. Subclasses never think about border math.

**Modularization:** `Thickness` and `SizeConstraint` are small value types — they belong alongside the other value types in Core and Layout respectively. `ScrollableList<T>` consolidates exact duplication into a single reusable piece. The new Layout/ directory has a clear single purpose: spatial arrangement of Widgets.

**Encapsulation:** Chrome properties on Widget are public (callers configure them). `RenderContent` and `MeasureContentHeight` are protected — only subclasses implement them. `Render` and `MeasureHeight` are public and non-virtual — the pipeline is sealed.

## Implementation plan

### Phase 1: Framework types in Ur.Terminal

- [ ] **1.1 Add `Thickness`** — `Ur.Terminal/Core/Thickness.cs`. Readonly record struct with `Top`, `Right`, `Bottom`, `Left`. Static helpers: `Uniform(int)`, `Zero`.

- [ ] **1.2 Add `Widget` abstract base class** — `Ur.Terminal/Components/Widget.cs`. Chrome properties (`Border`, `BorderForeground`, `BorderBackground`, `Background`, `Padding`). Public non-virtual `Render(Buffer, Rect)` that fills background → draws border → computes inner rect → calls abstract `RenderContent(Buffer, Rect)`. Abstract `HandleKey(KeyEvent) → bool`. Virtual `MeasureContentHeight(int availableWidth) → int?` returning null. Public non-virtual `MeasureHeight(int availableWidth) → int?` that subtracts horizontal chrome, calls `MeasureContentHeight`, adds vertical chrome. Public `ContentRect(Rect outerRect) → Rect`.

- [ ] **1.3 Add `SizeConstraint`** — `Ur.Terminal/Layout/SizeConstraint.cs`. Abstract record with three subtypes: `Fixed(int Size)`, `Content`, `Fill(int Weight = 1)`.

- [ ] **1.4 Add `VerticalStack`** — `Ur.Terminal/Layout/VerticalStack.cs`. Extends Widget. Holds `IReadOnlyList<Entry>` where `Entry` is `readonly record struct Entry(Widget Child, SizeConstraint Height)`. `RenderContent` implements the layout algorithm from the spec. `HandleKey` returns false. Constructor takes params or list of entries. Immutable after construction.

- [ ] **1.5 Add `Center`** — `Ur.Terminal/Layout/Center.cs`. Extends Widget. Constructed with child Widget, width, height. `RenderContent` computes centered position, calls `child.Render`. `HandleKey` delegates to child.

- [ ] **1.6 Add `ScrollableList<T>`** — `Ur.Terminal/Components/ScrollableList.cs`. Extends Widget. Generic over T. `Items` settable property (clamps selection/scroll on change). `SelectedIndex`, `SelectedItem`. `ItemRenderer: Action<Buffer, Rect, T, bool>`. `RenderContent` computes visible window, calls renderer per item, draws scroll indicators. `HandleKey` handles Up/Down/Home/End/PageUp/PageDown (returns true), Enter returns false, others return false.

### Phase 2: Migrate leaf components

- [ ] **2.1 Migrate `MessageList`** — Change from `IComponent` to `Widget`. Move current `Render` body into `RenderContent`. No chrome needed (no border, no background override beyond the fill it already does). `HandleKey` unchanged.

- [ ] **2.2 Migrate `ChatInput`** — Change to `Widget`. Set `Border = true`, `BorderForeground = new Color(80, 80, 80)`, `Background = Color.Black`. Remove private `RenderBorder()` and the manual border drawing from `Render`. Move content rendering into `RenderContent` — it now receives the inner rect (border already drawn). Override `MeasureContentHeight(int availableWidth)` to return `Math.Min(CountVisualLines(availableWidth), MaxVisibleLines)`. Remove `GetInputHeight()` — callers use `MeasureHeight()` instead. Update `_width` tracking to use the content rect width.

- [ ] **2.3 Migrate `ApiKeyModal`** — Change to `Widget`. Set `Border = true`, `BorderForeground`, `Background = new Color(30, 30, 60)`. Remove self-centering from `Render` (Center container will handle it). `RenderContent` receives the inner content rect and draws title/hint/input/footer relative to it. Remove `ModalWidth`/`ModalHeight` consts from the class; these become construction parameters or are used by the Center wrapper in ChatApp.

- [ ] **2.4 Migrate `ModelPickerModal`** — Change to `Widget`. Set chrome properties. Remove self-centering and manual border/fill. Remove duplicated scroll/selection fields and logic. Add a `ScrollableList<ModelInfo>` child field. `RenderContent` partitions its inner rect: title area, filter row, separator, list area (delegates to `ScrollableList.Render`), detail area. `HandleKey` routes Up/Down/Home/End/PageUp/PageDown to the ScrollableList child; handles filter typing and Enter/Escape itself.

- [ ] **2.5 Migrate `ExtensionManagerModal`** — Same pattern as ModelPickerModal. Replace duplicated scroll logic with `ScrollableList<UrExtensionInfo>` child. `RenderContent` partitions inner rect among title, filter, list (child), detail, footer. `HandleKey` routes list navigation to child.

### Phase 3: Wire up layout in ChatApp

- [ ] **3.1 Replace manual layout in `RenderFrame`** — Build a `VerticalStack` with two entries: `MessageList` as `Fill`, `ChatInput` as `Content`. Call `stack.Render(baseLayer.Content, screenRect)`. Remove manual height computation.

- [ ] **3.2 Replace modal centering and remove shadows** — Wrap each modal in a `Center(child, modalWidth, modalHeight)` when opening it. `ChatApp` renders the Center widget on the overlay layer. Remove the entire shadow block from `RenderFrame` (the pattern-match on modal type for dimensions, `MarkShadow` calls). Shadows were dropped from the layout system spec.

- [ ] **3.3 Clean up ChatApp** — Remove `_chatInput.GetInputHeight(w)` call (now handled by VerticalStack + MeasureHeight). Type the active-modal field as Widget rather than IComponent.

### Phase 4: Remove IComponent and update tests

- [ ] **4.1 Delete `IComponent`** — Remove `Ur.Terminal/Components/IComponent.cs`. Fix any remaining references.

- [ ] **4.2 Add framework tests** — Tests for `Thickness`, `Widget` chrome rendering (background fill, border, padding, ContentRect, MeasureHeight), `SizeConstraint` invariants, `VerticalStack` layout algorithm (Fixed, Content, Fill distribution, degenerate cases), `Center` positioning, `ScrollableList<T>` (selection, scrolling, EnsureVisible, key handling, item change clamping).

- [ ] **4.3 Update existing component tests** — `ChatInputTests`, `MessageListTests`, `ApiKeyModalTests`, `ModelPickerModalTests`, `ExtensionManagerModalTests`, `ChatAppTests` — update for Widget API changes (e.g., `GetInputHeight` → `MeasureHeight`, removed self-centering in modals, etc.).

## Validation

- **Tests:** `dotnet test` across all test projects. New framework type tests cover chrome rendering, layout algorithms, ScrollableList behavior. Existing component tests updated and passing.
- **Build:** `dotnet build` clean with no warnings.
- **Manual:** Run the TUI. Verify: base layout resizes correctly (message list fills, input grows with content). Modals center correctly with border/background. Model picker and extension manager scroll, filter, and select correctly. Ctrl+C cancel, slash commands, API key flow all functional.

## Design decisions

- **Widget.Render early-returns on degenerate rects.** If the content rect would be empty or negative after subtracting chrome overhead, `Render` draws nothing (or just background fill) and skips the `RenderContent` call. This avoids passing negative-sized rects to subclasses.

## Open questions

None currently.
