# Add `Measure()` to Widget for side-effect-free height queries

## Goal

Add a virtual `Measure(int availableWidth): int` method to `Widget` so that code
needing a widget's natural height — currently only `ScrollView` — can ask for it
without running the full layout engine or mutating widget state.

## Desired outcome

- `ScrollView.HandleInput` no longer depends on a stale `_contentHeight` cached from
  the previous `Draw()` call.
- A clean, side-effect-free API exists on `Widget` for "how tall would you be given
  this width?"
- `LayoutWithConstraints` remains in `ScrollView.Draw()` (it is still required to
  set up `X/Y/Width/Height` before `RenderTree`), but its dual role as a measurement
  oracle is eliminated.

## How we got here

The discussion started from a code smell in `ScrollView`: `Draw()` calls
`LayoutEngine.LayoutWithConstraints()` to obtain the content's natural height.
That is a phase violation — layout is supposed to precede drawing. Two problems
were identified:

1. `LayoutWithConstraints` temporarily mutates widget sizing properties
   (`HorizontalSizing`, `FixedWidth`) to fake an unconstrained measurement pass,
   then saves/restores them.
2. `_contentHeight` is a stale cache — `HandleInput` reads it from the previous
   frame, which means `maxScroll` is wrong for exactly one frame whenever content
   changes.

The proposal: add a pure `Measure()` method that answers "how tall?" without
touching layout state.

## Approaches considered

### Option A: Pure `Measure()` — minimal scope (recommended)

- Summary: Add `virtual int Measure(int availableWidth): int` to `Widget` with a
  default of `PreferredHeight`. Override in container widgets to recursively sum
  children. `ScrollView.HandleInput` calls `_content.Measure(Width - 1)` directly,
  removing the stale `_contentHeight` field.
- Pros: Minimal diff; fixes the staleness bug; establishes the right API contract;
  no changes to `LayoutEngine` or the layout protocol.
- Cons: `LayoutWithConstraints` remains in `Draw()` (still a phase violation; still
  mutates state transiently). That smell is documented but not removed.
- Failure modes: If a widget's natural height genuinely depends on the post-grow/shrink
  width of its children (not just the parent-provided `availableWidth`), `Measure()`
  may undercount. This is not a concern for the current widget set where content inside
  `ScrollView` uses `VerticalSizing.Fit`.

### Option B: Full Measure + Arrange split

- Summary: Separate `LayoutWithConstraints` into a pure `Measure` phase that returns
  height and an `Arrange` phase that mutates `X/Y/Width/Height` for rendering. The
  layout engine protocol gets a proper two-stage API.
- Pros: Fully resolves the phase violation.
- Cons: Invasive — changes `LayoutEngine`, `Widget`, and all call sites. The phase
  violation is benign at TUI scale; the complexity cost is not justified by the gain.
- Failure modes: Measure and Arrange can diverge if widgets compute differently in
  each phase.

### Option C: `BeforeLayout` hook on Widget

- Summary: `LayoutEngine` calls a virtual `OnLayoutComplete()` on each widget after
  it is positioned. `ScrollView.OnLayoutComplete()` calls `LayoutWithConstraints` on
  `_content`, moving the layout-in-draw to layout-in-layout.
- Pros: Fully fixes the phase violation without a full Measure/Arrange split.
- Cons: `LayoutEngine` grows a new extension point; the semantics of `OnLayoutComplete`
  are subtle (the widget tree is partially positioned when it fires). Higher risk than
  the benefit warrants for now.

## Recommended approach

Option A. The concrete pain is the `_contentHeight` staleness bug; Option A removes
it with minimal blast radius. The `LayoutWithConstraints`-in-Draw concern is real but
self-contained and clearly commented — it is a documented quirk, not a spreading
infection. A full phase-split (Option B or C) should be a separate decision once more
widgets need measurement.

## Related code

- `Ur.Widgets/Widget.cs` — base class; `Measure()` is added here as a virtual method
- `Ur.Widgets/Label.cs` — leaf widget; `Measure()` returns `_lines.Length`
  (matches `PreferredHeight`, so the default may suffice)
- `Ur.Widgets/Stack.cs` — container; needs override to sum/max children recursively
- `Ur.Widgets/ListView.cs` — container (vertical); same as Stack vertical case
- `Ur.Widgets/TextInput.cs` — leaf widget; `Measure()` returns 1 (matches `PreferredHeight`)
- `Ur.Widgets/ScrollView.cs` — primary consumer; `HandleInput` drops `_contentHeight`,
  calls `_content.Measure(Width - 1)` instead
- `Ur.Widgets/LayoutEngine.cs` — `LayoutWithConstraints` stays as-is; no changes needed
- `Ur.Widgets/Calculations.cs` — `CalculateDimension` is the reference implementation
  that `Measure()` mirrors for container logic

## Current state

- `ScrollView.Draw()` calls `LayoutEngine.LayoutWithConstraints(_content, 0, 0, contentWidth, 0)`,
  which runs the full 7-pass layout just to get `_content.Height`.
- `_contentHeight` field caches that height so `HandleInput` can compute `maxScroll`
  without re-running layout. One-frame staleness is the bug.
- No widget currently has a side-effect-free height-query API.

## Structural considerations

`Measure()` sits at the `Widget` abstraction layer and flows information upward
(children report to parents) — the same direction as the existing bottom-up layout
passes. It does not add a new dependency layer or cross a module boundary.

The method intentionally mirrors only the bottom-up measurement passes (Passes 1–4 in
`LayoutEngine`), not grow/shrink (Passes 3/6). This is correct: `Measure()` answers
"natural height given this width", which is what callers need. `ScrollView` in
particular needs the *unconstrained* content height, not a grow/shrink-adjusted one.

Container widgets (`Stack`, `ListView`) implement `Measure()` using the same child-
summing logic as `Calculations.CalculateDimension`. The duplication is small and
intentional — `CalculateDimension` reads already-laid-out `Width`/`Height` values,
while `Measure()` must work before any layout has run.

## Implementation plan

- [ ] Add `virtual int Measure(int availableWidth): int` to `Widget` in
  `Ur.Widgets/Widget.cs`. Default implementation returns `PreferredHeight`. Document
  that implementations must be pure (no mutation of `X/Y/Width/Height`).

- [ ] Override `Measure()` in `Stack` (`Ur.Widgets/Stack.cs`):
  - Vertical stack: sum `child.Measure(availableWidth)` over all children, plus
    `ChildGap * (Children.Count - 1)`, plus vertical padding/margin.
  - Horizontal stack: max of `child.Measure(availableWidth)` over all children, plus
    vertical padding/margin.

- [ ] Override `Measure()` in `ListView<T>` (`Ur.Widgets/ListView.cs`) — identical
  to vertical `Stack` since `ListView` is always vertical and `Fit`-height.

- [ ] Verify that `Label` and `TextInput` are correct with the default (`PreferredHeight`)
  so no override is needed. If the default is wrong, add minimal overrides.

- [ ] Refactor `ScrollView` (`Ur.Widgets/ScrollView.cs`):
  - Remove the `_contentHeight` field.
  - In `HandleInput`: replace `Math.Max(0, _contentHeight - Height)` with
    `Math.Max(0, _content.Measure(Width - 1) - Height)`.
  - In `Draw()`: remove the `_contentHeight = contentHeight` cache assignment (the
    local `contentHeight` variable from `_content.Height` after `LayoutWithConstraints`
    is unchanged).
  - Update the class-level doc comment to reflect that `_contentHeight` is gone.

## Validation

- Build the solution: `dotnet build`
- Manually verify scroll behaviour in the demo app: content should scroll correctly,
  auto-scroll should pin to bottom, and scrolling up/down near a content update should
  not jump by one row.
- Check that `maxScroll` in `HandleInput` is immediately correct after content is
  appended (the one-frame-stale bug) by adding items while scrolled up and pressing
  Down.

## Open questions

- Should `Measure()` account for `MinHeight`/`MaxHeight` constraints, or return the
  unconstrained natural height? `ScrollView` wants unconstrained (it needs to know how
  tall the content *wants* to be, not how tall it's allowed to be). Confirm this is
  correct for all current callers before implementing.
