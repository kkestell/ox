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
        foreach (var cell in rows[0].Cells)
        {
            Assert.Equal(Color.Red,    cell.Foreground);
            Assert.Equal(Color.Blue,   cell.Background);
            Assert.Equal(CellStyle.Dim, cell.Style);
        }
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
        var tool = new ToolRenderable(MakeStarted().FormatCall());
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
        var tool = new ToolRenderable(MakeStarted().FormatCall());
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
        var tool = new ToolRenderable(MakeStarted().FormatCall());
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
        var tool = new ToolRenderable(MakeStarted().FormatCall());
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
    public void Render_SingleUser_CircleGlyphWithBlueColor()
    {
        // A lone User item renders as a Circle child with └─ ● (last top-level).
        // The circle glyph at col 3 is blue.
        var list  = new EventList();
        var child = new TextRenderable();
        child.SetText("hello");
        list.Add(child);

        var rows = list.Render(40);
        Assert.Single(rows);
        // Col 0: └, col 1: ─, col 2: space, col 3: ● (blue), col 4: space, col 5+: "hello"
        Assert.Equal('└', RuneAt(rows[0], 0));
        Assert.Equal('─', RuneAt(rows[0], 1));
        Assert.Equal(' ', RuneAt(rows[0], 2));
        Assert.Equal('●', RuneAt(rows[0], 3));
        Assert.Equal(Color.Blue, rows[0].Cells[3].Foreground);
        Assert.Equal(' ', RuneAt(rows[0], 4));
        Assert.Equal('h', RuneAt(rows[0], 5));
    }

    [Fact]
    public void Render_UserWithOneChild_NestedLastChildBranch()
    {
        // User (last top-level, └─ ●) + one Circle child nested underneath.
        // The nested child gets 3-space nest prefix + └─ ● (last nested).
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var tool = new TextRenderable();
        tool.SetText("read_file");
        list.Add(tool, BubbleStyle.Circle, () => Color.Green);

        var rows = list.Render(40);
        Assert.Equal(2, rows.Count);

        // Row 0: user as last top-level Circle child — └─ ● (blue)
        Assert.Equal('└', RuneAt(rows[0], 0));
        Assert.Equal('●', RuneAt(rows[0], 3));
        Assert.Equal(Color.Blue, rows[0].Cells[3].Foreground);

        // Row 1: nested child — `   └─ ● read_file` (3-space nest + last child)
        Assert.Equal(' ', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        Assert.Equal(' ', RuneAt(rows[1], 2));
        Assert.Equal('└', RuneAt(rows[1], 3));
        Assert.Equal('─', RuneAt(rows[1], 4));
        Assert.Equal(' ', RuneAt(rows[1], 5));
        Assert.Equal('●', RuneAt(rows[1], 6));
        Assert.Equal(' ', RuneAt(rows[1], 7));
    }

    [Fact]
    public void Render_UserWithTwoChildren_NestedBranchAndLastBranch()
    {
        // User (last top-level, └─ ●) + two nested children.
        // First nested: `   ├─ ●`, second nested: `   └─ ●`.
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

        // Row 0: user — └─ ● (blue, last top-level)
        Assert.Equal('└', RuneAt(rows[0], 0));
        Assert.Equal(Color.Blue, rows[0].Cells[3].Foreground);

        // Row 1: non-last nested child — `   ├─ ●` (3-space nest + ├─ ●)
        Assert.Equal(' ', RuneAt(rows[1], 0)); // nest prefix (last parent = spaces)
        Assert.Equal('├', RuneAt(rows[1], 3));
        Assert.Equal('─', RuneAt(rows[1], 4));

        // Row 2: last nested child — `   └─ ●`
        Assert.Equal(' ', RuneAt(rows[2], 0));
        Assert.Equal('└', RuneAt(rows[2], 3));
        Assert.Equal('─', RuneAt(rows[2], 4));
    }

    [Fact]
    public void Render_UserContinuationWithChildren_VerticalBar()
    {
        // When a User message wraps and has nested children, continuation rows
        // use │ + 4 spaces to signal the trunk continues to nested children below.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("hello world"); // will wrap at narrow width
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("ok");
        list.Add(child, BubbleStyle.Circle, () => Color.White);

        // Width 11: child chrome = 5 → content width = 6.
        // "hello world" at width 6 → "hello" + "world" (wraps at space).
        var rows = list.Render(11);

        // Row 0: └─ ● hello (last top-level, blue circle)
        Assert.Equal('└', RuneAt(rows[0], 0));
        Assert.Equal('●', RuneAt(rows[0], 3));
        // Row 1: continuation — │ + 4 spaces (has nested children, so show trunk)
        // Wait — the user is last top-level, but has nested children. showVertical = true,
        // so isLast = false → MakeChildContinuationRow emits │ + 4 spaces.
        Assert.Equal('│', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        Assert.Equal(' ', RuneAt(rows[1], 2));
        Assert.Equal(' ', RuneAt(rows[1], 3));
        Assert.Equal(' ', RuneAt(rows[1], 4));
        // Row 2: nested child — `   └─ ● ok` (3-space nest for last parent + last nested)
        Assert.Equal(' ', RuneAt(rows[2], 0));
        Assert.Equal('└', RuneAt(rows[2], 3));
    }

    [Fact]
    public void Render_UserContinuationWithoutChildren_NoVerticalBar()
    {
        // When a User message wraps and has no nested children (and is last
        // top-level), continuation rows use 5 spaces (no vertical bar).
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("hello world");
        list.Add(user);

        // Width 11: child chrome = 5 → content width = 6, "hello world" wraps.
        var rows = list.Render(11);
        Assert.Equal(2, rows.Count);

        // Row 0: └─ ● hello
        Assert.Equal('└', RuneAt(rows[0], 0));
        // Row 1: continuation — 5 spaces (last top-level, no nested children).
        Assert.Equal(' ', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        Assert.Equal(' ', RuneAt(rows[1], 2));
        Assert.Equal(' ', RuneAt(rows[1], 3));
        Assert.Equal(' ', RuneAt(rows[1], 4));
        Assert.Equal('w', RuneAt(rows[1], 5)); // "world" content starts at col 5
    }

    [Fact]
    public void Render_NestedChildContinuation_VerticalBarForNonLast()
    {
        // When a non-last nested child's content wraps, continuation rows use
        // nest prefix + │ + 4 spaces to maintain the trunk for siblings below.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var c1 = new TextRenderable();
        c1.SetText("ab"); // will wrap at narrow width
        list.Add(c1, BubbleStyle.Circle, () => Color.White);

        var c2 = new TextRenderable();
        c2.SetText("end");
        list.Add(c2, BubbleStyle.Circle, () => Color.White);

        // Width 9: nest chrome = 3, child chrome = 5 → nested content width = 1.
        // "ab" at width 1 → hard break: "a" + "b".
        var rows = list.Render(9);

        // Row 0: └─ ● msg (user, last top-level)
        Assert.Equal('└', RuneAt(rows[0], 0));
        // Row 1: `   ├─ ● a` (nested non-last child, 3-space nest + ├─ ● + content)
        Assert.Equal(' ', RuneAt(rows[1], 0)); // nest prefix (last parent → spaces)
        Assert.Equal('├', RuneAt(rows[1], 3));
        // Row 2: `   │    b` (nested non-last continuation: 3-space nest + │ + 4 spaces)
        Assert.Equal(' ', RuneAt(rows[2], 0));
        Assert.Equal('│', RuneAt(rows[2], 3));
        Assert.Equal(' ', RuneAt(rows[2], 4));
        Assert.Equal(' ', RuneAt(rows[2], 5));
        Assert.Equal(' ', RuneAt(rows[2], 6));
        Assert.Equal(' ', RuneAt(rows[2], 7));
        // Row 3: `   └─ ● end` (nested last child)
        Assert.Equal('└', RuneAt(rows[3], 3));
    }

    [Fact]
    public void Render_NestedChildContinuation_SpacesForLastChild()
    {
        // When the last nested child wraps, continuation rows use
        // nest prefix + 5 spaces (no │) since no more siblings below.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("ab"); // will wrap at narrow width
        list.Add(child, BubbleStyle.Circle, () => Color.White);

        // Width 9: nested content width = 9 - 3 - 5 = 1. "ab" → "a" + "b".
        var rows = list.Render(9);

        // Row 0: └─ ● msg (user, last top-level)
        // Row 1: `   └─ ● a` (nested last child, 3-space nest + └─ ●)
        Assert.Equal('└', RuneAt(rows[1], 3));
        // Row 2: continuation — 3-space nest + 5 spaces (no │)
        Assert.Equal(' ', RuneAt(rows[2], 0));
        Assert.Equal(' ', RuneAt(rows[2], 1));
        Assert.Equal(' ', RuneAt(rows[2], 2));
        Assert.Equal(' ', RuneAt(rows[2], 3));
        Assert.Equal(' ', RuneAt(rows[2], 4));
        Assert.Equal(' ', RuneAt(rows[2], 5));
        Assert.Equal(' ', RuneAt(rows[2], 6));
        Assert.Equal(' ', RuneAt(rows[2], 7));
    }

    [Fact]
    public void Render_TwoGroups_ContinuousTree()
    {
        // Two User groups form one continuous tree. First user gets ├─ (non-last),
        // second user gets └─ (last). Each user's nested child is indented.
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
        // Group 1: user + nested child = 2. Group 2: user + nested child = 2. Total: 4.
        Assert.Equal(4, rows.Count);
        // Row 0: ├─ ● first (non-last top-level, blue circle)
        Assert.Equal('├', RuneAt(rows[0], 0));
        Assert.Equal(Color.Blue, rows[0].Cells[3].Foreground);
        // Row 1: │  └─ ● tool1 (nested under non-last parent → │ prefix)
        Assert.Equal('│', RuneAt(rows[1], 0));
        Assert.Equal('└', RuneAt(rows[1], 3));
        // Row 2: └─ ● second (last top-level, blue circle)
        Assert.Equal('└', RuneAt(rows[2], 0));
        Assert.Equal(Color.Blue, rows[2].Cells[3].Foreground);
        // Row 3:    └─ ● tool2 (nested under last parent → space prefix)
        Assert.Equal(' ', RuneAt(rows[3], 0));
        Assert.Equal('└', RuneAt(rows[3], 3));
    }

    [Fact]
    public void Render_OrphanGroup_CircleChildrenWithNoRoot()
    {
        // Items before the first User form an orphan group — rendered as top-level
        // Circle children with ├─/└─ connectors but no parent above them.
        var list = new EventList();
        var orphan = new TextRenderable();
        orphan.SetText("welcome");
        list.Add(orphan, BubbleStyle.Circle, () => Color.BrightBlack);

        var rows = list.Render(40);
        Assert.Single(rows);
        // The orphan is the sole (and last) top-level child: └─ ● welcome
        Assert.Equal('└', RuneAt(rows[0], 0));
        Assert.Equal('─', RuneAt(rows[0], 1));
        Assert.Equal('●', RuneAt(rows[0], 3));
    }

    [Fact]
    public void Render_OrphanGroupFollowedByUserGroup_ContinuousTree()
    {
        // Orphan group + User group: both are top-level siblings in one tree.
        // Orphan gets ├─ (non-last), User gets └─ (last).
        var list = new EventList();

        var orphan = new TextRenderable();
        orphan.SetText("welcome");
        list.Add(orphan, BubbleStyle.Circle, () => Color.BrightBlack);

        var user = new TextRenderable();
        user.SetText("hello");
        list.Add(user);

        var rows = list.Render(40);
        Assert.Equal(2, rows.Count);
        // Row 0: orphan — ├─ ● (non-last, since user follows)
        Assert.Equal('├', RuneAt(rows[0], 0));
        // Row 1: user — └─ ● (last top-level, blue circle)
        Assert.Equal('└', RuneAt(rows[1], 0));
        Assert.Equal(Color.Blue, rows[1].Cells[3].Foreground);
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
        // User renders as └─ ● (last top-level), child is nested underneath.
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
        // Plain: 1 row. User: 1 row. Nested child: 1 row. Total: 3.
        Assert.Equal(3, rows.Count);
        Assert.Equal('S', RuneAt(rows[0], 0)); // plain text verbatim
        Assert.Equal('└', RuneAt(rows[1], 0)); // user as last top-level circle
        Assert.Equal(Color.Blue, rows[1].Cells[3].Foreground);
        Assert.Equal(' ', RuneAt(rows[2], 0)); // nested child (last parent → spaces)
        Assert.Equal('└', RuneAt(rows[2], 3)); // nested └─ ●
    }

    [Fact]
    public void Render_CircleColor_PassedThroughToGlyph()
    {
        // The circle color from getCircleColor appears on the nested ● glyph cell.
        // Nested child ● is at col 6 (NestChrome 3 + child col 3 = 6).
        var list  = new EventList();
        var user  = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("tool");
        list.Add(child, BubbleStyle.Circle, () => Color.Red);

        var rows = list.Render(40);
        var circleCell = rows[1].Cells[6];
        Assert.Equal('●', circleCell.Rune);
        Assert.Equal(Color.Red, circleCell.Foreground);
    }

    [Fact]
    public void Render_CircleColorDefault_UsesWhite()
    {
        // When no getCircleColor is provided, the nested ● glyph defaults to white.
        var list  = new EventList();
        var user  = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("tool");
        list.Add(child, BubbleStyle.Circle);

        var rows = list.Render(40);
        var circleCell = rows[1].Cells[6];
        Assert.Equal('●', circleCell.Rune);
        Assert.Equal(Color.White, circleCell.Foreground);
    }

    [Fact]
    public void Render_NoBlankSeparatorBetweenNestedSiblings()
    {
        // Within a single tree group, nested children are rendered consecutively
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
        // 1 user + 2 nested children = 3 rows, none empty.
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

    [Fact]
    public void Render_UserCircleBlue_DoesNotAffectOrphanColor()
    {
        // The blue circle is specific to User items — orphan Circles retain
        // their own color. Verify an orphan followed by a User each has the
        // correct circle color.
        var list = new EventList();
        var orphan = new TextRenderable();
        orphan.SetText("welcome");
        list.Add(orphan, BubbleStyle.Circle, () => Color.BrightBlack);

        var user = new TextRenderable();
        user.SetText("hello");
        list.Add(user);

        var rows = list.Render(40);
        Assert.Equal(2, rows.Count);
        // Orphan circle at col 3: BrightBlack
        Assert.Equal('●', RuneAt(rows[0], 3));
        Assert.Equal(Color.BrightBlack, rows[0].Cells[3].Foreground);
        // User circle at col 3: Blue
        Assert.Equal('●', RuneAt(rows[1], 3));
        Assert.Equal(Color.Blue, rows[1].Cells[3].Foreground);
    }

    [Fact]
    public void Render_NestedChild_HasCorrectChrome()
    {
        // User + one nested child. Verify nested child row has `└─ ● ` starting
        // at col 3 (after 3-space nest prefix for last parent). Content at col 8.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("data");
        list.Add(child, BubbleStyle.Circle, () => Color.Green);

        var rows = list.Render(40);
        Assert.Equal(2, rows.Count);

        // Nested child row: `   └─ ● data`
        var nested = rows[1];
        Assert.Equal(' ', RuneAt(nested, 0)); // nest prefix col 0
        Assert.Equal(' ', RuneAt(nested, 1)); // nest prefix col 1
        Assert.Equal(' ', RuneAt(nested, 2)); // nest prefix col 2
        Assert.Equal('└', RuneAt(nested, 3)); // child branch
        Assert.Equal('─', RuneAt(nested, 4));
        Assert.Equal(' ', RuneAt(nested, 5));
        Assert.Equal('●', RuneAt(nested, 6)); // circle glyph
        Assert.Equal(Color.Green, nested.Cells[6].Foreground);
        Assert.Equal(' ', RuneAt(nested, 7));
        Assert.Equal('d', RuneAt(nested, 8)); // content starts at col 8
    }

    [Fact]
    public void Render_NestedChildContinuation_NonLastNestedHasVerticalBar()
    {
        // User + two nested children, first child wraps. Verify the continuation
        // row has nest prefix `│  ` + child continuation `│    ` = 8 cols for a
        // non-last nested child under a non-last parent.
        var list = new EventList();
        var user1 = new TextRenderable();
        user1.SetText("u1");
        list.Add(user1);

        var c1 = new TextRenderable();
        c1.SetText("ab"); // will wrap at 1-char content width
        list.Add(c1, BubbleStyle.Circle, () => Color.White);

        var c2 = new TextRenderable();
        c2.SetText("z");
        list.Add(c2, BubbleStyle.Circle, () => Color.White);

        // Add second user so first user is non-last (gets │ nest prefix).
        var user2 = new TextRenderable();
        user2.SetText("u2");
        list.Add(user2);

        // Width 9: nested content = 9 - 3 - 5 = 1. "ab" → "a" + "b".
        var rows = list.Render(9);

        // Row 0: ├─ ● u1 (non-last user)
        Assert.Equal('├', RuneAt(rows[0], 0));
        // Row 1: │  ├─ ● a (non-last nested child under non-last parent)
        Assert.Equal('│', RuneAt(rows[1], 0)); // nest prefix │
        Assert.Equal('├', RuneAt(rows[1], 3)); // non-last child
        // Row 2: │  │    b (continuation: nest │ + child │ + 4 spaces)
        Assert.Equal('│', RuneAt(rows[2], 0)); // nest prefix │
        Assert.Equal('│', RuneAt(rows[2], 3)); // child continuation │
        Assert.Equal(' ', RuneAt(rows[2], 4));
        Assert.Equal(' ', RuneAt(rows[2], 5));
        Assert.Equal(' ', RuneAt(rows[2], 6));
        Assert.Equal(' ', RuneAt(rows[2], 7));
        // Row 3: │  └─ ● z (last nested child under non-last parent)
        Assert.Equal('│', RuneAt(rows[3], 0));
        Assert.Equal('└', RuneAt(rows[3], 3));
    }

    [Fact]
    public void Render_NestedChildContinuation_LastNestedHasSpaces()
    {
        // User + one nested child that wraps. Verify the continuation row has
        // 8 spaces (3-space nest for last parent + 5-space last-child continuation).
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var child = new TextRenderable();
        child.SetText("ab"); // will wrap
        list.Add(child, BubbleStyle.Circle, () => Color.White);

        // Width 9: nested content = 1. "ab" → "a" + "b".
        var rows = list.Render(9);

        // Row 0: └─ ● msg (last top-level)
        // Row 1: `   └─ ● a` (last nested under last parent)
        Assert.Equal('└', RuneAt(rows[1], 3));
        // Row 2: continuation — 3-space nest + 5-space last-child = 8 spaces
        for (var col = 0; col < 8; col++)
            Assert.Equal(' ', RuneAt(rows[2], col));
    }

    [Fact]
    public void Render_NonLastUserNoChildren_ContinuationShowsVerticalBar()
    {
        // A non-last User with no nested children still shows │ on continuation
        // rows, because the trunk must continue to the sibling below.
        var list = new EventList();
        var user1 = new TextRenderable();
        user1.SetText("hello world"); // will wrap
        list.Add(user1);

        var user2 = new TextRenderable();
        user2.SetText("bye");
        list.Add(user2);

        // Width 11: child chrome = 5 → content width = 6. "hello world" wraps.
        var rows = list.Render(11);

        // Row 0: ├─ ● hello (non-last, no nested children)
        Assert.Equal('├', RuneAt(rows[0], 0));
        // Row 1: │    world (continuation — │ because sibling follows, not nested children)
        Assert.Equal('│', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        // Row 2: └─ ● bye (last top-level)
        Assert.Equal('└', RuneAt(rows[2], 0));
    }

    [Fact]
    public void Render_TrailingPlainAfterUser_UserIsStillLastTopLevel()
    {
        // Plain items don't count as top-level tree nodes. A User followed by
        // a trailing Plain should still be the last top-level (gets └─).
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var plain = new TextRenderable();
        plain.SetText("footer");
        list.Add(plain, BubbleStyle.Plain);

        var rows = list.Render(40);
        Assert.Equal(2, rows.Count);
        // Row 0: └─ ● msg (user is last top-level)
        Assert.Equal('└', RuneAt(rows[0], 0));
        // Row 1: footer (plain, verbatim)
        Assert.Equal('f', RuneAt(rows[1], 0));
    }

    [Fact]
    public void Render_TwoUsers_ContinuousTreeWithNestedChildren()
    {
        // Two users each with children. Verify the entire output forms one
        // continuous tree: first user ├─ ● (col 0), its children nested with
        // │ prefix; second user └─ ● (col 0), its children nested with spaces.
        var list = new EventList();

        var user1 = new TextRenderable();
        user1.SetText("first");
        list.Add(user1);
        var c1A = new TextRenderable();
        c1A.SetText("tool1");
        list.Add(c1A, BubbleStyle.Circle, () => Color.Yellow);
        var c1B = new TextRenderable();
        c1B.SetText("resp1");
        list.Add(c1B, BubbleStyle.Circle, () => Color.White);

        var user2 = new TextRenderable();
        user2.SetText("second");
        list.Add(user2);
        var c2 = new TextRenderable();
        c2.SetText("resp2");
        list.Add(c2, BubbleStyle.Circle, () => Color.Green);

        var rows = list.Render(40);
        // user1 + 2 nested + user2 + 1 nested = 5 rows
        Assert.Equal(5, rows.Count);

        // Row 0: ├─ ● first (non-last top-level)
        Assert.Equal('├', RuneAt(rows[0], 0));
        Assert.Equal(Color.Blue, rows[0].Cells[3].Foreground);
        // Row 1: │  ├─ ● tool1 (nested non-last under non-last parent)
        Assert.Equal('│', RuneAt(rows[1], 0));
        Assert.Equal('├', RuneAt(rows[1], 3));
        // Row 2: │  └─ ● resp1 (nested last under non-last parent)
        Assert.Equal('│', RuneAt(rows[2], 0));
        Assert.Equal('└', RuneAt(rows[2], 3));
        // Row 3: └─ ● second (last top-level)
        Assert.Equal('└', RuneAt(rows[3], 0));
        Assert.Equal(Color.Blue, rows[3].Cells[3].Foreground);
        // Row 4:    └─ ● resp2 (nested last under last parent)
        Assert.Equal(' ', RuneAt(rows[4], 0));
        Assert.Equal('└', RuneAt(rows[4], 3));
    }
}
