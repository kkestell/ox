# Composite Conversation View

## Goal

Rewrite `ConversationView` from a monolithic canvas (one `OnDrawingContent` drawing all entries manually) to a composite SubView architecture where each conversation entry is its own `View` subclass, stacked vertically with `Pos.Bottom()` chains and scrolled using Terminal.Gui's built-in infrastructure.

The text layout engine (`ConversationTextLayout`, `ConversationEntryLayout`) is well-tested and does genuinely custom work (word-wrapping styled segments across boundaries). It stays. What changes is how entries are composed into the scrollable view.

## What's Changing and Why

**Current architecture:** ConversationView owns a `List<ConversationEntry>` (data models), renders every line of every entry in a single `OnDrawingContent`, manually computes viewport offsets, intercepts mouse wheel events, and manages its own rendered-line cache.

**New architecture:** ConversationView becomes a scrollable container. Each `ConversationEntry` gets a corresponding `ConversationEntryView : View` that draws itself. Terminal.Gui handles layout (positioning), clipping (which entries are visible), and scrollbar rendering. ConversationView manages auto-scroll pin-to-bottom (app-level behavior) and the entry-to-view lifecycle.

### Why this is better

- **Idiomatic Terminal.Gui v2:** The framework is designed for SubView composition with Pos/Dim chains, not canvas rendering.
- **Each entry is a real View:** Opens the door for per-entry interactivity later (click to copy, expand/collapse, keyboard navigation between entries).
- **Less custom viewport math:** `ConversationViewportBehavior` mostly reimplements what `SetContentSize` + built-in scrolling already does.
- **Simpler drawing code:** Each entry view only draws its own content, not the entire conversation.

## Related Code

- `src/Ox/Views/ConversationView.cs` — Current monolithic view. **Rewrite target.**
- `src/Ox/Views/ConversationEntry.cs` — Data model for entries. Stays mostly as-is; the `Changed` event now triggers the entry view's relayout instead of invalidating the parent's cache.
- `src/Ox/Views/ConversationEntryLayout.cs` — Segments-to-RenderedLines transform. **Stays unchanged.** Used by `ConversationEntryView` instead of `ConversationView`.
- `src/Ox/Views/ConversationTextLayout.cs` — Core word-wrap algorithm. **Stays unchanged.**
- `src/Ox/Views/ConversationViewportBehavior.cs` — Pure viewport math. **Most of this is deleted.** Only `GetContentWidth` (horizontal padding) and `IsPinnedToBottom` survive; the rest is handled by Terminal.Gui.
- `src/Ox/Views/OxApp.cs` — Root layout. Minor changes: splash view management.
- `src/Ox/EventRouter.cs` — Routes agent events to `ConversationEntry` models. **No changes** — it speaks to the data model, not the view.
- `tests/Ur.Tests/ConversationViewLayoutTests.cs` — Tests the text layout core. **Stays unchanged.**
- `tests/Ur.Tests/ConversationViewportBehaviorTests.cs` — Tests viewport math. **Updated** to remove tests for deleted helpers; keeps tests for surviving helpers.

## Implementation Tasks

### Phase 1: Create ConversationEntryView

- [ ] **Create `src/Ox/Views/ConversationEntryView.cs`** — A `View` subclass that draws one `ConversationEntry`.
  - Constructor takes a `ConversationEntry` and subscribes to its `Changed` event.
  - `Width = Dim.Fill()` (fills parent's content width).
  - Height is computed from the number of wrapped lines (call `ConversationEntryLayout.LayoutSegments` with `Viewport.Width` minus circle chrome width).
  - `OnDrawingContent` draws the circle prefix (or nothing for Plain), continuation indent, and styled spans — the same logic currently in `ConversationView.RenderEntryWithChrome` and `RenderEntryPlain`.
  - On `Changed`, recalculate height via `RecalculateHeight()` which calls `SetContentSize` or updates `Height` if the wrapped line count changed, then `SetNeedsDraw()`.
  - The horizontal padding (1 column per side) is applied by this view, not the parent. Each entry view draws its own gutter.

- [ ] **Handle children (subagent nesting) inside ConversationEntryView.**
  - Children of a `ConversationEntry` become nested `ConversationEntryView` SubViews inside the parent entry view.
  - These are stacked with `Y = Pos.Bottom(prev)` within the entry view.
  - The parent entry view's height accounts for its own text lines plus all children.
  - Tail-clipping (MaxChildRows) is still handled here — if total child rows exceed the cap, show an ellipsis and only the last N rows of children.
  - Indentation: children are inset by `CircleChrome` (2 columns). Use `X = CircleChrome` and `Width = Dim.Fill(CircleChrome)` on child entry views, or handle via the parent's drawing offset.

### Phase 2: Rewrite ConversationView as a scrollable container

- [ ] **Rewrite `ConversationView` as a container of `ConversationEntryView` SubViews.**
  - Remove: `_cachedLines`, `_cachedWidth`, `GetRenderedLines()`, `DrawRenderedLine()`, `RenderEntryPlain()`, `RenderEntryWithChrome()`, `IndentLine()`, `MakeCirclePrefix()`, `MakeContinuationPrefix()`.
  - Remove: the `OnDrawingContent` override entirely. Terminal.Gui draws SubViews automatically.
  - Remove: `OnMouseEvent` override for wheel interception. Use built-in scroll behavior instead.
  - Keep: `AddEntry(ConversationEntry)` — now creates a `ConversationEntryView`, positions it with `Y = Pos.Bottom(lastView)` (or `Y = 0` for the first), sets `Width = Dim.Fill()`, and calls `Add(entryView)`.
  - Keep: `_autoScrollPinnedToBottom` and the scroll-to-bottom logic. This is app behavior (keep the user at the tail during streaming) that Terminal.Gui doesn't provide out of the box.
  - Keep: `ContentChanged` event (for OxApp to react to new content).
  - Keep: `EntryCount` property.
  - Add: track the last added `ConversationEntryView` so the next one can chain `Y = Pos.Bottom(lastView)`.

- [ ] **Enable built-in scrollbar.**
  - Set `ViewportSettings |= ViewportSettingsFlags.HasVerticalScrollBar` in the constructor.
  - When entries change height (via `ConversationEntryView.HeightChanged` event or similar), update `SetContentSize()` to the total height of all stacked entries.
  - Alternatively, if `Dim.Auto(DimAutoStyle.Content)` reliably computes the total from the Pos.Bottom chain, rely on that and skip manual `SetContentSize`.

- [ ] **Implement auto-scroll pin-to-bottom.**
  - Subscribe to `OnViewportChanged` to detect when the user scrolls manually.
  - If the user scrolls up (viewport Y moves away from bottom), disable auto-scroll.
  - If the user scrolls back to bottom, re-enable auto-scroll.
  - When new content arrives (entry added or existing entry height changes) and auto-scroll is enabled, scroll to bottom.
  - Use `ConversationViewportBehavior.IsPinnedToBottom` (keep this helper) to detect pin state.

- [ ] **Remove `RenderedLine` and `RenderSpan` from `ConversationView.cs`.**
  - These types move into `ConversationEntryView.cs` (they're only used for per-entry drawing now).
  - Or, if the entry view draws directly from `ConversationEntryLayout` output without an intermediate type, they can be removed entirely.

### Phase 3: Splash view

- [ ] **Extract splash art into a `SplashView : View`.**
  - Draws the "OX" ASCII art centered in its bounds.
  - `ConversationView` (or `OxApp`) shows `SplashView` when `EntryCount == 0`, hides it when the first entry arrives.
  - Simple approach: `SplashView` is a sibling of `ConversationView` in `OxApp`, toggled via `Visible`. Or it's a SubView of `ConversationView` that gets removed on first entry.

### Phase 4: Simplify ConversationViewportBehavior

- [ ] **Delete helpers that Terminal.Gui now handles:**
  - `GetWheelDelta` — no longer intercepting mouse wheel manually
  - `IsVerticalWheel` — same
  - `GetContentHeight` — Terminal.Gui computes this from SubView layout
  - `GetBottomViewportY` — use Terminal.Gui's content size minus viewport height
  - `ClampViewportY` — Terminal.Gui clamps viewport automatically
- [ ] **Keep:**
  - `GetContentWidth` — the 1-column horizontal padding is still Ox-specific
  - `HorizontalPaddingColumns` constant
  - `IsPinnedToBottom` — still needed for auto-scroll behavior
- [ ] **Update tests in `ConversationViewportBehaviorTests.cs`** — remove tests for deleted methods, keep tests for surviving methods.

### Phase 5: Blank-line separators

- [ ] **Handle inter-entry spacing.**
  - Currently, `GetRenderedLines` inserts a blank `RenderedLine` between top-level Circle/User entries.
  - In the composite approach, use Terminal.Gui's Margin or a simple spacer. Options:
    - Set `Margin.Top = 1` on each `ConversationEntryView` (except the first) for Circle/User entries.
    - Or insert a 1-row spacer View between entries.
  - Plain entries get no spacing (they're continuation content like tool results).

### Phase 6: Verify and test

- [ ] **Verify existing tests still pass** (`ConversationViewLayoutTests` should be unaffected since they test the pure layout core).
- [ ] **Write new tests for `ConversationEntryView`:**
  - Height calculation matches expected wrapped line count for given width.
  - Height updates when content changes (AppendSegment triggers recalculation).
  - Circle chrome is applied for User/Circle styles, absent for Plain.
  - Children are nested and indented correctly.
  - Tail-clipping works when children exceed MaxChildRows.
- [ ] **Write new tests for the rewritten `ConversationView`:**
  - Adding entries creates SubViews with correct Pos.Bottom chain.
  - Auto-scroll to bottom when pinned and new entry is added.
  - Auto-scroll disabled when user scrolls up.
  - Auto-scroll re-enabled when user scrolls back to bottom.
- [ ] **Manual verification:**
  - Streaming text renders correctly and scrolls to bottom.
  - Mouse wheel scrolling works (up disables auto-scroll, back to bottom re-enables).
  - Tool call lifecycle (yellow -> green/red) circle color transitions work.
  - Subagent nested entries render indented with tail-clipping.
  - Terminal resize causes correct relayout.
  - Splash art shows on empty conversation, disappears on first entry.
  - Built-in scrollbar appears when content exceeds viewport.

## Structural Considerations

**EventRouter is untouched.** It creates `ConversationEntry` data models and calls `conversationView.AddEntry()`. The view's internal change from canvas to composite is invisible to the router.

**ConversationEntry data model is untouched.** The `Changed` event, `Segments`, `Children`, `GetCircleColor` — all of this stays. The only consumer that changes is the view layer.

**Text layout engine is untouched.** `ConversationTextLayout` and `ConversationEntryLayout` continue to do the word-wrapping. They're now called by `ConversationEntryView` instead of `ConversationView`.

**Threading model is unchanged.** All mutations still happen on the UI thread via `Application.Invoke`. The entry's `Changed` event fires on the UI thread, the entry view reacts by recalculating height and calling `SetNeedsDraw()`.

## Open Questions

1. **Dim.Auto vs explicit SetContentSize for scroll height:** Does `Dim.Auto(DimAutoStyle.Content)` on the parent reliably compute the total height from a long Pos.Bottom chain of SubViews? If so, we can avoid manual `SetContentSize` calls. If not, we need to sum entry heights and call `SetContentSize` explicitly when entries change. The safe approach is to start with explicit `SetContentSize` and see if Dim.Auto can replace it.

2. **Entry view height update lifecycle:** When a streaming token causes a line wrap and the entry view's height increases by 1 row, does the Pos.Bottom chain for subsequent entries automatically relayout? Terminal.Gui's layout engine should handle this (height change -> `SetNeedsLayout()` on parent -> relayout all SubViews), but needs verification.

3. **Scroll performance with many entries:** With 100+ entries stacked via Pos.Bottom, does Terminal.Gui efficiently skip drawing off-screen SubViews, or does it attempt to draw all of them? If the latter, we may need virtualization later (but YAGNI for now).
