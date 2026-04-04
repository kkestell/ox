# Scrollable Table Widget with Row Selection

## Goal

Add a `Table<T>` widget that displays typed, columnar data with keyboard-navigable row selection and automatic scroll-to-center behavior. Wire it into the demo app behind a `[Model]` button that opens a dialog with 100 dummy rows to exercise the scrolling.

## Desired outcome

- A reusable `Table<T>` widget in `Ur.Widgets` with column definitions, row selection, and self-managed scrolling.
- A WinForms-inspired data-binding surface: `ObservableCollection<T>` data source + column value selectors.
- A `[Model]` button in the demo app that opens a dialog containing the table with enough rows to test scrolling.
- Up/Down arrows move the selection; the viewport keeps the selected row centered (clamped at edges).

## Related code

- `Ur.Widgets/Widget.cs` — Base class; Table inherits from this. Provides `Focusable`, `HandleInput`, `Draw`, `Layout`, `OffsetY`.
- `Ur.Widgets/ScrollView.cs` — Reference for self-managed scrolling via `OffsetY`, scrollbar drawing, and the `height=0` unconstrained-layout convention.
- `Ur.Widgets/ListView.cs` — Reference for `ObservableCollection<T>` data binding pattern. Table will use the same pattern but render rows itself rather than creating child widgets.
- `Ur.Widgets/Button.cs` — Reference for focus styling (inverted colors), `HandleInput` pattern, and `PreferredWidth`/`PreferredHeight` sizing.
- `Ur.Widgets/Dialog.cs` — Table will be hosted inside a Dialog subclass. Content area is a `Flex.Vertical()` exposed via `protected Flex Content`.
- `Ur.Demo/Program.cs` — `ChatDemoApp` constructor builds the layout; the `[Model]` button and `ModelDialog` will be added here.
- `Ur.Console/Key.cs` — `Key.Up`, `Key.Down`, `Key.Enter` used for navigation.
- `Ur.Drawing/ICanvas.cs` — `DrawText`, `DrawHLine`, `SetCell` for rendering cells, headers, separator, and scrollbar.

## Current state

- **Existing patterns to follow**: `ListView<T>` uses `ObservableCollection<T>` + `Func<T, Widget>` factory. Table will use `ObservableCollection<T>` + `Func<T, string>` column selectors instead — no child widgets per row, since the table draws everything itself.
- **ScrollView scrollbar**: Proportional thumb drawn with `█` on a `│` track in the rightmost column. Table will reuse this visual pattern.
- **Focus ring**: Application rebuilds the focus ring when a modal opens/closes. Table needs `Focusable = true` to receive arrow key input inside the dialog.
- **Dialog sizing**: 60% width, natural height capped at 80%. The table inside should use `Grow` sizing to fill the dialog's content area.

## Structural considerations

**Table is a leaf widget, not a container.** Unlike `ListView<T>` which creates a child widget per item and delegates drawing to the Renderer tree-walk, `Table<T>` draws all visible rows directly in `Draw()`. This is the right choice because:

1. Row selection and scroll offset are tightly coupled — managing them as internal state in one widget is simpler than coordinating across 100+ child widgets.
2. Column alignment requires global knowledge of column widths, which is natural in a single Draw() pass but awkward when each row is an independent widget.
3. Performance — no widget allocation per row, no tree-walk overhead for large datasets.

**Data binding mirrors ListView's ObservableCollection pattern** but column definitions replace the widget factory. The `TableColumn<T>` class holds a header string and a `Func<T, string>` value selector, following the WinForms `DataGridViewTextBoxColumn` + `DataPropertyName` pattern translated to a functional style.

**Scroll-to-center algorithm**: On every selection change, compute the ideal scroll offset that centers the selected row in the viewport. Clamp to `[0, maxOffset]` so the viewport never slides past the data boundaries. This runs in `HandleInput` after updating `SelectedIndex`, and the next `Layout`/`Draw` cycle picks up the new offset.

## Implementation plan

### 1. Create `TableColumn<T>` class

- [x] Create `Ur.Widgets/TableColumn.cs` with:
  - `string Header` — column header text
  - `Func<T, string> ValueSelector` — extracts display text from a row item
  - `int? Width` — explicit column width in characters; `null` means auto-distribute remaining space

### 2. Create `Table<T>` widget

- [x] Create `Ur.Widgets/Table.cs` with the following structure:

**Properties:**
- `ObservableCollection<T> DataSource` — the bound data; listen to `CollectionChanged` to stay in sync (clamp `SelectedIndex` on removals, etc.)
- `List<TableColumn<T>> Columns` — column definitions
- `int SelectedIndex` — currently selected row (-1 = none; default 0 if data exists)
- `event Action<T>? SelectionChanged` — fires when the selected row changes
- `event Action<T>? ItemActivated` — fires on Enter key

**Internal state:**
- `int _scrollOffset` — index of the first visible data row (not pixel offset)

**Constructor:**
- Set `Focusable = true`
- Set `HorizontalSizing = SizingMode.Grow`, `VerticalSizing = SizingMode.Grow`
- Subscribe to `DataSource.CollectionChanged`

**`Layout(int availableWidth, int availableHeight)`:**
- Set `Width = availableWidth`, `Height = availableHeight`
- Compute resolved column widths:
  - Sum explicit `Width` values + 1 (scrollbar column) + column separator characters
  - Distribute remaining width evenly among `null`-width columns
- Recompute `_scrollOffset` via the centering algorithm (so layout always reflects current selection)

**`Draw(ICanvas canvas)`:**
- **Row 0: Header row** — draw each column header, separated by `│`, with bold/highlighted style. Draw `─` separator line on row 1 (with `┼` at column boundaries).
- **Rows 2+: Data rows** — for each visible row (from `_scrollOffset` to `_scrollOffset + visibleRowCount`):
  - Extract cell text via each column's `ValueSelector`
  - Truncate to column width
  - If this row is selected: draw with inverted style (swap Fg/Bg)
  - Separate columns with `│`
- **Scrollbar** — draw in the rightmost column, using ScrollView's proportional thumb pattern (`█` thumb on `│` track)

**`HandleInput(InputEvent input)`:**
- `Key.Up` — decrement `SelectedIndex` (clamp to 0), fire `SelectionChanged`, recalculate scroll
- `Key.Down` — increment `SelectedIndex` (clamp to `DataSource.Count - 1`), fire `SelectionChanged`, recalculate scroll
- `Key.Enter` — fire `ItemActivated` with `DataSource[SelectedIndex]`

**Scroll-to-center algorithm** (private method, called after selection changes):
```
visibleRows = Height - 2  (subtract header + separator)
halfViewport = visibleRows / 2
idealOffset = SelectedIndex - halfViewport
maxOffset = max(0, DataSource.Count - visibleRows)
_scrollOffset = clamp(idealOffset, 0, maxOffset)
```

### 3. Create `ModelDialog` in the demo app

- [x] Create a `ModelDialog` class in `Ur.Demo/Program.cs` (or a new file) that subclasses `Dialog`:
  - Title: `"Select Model"`
  - Content contains a `Table<ModelOption>` where `ModelOption` is a simple record with `Name`, `Provider`, `ContextWindow` fields
  - Define 3 columns: Model Name, Provider, Context Window
  - Populate with 100 dummy rows in a loop (e.g. `$"model-{i}"`, rotating providers, varying context sizes)
  - Subscribe to `ItemActivated`: call `Close(DialogResult.OK)` so Enter on a row dismisses the picker immediately
  - OK button also closes with `DialogResult.OK`, reading the current selection
  - Table should use `Grow` sizing to fill the dialog content area

### 4. Add `[Model]` button to the demo input row

- [x] In `ChatDemoApp` constructor, after the `sendButton` definition:
  - Create a `new Button("Model")`
  - On `Clicked`: instantiate `ModelDialog`, subscribe to `Closed` to read the selected model, call `ShowModal(dialog)`
  - Add the button to `inputRow` (after `sendButton`, so layout is `[TextInput] [Send] [Model]`)

### 5. Handle `ObservableCollection` changes in Table

- [x] In the `CollectionChanged` handler:
  - **Add**: If `SelectedIndex == -1` and collection is now non-empty, set `SelectedIndex = 0`
  - **Remove**: If removed index <= `SelectedIndex`, decrement `SelectedIndex` (clamp to 0 or -1 if empty)
  - **Reset**: Reset `SelectedIndex` to 0 if collection non-empty, else -1; reset `_scrollOffset = 0`

## Validation

- **Manual verification**:
  - Run the demo app
  - Tab to `[Model]` button, press Enter — dialog opens with table
  - Verify header row with 3 columns is visible
  - Up/Down arrows move the blue/inverted selection highlight
  - Selection stays centered as you scroll through 100 rows
  - At the top and bottom, the viewport stops sliding (no blank space)
  - Scrollbar thumb moves proportionally
  - Enter on a row (or OK button) dismisses the dialog
  - Escape cancels the dialog
  - Focus returns to the main app after dialog closes

- **Edge cases to verify**:
  - Arrow key at row 0 (should not go negative)
  - Arrow key at last row (should not exceed count)
  - Resize terminal while dialog is open (layout should recompute)
  - Table with fewer rows than viewport height (no scrollbar thumb, selection still works)

- `Key.Enter` — fire `ItemActivated` with `DataSource[SelectedIndex]`. The Table itself does **not** dismiss anything — it just raises the event. Consumers (like `ModelDialog`) decide whether to close, navigate, or take any other action in their handler.
