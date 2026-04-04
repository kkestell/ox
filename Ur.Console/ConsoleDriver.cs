using System.Text;
using Ur.Drawing;

namespace Ur.Console;

/// <summary>
/// Production IDriver implementation for ANSI/VT terminals.
/// </summary>
/// <remarks>
/// All terminal interaction uses raw ANSI escape sequences written directly to stdout
/// rather than System.Console's higher-level helpers, because those helpers don't expose
/// alternate-screen or cursor-visibility control.
/// </remarks>
public sealed class ConsoleDriver : IDriver
{
    /// <summary>
    /// Gets the current terminal width in columns.
    /// </summary>
    public int Width => System.Console.WindowWidth;

    /// <summary>
    /// Gets the current terminal height in rows.
    /// </summary>
    public int Height => System.Console.WindowHeight;

    /// <summary>
    /// Initializes the terminal for TUI mode by entering alternate screen buffer,
    /// hiding the cursor, and clearing any prior content.
    /// </summary>
    public void Init()
    {
        System.Console.TreatControlCAsInput = true;
        System.Console.Write("\e[?1049h"); // Enter alternate screen buffer
        System.Console.Write("\e[?25l");   // Hide cursor
        System.Console.Write("\e[2J");     // Clear screen
        System.Console.Write("\e[H");      // Move cursor to home
        System.Console.Out.Flush();
    }

    /// <summary>
    /// Cleans up the terminal by exiting alternate screen buffer, showing the cursor,
    /// and resetting all text attributes. Called automatically via using() in AppRunner.
    /// </summary>
    public void Dispose()
    {
        System.Console.Write("\e[0m");     // Reset style
        System.Console.Write("\e[?25h");   // Show cursor
        System.Console.Write("\e[?1049l"); // Exit alternate screen buffer
        System.Console.TreatControlCAsInput = false;
        System.Console.Out.Flush();
    }

    /// <summary>
    /// Renders a complete Screen to the terminal in a single atomic write.
    /// </summary>
    /// <remarks>
    /// Batches all output into a StringBuilder before writing so the terminal sees
    /// one large write instead of thousands of small ones, dramatically reducing
    /// flicker on slower connections. Only emits style-change sequences when the
    /// style actually changes, keeping output small and avoiding redundant resets.
    /// </remarks>
    /// <param name="screen">The fully-rendered screen to display.</param>
    public void Present(Screen screen)
    {
        var sb = new StringBuilder();
        Style? currentStyle = null;

        for (ushort row = 0; row < screen.Height; row++)
        {
            // Rows and columns are 1-based in ANSI escape sequences.
            sb.Append($"\e[{row + 1};1H");

            for (ushort col = 0; col < screen.Width; col++)
            {
                var cell = screen.Get(col, row);
                if (currentStyle != cell.Style)
                {
                    AppendStyle(sb, cell.Style);
                    currentStyle = cell.Style;
                }
                sb.Append(cell.Rune);
            }
        }

        sb.Append("\e[0m");
        System.Console.Write(sb.ToString());
        System.Console.Out.Flush();
    }

    /// <summary>
    /// Blocks until the user presses a key, then returns a typed InputEvent.
    /// Control chords (Ctrl-C, Ctrl-D) are handled specially and returned as
    /// KeyEvents with the appropriate Key value. All other keys are mapped to
    /// their logical Key enum equivalent (Enter, Escape, etc.) or to Character
    /// for printable Unicode input.
    /// </summary>
    /// <returns>A KeyEvent representing the user's input, or a placeholder Unknown event if unmappable.</returns>
    public InputEvent ReadInput()
    {
        var keyInfo = System.Console.ReadKey(intercept: true);

        if (keyInfo.Key == ConsoleKey.C && keyInfo.Modifiers.HasFlag(ConsoleModifiers.Control))
            return new KeyEvent(Key.CtrlC);
        if (keyInfo.Key == ConsoleKey.D && keyInfo.Modifiers.HasFlag(ConsoleModifiers.Control))
            return new KeyEvent(Key.CtrlD);

        return keyInfo.Key switch
        {
            ConsoleKey.Enter     => new KeyEvent(Key.Enter),
            ConsoleKey.Escape    => new KeyEvent(Key.Escape),
            ConsoleKey.Backspace => new KeyEvent(Key.Backspace),
            ConsoleKey.Tab       => new KeyEvent(Key.Tab),
            ConsoleKey.UpArrow   => new KeyEvent(Key.Up),
            ConsoleKey.DownArrow => new KeyEvent(Key.Down),
            ConsoleKey.LeftArrow => new KeyEvent(Key.Left),
            ConsoleKey.RightArrow => new KeyEvent(Key.Right),
            _ => keyInfo.KeyChar != '\0'
                ? new KeyEvent(Key.Character, keyInfo.KeyChar)
                : new KeyEvent(Key.Unknown),
        };
    }

    /// <summary>
    /// Appends ANSI sequences for the given Style to the StringBuilder.
    /// </summary>
    /// <remarks>
    /// Always resets first (\e[0m) then re-applies all attributes from scratch
    /// rather than diffing individual attributes. The reset+reapply approach is
    /// simpler and the extra bytes are negligible compared to per-cell output.
    /// </remarks>
    private static void AppendStyle(StringBuilder sb, Style style)
    {
        sb.Append("\e[0m");

        var (fr, fg, fb) = style.Fg.Components;
        sb.Append($"\e[38;2;{fr};{fg};{fb}m");

        var (br, bg, bb) = style.Bg.Components;
        sb.Append($"\e[48;2;{br};{bg};{bb}m");

        if (style.Modifiers.HasFlag(Modifier.Bold))      sb.Append("\e[1m");
        if (style.Modifiers.HasFlag(Modifier.Dim))       sb.Append("\e[2m");
        if (style.Modifiers.HasFlag(Modifier.Italic))    sb.Append("\e[3m");
        if (style.Modifiers.HasFlag(Modifier.Underline)) sb.Append("\e[4m");
        if (style.Modifiers.HasFlag(Modifier.Blink))     sb.Append("\e[5m");
        if (style.Modifiers.HasFlag(Modifier.Reversed))  sb.Append("\e[7m");
        if (style.Modifiers.HasFlag(Modifier.Strike))    sb.Append("\e[9m");
    }
}
