# Model picker columnar display

## Goal

Update the model selection modal to show provider, model ID, context length, and pricing in a multi-column layout instead of just the display name.

## Desired outcome

Each list row renders as:

```
google     gemini-3-flash-preview    200k  $0.05  $0.80
anthropic  claude-sonnet-4-6         200k  $3.00  $15.00
```

- Two spaces between each column.
- Provider column: width = widest provider in the current filtered list.
- Model column: fills remaining horizontal space; truncates with `…` if needed.
- Context column: fixed width (5 chars — covers "1.0M" and "200k").
- Price-in column: fixed width (6 chars — covers "$75.00").
- Price-out column: fixed width (7 chars — covers "$75.00", right-aligned to row end).

## Related code

- `Ur.Tui/Components/ModelPickerModal.cs` — The modal being changed. Contains `RenderItem`, `RenderContent`, column sizing, and the detail area.
- `Ur/Providers/ModelInfo.cs` — Record with `Id`, `Name`, `ContextLength`, `InputCostPerToken`, `OutputCostPerToken`. All needed data is already present.
- `Ur/Providers/ModelCatalog.cs` — Populates `ModelInfo`. Per-token costs are raw decimals (e.g., `0.000003` = $3/Mtok).
- `Ur.Terminal/Components/ScrollableList.cs` — Renders items via `Action<Buffer, Rect, T, bool>` callback. No changes needed; the callback receives the full row rect.
- `Ur.Tui.Tests/ModelPickerModalTests.cs` — Tests that render the modal and assert on buffer content. Several will need updating.

## Current state

- `RenderItem` is a **static** method that renders only `model.Name`, truncated to the row width.
- The **detail area** (bottom 2 rows) shows `model.Id` and context length — this information will now be visible in the list rows, so the detail area can be simplified or removed.
- Modal is 60×20 (`ModalWidth`, `ModalHeight`). Content area after border = 58 chars wide.
- Column budget at 58 chars: provider(~10) + gap(2) + context(5) + gap(2) + priceIn(6) + gap(2) + priceOut(6) = 33 fixed → 25 chars for model name. That's tight for long model IDs (e.g., `claude-sonnet-4-6-20250514` is 26 chars). **Increase `ModalWidth` to 80** to give 45 chars for the model column.
- Filter searches `model.Name` and `model.Id` — no changes needed since the ID contains both provider and model slug.

## Structural considerations

All changes are confined to `ModelPickerModal` (view layer). No new abstractions, no hierarchy changes, no encapsulation violations. The `ScrollableList` component is unchanged — it already delegates item rendering to the callback.

The only design choice is how to pass column widths into `RenderItem`. Currently it's a static method. Making it an instance method lets it capture computed column widths as instance state, which is the simplest approach.

## Implementation plan

- [ ] **Increase modal width.** Change `ModalWidth` from 60 to 80 in `ModelPickerModal`.

- [ ] **Add formatting helpers** (private methods on `ModelPickerModal`):
  - `ExtractProvider(string id)` → returns substring before first `/`, or the full ID if no slash.
  - `FormatContext(int contextLength)` → `"200k"`, `"1.0M"`, etc.
  - `FormatPrice(decimal costPerToken)` → multiply by 1,000,000, format as `$X.XX`. Use `"—"` or `"free"` when zero.

- [ ] **Compute column widths when filter changes.** Add an instance field `_providerWidth` recomputed in `ApplyFilter()` (and in the constructor). Set it to the max length of `ExtractProvider(m.Id)` across `_filtered`. Other columns are constants.

- [ ] **Rewrite `RenderItem` as an instance method.** For each row:
  1. Fill background if selected (existing behavior).
  2. Write provider at `rect.X`, width `_providerWidth`.
  3. Write model slug at `rect.X + _providerWidth + 2`, width = remaining - fixed columns.
  4. Write context at its computed X offset, right-aligned within its 5-char slot.
  5. Write price-in and price-out in their fixed slots.
  6. Truncate model slug with `…` if it exceeds the model column width.

- [ ] **Update or remove the detail area.** The detail area currently shows ID and context, both now visible in the list. Options: remove it entirely (reclaiming 2 rows for the list), or keep it showing supplementary info (max output tokens, display name if different from ID slug). Recommend removing it to maximize list height.

- [ ] **Update `ScrollableList` item renderer assignment.** Change from `ItemRenderer = RenderItem` (static) to `ItemRenderer = RenderItem` (instance method). The delegate signature is unchanged.

- [ ] **Update tests in `ModelPickerModalTests.cs`:**
  - `Render_ShowsModelList`: assert on provider and model slug presence instead of display names.
  - `Render_DetailArea_DoesNotOverwriteLastVisibleListItem`: update or remove if detail area is removed.
  - `Filter_ResetsSelection` and `ArrowKeys_MoveSelection`: update expected model name assertions if `SelectedModel` behavior changes (it shouldn't — `SelectedModel` returns the `ModelInfo` object, not display text).
  - Add a test verifying column alignment: render two models, read the buffer rows, confirm provider and price columns are vertically aligned.

- [ ] **Build and run tests.** `dotnet build` and `dotnet test` from the repo root.

## Validation

- **Tests:** `dotnet test Ur.Tui.Tests` — all existing tests pass (updated as needed), plus new column-alignment test.
- **Manual verification:** Launch TUI, open model picker (Ctrl+M or however it's triggered), confirm columns are aligned and filter still works. Try with long model IDs to verify truncation.

## Open questions

- Should the detail area be removed entirely, or kept for supplementary info (e.g., display name, max output tokens)? Plan assumes removal — flag if you want to keep it.
- Price formatting: when a model is free (`$0.00`), show `"free"`, `"$0.00"`, or `"—"`?
