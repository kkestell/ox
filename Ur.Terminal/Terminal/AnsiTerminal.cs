using System.Diagnostics;
using System.Text;

namespace Ur.Terminal.Terminal;

public sealed class AnsiTerminal : ITerminal
{
    private readonly Stream _stdout;
    private bool _inRawMode;
    private bool _inAlternateBuffer;
    private bool _cursorHidden;
    private bool _disposed;

    public int Width => Console.WindowWidth;
    public int Height => Console.WindowHeight;

    public AnsiTerminal()
    {
        _stdout = Console.OpenStandardOutput();
        Console.CancelKeyPress += OnCancelKeyPress;
        AppDomain.CurrentDomain.ProcessExit += OnProcessExit;
    }

    public void EnterRawMode()
    {
        RunStty("-echo -icanon min 1");
        _inRawMode = true;
    }

    public void ExitRawMode()
    {
        if (!_inRawMode) return;
        RunStty("echo icanon");
        _inRawMode = false;
    }

    public void EnterAlternateBuffer()
    {
        WriteEscape("\x1b[?1049h");
        _inAlternateBuffer = true;
    }

    public void ExitAlternateBuffer()
    {
        if (!_inAlternateBuffer) return;
        WriteEscape("\x1b[?1049l");
        _inAlternateBuffer = false;
    }

    public void HideCursor()
    {
        WriteEscape("\x1b[?25l");
        _cursorHidden = true;
    }

    public void ShowCursor()
    {
        if (!_cursorHidden) return;
        WriteEscape("\x1b[?25h");
        _cursorHidden = false;
    }

    public void SetCursorPosition(int x, int y)
    {
        WriteEscape($"\x1b[{y + 1};{x + 1}H");
    }

    public void Write(ReadOnlySpan<byte> data)
    {
        _stdout.Write(data);
        _stdout.Flush();
    }

    public Stream OpenInput()
    {
        return new FileStream("/dev/tty", FileMode.Open, FileAccess.Read);
    }

    public void Dispose()
    {
        if (_disposed) return;
        _disposed = true;

        ShowCursor();
        WriteEscape("\x1b[0m");
        ExitAlternateBuffer();
        ExitRawMode();

        Console.CancelKeyPress -= OnCancelKeyPress;
        AppDomain.CurrentDomain.ProcessExit -= OnProcessExit;
    }

    private void OnCancelKeyPress(object? sender, ConsoleCancelEventArgs e)
    {
        Dispose();
    }

    private void OnProcessExit(object? sender, EventArgs e)
    {
        Dispose();
    }

    private void WriteEscape(string sequence)
    {
        var bytes = Encoding.UTF8.GetBytes(sequence);
        _stdout.Write(bytes);
        _stdout.Flush();
    }

    private static void RunStty(string args)
    {
        Process.Start(new ProcessStartInfo
        {
            FileName = "/bin/stty",
            Arguments = args,
            UseShellExecute = false,
        })?.WaitForExit();
    }
}
