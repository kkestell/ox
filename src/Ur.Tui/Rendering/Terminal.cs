namespace Ur.Tui.Rendering;

/// <summary>
/// Low-level ANSI terminal operations. This is the only place in the TUI that
/// writes raw escape sequences — everything above this layer works in terms of
/// rows, columns, and text.
///
/// All methods write directly to Console.Out. They are intentionally not
/// testable in isolation — correctness is verified by visual inspection.
/// The sequences used here are part of the VT100/ANSI standard and are
/// supported by all modern terminal emulators (Terminal.app, iTerm2, xterm,
/// Windows Terminal).
/// </summary>
internal static class Terminal
{
    // ANSI escape sequence prefix. All VT100 sequences begin with ESC ([).
    private const string Esc = "\e[";

    // --- Alternate screen buffer ---
    // The alternate buffer is a separate display surface. Entering it saves the
    // user's existing terminal content; exiting restores it. This is the standard
    // mechanism used by vim, less, man, etc. to take over the screen without
    // destroying the user's scroll history.

    /// <summary>Enters the alternate screen buffer and clears it.</summary>
    public static void EnterAlternateBuffer()
    {
        // \e[?1049h — switch to alternate screen buffer (xterm extension,
        // universally supported in modern terminals).
        Console.Write($"{Esc}?1049h");
    }

    /// <summary>Exits the alternate screen buffer, restoring the previous display.</summary>
    public static void ExitAlternateBuffer()
    {
        // \e[?1049l — switch back to the primary screen buffer.
        Console.Write($"{Esc}?1049l");
    }

    // --- Cursor visibility ---

    /// <summary>Hides the blinking cursor to prevent flicker during redraws.</summary>
    public static void HideCursor()
    {
        // \e[?25l — hide cursor (VT220 extension).
        Console.Write($"{Esc}?25l");
    }

    /// <summary>Restores cursor visibility. Must be called before exit to avoid leaving
    /// the terminal without a visible cursor.</summary>
    public static void ShowCursor()
    {
        // \e[?25h — show cursor.
        Console.Write($"{Esc}?25h");
    }

    // --- Cursor movement ---

    /// <summary>
    /// Moves the cursor to a 1-based (row, col) position on screen.
    /// Row 1 is the top of the screen; col 1 is the left edge.
    /// </summary>
    public static void MoveCursor(int row, int col)
    {
        // \e[{row};{col}H — cursor position (CUP). Both values are 1-based.
        Console.Write($"{Esc}{row};{col}H");
    }

    // --- Screen / line clearing ---

    /// <summary>Clears the entire visible screen without moving the cursor.</summary>
    public static void ClearScreen()
    {
        // \e[2J — erase entire display.
        Console.Write($"{Esc}2J");
    }

    /// <summary>
    /// Clears from the cursor to the end of the current line.
    /// Used after writing a content line to erase any leftover characters from a
    /// previous longer line in the same row.
    /// </summary>
    public static void ClearToEndOfLine()
    {
        // \e[0K — erase from cursor to end of line (EL with param 0).
        Console.Write($"{Esc}0K");
    }

    // --- Compound operations ---

    /// <summary>
    /// Moves to (row, col) and writes text, then clears to end-of-line.
    /// This is the primary building block for the viewport redraw loop:
    /// position, write content, erase leftovers from the previous frame.
    /// </summary>
    public static void Write(int row, int col, string text)
    {
        MoveCursor(row, col);
        Console.Write(text);
        ClearToEndOfLine();
    }

    // --- Terminal size ---

    /// <summary>
    /// Returns the current terminal dimensions as (width, height).
    /// Width and height are in character cells. Falls back to 80×24 if the
    /// terminal reports zero (e.g. when stdout is redirected).
    /// </summary>
    public static (int Width, int Height) GetSize()
    {
        var w = Console.WindowWidth;
        var h = Console.WindowHeight;
        // Zero dimensions can happen when stdout is not a real terminal.
        return (w > 0 ? w : 80, h > 0 ? h : 24);
    }
}
