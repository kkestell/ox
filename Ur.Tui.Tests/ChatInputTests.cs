using Ur.Terminal.Core;
using Ur.Terminal.Input;
using Ur.Tui.Components;
using Buffer = Ur.Terminal.Core.Buffer;

namespace Ur.Tui.Tests;

public class ChatInputTests
{
    private readonly ChatInput _input = new();

    private static KeyEvent Char(char c)
    {
        var key = c switch
        {
            >= 'a' and <= 'z' => (Key)(Key.A + (c - 'a')),
            >= 'A' and <= 'Z' => (Key)(Key.A + (c - 'A')),
            ' ' => Key.Space,
            '/' => Key.Unknown,
            _ => Key.Unknown,
        };
        return new KeyEvent(key, Modifiers.None, c);
    }

    private static KeyEvent Named(Key key) => new(key, Modifiers.None, null);

    private static KeyEvent ShiftEnter() => new(Key.Enter, Modifiers.Shift, null);

    private static KeyEvent Ctrl(Key key) => new(key, Modifiers.Ctrl, null);

    /// <summary>Render into a buffer to establish width for visual-row calculations.</summary>
    private void RenderAt(int width, int height)
    {
        var buffer = new Buffer(width, height);
        _input.Render(buffer, new Rect(0, 0, width, height));
    }

    [Fact]
    public void Render_ShowsBordersAndText()
    {
        var buffer = new Buffer(40, 3);
        var area = new Rect(0, 0, 40, 3);

        _input.HandleKey(Char('h'));
        _input.HandleKey(Char('e'));
        _input.HandleKey(Char('l'));
        _input.HandleKey(Char('l'));
        _input.HandleKey(Char('o'));

        _input.Render(buffer, area);

        // Top border: horizontal rule, no corners
        Assert.Equal('─', buffer.Get(0, 0).Char);
        Assert.Equal('─', buffer.Get(39, 0).Char);

        // Text on row 1 — prompt "❯ " at x=0..1, text starts at x=2
        Assert.Equal('❯', buffer.Get(0, 1).Char);
        Assert.Equal(' ', buffer.Get(1, 1).Char);
        Assert.Equal('h', buffer.Get(2, 1).Char);
        Assert.Equal('e', buffer.Get(3, 1).Char);
        Assert.Equal('l', buffer.Get(4, 1).Char);
        Assert.Equal('l', buffer.Get(5, 1).Char);
        Assert.Equal('o', buffer.Get(6, 1).Char);

        // Bottom border: horizontal rule, no corners
        Assert.Equal('─', buffer.Get(0, 2).Char);
        Assert.Equal('─', buffer.Get(39, 2).Char);
    }

    [Fact]
    public void HandleKey_PrintableChar_InsertsAtCursor()
    {
        _input.HandleKey(Char('a'));

        Assert.Equal("a", _input.Text);
    }

    [Fact]
    public void HandleKey_Backspace_DeletesBehindCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Backspace));

        Assert.Equal("a", _input.Text);
    }

    [Fact]
    public void HandleKey_Enter_ReturnsFalse()
    {
        _input.HandleKey(Char('x'));
        var consumed = _input.HandleKey(Named(Key.Enter));

        Assert.False(consumed);
    }

    [Fact]
    public void HandleKey_ArrowKeys_MovesCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Char('c'));

        // Cursor at 3 (end). Move left twice.
        _input.HandleKey(Named(Key.Left));
        _input.HandleKey(Named(Key.Left));

        // Insert 'x' at position 1
        _input.HandleKey(Char('x'));

        Assert.Equal("axbc", _input.Text);
    }

    [Fact]
    public void HandleKey_Home_MovesCursorToStart()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Home));
        _input.HandleKey(Char('x'));

        Assert.Equal("xab", _input.Text);
    }

    [Fact]
    public void HandleKey_End_MovesCursorToEnd()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Home));
        _input.HandleKey(Named(Key.End));
        _input.HandleKey(Char('c'));

        Assert.Equal("abc", _input.Text);
    }

    [Fact]
    public void HandleKey_Delete_DeletesAtCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Home));
        _input.HandleKey(Named(Key.Delete));

        Assert.Equal("b", _input.Text);
    }

    [Fact]
    public void Clear_ResetsTextAndCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.Clear();

        Assert.Equal("", _input.Text);

        // After clear, typing should start at position 0
        _input.HandleKey(Char('x'));
        Assert.Equal("x", _input.Text);
    }

    [Fact]
    public void Render_ShowsCursorAtEndAsInvertedSpace()
    {
        var buffer = new Buffer(40, 3);
        var area = new Rect(0, 0, 40, 3);

        _input.HandleKey(Char('a'));
        _input.Render(buffer, area);

        // Cursor is at col 1 (after 'a'). Prompt occupies x=0..1, text starts at x=2.
        // Cursor is at x=2+1=3.
        var cursorCell = buffer.Get(3, 1);
        Assert.Equal(' ', cursorCell.Char);
        Assert.Equal(Color.Black, cursorCell.Fg);
        Assert.Equal(Color.White, cursorCell.Bg);
    }

    [Fact]
    public void ShiftEnter_InsertsNewline()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('c'));

        Assert.Equal("ab\nc", _input.Text);
    }

    [Fact]
    public void InputHeight_GrowsWithLines()
    {
        const int width = 40;

        // 1 line -> 3 rows (top border + 1 line + bottom border)
        Assert.Equal(3, _input.MeasureHeight(width));

        _input.HandleKey(Char('a'));
        _input.HandleKey(ShiftEnter());
        // 2 lines -> 4 rows
        Assert.Equal(4, _input.MeasureHeight(width));

        _input.HandleKey(ShiftEnter());
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(ShiftEnter());
        // 5 lines -> 7 rows
        Assert.Equal(7, _input.MeasureHeight(width));

        _input.HandleKey(ShiftEnter());
        // 6 lines -> still capped at 7 rows
        Assert.Equal(7, _input.MeasureHeight(width));
    }

    [Fact]
    public void Backspace_AtLineStart_MergesWithPreviousLine()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('c'));
        _input.HandleKey(Char('d'));

        // Move to start of second line
        _input.HandleKey(Named(Key.Home));
        // Backspace merges with previous line
        _input.HandleKey(Named(Key.Backspace));

        Assert.Equal("abcd", _input.Text);
        Assert.Equal(3, _input.MeasureHeight(40)); // back to 1 line
    }

    [Fact]
    public void ShiftEnter_SplitsLineAtCursor()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Char('c'));
        _input.HandleKey(Char('d'));

        // Move cursor to middle
        _input.HandleKey(Named(Key.Left));
        _input.HandleKey(Named(Key.Left));

        // Shift+Enter splits "abcd" into "ab" and "cd"
        _input.HandleKey(ShiftEnter());

        Assert.Equal("ab\ncd", _input.Text);
    }

    [Fact]
    public void Delete_AtLineEnd_MergesNextLine()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('b'));

        // Move to end of first line
        _input.HandleKey(Named(Key.Home)); // start of line 2
        _input.HandleKey(Named(Key.Backspace)); // merge into line 1, cursor at end

        // Should now be "ab" on one line
        Assert.Equal("ab", _input.Text);
    }

    [Fact]
    public void Render_MultipleLines_ShowsAllLines()
    {
        var buffer = new Buffer(40, 4);
        var area = new Rect(0, 0, 40, 4);

        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('c'));
        _input.HandleKey(Char('d'));

        _input.Render(buffer, area);

        // Top border: horizontal rule
        Assert.Equal('─', buffer.Get(0, 0).Char);

        // Line 1 — prompt at x=0..1, text starts at x=2
        Assert.Equal('❯', buffer.Get(0, 1).Char);
        Assert.Equal('a', buffer.Get(2, 1).Char);
        Assert.Equal('b', buffer.Get(3, 1).Char);

        // Line 2 — continuation indent "  " at x=0..1, text starts at x=2
        Assert.Equal(' ', buffer.Get(0, 2).Char);
        Assert.Equal('c', buffer.Get(2, 2).Char);
        Assert.Equal('d', buffer.Get(3, 2).Char);

        // Bottom border: horizontal rule
        Assert.Equal('─', buffer.Get(0, 3).Char);
    }

    // --- Ctrl+A / Ctrl+E ---

    [Fact]
    public void CtrlA_MovesCursorToLineStart()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Char('c'));

        _input.HandleKey(Ctrl(Key.A));
        _input.HandleKey(Char('x'));

        Assert.Equal("xabc", _input.Text);
    }

    [Fact]
    public void CtrlE_MovesCursorToLineEnd()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Named(Key.Home));
        _input.HandleKey(Ctrl(Key.E));
        _input.HandleKey(Char('c'));

        Assert.Equal("abc", _input.Text);
    }

    // --- Left/Right wrap across logical lines ---

    [Fact]
    public void Left_AtLineStart_WrapsToEndOfPreviousLine()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('c'));

        // Cursor at (line 1, col 1). Move to start of line 1.
        _input.HandleKey(Named(Key.Home));
        // Left should wrap to end of line 0
        _input.HandleKey(Named(Key.Left));
        _input.HandleKey(Char('x'));

        Assert.Equal("abx\nc", _input.Text);
    }

    [Fact]
    public void Right_AtLineEnd_WrapsToStartOfNextLine()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('b'));
        _input.HandleKey(Char('c'));

        // Move to end of line 0
        _input.HandleKey(Named(Key.Home)); // start of line 1
        _input.HandleKey(Named(Key.Left)); // end of line 0 (via wrap)
        // Now Right should wrap to start of line 1
        _input.HandleKey(Named(Key.Right));
        _input.HandleKey(Char('x'));

        Assert.Equal("a\nxbc", _input.Text);
    }

    // --- Up/Down across logical lines ---

    [Fact]
    public void Up_MovesCursorToPreviousLine()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('c'));

        RenderAt(40, 5);

        _input.HandleKey(Named(Key.Up));
        _input.HandleKey(Char('x'));

        Assert.Equal("axb\nc", _input.Text);
    }

    [Fact]
    public void Down_MovesCursorToNextLine()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('b'));
        _input.HandleKey(Char('c'));

        // Move to line 0
        RenderAt(40, 5);
        _input.HandleKey(Named(Key.Up));
        // Move to col 0, then Down to start of line 1
        _input.HandleKey(Named(Key.Home));
        _input.HandleKey(Named(Key.Down));
        _input.HandleKey(Char('x'));

        Assert.Equal("a\nxbc", _input.Text);
    }

    [Fact]
    public void Up_AtFirstLine_DoesNothing()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));

        RenderAt(40, 3);
        _input.HandleKey(Named(Key.Up));
        _input.HandleKey(Char('x'));

        // Cursor was at end (col 2), Up does nothing, insert at col 2
        Assert.Equal("abx", _input.Text);
    }

    [Fact]
    public void Down_AtLastLine_DoesNothing()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('b'));

        RenderAt(40, 5);
        _input.HandleKey(Named(Key.Down));
        _input.HandleKey(Char('x'));

        // Cursor was at end of line 1, Down does nothing
        Assert.Equal("a\nbx", _input.Text);
    }

    [Fact]
    public void Up_ClampsColumnToShorterLine()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(Char('b'));
        _input.HandleKey(Char('c'));
        _input.HandleKey(Char('d'));
        _input.HandleKey(Char('e'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('x'));
        _input.HandleKey(Char('y'));

        // Cursor at (line 1, col 2). Line 0 has 5 chars.
        RenderAt(40, 5);
        _input.HandleKey(Named(Key.Up));
        _input.HandleKey(Char('z'));

        // Up preserves col=2, so insert at col 2 of line 0
        Assert.Equal("abzcde\nxy", _input.Text);
    }

    // --- Soft wrapping ---

    [Fact]
    public void MeasureHeight_AccountsForWrappedLines()
    {
        // Type 15 characters into a 12-wide area (10 columns of content): wraps to 2 visual lines
        foreach (var c in "hello world foo")
            _input.HandleKey(Char(c));

        // 2 visual rows (wrapped) + 2 borders = 4
        Assert.Equal(4, _input.MeasureHeight(12));
    }

    [Fact]
    public void Render_SoftWrapsLongLine()
    {
        // Width=10, prompt takes 2 cols, so content width=8.
        // "abcde fghij" (11 chars) wraps at word boundary after "abcde " (6 chars).
        foreach (var c in "abcde fghij")
            _input.HandleKey(Char(c));

        // 2 visual lines + 2 borders = 4 rows
        var buffer = new Buffer(10, 4);
        _input.Render(buffer, new Rect(0, 0, 10, 4));

        // First visual row: prompt at x=0..1, "abcde " at x=2..7
        Assert.Equal('a', buffer.Get(2, 1).Char);
        Assert.Equal('e', buffer.Get(6, 1).Char);

        // Second visual row: indent at x=0..1, "fghij" at x=2..6
        Assert.Equal('f', buffer.Get(2, 2).Char);
        Assert.Equal('j', buffer.Get(6, 2).Char);
    }

    [Fact]
    public void Render_HardWrapsWhenNoSpace()
    {
        // Width=10, content width=8. "abcdefghij" (10 chars) hard-breaks at 8.
        foreach (var c in "abcdefghij")
            _input.HandleKey(Char(c));

        var buffer = new Buffer(10, 4);
        _input.Render(buffer, new Rect(0, 0, 10, 4));

        // First visual row: prompt at x=0..1, "abcdefgh" at x=2..9
        Assert.Equal('a', buffer.Get(2, 1).Char);
        Assert.Equal('h', buffer.Get(9, 1).Char);

        // Second visual row: indent at x=0..1, "ij" at x=2..3
        Assert.Equal('i', buffer.Get(2, 2).Char);
        Assert.Equal('j', buffer.Get(3, 2).Char);
    }

    [Fact]
    public void Up_NavigatesAcrossWrappedVisualRows()
    {
        // "abcde fghij" at width 8 wraps within 6 content columns to "abcde " / "fghij"
        // Cursor ends at col 11 (after 'j') = visual row 1, visual col 5
        foreach (var c in "abcde fghij")
            _input.HandleKey(Char(c));

        RenderAt(8, 4);
        _input.HandleKey(Named(Key.Up));
        // Should be on visual row 0, col 5 → logical col 5 (the space)
        _input.HandleKey(Char('x'));

        Assert.Equal("abcdex fghij", _input.Text);
    }

    [Fact]
    public void Down_NavigatesAcrossWrappedVisualRows()
    {
        // "abcde fghij" at width 8 wraps within 6 content columns to "abcde " / "fghij"
        foreach (var c in "abcde fghij")
            _input.HandleKey(Char(c));

        // Move cursor to col 2 on visual row 0
        RenderAt(8, 4);
        _input.HandleKey(Named(Key.Home)); // col 0
        _input.HandleKey(Named(Key.Right));
        _input.HandleKey(Named(Key.Right)); // col 2

        _input.HandleKey(Named(Key.Down));
        // Visual row 1 col 2 → logical col 6+2 = 8
        _input.HandleKey(Char('x'));

        Assert.Equal("abcde fgxhij", _input.Text);
    }

    // --- Scrolling ---

    [Fact]
    public void Render_ScrollsToKeepCursorVisible()
    {
        // Create 7 logical lines, exceeding MaxVisibleLines (5)
        for (var i = 0; i < 6; i++)
        {
            _input.HandleKey(Char((char)('a' + i)));
            _input.HandleKey(ShiftEnter());
        }
        _input.HandleKey(Char('g'));

        // Cursor is on last line. Render should scroll so cursor is visible.
        var buffer = new Buffer(40, 7); // 5 visible + 2 borders
        _input.Render(buffer, new Rect(0, 0, 40, 7));

        // Last visible row (y=5) should show 'g' (the line with cursor) — text starts at x=2
        Assert.Equal('g', buffer.Get(2, 5).Char);

        // Move cursor to top
        for (var i = 0; i < 6; i++)
            _input.HandleKey(Named(Key.Up));

        _input.Render(buffer, new Rect(0, 0, 40, 7));

        // First visible row (y=1) should now show 'a' at x=2
        Assert.Equal('a', buffer.Get(2, 1).Char);
    }

    [Fact]
    public void Render_ScrollsWrappedContent()
    {
        // Create a long line that wraps into many visual rows at width 7
        // 25 chars into 7 columns = ceil(25/7)=4 visual rows, plus one more line = 5 visual rows.
        // With MaxVisibleLines=5 the cursor on the last row should just fit.
        foreach (var c in "abcdefghijklmnopqrstuvwxy")
            _input.HandleKey(Char(c));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('z'));

        // 5 visual rows total, max visible 5. Cursor on last row.
        var buffer = new Buffer(7, 7);
        _input.Render(buffer, new Rect(0, 0, 7, 7));

        // Last visible row should show 'z' — text starts at x=2
        Assert.Equal('z', buffer.Get(2, 5).Char);
    }

    [Fact]
    public void Delete_AtEndOfLine_MergesNextLine()
    {
        _input.HandleKey(Char('a'));
        _input.HandleKey(ShiftEnter());
        _input.HandleKey(Char('b'));

        // Move to end of first line
        _input.HandleKey(Named(Key.Home)); // start of line 1
        _input.HandleKey(Named(Key.Left)); // end of line 0

        // Delete at end merges next line
        _input.HandleKey(Named(Key.Delete));

        Assert.Equal("ab", _input.Text);
    }
}
