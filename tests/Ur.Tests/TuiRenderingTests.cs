using Ur.AgentLoop;
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
// ToolRenderable
// ---------------------------------------------------------------------------

public sealed class ToolRenderableTests
{
    private static string RowText(CellRow row) =>
        new(row.Cells.Select(c => c.Rune).ToArray());

    /// <summary>Helper to create a ToolCallStarted with a simple signature.</summary>
    private static ToolCallStarted MakeStarted(string toolName = "read_file", string? arg = "foo.txt") =>
        new()
        {
            CallId = "call_1",
            ToolName = toolName,
            Arguments = arg is not null
                ? new Dictionary<string, object?> { ["path"] = arg }
                : new Dictionary<string, object?>()
        };

    [Fact]
    public void Render_Started_ShowsSignatureOnly()
    {
        // Initial state: just the call signature, no suffix, yellow circle.
        var tool = new ToolRenderable(MakeStarted());
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("read_file", text);
        Assert.DoesNotContain("[awaiting approval]", text);
        Assert.Equal(Color.Yellow, tool.CircleColor);
    }

    [Fact]
    public void Render_Completed_DoesNotShowArrowOk()
    {
        // Circle color (green) conveys success — no text suffix needed.
        var tool = new ToolRenderable(MakeStarted());
        tool.SetCompleted(isError: false);
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("read_file", text);
        Assert.DoesNotContain("→ ok", text);
        Assert.Equal(Color.Green, tool.CircleColor);
    }

    [Fact]
    public void Render_CompletedError_DoesNotShowArrowError()
    {
        // Circle color (red) conveys failure — no text suffix needed.
        var tool = new ToolRenderable(MakeStarted());
        tool.SetCompleted(isError: true);
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("read_file", text);
        Assert.DoesNotContain("→ error", text);
        Assert.Equal(Color.Red, tool.CircleColor);
    }

    [Fact]
    public void Render_AwaitingApproval_StillShowsApprovalText()
    {
        // Yellow circle alone doesn't distinguish "awaiting" from "running",
        // so the text label is retained.
        var tool = new ToolRenderable(MakeStarted());
        tool.SetAwaitingApproval();
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("read_file", text);
        Assert.Contains("[awaiting approval]", text);
    }
}

// ---------------------------------------------------------------------------
// SubagentRenderable — tree-based subagent rendering
// ---------------------------------------------------------------------------

public sealed class SubagentRenderableTests
{
    private static string RowText(CellRow row) =>
        new(row.Cells.Select(c => c.Rune).ToArray());

    [Fact]
    public void Render_NoChildren_ReturnsJustSignatureRow()
    {
        // With no inner events, only the tool call signature is rendered.
        var r    = new SubagentRenderable("abc123", "run_subagent(prompt: \"hello\")");
        var rows = r.Render(40);
        Assert.Single(rows);
        Assert.Contains("run_subagent", RowText(rows[0]));
    }

    [Fact]
    public void Render_WithChildren_SignaturePlusInnerTreeRows()
    {
        // The signature row is followed by inner EventList tree output.
        // Inner children are orphan Circles (no User root in the inner list),
        // so they get tree connectors (├─/└─) from the inner EventList.
        var r     = new SubagentRenderable("abc", "run_subagent(prompt: \"hi\")");
        var child = new TextRenderable();
        child.SetText("hello");
        r.AddChild(child, BubbleStyle.Circle, () => Color.Green);

        var rows = r.Render(40);
        Assert.Equal(2, rows.Count);
        Assert.Contains("run_subagent", RowText(rows[0]));
        // The inner orphan child gets └─ ● chrome — circle at col 3.
        Assert.Equal('●', rows[1].Cells[3].Rune);
    }

    [Fact]
    public void CircleColor_YellowWhileRunning_GreenOnCompletion()
    {
        var r = new SubagentRenderable("abc", "run_subagent(prompt: \"hi\")");
        Assert.Equal(Color.Yellow, r.CircleColor);
        r.SetCompleted();
        Assert.Equal(Color.Green, r.CircleColor);
    }

    [Fact]
    public void SetCompleted_IsIdempotent()
    {
        var r = new SubagentRenderable("abc", "run_subagent(prompt: \"hi\")");
        r.SetCompleted();
        r.SetCompleted(); // Second call should not throw or change state.
        Assert.Equal(Color.Green, r.CircleColor);
    }

    [Fact]
    public void Render_TailClips_WhenInnerRowsExceedMaxInnerRows()
    {
        // Each Circle child in the inner EventList produces 1 tree row.
        // Add enough children to exceed MaxInnerRows (20). The visible inner
        // row count (below the signature) must be capped, plus an ellipsis row.
        var r = new SubagentRenderable("clip-test", "run_subagent(prompt: \"big\")");
        for (var i = 0; i < 30; i++)
        {
            var child = new TextRenderable();
            child.SetText($"line {i}");
            r.AddChild(child, BubbleStyle.Circle, () => Color.White);
        }

        var rows = r.Render(80);
        // Row 0 = signature, row 1 = ellipsis (├─ ● ...), rows 2+ = 20 tail-clipped inner rows.
        var innerRowCount = rows.Count - 1; // subtract signature
        // 20 clipped rows + 1 ellipsis = 21 inner rows.
        Assert.Equal(21, innerRowCount);
        // Verify the ellipsis row is present.
        Assert.Contains("...", RowText(rows[1]));
    }

    [Fact]
    public void AddChild_FiresChangedEvent()
    {
        var r     = new SubagentRenderable("abc", "run_subagent(prompt: \"hi\")");
        var fired = false;
        r.Changed += () => fired = true;

        var child = new TextRenderable();
        r.AddChild(child, BubbleStyle.Circle);

        Assert.True(fired);
    }

    [Fact]
    public void ChildMutation_BubblesChangedEventUp()
    {
        // When a child's content changes, the SubagentRenderable's Changed should fire.
        var r     = new SubagentRenderable("abc", "run_subagent(prompt: \"hi\")");
        var child = new TextRenderable();
        r.AddChild(child, BubbleStyle.Circle);

        var fired = false;
        r.Changed += () => fired = true;
        child.Append("more text");

        Assert.True(fired);
    }
}

// ---------------------------------------------------------------------------
// EventList — tree rendering
// ---------------------------------------------------------------------------

public sealed class EventListTests
{
    // Helper to get a specific cell's rune.
    private static char RuneAt(CellRow row, int col) => row.Cells[col].Rune;

    [Fact]
    public void Render_EmptyList_ReturnsEmpty()
    {
        var list = new EventList();
        Assert.Empty(list.Render(40));
    }

    [Fact]
    public void Render_SingleUser_PromptGlyphOnFirstRow()
    {
        // A lone User item renders with ❯ prefix and no tree connectors below.
        var list  = new EventList();
        var child = new TextRenderable();
        child.SetText("hello");
        list.Add(child);

        var rows = list.Render(40);
        Assert.Single(rows);
        // Col 0: ❯, col 1: space, col 2+: "hello"
        Assert.Equal('❯', RuneAt(rows[0], 0));
        Assert.Equal(' ', RuneAt(rows[0], 1));
        Assert.Equal('h', RuneAt(rows[0], 2));
    }

    [Fact]
    public void Render_UserWithOneChild_LastChildBranch()
    {
        // User root + one Circle child: the child gets └─ ● (last child connector).
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var tool = new TextRenderable();
        tool.SetText("read_file");
        list.Add(tool, BubbleStyle.Circle, () => Color.Green);

        var rows = list.Render(40);
        Assert.Equal(2, rows.Count);

        // Row 0: user root
        Assert.Equal('❯', RuneAt(rows[0], 0));

        // Row 1: last (and only) child — └─ ● prefix
        Assert.Equal('└', RuneAt(rows[1], 0));
        Assert.Equal('─', RuneAt(rows[1], 1));
        Assert.Equal(' ', RuneAt(rows[1], 2));
        Assert.Equal('●', RuneAt(rows[1], 3));
        Assert.Equal(' ', RuneAt(rows[1], 4));
    }

    [Fact]
    public void Render_UserWithTwoChildren_BranchAndLastBranch()
    {
        // User root + two children: first gets ├─ ●, second gets └─ ●.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var c1 = new TextRenderable();
        c1.SetText("first");
        list.Add(c1, BubbleStyle.Circle, () => Color.Yellow);

        var c2 = new TextRenderable();
        c2.SetText("second");
        list.Add(c2, BubbleStyle.Circle, () => Color.Green);

        var rows = list.Render(40);
        Assert.Equal(3, rows.Count);

        // Row 1: non-last child — ├─ ●
        Assert.Equal('├', RuneAt(rows[1], 0));
        Assert.Equal('─', RuneAt(rows[1], 1));

        // Row 2: last child — └─ ●
        Assert.Equal('└', RuneAt(rows[2], 0));
        Assert.Equal('─', RuneAt(rows[2], 1));
    }

    [Fact]
    public void Render_UserContinuationWithChildren_VerticalBar()
    {
        // When a User message wraps and has children, continuation rows use │ prefix
        // to signal the vertical trunk continues to the children below.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("hello world"); // will wrap at narrow width
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("tool");
        list.Add(child, BubbleStyle.Circle, () => Color.White);

        // Width 8: root chrome = 2 → content width = 6.
        // "hello world" at width 6 → "hello" + "world" (wraps at space).
        var rows = list.Render(8);

        // Row 0: ❯ hello
        Assert.Equal('❯', RuneAt(rows[0], 0));
        // Row 1: │ world (continuation with vertical bar because children follow)
        Assert.Equal('│', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        // Row 2: └─ ● tool (the child)
        Assert.Equal('└', RuneAt(rows[2], 0));
    }

    [Fact]
    public void Render_UserContinuationWithoutChildren_NoVerticalBar()
    {
        // When a User message wraps but has no children, continuation rows use spaces
        // (no vertical bar) since there's nothing to connect to below.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("hello world");
        list.Add(user);

        // Width 8: content width = 6, "hello world" wraps.
        var rows = list.Render(8);
        Assert.Equal(2, rows.Count);

        // Row 1: continuation — space + space (no │), then content continues.
        Assert.Equal(' ', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        Assert.Equal('w', RuneAt(rows[1], 2)); // "world" content starts at col 2
    }

    [Fact]
    public void Render_ChildContinuation_VerticalBarForNonLast()
    {
        // When a non-last child's content wraps, continuation rows use │ + 4 spaces
        // to maintain the vertical trunk for siblings below.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var c1 = new TextRenderable();
        c1.SetText("abcdefghij"); // will wrap at narrow width
        list.Add(c1, BubbleStyle.Circle, () => Color.White);

        var c2 = new TextRenderable();
        c2.SetText("end");
        list.Add(c2, BubbleStyle.Circle, () => Color.White);

        // Width 10: child chrome = 5 → child content width = 5.
        // "abcdefghij" at width 5 → hard break: "abcde" + "fghij".
        var rows = list.Render(10);

        // Row 0: ❯ msg
        // Row 1: ├─ ● abcde (non-last child, first row)
        Assert.Equal('├', RuneAt(rows[1], 0));
        // Row 2: │    fghij (non-last child, continuation — │ + 4 spaces)
        Assert.Equal('│', RuneAt(rows[2], 0));
        Assert.Equal(' ', RuneAt(rows[2], 1));
        Assert.Equal(' ', RuneAt(rows[2], 2));
        Assert.Equal(' ', RuneAt(rows[2], 3));
        Assert.Equal(' ', RuneAt(rows[2], 4));
        // Row 3: └─ ● end (last child)
        Assert.Equal('└', RuneAt(rows[3], 0));
    }

    [Fact]
    public void Render_ChildContinuation_SpacesForLastChild()
    {
        // When the last child's content wraps, continuation rows use 5 spaces
        // (no vertical bar) since there are no more siblings below.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("abcdefghij"); // will wrap
        list.Add(child, BubbleStyle.Circle, () => Color.White);

        // Width 10: child content = 5. "abcdefghij" → "abcde" + "fghij".
        var rows = list.Render(10);

        // Row 1: └─ ● abcde (last child, first row)
        Assert.Equal('└', RuneAt(rows[1], 0));
        // Row 2: continuation — 5 spaces (no │)
        Assert.Equal(' ', RuneAt(rows[2], 0));
        Assert.Equal(' ', RuneAt(rows[2], 1));
        Assert.Equal(' ', RuneAt(rows[2], 2));
        Assert.Equal(' ', RuneAt(rows[2], 3));
        Assert.Equal(' ', RuneAt(rows[2], 4));
    }

    [Fact]
    public void Render_TwoGroups_NoBlankRowBetweenThem()
    {
        // Two User groups rendered consecutively with no blank separator.
        var list = new EventList();

        var user1 = new TextRenderable();
        user1.SetText("first");
        list.Add(user1);
        var c1 = new TextRenderable();
        c1.SetText("tool1");
        list.Add(c1, BubbleStyle.Circle, () => Color.White);

        var user2 = new TextRenderable();
        user2.SetText("second");
        list.Add(user2);
        var c2 = new TextRenderable();
        c2.SetText("tool2");
        list.Add(c2, BubbleStyle.Circle, () => Color.White);

        var rows = list.Render(40);
        // Group 1: user + child = 2 rows. Group 2: user + child = 2 rows. No separator.
        Assert.Equal(4, rows.Count);
        // Row 0: ❯ first, Row 1: └─ ● tool1, Row 2: ❯ second, Row 3: └─ ● tool2
        Assert.Equal('❯', RuneAt(rows[0], 0));
        Assert.Equal('└', RuneAt(rows[1], 0));
        Assert.Equal('❯', RuneAt(rows[2], 0));
        Assert.Equal('└', RuneAt(rows[3], 0));
    }

    [Fact]
    public void Render_OrphanGroup_CircleChildrenWithNoRoot()
    {
        // Items before the first User form an orphan group — rendered as Circle children
        // with ├─/└─ connectors but no ❯ root row above them.
        var list = new EventList();
        var orphan = new TextRenderable();
        orphan.SetText("welcome");
        list.Add(orphan, BubbleStyle.Circle, () => Color.BrightBlack);

        var rows = list.Render(40);
        Assert.Single(rows);
        // The orphan is the sole (and last) child: └─ ● welcome
        Assert.Equal('└', RuneAt(rows[0], 0));
        Assert.Equal('─', RuneAt(rows[0], 1));
        Assert.Equal('●', RuneAt(rows[0], 3));
    }

    [Fact]
    public void Render_OrphanGroupFollowedByUserGroup_NoBlankSeparator()
    {
        // Orphan group + User group: rendered consecutively with no separator.
        var list = new EventList();

        var orphan = new TextRenderable();
        orphan.SetText("welcome");
        list.Add(orphan, BubbleStyle.Circle, () => Color.BrightBlack);

        var user = new TextRenderable();
        user.SetText("hello");
        list.Add(user);

        var rows = list.Render(40);
        // Orphan: 1 row. User: 1 row. Total: 2 (no blank separator).
        Assert.Equal(2, rows.Count);
        Assert.Equal('└', RuneAt(rows[0], 0)); // orphan tree child
        Assert.Equal('❯', RuneAt(rows[1], 0)); // user root
    }

    [Fact]
    public void Render_PlainItem_RendersVerbatimWithNoChrome()
    {
        // Plain items render at full width with no tree prefix.
        var list = new EventList();
        var plain = new TextRenderable();
        plain.SetText("Session: abc123");
        list.Add(plain, BubbleStyle.Plain);

        var rows = list.Render(40);
        Assert.Single(rows);
        // First character is 'S' — no ❯, no ●, no tree connectors.
        Assert.Equal('S', RuneAt(rows[0], 0));
    }

    [Fact]
    public void Render_PlainFollowedByUserGroup_NoSeparator()
    {
        // Plain item followed by a User group — no blank separator between them.
        var list = new EventList();

        var plain = new TextRenderable();
        plain.SetText("Session: abc123");
        list.Add(plain, BubbleStyle.Plain);

        var user = new TextRenderable();
        user.SetText("hello");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("response");
        list.Add(child, BubbleStyle.Circle, () => Color.White);

        var rows = list.Render(40);
        // Plain: 1 row. User: 1 row. Child: 1 row. Total: 3 (no separators).
        Assert.Equal(3, rows.Count);
        Assert.Equal('S', RuneAt(rows[0], 0)); // plain text
        Assert.Equal('❯', RuneAt(rows[1], 0)); // user root
        Assert.Equal('└', RuneAt(rows[2], 0)); // child
    }

    [Fact]
    public void Render_CircleColor_PassedThroughToGlyph()
    {
        // The circle color from getCircleColor appears on the ● glyph cell.
        var list  = new EventList();
        var user  = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("tool");
        list.Add(child, BubbleStyle.Circle, () => Color.Red);

        var rows = list.Render(40);
        // The ● glyph is at col 3 of the child row.
        var circleCell = rows[1].Cells[3];
        Assert.Equal('●', circleCell.Rune);
        Assert.Equal(Color.Red, circleCell.Foreground);
    }

    [Fact]
    public void Render_CircleColorDefault_UsesWhite()
    {
        // When no getCircleColor is provided, the ● glyph defaults to white.
        var list  = new EventList();
        var user  = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("tool");
        list.Add(child, BubbleStyle.Circle);

        var rows = list.Render(40);
        var circleCell = rows[1].Cells[3];
        Assert.Equal('●', circleCell.Rune);
        Assert.Equal(Color.White, circleCell.Foreground);
    }

    [Fact]
    public void Render_NoBlankSeparatorBetweenSiblingsInGroup()
    {
        // Within a single tree group, children are rendered consecutively
        // with no blank rows between them.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var c1 = new TextRenderable();
        c1.SetText("a");
        list.Add(c1, BubbleStyle.Circle, () => Color.White);

        var c2 = new TextRenderable();
        c2.SetText("b");
        list.Add(c2, BubbleStyle.Circle, () => Color.White);

        var rows = list.Render(40);
        // 1 root + 2 children = 3 rows, none empty.
        Assert.Equal(3, rows.Count);
        Assert.All(rows, row => Assert.NotEmpty(row.Cells));
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
