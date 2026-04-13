namespace Ox.Terminal.Input;

/// <summary>
/// Minimal keyboard event payload.
/// Te keeps both the normalized <see cref="KeyCode"/> and the optional original
/// <see cref="ConsoleKeyInfo"/> so callers can build portable logic first and
/// still inspect the platform-specific source when needed.
/// </summary>
public sealed class KeyEventArgs : EventArgs
{
    public KeyCode KeyCode { get; }
    public char KeyChar { get; }
    public ConsoleKeyInfo? ConsoleKeyInfo { get; }
    public DateTime Timestamp { get; }
    public bool Handled { get; set; }

    public KeyEventArgs(KeyCode keyCode, char keyChar = '\0', ConsoleKeyInfo? consoleKeyInfo = null, DateTime? timestamp = null)
    {
        KeyCode = keyCode;
        KeyChar = keyChar;
        ConsoleKeyInfo = consoleKeyInfo;
        Timestamp = timestamp ?? DateTime.UtcNow;
    }

    public static KeyEventArgs FromConsoleKeyInfo(ConsoleKeyInfo keyInfo) =>
        new(KeyCodeExtensions.FromConsoleKeyInfo(keyInfo), keyInfo.KeyChar, keyInfo, DateTime.UtcNow);
}
