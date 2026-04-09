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

        // EventList wraps the user text with tree chrome (5 cols for ChildChrome),
        // so 'h' starts at x=5, y=0. Scan row 0 to find it.
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
        tool.SetCompleted(isError: false, result: null);
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
        tool.SetCompleted(isError: true, result: null);
        var rows = tool.Render(80);
        var text = RowText(rows[0]);
        Assert.Contains("read_file", text);
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
        Assert.Contains("read_file", text);
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
    public void Render_UserContinuationWithChildren_VerticalBarAlignedWithNestedChildren()
    {
        // When the last top-level User message wraps and has nested children,
        // continuation rows place │ at column 3 — aligned with the nested
        // children's ├/└ — not at column 0 (which would falsely imply more
        // top-level siblings below).
        //
        // Correct:
        //   └─ ● hello
        //      │ world
        //      └─ ● ok
        //
        // Wrong (│ at col 0):
        //   └─ ● hello
        //   │    world
        //      └─ ● ok
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
        // Row 1: continuation — `   │ world` (│ at col 3, aligned with nested children)
        Assert.Equal(' ', RuneAt(rows[1], 0));
        Assert.Equal(' ', RuneAt(rows[1], 1));
        Assert.Equal(' ', RuneAt(rows[1], 2));
        Assert.Equal('│', RuneAt(rows[1], 3));
        Assert.Equal(' ', RuneAt(rows[1], 4));
        Assert.Equal('w', RuneAt(rows[1], 5)); // "world" content starts at col 5
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
