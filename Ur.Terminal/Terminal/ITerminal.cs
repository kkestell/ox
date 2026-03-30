namespace Ur.Terminal.Terminal;

public interface ITerminal : IDisposable
{
    int Width { get; }
    int Height { get; }
    void EnterRawMode();
    void ExitRawMode();
    void EnterAlternateBuffer();
    void ExitAlternateBuffer();
    void HideCursor();
    void ShowCursor();
    void SetCursorPosition(int x, int y);
    void Write(ReadOnlySpan<byte> data);
    Stream OpenInput();
}
