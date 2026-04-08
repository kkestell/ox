namespace Ox.Rendering;

/// <summary>
/// Low-level ANSI terminal lifecycle helpers. This is the only place in the TUI
/// that writes the escape sequences for alternate-buffer management and cursor
/// visibility — the narrow subset of terminal control that belongs to Ox.
///
/// Cell-level rendering (SGR, cursor positioning, cell diffing) has moved to
/// <c>Te.Rendering.ConsoleBuffer</c>, which owns the double-buffer and calls
/// <c>Render(Console.Out)</c> to emit only changed cells each frame.
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

    // --- Screen clearing ---

    /// <summary>
    /// Clears the entire visible screen. Called once in <see cref="Viewport.Start"/> to
    /// blank the alternate buffer before the first ConsoleBuffer.Render() paints it.
    /// After that, ConsoleBuffer.Render() writes only changed cells so no clearing is
    /// needed between frames.
    /// </summary>
    public static void ClearScreen()
    {
        // \e[2J — erase entire display.
        Console.Write($"{Esc}2J");
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
