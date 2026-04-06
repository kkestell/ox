using Ur.Tui.Rendering;

namespace Ur.Tests;

/// <summary>
/// Tests for the cell-based TUI rendering layer (Color, Cell, CellRow, ScreenBuffer,
/// TextRenderable, ToolRenderable, SubagentRenderable, EventList).
///
/// These tests operate entirely on the data model — no ANSI codes, no terminal I/O.
/// The cell types are pure value types or simple classes; their behavior can be
/// asserted by inspecting CellRow.Cells directly.
/// </summary>

// ---------------------------------------------------------------------------
// ScreenBuffer
// ---------------------------------------------------------------------------

public sealed class ScreenBufferTests
{
    [Fact]
    public void WriteRow_FillsBufferWithCells()
    {
        var buffer = new ScreenBuffer(5, 1);
        var row    = CellRow.FromText("hello", Color.Default, Color.Default);
        buffer.WriteRow(0, row);

        Assert.Equal('h', buffer[0, 0].Rune);
        Assert.Equal('e', buffer[0, 1].Rune);
        Assert.Equal('l', buffer[0, 2].Rune);
        Assert.Equal('l', buffer[0, 3].Rune);
        Assert.Equal('o', buffer[0, 4].Rune);
    }

    [Fact]
    public void WriteRow_TruncatesRowsWiderThanBuffer()
    {
        // A 10-character row into a 5-wide buffer — only first 5 chars should appear.
        var buffer = new ScreenBuffer(5, 1);
        var row    = CellRow.FromText("hello world", Color.Default, Color.Default);
        buffer.WriteRow(0, row);

        Assert.Equal('h', buffer[0, 0].Rune);
        Assert.Equal('o', buffer[0, 4].Rune);
        // Width is 5, so col 5 doesn't exist — just verify no exception was thrown.
    }

    [Fact]
    public void WriteRow_PadsRowsShorterThanBuffer()
    {
        // A 3-character row into a 5-wide buffer — remaining cells should be Cell.Empty.
        var buffer = new ScreenBuffer(5, 1);
        var row    = CellRow.FromText("hi!", Color.Red, Color.Default);
        buffer.WriteRow(0, row);

        Assert.Equal('!', buffer[0, 2].Rune);
        // Columns 3 and 4 must be padded with empty cells.
        Assert.Equal(Cell.Empty, buffer[0, 3]);
        Assert.Equal(Cell.Empty, buffer[0, 4]);
    }

    [Fact]
    public void WriteRow_OutOfBoundsRowIsIgnored()
    {
        var buffer = new ScreenBuffer(5, 2);
        var row    = CellRow.FromText("oops", Color.Default, Color.Default);

        // Neither of these should throw.
        buffer.WriteRow(-1, row);
        buffer.WriteRow(2,  row);

        // Buffer remains untouched.
        Assert.Equal(Cell.Empty, buffer[0, 0]);
        Assert.Equal(Cell.Empty, buffer[1, 0]);
    }

    [Fact]
    public void Clear_ResetsAllCellsToEmpty()
    {
        var buffer = new ScreenBuffer(3, 2);
        buffer.WriteRow(0, CellRow.FromText("abc", Color.Red, Color.Blue));
        buffer.Clear();

        for (var r = 0; r < 2; r++)
        for (var c = 0; c < 3; c++)
            Assert.Equal(Cell.Empty, buffer[r, c]);
    }

    [Fact]
    public void Constructor_ClampsDimensionsToMinimumOne()
    {
        var buffer = new ScreenBuffer(0, -5);
        Assert.Equal(1, buffer.Width);
        Assert.Equal(1, buffer.Height);
    }
}

// ---------------------------------------------------------------------------
// TextRenderable — word-wrap
// ---------------------------------------------------------------------------

public sealed class TextRenderableTests
{
    // Helper: extract the text from a CellRow as a plain string.
    private static string RowText(CellRow row) => new(row.Cells.Select(c => c.Rune).ToArray());

    [Fact]
    public void Render_EmptyText_ReturnsSingleEmptyRow()
    {
        var r = new TextRenderable();
        // Nothing set — text is empty.
        var rows = r.Render(80);
        Assert.Single(rows);
        Assert.Empty(rows[0].Cells);
    }

    [Fact]
    public void Render_ShortText_FitsOnOneLine()
    {
        var r = new TextRenderable();
        r.SetText("hello");
        var rows = r.Render(80);
        Assert.Single(rows);
        Assert.Equal("hello", RowText(rows[0]));
    }

    [Fact]
    public void Render_TextExactlyAtWidth_FitsOnOneLine()
    {
        var r = new TextRenderable();
        r.SetText("12345");
        var rows = r.Render(5);
        Assert.Single(rows);
        Assert.Equal("12345", RowText(rows[0]));
    }

    [Fact]
    public void Render_WordWrap_BreaksAtSpaceBoundary()
    {
        // "hello world" at width=5: "world" doesn't fit on the first line.
        // Character at index 5 is ' ', so we split there.
        var r = new TextRenderable();
        r.SetText("hello world");
        var rows = r.Render(5);
        Assert.Equal(2, rows.Count);
        Assert.Equal("hello", RowText(rows[0]));
        Assert.Equal("world", RowText(rows[1]));
    }

    [Fact]
    public void Render_WordWrap_BreaksAtLastSpaceWithinWidth()
    {
        // "hi there" at width=6: last space within [0..6] is at index 2.
        // Expected: "hi" / "there"
        var r = new TextRenderable();
        r.SetText("hi there");
        var rows = r.Render(6);
        Assert.Equal(2, rows.Count);
        Assert.Equal("hi", RowText(rows[0]));
        Assert.Equal("there", RowText(rows[1]));
    }

    [Fact]
    public void Render_WordWrap_HardBreakWhenNoSpaceFits()
    {
        // "abcdefgh" at width=4: no spaces — hard break every 4 chars.
        var r = new TextRenderable();
        r.SetText("abcdefgh");
        var rows = r.Render(4);
        Assert.Equal(2, rows.Count);
        Assert.Equal("abcd", RowText(rows[0]));
        Assert.Equal("efgh", RowText(rows[1]));
    }

    [Fact]
    public void Render_NewlineCausesHardBreak()
    {
        var r = new TextRenderable();
        r.SetText("line one\nline two");
        var rows = r.Render(80);
        Assert.Equal(2, rows.Count);
        Assert.Equal("line one", RowText(rows[0]));
        Assert.Equal("line two", RowText(rows[1]));
    }

    [Fact]
    public void Render_BlankLineInSource_EmitsEmptyRow()
    {
        var r = new TextRenderable();
        r.SetText("a\n\nb");
        var rows = r.Render(80);
        Assert.Equal(3, rows.Count);
        Assert.Equal("a", RowText(rows[0]));
        Assert.Empty(rows[1].Cells);
        Assert.Equal("b", RowText(rows[2]));
    }

    [Fact]
    public void Render_StyleAppliedToAllCells()
    {
        var r = new TextRenderable(foreground: Color.Red, background: Color.Blue, style: CellStyle.Dim);
        r.SetText("hi");
        var rows = r.Render(80);
        Assert.All(rows[0].Cells, cell =>
        {
            Assert.Equal(Color.Red,    cell.Foreground);
            Assert.Equal(Color.Blue,   cell.Background);
            Assert.Equal(CellStyle.Dim, cell.Style);
        });
    }

    [Fact]
    public void Render_ChangedEventFiredOnAppend()
    {
        var r = new TextRenderable();
        var fired = false;
        r.Changed += () => fired = true;
        r.Append("hello");
        Assert.True(fired);
    }

    [Fact]
    public void Render_CacheHitDoesNotRecompute()
    {
        // Render twice with the same width and text — the result should be the
        // same reference (cached), proving we don't re-wrap unnecessarily.
        var r = new TextRenderable();
        r.SetText("hello");
        var first  = r.Render(80);
        var second = r.Render(80);
        Assert.Same(first, second);
    }
}

// ---------------------------------------------------------------------------
// SubagentRenderable
// ---------------------------------------------------------------------------

public sealed class SubagentRenderableTests
{
    [Fact]
    public void Render_WithNoChildren_HasOnlyHeader()
    {
        var r    = new SubagentRenderable("abc123");
        var rows = r.Render(40);
        Assert.Single(rows);
        var text = new string(rows[0].Cells.Select(c => c.Rune).ToArray());
        Assert.Contains("abc123", text);
    }

    [Fact]
    public void Render_WithCompletedFlag_HasHeaderAndFooter()
    {
        var r = new SubagentRenderable("abc123");
        r.SetCompleted();
        var rows = r.Render(40);
        Assert.Equal(2, rows.Count);
        var footerText = new string(rows[1].Cells.Select(c => c.Rune).ToArray());
        Assert.Contains("subagent complete", footerText);
    }

    [Fact]
    public void Render_ChildRowsAreIndented()
    {
        // A child that emits one row of "hello" should be indented by 2 space cells.
        var r     = new SubagentRenderable("xyz");
        var child = new TextRenderable();
        child.SetText("hello");
        r.AddChild(child);

        var rows = r.Render(40);
        // rows[0] = header, rows[1] = indented child
        Assert.Equal(2, rows.Count);
        Assert.Equal(' ', rows[1].Cells[0].Rune);
        Assert.Equal(' ', rows[1].Cells[1].Rune);
        Assert.Equal('h', rows[1].Cells[2].Rune);
    }

    [Fact]
    public void SetCompleted_IsIdempotent()
    {
        // Calling SetCompleted twice must not add a second footer.
        var r = new SubagentRenderable("abc");
        r.SetCompleted();
        r.SetCompleted();
        var rows = r.Render(40);
        Assert.Equal(2, rows.Count); // header + one footer
    }

    [Fact]
    public void AddChild_FiresChangedEvent()
    {
        var r     = new SubagentRenderable("abc");
        var fired = false;
        r.Changed += () => fired = true;

        var child = new TextRenderable();
        r.AddChild(child);

        Assert.True(fired);
    }

    [Fact]
    public void ChildMutation_BubblesChangedEventUp()
    {
        // When a child's content changes, the SubagentRenderable's Changed should fire.
        var r     = new SubagentRenderable("abc");
        var child = new TextRenderable();
        r.AddChild(child);

        var fired = false;
        r.Changed += () => fired = true;
        child.Append("more text");

        Assert.True(fired);
    }
}

// ---------------------------------------------------------------------------
// EventList — bubble chrome
// ---------------------------------------------------------------------------

public sealed class EventListTests
{
    // Helpers to navigate bubble-wrapped rows.
    private static char RuneAt(CellRow row, int col) => row.Cells[col].Rune;
    private static Color BgAt(CellRow row, int col) => row.Cells[col].Background;

    [Fact]
    public void Render_EmptyList_ReturnsEmpty()
    {
        var list = new EventList();
        Assert.Empty(list.Render(40));
    }

    [Fact]
    public void Render_OneChild_HasPaddingRowsAroundContent()
    {
        // Structure: top-pad, content-rows, bottom-pad (no blank separator for first bubble).
        var list  = new EventList();
        var child = new TextRenderable();
        child.SetText("hello");
        list.Add(child, BubbleStyle.Assistant);

        var rows = list.Render(20);
        // top-pad + 1 content row + bottom-pad = 3 rows
        Assert.Equal(3, rows.Count);
    }

    [Fact]
    public void Render_TwoChildren_HasBlankRowBetweenBubbles()
    {
        var list = new EventList();
        var c1   = new TextRenderable();
        c1.SetText("one");
        var c2 = new TextRenderable();
        c2.SetText("two");
        list.Add(c1, BubbleStyle.System);
        list.Add(c2, BubbleStyle.System);

        var rows = list.Render(20);
        // bubble1: top+content+bottom = 3, blank separator = 1, bubble2: 3 → total = 7
        Assert.Equal(7, rows.Count);
        // The blank separator is rows[3] — it should be an empty CellRow.
        Assert.Empty(rows[3].Cells);
    }

    [Fact]
    public void Render_ContentRow_HasBarGlyphAtColumnOne()
    {
        var list  = new EventList();
        var child = new TextRenderable();
        child.SetText("x");
        list.Add(child, BubbleStyle.User);

        var rows = list.Render(20);
        // rows[0] = top padding, rows[1] = content row
        var contentRow = rows[1];
        // col 0 = left margin (space), col 1 = bar glyph '▎'
        Assert.Equal(' ',  RuneAt(contentRow, 0));
        Assert.Equal('▎', RuneAt(contentRow, 1));
    }

    [Fact]
    public void Render_ContentRow_DefaultChildBackgroundReplacedWithBubbleBg()
    {
        // TextRenderable uses Color.Default background. EventList must override it
        // with the bubble background so the fill is seamless.
        var list  = new EventList();
        var child = new TextRenderable(); // default fg, default bg
        child.SetText("x");
        list.Add(child, BubbleStyle.User);

        var rows = list.Render(20);
        var contentRow = rows[1];
        // The content cells start at col 3 (0=margin, 1=bar, 2=inner-pad, 3=first char).
        // Their background should be the User bubble bg (Color.FromIndex(236)), not Default.
        var contentCellBg = BgAt(contentRow, 3);
        Assert.NotEqual(Color.Default, contentCellBg);
        Assert.Equal(Color.FromIndex(236), contentCellBg);
    }

    [Fact]
    public void Add_FiresChangedEvent()
    {
        var list  = new EventList();
        var fired = false;
        list.Changed += () => fired = true;

        var child = new TextRenderable();
        list.Add(child);

        Assert.True(fired);
    }

    [Fact]
    public void ChildMutation_BubblesChangedEventUp()
    {
        var list  = new EventList();
        var child = new TextRenderable();
        list.Add(child);

        var fired = false;
        list.Changed += () => fired = true;
        child.Append("new content");

        Assert.True(fired);
    }
}
