using System.Collections.ObjectModel;
using System.Collections.Specialized;
using Ur.Console;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A data-driven table widget that displays typed, columnar data with keyboard-
/// navigable row selection and automatic scroll-to-center behavior.
///
/// Design:
/// - Table is a leaf widget, not a container. Unlike ListView{T} which creates a
///   child widget per item, Table draws all visible rows directly in Draw(). This
///   keeps column alignment simple (global knowledge of widths in one Draw pass),
///   keeps selection and scroll offset tightly coupled as internal state, and avoids
///   per-row widget allocation overhead for large datasets.
/// - Data binding mirrors ListView's ObservableCollection pattern, but column
///   definitions (TableColumn{T}) replace the widget factory. Each column holds a
///   header and a Func{T, string} value selector — a functional take on the WinForms
///   DataGridViewTextBoxColumn + DataPropertyName pattern.
/// - Scrolling uses a row-index offset (not pixel offset). On every selection change,
///   the scroll-to-center algorithm computes the ideal offset that centers the
///   selected row in the viewport, clamped so the viewport never slides past data
///   boundaries. This runs in HandleInput; the next Layout/Draw cycle picks it up.
/// </summary>
/// <typeparam name="T">The row item type.</typeparam>
public class Table<T> : Widget
{
    // Resolved pixel widths for each column, recomputed every Layout pass.
    private int[] _resolvedWidths = [];

    // Index of the first visible data row. Managed by the scroll-to-center algorithm.
    private int _scrollOffset;

    /// <summary>
    /// The observable data source. Mutate this collection to add, remove, or replace
    /// rows; the table clamps its selection and scroll offset automatically via
    /// CollectionChanged. Same pattern as ListView{T}.Items.
    /// </summary>
    public ObservableCollection<T> DataSource { get; }

    /// <summary>
    /// Column definitions controlling what data is extracted and how it's displayed.
    /// Set once before the table is shown; column changes after layout are not
    /// dynamically tracked (similar to how DataGridView columns work in WinForms).
    /// </summary>
    public List<TableColumn<T>> Columns { get; }

    /// <summary>
    /// Index of the currently selected row, or -1 if no data. Clamped to valid
    /// bounds on every mutation. Setting this directly does not fire SelectionChanged.
    /// </summary>
    public int SelectedIndex { get; set; }

    /// <summary>
    /// Fires when the selected row changes via keyboard navigation. The argument
    /// is the newly selected item.
    /// </summary>
    public event Action<T>? SelectionChanged;

    /// <summary>
    /// Fires when the user presses Enter on the selected row. The Table itself does
    /// not dismiss anything — consumers (like ModelDialog) decide how to respond.
    /// </summary>
    public event Action<T>? ItemActivated;

    public Table(ObservableCollection<T> dataSource, List<TableColumn<T>> columns)
    {
        DataSource = dataSource ?? throw new ArgumentNullException(nameof(dataSource));
        Columns = columns ?? throw new ArgumentNullException(nameof(columns));

        // Start with the first row selected if data exists.
        SelectedIndex = DataSource.Count > 0 ? 0 : -1;

        Focusable = true;
        HorizontalSizing = SizingMode.Grow;
        VerticalSizing = SizingMode.Grow;

        DataSource.CollectionChanged += OnDataSourceChanged;
    }

    /// <summary>
    /// Keeps SelectedIndex valid when the underlying collection changes.
    /// Follows the same Add/Remove/Reset pattern as ListView{T} but operates
    /// on the selection index rather than child widgets.
    /// </summary>
    private void OnDataSourceChanged(object? sender, NotifyCollectionChangedEventArgs e)
    {
        switch (e.Action)
        {
            case NotifyCollectionChangedAction.Add:
                // If nothing was selected and data appeared, select the first row.
                if (SelectedIndex == -1 && DataSource.Count > 0)
                    SelectedIndex = 0;
                break;

            case NotifyCollectionChangedAction.Remove:
                if (DataSource.Count == 0)
                {
                    SelectedIndex = -1;
                }
                else if (e.OldStartingIndex <= SelectedIndex)
                {
                    // Removed row was at or before the selection — shift selection
                    // up to stay on the same logical item, clamped to bounds.
                    SelectedIndex = Math.Max(0, SelectedIndex - 1);
                }
                break;

            case NotifyCollectionChangedAction.Reset:
                SelectedIndex = DataSource.Count > 0 ? 0 : -1;
                _scrollOffset = 0;
                break;
        }

        RecalculateScroll();
    }

    /// <summary>
    /// Sizes the table to fill available space and resolves column widths.
    ///
    /// Column width resolution: explicit Width values are honored first, then
    /// remaining space (after separators + scrollbar) is distributed evenly
    /// among auto-width columns. This keeps the algorithm simple and predictable.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        Width = availableWidth;
        Height = availableHeight;

        ResolveColumnWidths();
        RecalculateScroll();
    }

    /// <summary>
    /// Computes the resolved pixel width for each column. Fixed-width columns get
    /// their exact value; remaining space is split evenly among auto columns.
    /// One column is reserved for the scrollbar, and separators between columns
    /// consume one character each.
    /// </summary>
    private void ResolveColumnWidths()
    {
        if (Columns.Count == 0)
        {
            _resolvedWidths = [];
            return;
        }

        _resolvedWidths = new int[Columns.Count];

        // Chrome: one separator character between each pair of columns, plus one
        // column for the scrollbar on the right edge.
        var separatorCount = Columns.Count - 1;
        var chromeWidth = separatorCount + 1; // separators + scrollbar

        var usedByFixed = 0;
        var autoCount = 0;

        for (var i = 0; i < Columns.Count; i++)
        {
            if (Columns[i].Width is { } fixedW)
            {
                _resolvedWidths[i] = fixedW;
                usedByFixed += fixedW;
            }
            else
            {
                autoCount++;
            }
        }

        // Distribute remaining space evenly among auto-width columns.
        var remaining = Math.Max(0, Width - chromeWidth - usedByFixed);
        var autoWidth = autoCount > 0 ? remaining / autoCount : 0;

        for (var i = 0; i < Columns.Count; i++)
        {
            if (Columns[i].Width is null)
                _resolvedWidths[i] = autoWidth;
        }
    }

    /// <summary>
    /// Scroll-to-center: computes the scroll offset that places the selected row
    /// in the middle of the visible area, clamped so the viewport never slides
    /// past data boundaries.
    /// </summary>
    private void RecalculateScroll()
    {
        if (SelectedIndex < 0 || DataSource.Count == 0)
        {
            _scrollOffset = 0;
            return;
        }

        // Two rows of chrome: header row + separator line.
        var visibleRows = Math.Max(0, Height - 2);
        if (visibleRows == 0)
        {
            _scrollOffset = 0;
            return;
        }

        var halfViewport = visibleRows / 2;
        var idealOffset = SelectedIndex - halfViewport;
        var maxOffset = Math.Max(0, DataSource.Count - visibleRows);
        _scrollOffset = Math.Clamp(idealOffset, 0, maxOffset);
    }

    /// <summary>
    /// Up/Down arrows move the selection; Enter activates the selected row.
    /// After every selection change the scroll offset is recalculated so the
    /// selected row stays centered in the viewport.
    /// </summary>
    public override void HandleInput(InputEvent input)
    {
        if (DataSource.Count == 0) return;

        if (input is KeyEvent { Key: Key.Up })
        {
            if (SelectedIndex > 0)
            {
                SelectedIndex--;
                RecalculateScroll();
                SelectionChanged?.Invoke(DataSource[SelectedIndex]);
            }
        }
        else if (input is KeyEvent { Key: Key.Down })
        {
            if (SelectedIndex < DataSource.Count - 1)
            {
                SelectedIndex++;
                RecalculateScroll();
                SelectionChanged?.Invoke(DataSource[SelectedIndex]);
            }
        }
        else if (input is KeyEvent { Key: Key.Enter })
        {
            if (SelectedIndex >= 0 && SelectedIndex < DataSource.Count)
                ItemActivated?.Invoke(DataSource[SelectedIndex]);
        }
    }

    /// <summary>
    /// Renders the table: header row, separator, visible data rows with selection
    /// highlight, and a proportional scrollbar in the rightmost column.
    ///
    /// Layout of each row:
    ///   [col0 text][│][col1 text][│][col2 text][scrollbar]
    ///
    /// The header and separator use bold/highlight styling. The selected data row
    /// is drawn with swapped Fg/Bg colors so it stands out as a highlight bar.
    /// </summary>
    public override void Draw(ICanvas canvas)
    {
        if (Columns.Count == 0 || Width <= 0 || Height <= 0) return;

        var headerStyle = new Style(Color.BrightWhite, Color.Black, Modifier.Bold);
        var separatorStyle = Style.Default;
        var normalStyle = Style.Default;
        var selectedStyle = new Style(Color.Black, Color.White);

        // Row 0: column headers.
        DrawRow(canvas, 0, i => Columns[i].Header, headerStyle, headerStyle);

        // Row 1: horizontal separator line with ┼ at column boundaries.
        if (Height > 1)
            DrawSeparator(canvas, 1, separatorStyle);

        // Rows 2+: visible data rows.
        var visibleRows = Math.Max(0, Height - 2);
        for (var i = 0; i < visibleRows; i++)
        {
            var dataIndex = _scrollOffset + i;
            if (dataIndex >= DataSource.Count) break;

            var item = DataSource[dataIndex];
            var isSelected = dataIndex == SelectedIndex;
            var rowStyle = isSelected ? selectedStyle : normalStyle;

            DrawRow(canvas, i + 2, colIdx => Columns[colIdx].ValueSelector(item), rowStyle, rowStyle);
        }

        // Scrollbar in the rightmost column.
        DrawScrollbar(canvas);
    }

    /// <summary>
    /// Draws a single row (header or data) at the given screen row, using a function
    /// that maps column index to display text. Columns are separated by │ characters.
    /// Text is truncated to the resolved column width.
    /// </summary>
    private void DrawRow(ICanvas canvas, int row, Func<int, string> textSelector,
        Style cellStyle, Style separatorStyle)
    {
        var x = 0;
        for (var col = 0; col < Columns.Count; col++)
        {
            // Draw separator between columns (not before the first one).
            if (col > 0)
            {
                canvas.SetCell(x, row, '│', separatorStyle);
                x++;
            }

            var colWidth = col < _resolvedWidths.Length ? _resolvedWidths[col] : 0;
            var text = textSelector(col);

            // Truncate or pad to exactly colWidth characters.
            if (text.Length > colWidth)
                text = text[..colWidth];

            canvas.DrawText(x, row, text, cellStyle);

            // Fill remaining cells in this column with the cell style so the
            // selection highlight extends across the full column width.
            for (var pad = text.Length; pad < colWidth; pad++)
                canvas.SetCell(x + pad, row, ' ', cellStyle);

            x += colWidth;
        }
    }

    /// <summary>
    /// Draws a horizontal separator line with ─ across columns and ┼ at column
    /// boundaries where the vertical separators intersect.
    /// </summary>
    private void DrawSeparator(ICanvas canvas, int row, Style style)
    {
        var x = 0;
        for (var col = 0; col < Columns.Count; col++)
        {
            if (col > 0)
            {
                canvas.SetCell(x, row, '┼', style);
                x++;
            }

            var colWidth = col < _resolvedWidths.Length ? _resolvedWidths[col] : 0;
            canvas.DrawHLine(x, row, colWidth, '─', style);
            x += colWidth;
        }
    }

    /// <summary>
    /// Draws a proportional scrollbar in the rightmost column, reusing the same
    /// visual pattern as ScrollView: █ for the thumb, │ for the track. When all
    /// rows fit in the viewport, only the track is drawn (no thumb).
    /// </summary>
    private void DrawScrollbar(ICanvas canvas)
    {
        var scrollbarX = Width - 1;
        var trackHeight = Height;

        if (DataSource.Count <= Height - 2)
        {
            // All data fits — track only, no thumb.
            canvas.DrawVLine(scrollbarX, 0, trackHeight, '│', Style.Default);
            return;
        }

        var visibleRows = Math.Max(1, Height - 2);
        var totalRows = DataSource.Count;
        var maxOffset = Math.Max(1, totalRows - visibleRows);

        // Thumb size proportional to how much data is visible.
        var thumbSize = Math.Max(1, trackHeight * visibleRows / totalRows);

        // Thumb position proportional to scroll offset within the scrollable range.
        var thumbTop = (int)((float)_scrollOffset / maxOffset * (trackHeight - thumbSize));

        for (var row = 0; row < trackHeight; row++)
        {
            var ch = (row >= thumbTop && row < thumbTop + thumbSize) ? '█' : '│';
            canvas.SetCell(scrollbarX, row, ch, Style.Default);
        }
    }
}
