using System.Collections.ObjectModel;
using Ur.Console;
using Ur.Drawing;
using Ur.Widgets;
using Xunit;

namespace Ur.Tests.Widgets;

public class TableTests
{
    // Simple row type for testing — mirrors the ModelOption pattern in the demo.
    private record Row(string Name, string Value);

    // Builds a Table<Row> with two auto-width columns and the given number of rows.
    // Returns the table and its data source so tests can mutate items independently.
    private static (Table<Row> table, ObservableCollection<Row> data) MakeTable(
        int rowCount,
        int? col1Width = null,
        int? col2Width = null)
    {
        var data = new ObservableCollection<Row>();
        for (var i = 0; i < rowCount; i++)
            data.Add(new Row($"name-{i}", $"val-{i}"));

        var columns = new List<TableColumn<Row>>
        {
            new("Name", r => r.Name, col1Width),
            new("Value", r => r.Value, col2Width),
        };

        var table = new Table<Row>(data, columns);
        return (table, data);
    }

    // Runs Layout and Render on a table so we can inspect drawn output.
    private static Screen DrawTable(Table<Row> table, int width = 40, int height = 12)
    {
        table.Layout(width, height);
        return Renderer.Render(table);
    }

    // Reads a row of the screen as a trimmed string for easy assertions.
    private static string RowText(Screen screen, int row)
    {
        var chars = new char[screen.Width];
        for (ushort col = 0; col < screen.Width; col++)
            chars[col] = screen.Get(col, (ushort)row).Rune;
        return new string(chars).TrimEnd();
    }

    // -------------------------------------------------------------------------
    // Constructor and defaults
    // -------------------------------------------------------------------------

    [Fact]
    public void Constructor_WithData_SelectsFirstRow()
    {
        var (table, _) = MakeTable(5);
        Assert.Equal(0, table.SelectedIndex);
    }

    [Fact]
    public void Constructor_WithEmptyData_SelectsNone()
    {
        var (table, _) = MakeTable(0);
        Assert.Equal(-1, table.SelectedIndex);
    }

    [Fact]
    public void Constructor_SetsGrowSizing()
    {
        var (table, _) = MakeTable(1);
        Assert.Equal(SizingMode.Grow, table.HorizontalSizing);
        Assert.Equal(SizingMode.Grow, table.VerticalSizing);
    }

    [Fact]
    public void Constructor_ThrowsOnNullDataSource()
    {
        Assert.Throws<ArgumentNullException>(() =>
            new Table<Row>(null!, []));
    }

    [Fact]
    public void Constructor_ThrowsOnNullColumns()
    {
        Assert.Throws<ArgumentNullException>(() =>
            new Table<Row>([], null!));
    }

    // -------------------------------------------------------------------------
    // Selection movement and boundary clamping
    // -------------------------------------------------------------------------

    [Fact]
    public void HandleInput_Down_IncrementsSelection()
    {
        var (table, _) = MakeTable(5);
        table.HandleInput(new KeyEvent(Key.Down));
        Assert.Equal(1, table.SelectedIndex);
    }

    [Fact]
    public void HandleInput_Up_DecrementsSelection()
    {
        var (table, _) = MakeTable(5);
        table.SelectedIndex = 3;
        table.HandleInput(new KeyEvent(Key.Up));
        Assert.Equal(2, table.SelectedIndex);
    }

    [Fact]
    public void HandleInput_Up_ClampsAtZero()
    {
        var (table, _) = MakeTable(5);
        // Already at 0 — pressing Up should stay at 0.
        table.HandleInput(new KeyEvent(Key.Up));
        Assert.Equal(0, table.SelectedIndex);
    }

    [Fact]
    public void HandleInput_Down_ClampsAtLastRow()
    {
        var (table, _) = MakeTable(3);
        // Press Down well past the end — should clamp to last index.
        for (var i = 0; i < 10; i++)
            table.HandleInput(new KeyEvent(Key.Down));
        Assert.Equal(2, table.SelectedIndex);
    }

    [Fact]
    public void HandleInput_Up_AtZero_DoesNotFireSelectionChanged()
    {
        var (table, _) = MakeTable(3);
        var fired = false;
        table.SelectionChanged += _ => fired = true;

        table.HandleInput(new KeyEvent(Key.Up));
        Assert.False(fired);
    }

    [Fact]
    public void HandleInput_Down_AtLastRow_DoesNotFireSelectionChanged()
    {
        var (table, _) = MakeTable(3);
        table.SelectedIndex = 2;
        var fired = false;
        table.SelectionChanged += _ => fired = true;

        table.HandleInput(new KeyEvent(Key.Down));
        Assert.False(fired);
    }

    [Fact]
    public void HandleInput_Down_FiresSelectionChanged()
    {
        var (table, _) = MakeTable(5);
        Row? received = null;
        table.SelectionChanged += item => received = item;

        table.HandleInput(new KeyEvent(Key.Down));
        Assert.NotNull(received);
        Assert.Equal("name-1", received.Name);
    }

    [Fact]
    public void HandleInput_Enter_FiresItemActivated()
    {
        var (table, _) = MakeTable(5);
        Row? activated = null;
        table.ItemActivated += item => activated = item;

        table.HandleInput(new KeyEvent(Key.Enter));
        Assert.NotNull(activated);
        Assert.Equal("name-0", activated.Name);
    }

    [Fact]
    public void HandleInput_OnEmptyTable_IsNoOp()
    {
        var (table, _) = MakeTable(0);
        var selectionFired = false;
        var activatedFired = false;
        table.SelectionChanged += _ => selectionFired = true;
        table.ItemActivated += _ => activatedFired = true;

        // None of these should throw or fire events.
        var ex = Record.Exception(() =>
        {
            table.HandleInput(new KeyEvent(Key.Up));
            table.HandleInput(new KeyEvent(Key.Down));
            table.HandleInput(new KeyEvent(Key.Enter));
        });

        Assert.Null(ex);
        Assert.False(selectionFired);
        Assert.False(activatedFired);
    }

    // -------------------------------------------------------------------------
    // Scroll-to-center algorithm
    // -------------------------------------------------------------------------

    [Fact]
    public void Scroll_KeepsSelectionVisible_AtTop()
    {
        // With 20 rows in a 7-row viewport (5 data rows visible after header+sep),
        // selecting row 0 should show it in the visible area.
        var (table, _) = MakeTable(20);
        var screen = DrawTable(table, 40, 7);

        // Row 0 of data is at screen row 2 (after header and separator).
        Assert.Contains("name-0", RowText(screen, 2));
    }

    [Fact]
    public void Scroll_KeepsSelectionVisible_AtBottom()
    {
        var (table, _) = MakeTable(20);
        // Move selection to the last row.
        for (var i = 0; i < 19; i++)
            table.HandleInput(new KeyEvent(Key.Down));

        var screen = DrawTable(table, 40, 7);

        // The last item should be visible somewhere in the data area.
        var allVisible = string.Join(" ", Enumerable.Range(2, 5).Select(r => RowText(screen, r)));
        Assert.Contains("name-19", allVisible);
    }

    [Fact]
    public void Scroll_CentersSelection_InMiddle()
    {
        // 50 rows, 12-row viewport = 10 visible data rows. Select row 25 (middle).
        // The selected row should appear near the center of the viewport.
        var (table, _) = MakeTable(50);
        for (var i = 0; i < 25; i++)
            table.HandleInput(new KeyEvent(Key.Down));

        var screen = DrawTable(table, 40, 12);

        // Find which screen row contains the selected item.
        var selectedRow = -1;
        for (var r = 2; r < 12; r++)
        {
            if (RowText(screen, r).Contains("name-25"))
            {
                selectedRow = r;
                break;
            }
        }

        Assert.NotEqual(-1, selectedRow);
        // Should be within the middle third of the data area (rows 2-11).
        // Center would be around row 6-7; allow ±2 for integer division.
        Assert.InRange(selectedRow, 4, 9);
    }

    [Fact]
    public void Scroll_FewerRowsThanViewport_NoScrollNeeded()
    {
        // Only 3 data rows in a 12-row viewport — all should be visible, no scrollbar thumb.
        var (table, _) = MakeTable(3);
        var screen = DrawTable(table, 40, 12);

        Assert.Contains("name-0", RowText(screen, 2));
        Assert.Contains("name-1", RowText(screen, 3));
        Assert.Contains("name-2", RowText(screen, 4));
    }

    // -------------------------------------------------------------------------
    // ObservableCollection change handling
    // -------------------------------------------------------------------------

    [Fact]
    public void CollectionAdd_WhenEmpty_SelectsFirstItem()
    {
        var (table, data) = MakeTable(0);
        Assert.Equal(-1, table.SelectedIndex);

        data.Add(new Row("new", "item"));
        Assert.Equal(0, table.SelectedIndex);
    }

    [Fact]
    public void CollectionRemove_BeforeSelection_ShiftsSelectionUp()
    {
        var (table, data) = MakeTable(5);
        table.SelectedIndex = 3;

        // Remove item at index 1 (before selection) — selection should shift to 2.
        data.RemoveAt(1);
        Assert.Equal(2, table.SelectedIndex);
    }

    [Fact]
    public void CollectionRemove_AtSelection_ShiftsSelectionUp()
    {
        var (table, data) = MakeTable(5);
        table.SelectedIndex = 2;

        // Remove the selected item — selection should shift to previous item.
        data.RemoveAt(2);
        Assert.Equal(1, table.SelectedIndex);
    }

    [Fact]
    public void CollectionRemove_AfterSelection_LeavesSelectionUnchanged()
    {
        var (table, data) = MakeTable(5);
        table.SelectedIndex = 1;

        // Remove item after the selection — index should not change.
        data.RemoveAt(3);
        Assert.Equal(1, table.SelectedIndex);
    }

    [Fact]
    public void CollectionRemove_LastItem_SelectionBecomesNegative()
    {
        var (table, data) = MakeTable(1);
        Assert.Equal(0, table.SelectedIndex);

        data.RemoveAt(0);
        Assert.Equal(-1, table.SelectedIndex);
    }

    [Fact]
    public void CollectionReset_ResetsSelectionAndScroll()
    {
        var (table, data) = MakeTable(10);
        table.SelectedIndex = 5;

        data.Clear();
        Assert.Equal(-1, table.SelectedIndex);
    }

    // -------------------------------------------------------------------------
    // Column width resolution
    // -------------------------------------------------------------------------

    [Fact]
    public void Layout_FixedWidthColumns_HonouredExactly()
    {
        // Two columns with fixed widths of 10 and 8. Table width = 30.
        // Chrome: 1 separator + 1 scrollbar = 2. Remaining = 30 - 10 - 8 - 2 = 10 unused.
        // But both columns have explicit widths, so no auto columns. Each should be exact.
        var (table, _) = MakeTable(3, col1Width: 10, col2Width: 8);
        var screen = DrawTable(table, 30, 5);

        // Header text should appear and the separator '│' should be at column 10.
        var headerRow = RowText(screen, 0);
        Assert.Contains("Name", headerRow);
        Assert.Contains("Value", headerRow);
    }

    [Fact]
    public void Layout_AutoWidthColumns_ShareRemainingSpace()
    {
        // Two auto columns, table width = 41. Chrome = 1 sep + 1 scrollbar = 2.
        // Remaining = 39. Each auto column gets 39/2 = 19 chars.
        var (table, _) = MakeTable(3);
        var screen = DrawTable(table, 41, 5);

        // Both column headers should be visible without truncation.
        var headerRow = RowText(screen, 0);
        Assert.Contains("Name", headerRow);
        Assert.Contains("Value", headerRow);
    }

    // -------------------------------------------------------------------------
    // Draw correctness
    // -------------------------------------------------------------------------

    [Fact]
    public void Draw_RendersHeaderAndSeparator()
    {
        var (table, _) = MakeTable(3);
        var screen = DrawTable(table, 40, 7);

        var headerRow = RowText(screen, 0);
        var separatorRow = RowText(screen, 1);

        Assert.Contains("Name", headerRow);
        Assert.Contains("│", headerRow); // column separator
        Assert.Contains("─", separatorRow); // horizontal rule
        Assert.Contains("┼", separatorRow); // intersection
    }

    [Fact]
    public void Draw_SelectedRow_HasInvertedStyle()
    {
        var (table, _) = MakeTable(5);
        table.Layout(40, 10);
        var screen = Renderer.Render(table);

        // Row 0 is selected (index 0) → drawn at screen row 2.
        // The selected row should have black foreground and white background (inverted).
        var cell = screen.Get(0, 2);
        Assert.Equal(Color.Black, cell.Style.Fg);
        Assert.Equal(Color.White, cell.Style.Bg);
    }

    [Fact]
    public void Draw_UnselectedRow_HasDefaultStyle()
    {
        var (table, _) = MakeTable(5);
        table.Layout(40, 10);
        var screen = Renderer.Render(table);

        // Row 1 (index 1, not selected) → drawn at screen row 3.
        var cell = screen.Get(0, 3);
        Assert.Equal(Color.White, cell.Style.Fg);
        Assert.Equal(Color.Black, cell.Style.Bg);
    }

    [Fact]
    public void Draw_EmptyTable_DoesNotThrow()
    {
        var (table, _) = MakeTable(0);
        var ex = Record.Exception(() => DrawTable(table, 40, 10));
        Assert.Null(ex);
    }

    // -------------------------------------------------------------------------
    // Scrollbar
    // -------------------------------------------------------------------------

    [Fact]
    public void Scrollbar_TrackOnly_WhenAllRowsFit()
    {
        var (table, _) = MakeTable(3);
        var screen = DrawTable(table, 40, 10);

        // All 3 data rows fit in 8 visible rows — scrollbar should be all track.
        var scrollbarX = (ushort)(table.Width - 1);
        for (ushort row = 0; row < table.Height; row++)
            Assert.Equal('│', screen.Get(scrollbarX, row).Rune);
    }

    [Fact]
    public void Scrollbar_HasThumb_WhenContentOverflows()
    {
        var (table, _) = MakeTable(50);
        var screen = DrawTable(table, 40, 10);

        var scrollbarX = (ushort)(table.Width - 1);
        var hasThumb = Enumerable.Range(0, table.Height)
            .Any(row => screen.Get(scrollbarX, (ushort)row).Rune == '█');
        Assert.True(hasThumb);
    }

    // -------------------------------------------------------------------------
    // Single-row table edge case
    // -------------------------------------------------------------------------

    [Fact]
    public void SingleRow_SelectionStaysAtZero()
    {
        var (table, _) = MakeTable(1);

        table.HandleInput(new KeyEvent(Key.Up));
        Assert.Equal(0, table.SelectedIndex);

        table.HandleInput(new KeyEvent(Key.Down));
        Assert.Equal(0, table.SelectedIndex);
    }
}
