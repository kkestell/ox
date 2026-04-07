namespace Ur.Tui.Rendering;

/// <summary>
/// Low-level ANSI terminal operations. This is the only place in the TUI that
/// writes raw escape sequences — everything above this layer works in terms of
/// <see cref="Cell"/> values, <see cref="CellRow"/> objects, and <see cref="ScreenBuffer"/> grids.
///
/// The primary entry point for a full-frame repaint is <see cref="Flush(ScreenBuffer)"/>,
/// which iterates every cell in the buffer and emits the minimal SGR sequence needed
/// to transition from the previous cell's attributes to the current one. This "lazy diff"
/// avoids redundant escape sequences while keeping the logic straightforward.
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
    /// blank the alternate buffer before the first Flush() paints it. After that,
    /// Flush() writes every cell so no clearing is needed between frames.
    /// </summary>
    public static void ClearScreen()
    {
        // \e[2J — erase entire display.
        Console.Write($"{Esc}2J");
    }

    // --- Cell-buffer flush ---

    /// <summary>
    /// Paints a complete frame by iterating every cell in <paramref name="buffer"/> and
    /// emitting the cursor-position and SGR sequences needed to render it.
    ///
    /// To minimize output volume, we track the "current" foreground, background, and style
    /// and only emit an SGR escape when at least one attribute changes from the previous cell.
    /// Because we visit cells in strict row/column order and move the cursor explicitly for
    /// each row, we never need to emit cursor-right moves — we just write the character and
    /// the terminal advances automatically.
    ///
    /// A final SGR reset after the last cell ensures the terminal is left in a clean state.
    /// One Console.Out.Flush() call at the very end keeps all output in a single write-system-call
    /// batch, which prevents the partial-frame flicker that would occur with per-cell flushes.
    /// </summary>
    public static void Flush(ScreenBuffer buffer)
    {
        var writer = Console.Out;

        // Null means "unknown" — forces SGR emission on the very first cell of each frame.
        Color?    curFg    = null;
        Color?    curBg    = null;
        CellStyle? curStyle = null;

        for (var row = 0; row < buffer.Height; row++)
        {
            // Position the cursor at the start of this row (1-based).
            writer.Write($"{Esc}{row + 1};1H");

            for (var col = 0; col < buffer.Width; col++)
            {
                var cell = buffer[row, col];

                // Only emit SGR when something has changed — avoids redundant escape sequences
                // for long runs of identically-styled cells (e.g. a solid-color background fill).
                if (cell.Foreground != curFg || cell.Background != curBg || cell.Style != curStyle)
                {
                    writer.Write(BuildSgr(cell.Foreground, cell.Background, cell.Style));
                    curFg    = cell.Foreground;
                    curBg    = cell.Background;
                    curStyle = cell.Style;
                }

                writer.Write(cell.Rune);
            }
        }

        // Reset all SGR attributes so the terminal is in a known state after the frame.
        writer.Write($"{Esc}0m");
        writer.Flush();
    }

    /// <summary>
    /// Builds a single SGR escape sequence that sets foreground, background, and style
    /// attributes in one round-trip. Style flags are emitted first (using the numeric SGR
    /// parameters 1–7), followed by the color parameters. The sequence always starts with
    /// a reset (SGR 0) to clear any attributes left over from the previous cell's escape.
    /// </summary>
    private static string BuildSgr(Color fg, Color bg, CellStyle style)
    {
        // Start with a full attribute reset, then re-apply what we want.
        // This is simpler than computing a minimal delta and is fast enough given that
        // we only call BuildSgr when an attribute actually changes.
        var sb = new System.Text.StringBuilder();
        sb.Append($"{Esc}0");

        if (style.HasFlag(CellStyle.Bold))      sb.Append(";1");
        if (style.HasFlag(CellStyle.Dim))       sb.Append(";2");
        if (style.HasFlag(CellStyle.Italic))    sb.Append(";3");
        if (style.HasFlag(CellStyle.Underline)) sb.Append(";4");
        if (style.HasFlag(CellStyle.Reverse))   sb.Append(";7");

        sb.Append(';');
        sb.Append(SgrForColor(fg, background: false));
        sb.Append(';');
        sb.Append(SgrForColor(bg, background: true));
        sb.Append('m');

        return sb.ToString();
    }

    /// <summary>
    /// Returns the SGR parameter string for a color, varying by whether it is used
    /// as a foreground or background color and by its <see cref="ColorKind"/>:
    ///   Default  → 39 (fg) / 49 (bg)
    ///   Basic    → 30+value (fg) / 40+value (bg)
    ///   Bright   → 90+value (fg) / 100+value (bg)
    ///   Color256 → 38;5;{value} (fg) / 48;5;{value} (bg)
    /// </summary>
    private static string SgrForColor(Color color, bool background) => color.Kind switch
    {
        ColorKind.Default  => background ? "49" : "39",
        ColorKind.Basic    => background ? $"{40 + color.Value}" : $"{30 + color.Value}",
        ColorKind.Bright   => background ? $"{100 + color.Value}" : $"{90 + color.Value}",
        ColorKind.Color256 => background ? $"48;5;{color.Value}" : $"38;5;{color.Value}",
        _                  => background ? "49" : "39"
    };

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
