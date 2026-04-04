using Ur.Drawing;

namespace Ur.Console;

/// <summary>
/// Abstraction for terminal I/O operations.
/// </summary>
/// <remarks>
/// Keeping this interface thin lets tests swap in a fake driver (no TTY needed)
/// and would allow a future ncurses or web-socket backend without touching AppRunner.
/// </remarks>
public interface IDriver : IDisposable
{
    /// <summary>
    /// Gets the current terminal width in columns.
    /// </summary>
    int Width { get; }

    /// <summary>
    /// Gets the current terminal height in rows.
    /// </summary>
    int Height { get; }

    /// <summary>
    /// Initializes the terminal for TUI mode (alternate buffer, hidden cursor, etc.).
    /// Must be called once before any Present or ReadInput calls.
    /// </summary>
    void Init();

    /// <summary>
    /// Renders a fully-rendered Screen to the display in a single atomic operation.
    /// Batches all output before flushing to minimize flicker.
    /// </summary>
    /// <param name="screen">The complete rendered screen to display.</param>
    void Present(Screen screen);

    /// <summary>
    /// Blocks until the user produces an input event, then returns it as a typed InputEvent.
    /// </summary>
    /// <remarks>
    /// Returning a typed InputEvent (rather than raw ConsoleKeyInfo) keeps
    /// the rest of the system decoupled from System.Console specifics.
    /// </remarks>
    /// <returns>The next input event from the user.</returns>
    InputEvent ReadInput();
}
