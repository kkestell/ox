using Te.Rendering;
using Ur.AgentLoop;
using Ox.Rendering;

namespace Ur.Tests;

/// <summary>
/// Tests for the cell-based TUI rendering layer (Color, Cell, CellRow, ConsoleBuffer,
/// TextRenderable, ToolRenderable, SubagentRenderable, EventList, Viewport).
///
/// These tests operate entirely on the data model — no ANSI codes, no terminal I/O.
/// The cell types are pure value types or simple classes; their behavior can be
/// asserted by inspecting CellRow.Cells directly.
/// </summary>

// ---------------------------------------------------------------------------
// ViewportBuffer — Viewport.BuildFrame / ConsoleBuffer integration
//
// Low-level ConsoleBuffer unit tests (SetCell, Resize, Clear, etc.) live in
// Te.Tests/ConsoleBufferTests.cs. These tests verify that Viewport.BuildFrame
// correctly populates the persistent _buffer and that the coordinate convention
// (x=col, y=row in ConsoleBuffer) is applied throughout.
// ---------------------------------------------------------------------------

public sealed class ViewportBufferTests
{
    // Height of the input area (top rule + text + spacer + status + blank).
    private const int InputAreaRows = 5;
    private static readonly Color Grey15 = Color.FromIndex(235);
    private static readonly Color Grey23 = Color.FromIndex(237);
    private static readonly Color Grey50 = Color.FromIndex(244);

    [Fact]
    public void BuildFrame_ConversationText_AppearsInBuffer()
    {
        // A single User message should render as a tree row in the conversation
        // area. The first character of 'hello' should appear somewhere in row 0.
        var list = new EventList();
        var text = new TextRenderable();
        text.SetText("hello");
        list.Add(text);

        var viewport = new Viewport(list);
        viewport.BuildFrame(80, 24);

        // EventList wraps the user text with circle chrome (2 cols for CircleChrome),
        // so 'h' starts at x=2, y=0. Scan row 0 to find it.
        var row0Text = new string(
            Enumerable.Range(0, 80)
                .Select(col => viewport._buffer.GetCell(col, 0).Rune)
                .ToArray());
        Assert.Contains("hello", row0Text);
    }

    [Fact]
    public void BuildFrame_InputAreaRule_AppearsAtCorrectRow()
    {
        // The top input-area rule (▇) should appear at y = height - InputAreaRows.
        // This verifies the coordinate convention: GetCell uses (x=col, y=row).
        var viewport = new Viewport(new EventList());
        viewport.BuildFrame(10, 10);

        // Top rule row: 10 - 5 = 5.
        const char topRuleChar = '▇';
        Assert.Equal(topRuleChar, viewport._buffer.GetCell(0, 5).Rune);
        Assert.Equal(topRuleChar, viewport._buffer.GetCell(9, 5).Rune);
        Assert.Equal(Grey15, viewport._buffer.GetCell(0, 5).Foreground);
        Assert.Equal(Grey23, viewport._buffer.GetCell(0, 5).Background);
    }

    [Fact]
    public void BuildFrame_SidebarSeparator_AppearsWhenSidebarVisible()
    {
        // When a sidebar is attached and visible, a │ separator should appear
        // at the column just after the left-column width, in every row.
        // Use a TodoSection with items so HasContent is driven by real data,
        // not by an always-true override.
        var store   = new Ur.Todo.TodoStore();
        store.Update([new Ur.Todo.TodoItem("Task", Ur.Todo.TodoStatus.Pending)]);
        var section = new TodoSection(store);
        var sidebar = new Sidebar();
        sidebar.AddSection(section);

        var viewport = new Viewport(new EventList(), sidebar);
        viewport.BuildFrame(40, 10);

        // Sidebar gets up to width/3 ≤ MaxSidebarWidth columns.
        // With width=40: sidebarWidth = min(36, 40/3) = 13, leftWidth = 40 - 13 - 1 = 26.
        // Separator column = 26.
        const char separatorChar = '│';
        Assert.Equal(separatorChar, viewport._buffer.GetCell(26, 0).Rune);
        Assert.Equal(separatorChar, viewport._buffer.GetCell(26, 9).Rune);
    }

    [Fact]
    public void BuildFrame_RebuildWithNewPrompt_BufferReflectsNewState()
    {
        // Each BuildFrame call must clear the back buffer before writing so that
        // stale cells from the previous frame do not bleed through. Verify this
        // by rebuilding after changing the input prompt and asserting the new
        // input content appears at the expected cell.
        var viewport = new Viewport(new EventList());

        // First build with an empty input row. For height=24 the text row is at
        // y = (24 - 5) + 1 = 20 (viewportHeight + top-rule row + text row).
        // With the built-in chevron removed, the cursor now starts at the first
        // editable column after the footer margin.
        viewport.BuildFrame(80, 24);
        var textRow = 24 - 5 + 1; // 20
        var cursorCell = viewport._buffer.GetCell(2, textRow);
        Assert.Equal(' ', cursorCell.Rune);
        Assert.Equal(TextDecoration.Reverse, cursorCell.Decorations);

        // Change the input text and rebuild — the buffer must clear first,
        // otherwise the old cursor cell at x=2 would survive.
        viewport.SetInputPrompt("test");
        viewport.BuildFrame(80, 24);
        Assert.Equal('t', viewport._buffer.GetCell(2, textRow).Rune);
    }

    [Fact]
    public void BuildFrame_TurnRunning_KeepsInputCursorVisible()
    {
        // The footer should stay in composer mode while a turn runs so the
        // cursor does not disappear when the status line switches to the throbber.
        var viewport = new Viewport(new EventList());
        viewport.SetTurnRunning(true);

        viewport.BuildFrame(80, 24);

        var textRow = 24 - 5 + 1;
        var cursorCell = viewport._buffer.GetCell(2, textRow);
        Assert.Equal(' ', cursorCell.Rune);
        Assert.Equal(TextDecoration.Reverse, cursorCell.Decorations);
    }

    [Fact]
    public void BuildFrame_InputFooterRows_UseRequestedBackgrounds()
    {
        var viewport = new Viewport(new EventList());

        viewport.BuildFrame(10, 10);

        var inputRow = 10 - InputAreaRows + 1;
        var spacerRow = 10 - InputAreaRows + 2;
        var statusRow = 10 - InputAreaRows + 3;
        var blankFooterRow = 10 - InputAreaRows + 4;

        Assert.Equal(Grey15, viewport._buffer.GetCell(0, inputRow).Background);
        Assert.Equal(Color.Default, viewport._buffer.GetCell(0, inputRow).Foreground);
        Assert.Equal(Color.Default, viewport._buffer.GetCell(1, inputRow).Foreground);
        Assert.Equal(Color.Default, viewport._buffer.GetCell(2, inputRow).Foreground);
        Assert.Equal(Grey15, viewport._buffer.GetCell(9, inputRow).Background);

        Assert.Equal(' ', viewport._buffer.GetCell(0, spacerRow).Rune);
        Assert.Equal(Grey15, viewport._buffer.GetCell(0, spacerRow).Background);
        Assert.Equal(Grey15, viewport._buffer.GetCell(9, spacerRow).Background);

        Assert.Equal(Grey15, viewport._buffer.GetCell(0, statusRow).Background);
        Assert.Equal(Grey15, viewport._buffer.GetCell(9, statusRow).Background);

        Assert.Equal(Grey15, viewport._buffer.GetCell(0, blankFooterRow).Background);
        Assert.Equal(Grey15, viewport._buffer.GetCell(9, blankFooterRow).Background);
    }

    [Fact]
    public void BuildFrame_StatusLine_ModelUsesGrey50OnFooterBackground()
    {
        var viewport = new Viewport(new EventList());
        viewport.SetModelId("openai/gpt");

        viewport.BuildFrame(20, 10);

        var statusRow = 10 - InputAreaRows + 3;
        var modelStartCol = 20 - "openai/gpt".Length - 2;
        var firstModelCell = viewport._buffer.GetCell(modelStartCol, statusRow);
        Assert.Equal('o', firstModelCell.Rune);
        Assert.Equal(Grey50, firstModelCell.Foreground);
        Assert.Equal(Grey15, firstModelCell.Background);
    }

    [Fact]
    public void BuildThrobberCells_AtTurnStart_RendersOne()
    {
        var cells = Viewport.BuildThrobberCells(Viewport.ComputeThrobberCounter(0));

        Assert.Equal(15, cells.Count);
        var expected = new[]
        {
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.White
        };

        for (var i = 0; i < expected.Length; i++)
        {
            Assert.NotEqual(' ', cells[i * 2].Rune);
            Assert.Equal(expected[i], cells[i * 2].Foreground);
            Assert.Equal(Color.Default, cells[i * 2].Background);

            if (i < expected.Length - 1)
            {
                Assert.Equal(' ', cells[(i * 2) + 1].Rune);
                Assert.Equal(Color.Default, cells[(i * 2) + 1].Foreground);
                Assert.Equal(Color.Default, cells[(i * 2) + 1].Background);
            }
        }
    }

    [Fact]
    public void BuildThrobberCells_RendersCounterAsEightBitBinary()
    {
        // After two elapsed seconds the visible value should be 3, so the row
        // reads 00000011 with the MSB on the left.
        var cells = Viewport.BuildThrobberCells(Viewport.ComputeThrobberCounter(2_000));

        var expected = new[]
        {
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.BrightBlack,
            Color.White,
            Color.White
        };

        for (var i = 0; i < expected.Length; i++)
        {
            Assert.NotEqual(' ', cells[i * 2].Rune);
            Assert.Equal(expected[i], cells[i * 2].Foreground);
        }
    }

    [Fact]
    public void BuildThrobberCells_After255Seconds_WrapsBackToZero()
    {
        var wrapped = Viewport.BuildThrobberCells(Viewport.ComputeThrobberCounter(255_000));

        for (var i = 0; i < 8; i++)
            Assert.Equal(Color.BrightBlack, wrapped[i * 2].Foreground);
    }

    [Fact]
    public void BuildFrame_NewTurn_StartsCounterAtOneInsteadOfProcessUptime()
    {
        long tickCountMs = 147_000;
        var viewport = new Viewport(new EventList(), null, () => tickCountMs);

        viewport.SetTurnRunning(true);
        viewport.BuildFrame(20, 10);

        var statusRow = 10 - InputAreaRows + 3;
        var expected = "00000001";
        for (var i = 0; i < expected.Length; i++)
        {
            var cell = viewport._buffer.GetCell(2 + (i * 2), statusRow);
            Assert.NotEqual(' ', cell.Rune);
            Assert.Equal(
                expected[i] == '1' ? Color.White : Color.BrightBlack,
                cell.Foreground);

            if (i < expected.Length - 1)
                Assert.Equal(' ', viewport._buffer.GetCell(2 + (i * 2) + 1, statusRow).Rune);
        }
    }

    [Fact]
    public void BuildFrame_Resize_BufferDimensionsMatchNewSize()
    {
        // Reproduce the resize bug: when the terminal shrinks, old content
        // outside the new dimensions must not survive. BuildFrame should
        // resize the buffer to the new dimensions so stale cells beyond the
        // new bounds are gone.
        var list = new EventList();
        var text = new TextRenderable();
        text.SetText("wide content that fills a large terminal");
        list.Add(text);

        var viewport = new Viewport(list);
        viewport.BuildFrame(80, 24);

        // Verify initial size.
        Assert.Equal(80, viewport._buffer.Width);
        Assert.Equal(24, viewport._buffer.Height);

        // Shrink the terminal.
        viewport.BuildFrame(40, 12);

        // Buffer dimensions must match the new size.
        Assert.Equal(40, viewport._buffer.Width);
        Assert.Equal(12, viewport._buffer.Height);
    }

    [Fact]
    public void BuildFrame_Grow_BufferDimensionsMatchNewSize()
    {
        // The inverse case: growing the terminal must also resize the buffer.
        var viewport = new Viewport(new EventList());
        viewport.BuildFrame(40, 12);

        viewport.BuildFrame(120, 40);

        Assert.Equal(120, viewport._buffer.Width);
        Assert.Equal(40, viewport._buffer.Height);
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
        var r = new TextRenderable(foreground: Color.Red, background: Color.Blue, decorations: TextDecoration.Dim);
        r.SetText("hi");
        var rows = r.Render(80);
        foreach (var cell in rows[0].Cells)
        {
            Assert.Equal(Color.Red,             cell.Foreground);
            Assert.Equal(Color.Blue,            cell.Background);
            Assert.Equal(TextDecoration.Dim,    cell.Decorations);
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
        // FormatCall now uses display names: read_file → Read.
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("Read", text);
        Assert.DoesNotContain("[awaiting approval]", text);
        Assert.Equal(Color.Yellow, tool.CircleColor);
    }

    [Fact]
    public void Render_Completed_DoesNotShowArrowOk()
    {
        // Circle color (green) conveys success — no text suffix needed.
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetCompleted(isError: false, result: null);
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("Read", text);
        Assert.DoesNotContain("→ ok", text);
        Assert.Equal(Color.Green, tool.CircleColor);
    }

    [Fact]
    public void Render_CompletedError_DoesNotShowArrowError()
    {
        // Circle color (red) conveys failure — no text suffix needed.
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetCompleted(isError: true, result: null);
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("Read", text);
        Assert.DoesNotContain("→ error", text);
        Assert.Equal(Color.Red, tool.CircleColor);
    }

    [Fact]
    public void Render_AwaitingApproval_NoTextSuffix()
    {
        // The permission prompt is shown in the input area, not inline.
        // AwaitingApproval only affects circle color — no text suffix.
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetAwaitingApproval();
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("Read", text);
        Assert.DoesNotContain("[awaiting approval]", text);
    }

    [Fact]
    public void Render_CompletedWithResult_ShowsResultLines()
    {
        // Result lines appear beneath the signature with └─ on the first line
        // and indentation on continuations.
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetCompleted(isError: false, result: "line1\nline2\nline3");
        var rows = tool.Render(80);

        Assert.Equal(4, rows.Count); // signature + 3 result lines
        Assert.Contains("└─", RowText(rows[1]));
        Assert.Contains("line1", RowText(rows[1]));
        // Continuation lines use space-indent, not └─.
        Assert.DoesNotContain("└─", RowText(rows[2]));
        Assert.Contains("line2", RowText(rows[2]));
        Assert.DoesNotContain("└─", RowText(rows[3]));
        Assert.Contains("line3", RowText(rows[3]));
    }

    [Fact]
    public void Render_CompletedWithResult_ExactlyMaxLines_NoTruncation()
    {
        // Boundary: exactly MaxResultLines (5) should show all lines with no truncation indicator.
        var lines = string.Join("\n", Enumerable.Range(1, 5).Select(i => $"line{i}"));
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetCompleted(isError: false, result: lines);
        var rows = tool.Render(80);

        Assert.Equal(6, rows.Count); // signature + 5 result lines, no truncation
        for (var i = 0; i < rows.Count; i++)
            Assert.DoesNotContain("more lines", RowText(rows[i]));
    }

    [Fact]
    public void Render_CompletedWithResult_TruncatesLongOutput()
    {
        // Results exceeding MaxResultLines (5) are truncated with a "(N more lines)" indicator.
        var lines = string.Join("\n", Enumerable.Range(1, 10).Select(i => $"line{i}"));
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetCompleted(isError: false, result: lines);
        var rows = tool.Render(80);

        // signature + 5 visible lines + 1 truncation indicator
        Assert.Equal(7, rows.Count);
        Assert.Contains("(5 more lines)", RowText(rows[6]));
    }

    [Fact]
    public void Render_CompletedWithEmptyResult_ShowsSignatureOnly()
    {
        // Empty or whitespace-only results produce no result rows.
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetCompleted(isError: false, result: "");
        var rows = tool.Render(80);
        Assert.Single(rows);

        var tool2 = new ToolRenderable(MakeStarted().FormatCall());
        tool2.SetCompleted(isError: false, result: "   \n  \n ");
        rows = tool2.Render(80);
        Assert.Single(rows);
    }

    [Fact]
    public void Render_CompletedWithNullResult_ShowsSignatureOnly()
    {
        // Null result is the same as no output — signature row only.
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetCompleted(isError: false, result: null);
        var rows = tool.Render(80);
        Assert.Single(rows);
    }

    [Fact]
    public void Render_CompletedError_ShowsResultLines()
    {
        // Error results are rendered the same way — the circle color (red)
        // distinguishes errors, not the result text formatting.
        var tool = new ToolRenderable(MakeStarted().FormatCall());
        tool.SetCompleted(isError: true, result: "Permission denied");
        var rows = tool.Render(80);

        Assert.Equal(2, rows.Count);
        Assert.Contains("└─", RowText(rows[1]));
        Assert.Contains("Permission denied", RowText(rows[1]));
        Assert.Equal(Color.Red, tool.CircleColor);
    }
}

// ---------------------------------------------------------------------------
// SubagentRenderable — subagent rendering
// ---------------------------------------------------------------------------

public sealed class SubagentRenderableTests
{
    private static string RowText(CellRow row) =>
        new(row.Cells.Select(c => c.Rune).ToArray());

    [Fact]
    public void Render_NoChildren_ReturnsJustSignatureRow()
    {
        // With no inner events, only the tool call signature is rendered.
        var r    = new SubagentRenderable("abc123", "Subagent(\"hello\")");
        var rows = r.Render(40);
        Assert.Single(rows);
        Assert.Contains("Subagent", RowText(rows[0]));
    }

    [Fact]
    public void Render_WithChildren_SignaturePlusInnerFlatRows()
    {
        // The signature row is followed by inner EventList flat output.
        // Inner children get ● prefix from the inner EventList.
        var r     = new SubagentRenderable("abc", "Subagent(\"hi\")");
        var child = new TextRenderable();
        child.SetText("hello");
        r.AddChild(child, BubbleStyle.Circle, () => Color.Green);

        var rows = r.Render(40);
        // Signature row + 1 inner child row (single child = no blank separators) = 2 rows.
        Assert.Equal(2, rows.Count);
        Assert.Contains("Subagent", RowText(rows[0]));
        // The inner child gets flat ● prefix — circle at col 0.
        Assert.Equal('●', rows[1].Cells[0].Rune);
    }

    [Fact]
    public void CircleColor_YellowWhileRunning_GreenOnCompletion()
    {
        var r = new SubagentRenderable("abc", "Subagent(\"hi\")");
        Assert.Equal(Color.Yellow, r.CircleColor);
        r.SetCompleted();
        Assert.Equal(Color.Green, r.CircleColor);
    }

    [Fact]
    public void SetCompleted_IsIdempotent()
    {
        var r = new SubagentRenderable("abc", "Subagent(\"hi\")");
        r.SetCompleted();
        r.SetCompleted(); // Second call should not throw or change state.
        Assert.Equal(Color.Green, r.CircleColor);
    }

    [Fact]
    public void Render_TailClips_WhenInnerRowsExceedMaxInnerRows()
    {
        // Each Circle child in the flat inner EventList produces 1 circle row
        // plus a blank separator between items. Add enough children to exceed
        // MaxInnerRows (20). The visible inner row count must be capped.
        var r = new SubagentRenderable("clip-test", "Subagent(\"big\")");
        for (var i = 0; i < 30; i++)
        {
            var child = new TextRenderable();
            child.SetText($"line {i}");
            r.AddChild(child, BubbleStyle.Circle, () => Color.White);
        }

        var rows = r.Render(80);
        // Row 0 = signature. Inner rows are tail-clipped to 20 + 1 ellipsis = 21.
        var innerRowCount = rows.Count - 1; // subtract signature
        Assert.Equal(21, innerRowCount);
        // Verify the ellipsis row is present (● ...).
        Assert.Contains("...", RowText(rows[1]));
    }

    [Fact]
    public void AddChild_FiresChangedEvent()
    {
        var r     = new SubagentRenderable("abc", "Subagent(\"hi\")");
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
        var r     = new SubagentRenderable("abc", "Subagent(\"hi\")");
        var child = new TextRenderable();
        r.AddChild(child, BubbleStyle.Circle);

        var fired = false;
        r.Changed += () => fired = true;
        child.Append("more text");

        Assert.True(fired);
    }
}

// ---------------------------------------------------------------------------
// EventList — flat rendering
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
        // A lone User item renders with ● prefix. Circle at col 0 is blue.
        var list  = new EventList();
        var child = new TextRenderable();
        child.SetText("hello");
        list.Add(child);

        var rows = list.Render(40);
        Assert.Single(rows);
        // Col 0: ● (blue), col 1: space, col 2+: "hello"
        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.Blue, rows[0].Cells[0].Foreground);
        Assert.Equal(' ', RuneAt(rows[0], 1));
        Assert.Equal('h', RuneAt(rows[0], 2));
    }

    [Fact]
    public void Render_UserWithOneChild_FlatWithBlankSeparator()
    {
        // User + one Circle child: both get ● prefix, separated by a blank line.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var tool = new TextRenderable();
        tool.SetText("Read");
        list.Add(tool, BubbleStyle.Circle, () => Color.Green);

        var rows = list.Render(40);
        // Row 0: ● msg (blue), row 1: blank separator, row 2: ● Read (green)
        Assert.Equal(3, rows.Count);

        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.Blue, rows[0].Cells[0].Foreground);

        Assert.Empty(rows[1].Cells); // blank separator

        Assert.Equal('●', RuneAt(rows[2], 0));
        Assert.Equal(Color.Green, rows[2].Cells[0].Foreground);
    }

    [Fact]
    public void Render_UserWithTwoChildren_AllFlatWithSeparators()
    {
        // User + two Circle children: each gets ● prefix, blank lines between.
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
        // user + blank + c1 + blank + c2 = 5 rows
        Assert.Equal(5, rows.Count);

        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.Blue, rows[0].Cells[0].Foreground);
        Assert.Empty(rows[1].Cells); // blank
        Assert.Equal('●', RuneAt(rows[2], 0));
        Assert.Equal(Color.Yellow, rows[2].Cells[0].Foreground);
        Assert.Empty(rows[3].Cells); // blank
        Assert.Equal('●', RuneAt(rows[4], 0));
        Assert.Equal(Color.Green, rows[4].Cells[0].Foreground);
    }

    [Fact]
    public void Render_UserContinuation_IndentedWithSpaces()
    {
        // When a User message wraps, continuation rows use 2-space indent.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("hello world"); // will wrap at narrow width
        list.Add(user);

        // Width 8: circle chrome = 2 → content width = 6.
        // "hello world" at width 6 → "hello" + "world" (wraps at space).
        var rows = list.Render(8);
        Assert.Equal(2, rows.Count);

        // Row 0: ● hello (blue circle)
        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal('h', RuneAt(rows[0], 2));
        // Row 1: continuation — 2 spaces + "world"
        Assert.Equal(' ', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        Assert.Equal('w', RuneAt(rows[1], 2));
    }

    [Fact]
    public void Render_TwoGroups_FlatWithSeparators()
    {
        // Two User groups: each item gets ● prefix, blank lines between all items.
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
        // user1 + blank + c1 + blank + user2 + blank + c2 = 7 rows
        Assert.Equal(7, rows.Count);
        Assert.Equal('●', RuneAt(rows[0], 0)); // user1
        Assert.Equal(Color.Blue, rows[0].Cells[0].Foreground);
        Assert.Empty(rows[1].Cells);            // blank
        Assert.Equal('●', RuneAt(rows[2], 0)); // tool1
        Assert.Empty(rows[3].Cells);            // blank
        Assert.Equal('●', RuneAt(rows[4], 0)); // user2
        Assert.Equal(Color.Blue, rows[4].Cells[0].Foreground);
        Assert.Empty(rows[5].Cells);            // blank
        Assert.Equal('●', RuneAt(rows[6], 0)); // tool2
    }

    [Fact]
    public void Render_OrphanCircle_RenderedWithCirclePrefix()
    {
        // Circle items without a preceding User still get ● prefix.
        var list = new EventList();
        var orphan = new TextRenderable();
        orphan.SetText("welcome");
        list.Add(orphan, BubbleStyle.Circle, () => Color.BrightBlack);

        var rows = list.Render(40);
        Assert.Single(rows);
        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.BrightBlack, rows[0].Cells[0].Foreground);
    }

    [Fact]
    public void Render_OrphanFollowedByUser_BothGetCirclePrefix()
    {
        // Orphan Circle + User: both get ● prefix with blank separator.
        var list = new EventList();

        var orphan = new TextRenderable();
        orphan.SetText("welcome");
        list.Add(orphan, BubbleStyle.Circle, () => Color.BrightBlack);

        var user = new TextRenderable();
        user.SetText("hello");
        list.Add(user);

        var rows = list.Render(40);
        // orphan + blank + user = 3 rows
        Assert.Equal(3, rows.Count);
        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.BrightBlack, rows[0].Cells[0].Foreground);
        Assert.Empty(rows[1].Cells); // blank
        Assert.Equal('●', RuneAt(rows[2], 0));
        Assert.Equal(Color.Blue, rows[2].Cells[0].Foreground);
    }

    [Fact]
    public void Render_PlainItem_RendersVerbatimWithNoChrome()
    {
        // Plain items render at full width with no circle prefix.
        var list = new EventList();
        var plain = new TextRenderable();
        plain.SetText("Session: abc123");
        list.Add(plain, BubbleStyle.Plain);

        var rows = list.Render(40);
        Assert.Single(rows);
        Assert.Equal('S', RuneAt(rows[0], 0));
    }

    [Fact]
    public void Render_PlainFollowedByUserAndChild_NoSeparatorBeforePlain()
    {
        // Plain item followed by a User + Circle child. The Plain has no blank
        // separator before the first circle item (Plain is not a circle item).
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
        // Plain: 1 row. User: ● hello. Blank. Child: ● response. Total: 4.
        Assert.Equal(4, rows.Count);
        Assert.Equal('S', RuneAt(rows[0], 0)); // plain text verbatim
        Assert.Equal('●', RuneAt(rows[1], 0)); // user with blue circle
        Assert.Equal(Color.Blue, rows[1].Cells[0].Foreground);
        Assert.Empty(rows[2].Cells);            // blank separator
        Assert.Equal('●', RuneAt(rows[3], 0)); // child circle
    }

    [Fact]
    public void Render_CircleColor_PassedThroughToGlyph()
    {
        // The circle color from getCircleColor appears on the ● glyph at col 0.
        var list  = new EventList();
        var child = new TextRenderable();
        child.SetText("tool");
        list.Add(child, BubbleStyle.Circle, () => Color.Red);

        var rows = list.Render(40);
        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.Red, rows[0].Cells[0].Foreground);
    }

    [Fact]
    public void Render_CircleColorDefault_UsesWhite()
    {
        // When no getCircleColor is provided, the ● glyph defaults to white.
        var list  = new EventList();
        var child = new TextRenderable();
        child.SetText("tool");
        list.Add(child, BubbleStyle.Circle);

        var rows = list.Render(40);
        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.White, rows[0].Cells[0].Foreground);
    }

    [Fact]
    public void Render_BlankSeparatorsBetweenCircleItems()
    {
        // Circle items are separated by blank rows.
        var list = new EventList();

        var c1 = new TextRenderable();
        c1.SetText("a");
        list.Add(c1, BubbleStyle.Circle, () => Color.White);

        var c2 = new TextRenderable();
        c2.SetText("b");
        list.Add(c2, BubbleStyle.Circle, () => Color.White);

        var rows = list.Render(40);
        // c1 + blank + c2 = 3 rows
        Assert.Equal(3, rows.Count);
        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Empty(rows[1].Cells); // blank separator
        Assert.Equal('●', RuneAt(rows[2], 0));
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
    public void Render_UserCircleBlue_DoesNotAffectCircleItemColor()
    {
        // The blue circle is specific to User items — Circle items retain
        // their own color.
        var list = new EventList();
        var orphan = new TextRenderable();
        orphan.SetText("welcome");
        list.Add(orphan, BubbleStyle.Circle, () => Color.BrightBlack);

        var user = new TextRenderable();
        user.SetText("hello");
        list.Add(user);

        var rows = list.Render(40);
        // orphan + blank + user = 3 rows
        Assert.Equal(3, rows.Count);
        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.BrightBlack, rows[0].Cells[0].Foreground);
        Assert.Equal('●', RuneAt(rows[2], 0));
        Assert.Equal(Color.Blue, rows[2].Cells[0].Foreground);
    }

    [Fact]
    public void Render_CircleItem_HasCorrectChrome()
    {
        // Circle item: ● prefix at col 0, space at col 1, content at col 2.
        var list = new EventList();
        var child = new TextRenderable();
        child.SetText("data");
        list.Add(child, BubbleStyle.Circle, () => Color.Green);

        var rows = list.Render(40);
        Assert.Single(rows);

        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.Green, rows[0].Cells[0].Foreground);
        Assert.Equal(' ', RuneAt(rows[0], 1));
        Assert.Equal('d', RuneAt(rows[0], 2)); // content starts at col 2
    }

    [Fact]
    public void Render_CircleContinuation_IndentedWithSpaces()
    {
        // When a Circle item wraps, continuation rows use 2-space indent.
        var list = new EventList();
        var child = new TextRenderable();
        child.SetText("ab"); // will wrap at narrow width
        list.Add(child, BubbleStyle.Circle, () => Color.White);

        // Width 3: circle chrome = 2 → content width = 1. "ab" → "a" + "b".
        var rows = list.Render(3);
        Assert.Equal(2, rows.Count);

        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal('a', RuneAt(rows[0], 2));
        // Continuation: 2 spaces + "b"
        Assert.Equal(' ', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        Assert.Equal('b', RuneAt(rows[1], 2));
    }

    [Fact]
    public void Render_TrailingPlainAfterUser_NoExtraSeparator()
    {
        // Plain items don't get circle chrome. A User followed by a trailing
        // Plain should have no blank separator before the plain item.
        var list = new EventList();
        var user = new TextRenderable();
        user.SetText("msg");
        list.Add(user);

        var plain = new TextRenderable();
        plain.SetText("footer");
        list.Add(plain, BubbleStyle.Plain);

        var rows = list.Render(40);
        Assert.Equal(2, rows.Count);
        // Row 0: ● msg (user with blue circle)
        Assert.Equal('●', RuneAt(rows[0], 0));
        // Row 1: footer (plain, verbatim, no blank before it)
        Assert.Equal('f', RuneAt(rows[1], 0));
    }

    [Fact]
    public void Render_TwoUsersWithChildren_AllFlatWithSeparators()
    {
        // Two users each with children. All items are flat with blank separators.
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
        // user1 + blank + c1A + blank + c1B + blank + user2 + blank + c2 = 9 rows
        Assert.Equal(9, rows.Count);

        Assert.Equal('●', RuneAt(rows[0], 0));
        Assert.Equal(Color.Blue, rows[0].Cells[0].Foreground);
        Assert.Empty(rows[1].Cells);
        Assert.Equal('●', RuneAt(rows[2], 0));
        Assert.Equal(Color.Yellow, rows[2].Cells[0].Foreground);
        Assert.Empty(rows[3].Cells);
        Assert.Equal('●', RuneAt(rows[4], 0));
        Assert.Empty(rows[5].Cells);
        Assert.Equal('●', RuneAt(rows[6], 0));
        Assert.Equal(Color.Blue, rows[6].Cells[0].Foreground);
        Assert.Empty(rows[7].Cells);
        Assert.Equal('●', RuneAt(rows[8], 0));
        Assert.Equal(Color.Green, rows[8].Cells[0].Foreground);
    }
}

// ---------------------------------------------------------------------------
// ContextSection
// ---------------------------------------------------------------------------

public sealed class ContextSectionTests
{
    /// <summary>Converts a CellRow to a plain string for assertions.</summary>
    private static string RowToString(CellRow row) =>
        new(row.Cells.Select(c => c.Rune).ToArray());

    [Fact]
    public void HasContent_FalseBeforeUsageSet()
    {
        var section = new ContextSection();
        Assert.False(section.HasContent);
    }

    [Fact]
    public void HasContent_TrueAfterUsageSet()
    {
        var section = new ContextSection();
        section.SetUsage("125,000 / 250,000 - 50%");
        Assert.True(section.HasContent);
    }

    [Fact]
    public void Render_NoUsage_ShowsPlaceholder()
    {
        var section = new ContextSection();
        var rows = section.Render(30);
        var text = string.Join("\n", rows.Select(RowToString));

        Assert.Contains("Context", text);
        Assert.Contains("—", text);
    }

    [Fact]
    public void Render_WithUsage_ShowsText()
    {
        var section = new ContextSection();
        section.SetUsage("125,000 / 250,000 - 50%");
        var rows = section.Render(40);
        var text = string.Join("\n", rows.Select(RowToString));

        Assert.Contains("Context", text);
        Assert.Contains("125,000 / 250,000 - 50%", text);
    }

    [Fact]
    public void Changed_FiresOnSetUsage()
    {
        var section = new ContextSection();
        var fired = false;
        section.Changed += () => fired = true;

        section.SetUsage("1,000 / 10,000 - 10%");

        Assert.True(fired);
    }

    [Fact]
    public void Render_UsageTextIsBrightBlack()
    {
        var section = new ContextSection();
        section.SetUsage("1,000 / 10,000 - 10%");

        var rows = section.Render(40);
        // Find the row with the usage text and verify its color.
        var usageRow = rows.First(r => RowToString(r).Contains("1,000"));
        var firstDigitCell = usageRow.Cells.First(c => c.Rune == '1');
        Assert.Equal(Color.BrightBlack, firstDigitCell.Foreground);
    }
}
