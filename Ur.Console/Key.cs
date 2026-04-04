namespace Ur.Console;

/// <summary>
/// Enumeration of logical keys the application can reason about.
/// </summary>
/// <remarks>
/// Character is a catch-all for printable Unicode; the actual glyph lives in
/// KeyEvent.Char. Everything else is a named special or control key.
/// Unknown is used when ConsoleDriver receives a key it can't map.
/// </remarks>
public enum Key
{
    /// <summary>
    /// Printable Unicode character (the actual character is in KeyEvent.Char).
    /// </summary>
    Character,

    /// <summary>
    /// Enter/Return key.
    /// </summary>
    Enter,

    /// <summary>
    /// Escape key.
    /// </summary>
    Escape,

    /// <summary>
    /// Backspace key.
    /// </summary>
    Backspace,

    /// <summary>
    /// Tab key.
    /// </summary>
    Tab,

    /// <summary>
    /// Up arrow key.
    /// </summary>
    Up,

    /// <summary>
    /// Down arrow key.
    /// </summary>
    Down,

    /// <summary>
    /// Left arrow key.
    /// </summary>
    Left,

    /// <summary>
    /// Right arrow key.
    /// </summary>
    Right,

    /// <summary>
    /// Ctrl-C keyboard shortcut.
    /// </summary>
    CtrlC,

    /// <summary>
    /// Ctrl-D keyboard shortcut.
    /// </summary>
    CtrlD,

    /// <summary>
    /// Unmappable key. Should be ignored by applications.
    /// </summary>
    Unknown,
}
