using System.Diagnostics;
using System.Text;

namespace Ur.Terminal.Terminal;

public sealed class AnsiTerminal : ITerminal
{
    private const string EnterAlternateBufferSequence = "\x1b[?1049h";
    private const string ExitAlternateBufferSequence = "\x1b[?1049l";
    private const string HideCursorSequence = "\x1b[?25l";
    private const string ShowCursorSequence = "\x1b[?25h";
    private const string ResetFormattingSequence = "\x1b[0m";
    private const string EnableKittyKeyboardSequence = "\x1b[>27u";
    private const string DisableKittyKeyboardSequence = "\x1b[<u";

    private readonly Stream _stdout;
    private bool _inRawMode;
    private bool _inAlternateBuffer;
    private bool _cursorHidden;
    private bool _kittyKeyboardEnabled;
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
        if (_inRawMode)
            return;

        RunStty("-echo -icanon min 0 time 1");
        _inRawMode = true;
    }

    public void ExitRawMode()
    {
        if (!_inRawMode) return;
        DisableKittyKeyboard();
        RunStty("echo icanon");
        _inRawMode = false;
    }

    public void EnterAlternateBuffer()
    {
        WriteEscape(EnterAlternateBufferSequence);
        _inAlternateBuffer = true;

        if (_inRawMode)
            ReassertKittyKeyboard();
    }

    public void ExitAlternateBuffer()
    {
        if (!_inAlternateBuffer) return;
        WriteEscape(ExitAlternateBufferSequence);
        _inAlternateBuffer = false;
    }

    public void HideCursor()
    {
        WriteEscape(HideCursorSequence);
        _cursorHidden = true;
    }

    public void ShowCursor()
    {
        if (!_cursorHidden) return;
        WriteEscape(ShowCursorSequence);
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
        WriteEscape(ResetFormattingSequence);
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

    private void EnableKittyKeyboard()
    {
        if (_kittyKeyboardEnabled)
            return;

        WriteEscape(EnableKittyKeyboardSequence);
        _kittyKeyboardEnabled = true;
    }

    private void ReassertKittyKeyboard()
    {
        WriteEscape(EnableKittyKeyboardSequence);
        _kittyKeyboardEnabled = true;
    }

    private void DisableKittyKeyboard()
    {
        if (!_kittyKeyboardEnabled)
            return;

        WriteEscape(DisableKittyKeyboardSequence);
        _kittyKeyboardEnabled = false;
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
