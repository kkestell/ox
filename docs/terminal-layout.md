# Terminal Layout System

> Part of: [Terminal Framework](terminal-framework.md) · [Ur Architecture](index.md)

## Purpose and Scope

Layout primitives and a unified component base type for the Ur.Terminal framework. `Widget` replaces `IComponent` as the base abstraction — every renderable element is a Widget with optional chrome (border, background, padding) and optional measurement. Layout containers (VerticalStack, Center) and reusable components (ScrollableList) are Widgets themselves. Enough to eliminate manual rect arithmetic in applications without building a full layout engine.

### Non-Goals

- Not a general-purpose layout engine. No CSS-style flexbox, grid, or constraint solver. The layout vocabulary is: vertical partition, centering, decoration.
- No horizontal layout, grid, or arbitrary nesting in v1. The interfaces allow adding these later without breaking existing code.
- No focus system or automatic key routing through the layout tree. Applications route key events to leaf widgets directly. Containers return `false` from `HandleKey`.
- No reactive/data-binding system. Layout is recomputed each frame from current state.
- No shadow support. Shadows were dropped to keep things simple. The Layer shadow mask in Ur.Terminal remains available if needed later.

## Context

### Dependencies

| Dependency | What it provides | Interface |
|---|---|---|
| Ur.Terminal Core | `Buffer`, `Rect`, `Cell`, `Color` | Value types for rendering |

### Dependents

| Dependent | What it needs | Interface |
|---|---|---|
| Ur.Tui (chat app) | Widget base class, layout containers, ScrollableList | All public types in this module |

## Data Structures

### Widget (abstract base class)

- **Purpose:** The unified base type for all renderable, interactive UI elements. Replaces `IComponent`. Every component in the system is a Widget — layout containers, leaf components, reusable pieces like ScrollableList. Chrome (border, background, padding) is not a separate decorator; it's configuration on any widget.
- **Shape:** Abstract class.
  - **Chrome properties:**
    - `Border: bool` — draw a box-drawing border (1 cell on each side)
    - `BorderForeground: Color?` — border line color
    - `BorderBackground: Color?` — border cell background
    - `Background: Color?` — fill the entire widget rect with this color before drawing border/content
    - `Padding: Thickness` — space between border (or edge, if no border) and inner content
  - **Rendering contract:**
    - `Render(Buffer, Rect)` — **not virtual.** Draws chrome (background fill → border → compute inner rect), then calls `RenderContent(buffer, innerRect)`. Subclasses never override this.
    - `RenderContent(Buffer, Rect)` — **abstract.** Subclasses implement this. The rect they receive has chrome already subtracted. A MessageList never thinks about borders.
    - `HandleKey(KeyEvent) → bool` — **abstract.** Process a key event, return true if consumed.
  - **Measurement:**
    - `MeasureContentHeight(int availableWidth) → int?` — **virtual, returns null.** Subclasses that have a preferred height override this (e.g., ChatInput returns its wrapped line count). The `availableWidth` is the content width — chrome is already subtracted.
    - `MeasureHeight(int availableWidth) → int?` — **public, not virtual.** Calls `MeasureContentHeight(availableWidth - horizontalChrome)` and adds vertical chrome (border + padding). This is what VerticalStack calls. Subclasses never override this; they override `MeasureContentHeight`.
  - **Geometry:**
    - `ContentRect(Rect outerRect) → Rect` — computes the inner rect given an outer rect. Public so callers can inspect geometry if needed.
  - **Chrome overhead:**
    - Horizontal: `(Border ? 2 : 0) + Padding.Left + Padding.Right`
    - Vertical: `(Border ? 2 : 0) + Padding.Top + Padding.Bottom`
- **Why a base class, not an interface:** Chrome is universal. Every widget can have a border, background, and padding. Making these properties on the base class means the Render/RenderContent split happens once — every subclass automatically gets correct chrome rendering without any extra work. An interface would push chrome handling into every implementation or require a separate decorator, which is the complexity this design eliminates.
- **Why Render is not virtual:** The chrome → content pipeline is fixed. Subclasses draw content; the base class draws chrome. This guarantees that chrome is always rendered correctly regardless of the subclass. No one forgets to call `base.Render()`.

### Thickness

- **Purpose:** Describes spacing on four sides. Used for Widget padding.
- **Shape:** `readonly record struct Thickness(int Top, int Right, int Bottom, int Left)`
- **Operations:**
  - `Thickness.Uniform(int value)` — same on all sides
  - `Thickness.Zero` — no spacing
- **Why this shape:** Padding needs to be asymmetric (modals currently use 1 left/right, 0 top/bottom). A four-sided value handles all cases. Record struct for cheap copying and structural equality.

### SizeConstraint

- **Purpose:** How a child in a layout container expresses its desired height.
- **Shape:** Discriminated union (abstract record with subtypes):
  - `SizeConstraint.Fixed(int Size)` — exactly N rows.
  - `SizeConstraint.Content` — use the widget's `MeasureHeight`. No callback needed; VerticalStack calls `widget.MeasureHeight(availableWidth)` directly.
  - `SizeConstraint.Fill(int Weight = 1)` — take remaining space, proportional to weight.
- **Invariants:**
  - Fixed size must be ≥ 0.
  - Content requires the widget's `MeasureHeight` to return non-null (otherwise degenerate — treated as 0).
  - Fill weight must be ≥ 1.
- **Why this shape:** These are the three sizing modes present in the current TUI code (ChatInput = content-measured, MessageList = fill, modal dimensions = fixed). `Content` no longer needs a `Func<int, int>` because measurement lives on Widget itself.

### VerticalStack

- **Purpose:** Widget that partitions a rect vertically among children.
- **Shape:** Extends `Widget`. Contains an immutable ordered list of `Entry` values.
  - `readonly record struct Entry(Widget Child, SizeConstraint Height)`
- **Layout algorithm (in RenderContent):**
  1. Measure all Content children: call `entry.Child.MeasureHeight(area.Width)`.
  2. Sum Fixed + measured Content heights. This is the "claimed" space.
  3. Remaining = `area.Height - claimed`. Distribute among Fill children by weight.
  4. Assign Y positions top-down, create a Rect for each child, call `child.Render(buffer, childRect)`.
  5. If total claimed > available height: Content and Fixed children are clamped. Fill children get 0. (Degenerate case — screen too small.)
- **HandleKey:** Returns `false`. The application routes keys to specific children directly.
- **Immutable.** Reconstruct the stack when the layout changes. The objects are lightweight.
- **Why this shape:** The current ChatApp layout is exactly this: MessageList (Fill) + ChatInput (Content). Entry is a record struct for value semantics.

### Center

- **Purpose:** Widget that positions a fixed-size child in the center of its area.
- **Shape:** Extends `Widget`. Constructed with a child `Widget`, a `width`, and a `height`.
- **Layout (in RenderContent):**
  - `innerX = area.X + (area.Width - width) / 2`
  - `innerY = area.Y + (area.Height - height) / 2`
  - Calls `child.Render(buffer, new Rect(innerX, innerY, width, height))`.
- **HandleKey:** Delegates to child.

### ScrollableList\<T\>

- **Purpose:** Reusable scrollable, selectable list. Manages selection state, scroll offset, visible window, and scroll indicators. The caller provides items and a render callback for each item.
- **Shape:** Extends `Widget`. Generic over item type `T`.
  - `Items: IReadOnlyList<T>` — settable property. When items change (e.g., filter), selection and scroll are clamped.
  - `SelectedIndex: int` — current selection (read-only to callers; mutated via key input).
  - `SelectedItem: T?` — convenience accessor.
  - `ItemRenderer: Action<Buffer, Rect, T, bool>` — callback to render one item. `bool` = isSelected. Called for each visible item during RenderContent.
- **RenderContent behavior:**
  1. Compute visible window: `visibleCount = area.Height` (one row per item for v1).
  2. Clamp `_scrollOffset` so selected item is visible (EnsureVisible logic).
  3. For each visible item: compute item rect (`area.X, area.Y + i, area.Width, 1`), call `ItemRenderer`.
  4. Draw scroll indicators (▲/▼) at top-right/bottom-right if items overflow above/below.
- **HandleKey:**
  - Up/Down → move selection, return true
  - Home/End → jump to first/last, return true
  - PageUp/PageDown → move by visible page, return true
  - Enter → return false. The caller reads `SelectedItem` and handles activation directly. No callback plumbing needed.
  - Other keys → return false
- **Invariants:**
  - `SelectedIndex` is always in `[0, Items.Count - 1]` or -1 if Items is empty.
  - `_scrollOffset` is always in `[0, max(0, Items.Count - visibleCount)]`.
- **Why generic + callback:** The modals have different item rendering (model picker shows context length and cost, extension manager shows tier and status). The list handles the mechanical parts; the caller handles the visual parts.

## Internal Design

### Module Structure

```
Ur.Terminal/
  Core/
    Thickness.cs          — four-sided spacing value
    ... (existing: Color, Cell, Rect, Buffer)
  Layout/
    SizeConstraint.cs     — Fixed | Content | Fill
    VerticalStack.cs      — vertical partitioning
    Center.cs             — centering container
  Components/
    Widget.cs             — abstract base class (replaces IComponent)
    ScrollableList.cs     — generic scrollable list
```

### How the TUI Uses These

**Main layout (base layer):**
```
VerticalStack
├── MessageList                  [Fill]
└── ChatInput (Border = true)    [Content → MeasureHeight includes border]
```

**Modal overlay (overlay layer):**
```
Center(width, height)
└── ApiKeyModal (Border = true, Background = color, Padding = ...)
```

ChatApp constructs these trees once (or reconstructs when modals open/close) and calls `Render` on the root each frame. Key routing remains manual — ChatApp knows which widget has focus and calls `HandleKey` on it directly.

**Modals after refactor:** Modal widgets stop computing their own centering, border drawing, and background fills. Their `RenderContent` receives a rect that is already the inner content area. ModelPickerModal and ExtensionManagerModal contain a `ScrollableList<T>` child for their list portion.

## Design Decisions

### Widget as abstract base class, not interface + decorator

- **Context:** How to handle chrome (border, background, padding) on components.
- **Options considered:**
  - `IComponent` interface + `Widget` decorator that wraps components. Composition over inheritance. But creates an artificial distinction between "components" and "widgets" when they're the same thing. Requires wrapping every component that needs a border: `Widget(ChatInput)`, `Center(Widget(modal))`. The wrapping is ceremony, not design.
  - Abstract `Widget` base class with chrome properties. Every component extends Widget. Chrome is just configuration — set `Border = true` on any widget. `Render` (final) draws chrome and calls `RenderContent` (abstract). No wrapping, no decorator, no artificial separation.
- **Choice:** Abstract base class.
- **Rationale:** A widget IS a component. The decorator pattern solves a problem that doesn't exist in a single-developer codebase where you control every type. The base class approach is simpler: one type, one concept. Chrome is automatic — subclasses implement `RenderContent` and get border/background/padding for free.

### MeasureHeight split: public final + protected virtual

- **Context:** VerticalStack needs to know a widget's total height (content + chrome). Who adds the chrome overhead?
- **Options considered:**
  - Subclasses report total height including chrome — fragile, duplicates chrome math in every measurable widget.
  - Base class handles it: `MeasureHeight` (public, final) calls `MeasureContentHeight` (protected, virtual) and adds chrome. Subclasses only think about content height.
- **Choice:** Split.
- **Rationale:** Content measurement and chrome overhead are independent concerns. ChatInput reports "I need 3 lines for my text." Widget.MeasureHeight adds 2 for the border. Neither knows about the other's details.

### ScrollableList as a framework widget, not an app concern

- **Context:** Two modals duplicate ~80 lines of scrollable list logic (fields, EnsureVisible, render loop, scroll indicators, key handling).
- **Options considered:**
  - Keep in the app. Each modal manages its own list. Lower coupling, but exact duplication.
  - Extract to framework. Single implementation, modals provide render callbacks.
- **Choice:** Framework widget.
- **Rationale:** The duplication is exact. Any future list-based widget would need the same thing. The generic + callback design keeps it flexible without coupling the framework to app types.

### One row per item in ScrollableList v1

- **Context:** Should ScrollableList support variable-height items?
- **Choice:** Fixed 1-row for v1. Both current consumers (model picker, extension manager) use single-row items. Variable height adds significant complexity. Can be added later.

## Open Questions

None currently.
