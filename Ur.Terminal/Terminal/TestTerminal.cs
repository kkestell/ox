namespace Ur.Terminal.Terminal;

public sealed class TestTerminal : ITerminal
{
    private readonly List<byte[]> _writes = new();
    private readonly MemoryStream _inputStream = new();

    public int Width { get; set; } = 80;
    public int Height { get; set; } = 24;
    public IReadOnlyList<byte[]> Writes => _writes;
    public bool IsRawMode { get; private set; }
    public bool IsAlternateBuffer { get; private set; }
    public bool IsCursorHidden { get; private set; }
    public bool IsDisposed { get; private set; }

    public void EnterRawMode() => IsRawMode = true;
    public void ExitRawMode() => IsRawMode = false;
    public void EnterAlternateBuffer() => IsAlternateBuffer = true;
    public void ExitAlternateBuffer() => IsAlternateBuffer = false;
    public void HideCursor() => IsCursorHidden = true;
    public void ShowCursor() => IsCursorHidden = false;
    public void SetCursorPosition(int x, int y) { }

    public void Write(ReadOnlySpan<byte> data)
    {
        _writes.Add(data.ToArray());
    }

    public Stream OpenInput() => _inputStream;

    public void SetInputBytes(byte[] data)
    {
        _inputStream.SetLength(0);
        _inputStream.Write(data);
        _inputStream.Position = 0;
    }

    public void Dispose()
    {
        IsDisposed = true;
        _inputStream.Dispose();
    }
}
