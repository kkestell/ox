using System.Collections.Concurrent;
using System.Text;
using Te.Input;
using Te.Rendering;

namespace Te.Demo;

/// <summary>
/// Small interactive demo for the extracted Te slice.
/// The goal is not to invent a new framework layer yet. Instead, this keeps the
/// sample close to the raw primitives so future extraction decisions can be made
/// against concrete usage rather than speculation.
/// </summary>
public static class Program
{
    private const string Escape = "\u001b[";
    private const string Prompt = "Arrows move, Tab changes color, Esc/Q exits";
    private const int EventHistoryLimit = 8;
    private const int TitleRow = 1;
    private const int PromptRow = 2;
    private const int ContentStartRow = 4;

    public static async Task Main()
    {
        if (Console.IsOutputRedirected)
        {
            Console.Error.WriteLine("Te demo requires an interactive terminal.");
            return;
        }

        using var inputSource = new TerminalInputSource(new TerminalInputSourceOptions
        {
            EnableMouse = true,
            EnableMouseMove = false,
        });
        using var coordinator = new InputCoordinator(inputSource);
        var state = new DemoState();
        ConsoleCancelEventHandler cancelHandler = (_, e) =>
        {
            // Keep shutdown inside the normal loop so alternate-screen cleanup
            // always happens in the finally block.
            e.Cancel = true;
            state.ShouldExit = true;
        };

        // Events fire eagerly on enqueue (on the background stdin reader thread),
        // so these handlers update state immediately. The main loop just redraws.
        coordinator.KeyReceived += (_, args) => HandleKey(state, args);
        coordinator.MouseReceived += (_, args) => HandleMouse(state, args);

        Console.CancelKeyPress += cancelHandler;

        try
        {
            EnterAlternateScreen();
            HideCursor();
            await RunDemoAsync(coordinator, state);
        }
        finally
        {
            ShowCursor();
            ExitAlternateScreen();
            Console.CancelKeyPress -= cancelHandler;
        }
    }

    private static async Task RunDemoAsync(InputCoordinator coordinator, DemoState state)
    {
        var (width, height) = GetTerminalSize();
        var buffer = new ConsoleBuffer(width, height);
        state.ClampToBounds(width, height);

        while (!state.ShouldExit)
        {
            var (nextWidth, nextHeight) = GetTerminalSize();
            if (nextWidth != buffer.Width || nextHeight != buffer.Height)
            {
                buffer.Resize(nextWidth, nextHeight);
                state.ClampToBounds(nextWidth, nextHeight);
            }

            // Events already fired eagerly during enqueue (handlers ran on the
            // stdin reader thread). Drain the channel to prevent unbounded growth,
            // then redraw.
            while (coordinator.Reader.TryRead(out _)) { }

            DrawFrame(buffer, state);
            buffer.Render(Console.Out);

            // Block until the next input event arrives — no polling, no Thread.Sleep.
            // WaitToReadAsync returns false when the channel completes (coordinator disposed).
            if (!await coordinator.Reader.WaitToReadAsync())
                break;
        }
    }

    private static void HandleKey(DemoState state, KeyEventArgs args)
    {
        var keyCode = args.KeyCode.WithoutModifiers();
        switch (keyCode)
        {
            case KeyCode.CursorLeft:
                state.PlayerX--;
                break;
            case KeyCode.CursorRight:
                state.PlayerX++;
                break;
            case KeyCode.CursorUp:
                state.PlayerY--;
                break;
            case KeyCode.CursorDown:
                state.PlayerY++;
                break;
            case KeyCode.Tab:
                state.AdvanceColor();
                break;
            case KeyCode.C when args.KeyCode.HasCtrl():
                state.ShouldExit = true;
                break;
            case KeyCode.Esc:
            case KeyCode.Q:
                state.ShouldExit = true;
                break;
        }

        state.RecordEvent(DescribeKey(args));
    }

    private static void HandleMouse(DemoState state, MouseEventArgs args)
    {
        state.LastMouseSummary = DescribeMouse(args);

        if (args.HasAnyFlag(MouseFlags.Button1Pressed, MouseFlags.Button1Dragged))
        {
            state.PlayerX = args.Position.X;
            state.PlayerY = args.Position.Y;
        }

        if (ShouldRecordMouseEvent(args))
            state.RecordEvent(DescribeMouseEventForHistory(args));
    }

    private static void DrawFrame(ConsoleBuffer buffer, DemoState state)
    {
        buffer.Clear();

        var width = buffer.Width;
        var height = buffer.Height;
        var mouseRow = Math.Max(ContentStartRow, height - 4);
        var statusRow = Math.Max(ContentStartRow, height - 3);
        var recentKeysRow = Math.Max(ContentStartRow, height - 2);
        var eventAreaTop = ContentStartRow;
        var eventAreaBottom = Math.Max(eventAreaTop - 1, mouseRow - 1);

        DrawBorder(buffer, width, height);
        WriteText(buffer, 2, TitleRow, "Te Demo", Color.Cyan, Color.Default, TextDecoration.Bold);
        WriteText(buffer, 2, PromptRow, $"{Prompt}, click/drag moves @", Color.BrightBlack, Color.Default);

        WriteText(buffer, 2, mouseRow, state.LastMouseSummary, Color.Magenta, Color.Default);
        var status = $"Position: ({state.PlayerX}, {state.PlayerY})   Color: {state.PlayerColor.Kind}/{state.PlayerColor.Value}";
        WriteText(buffer, 2, statusRow, status, Color.Yellow, Color.Default);
        WriteText(buffer, 2, recentKeysRow, "Recent keys:", Color.Green, Color.Default);

        var events = state.GetRecentEvents();
        var availableEventRows = Math.Max(0, eventAreaBottom - eventAreaTop + 1);
        var visibleEventCount = Math.Min(events.Count, availableEventRows);
        var firstVisibleIndex = Math.Max(0, events.Count - visibleEventCount);
        var firstVisibleRow = eventAreaBottom - visibleEventCount + 1;

        for (var i = 0; i < visibleEventCount; i++)
            WriteText(buffer, 4, firstVisibleRow + i, events[firstVisibleIndex + i], Color.White, Color.Default);

        state.ClampToBounds(width, height);
        buffer.SetCell(state.PlayerX, state.PlayerY, '@', state.PlayerColor, Color.Default, TextDecoration.Bold);
    }

    private static void DrawBorder(ConsoleBuffer buffer, int width, int height)
    {
        if (width < 2 || height < 2)
            return;

        buffer.FillCells(0, 0, width, '─', Color.BrightBlack, Color.Default);
        buffer.FillCells(0, height - 1, width, '─', Color.BrightBlack, Color.Default);

        for (var row = 0; row < height; row++)
        {
            buffer.SetCell(0, row, '│', Color.BrightBlack, Color.Default);
            buffer.SetCell(width - 1, row, '│', Color.BrightBlack, Color.Default);
        }

        buffer.SetCell(0, 0, '┌', Color.BrightBlack, Color.Default);
        buffer.SetCell(width - 1, 0, '┐', Color.BrightBlack, Color.Default);
        buffer.SetCell(0, height - 1, '└', Color.BrightBlack, Color.Default);
        buffer.SetCell(width - 1, height - 1, '┘', Color.BrightBlack, Color.Default);
    }

    private static void WriteText(
        ConsoleBuffer buffer,
        int x,
        int y,
        string text,
        Color foreground,
        Color background,
        TextDecoration decorations = TextDecoration.None)
    {
        if (y < 0 || y >= buffer.Height)
            return;

        for (var i = 0; i < text.Length; i++)
        {
            var column = x + i;
            if (column < 0 || column >= buffer.Width)
                continue;

            buffer.SetCell(column, y, text[i], foreground, background, decorations);
        }
    }

    private static string DescribeKey(KeyEventArgs args)
    {
        var builder = new StringBuilder();
        if (args.KeyCode.HasCtrl())
            builder.Append("Ctrl+");
        if (args.KeyCode.HasAlt())
            builder.Append("Alt+");
        if (args.KeyCode.HasShift())
            builder.Append("Shift+");
        builder.Append(args.KeyCode.WithoutModifiers());
        return builder.ToString();
    }

    private static string DescribeMouse(MouseEventArgs args) =>
        $"Mouse: ({args.Position.X}, {args.Position.Y}) {FormatFlags(args.Flags)}";

    private static string DescribeMouseEventForHistory(MouseEventArgs args) =>
        $"mouse {args.Position.X},{args.Position.Y} {FormatFlags(args.Flags)}";

    private static bool ShouldRecordMouseEvent(MouseEventArgs args) =>
        args.HasAnyFlag(
            MouseFlags.Button1Pressed,
            MouseFlags.Button1Released,
            MouseFlags.Button2Pressed,
            MouseFlags.Button2Released,
            MouseFlags.Button3Pressed,
            MouseFlags.Button3Released,
            MouseFlags.WheeledUp,
            MouseFlags.WheeledDown,
            MouseFlags.WheeledLeft,
            MouseFlags.WheeledRight) &&
        !args.HasAnyFlag(MouseFlags.Button1Dragged, MouseFlags.Button2Dragged, MouseFlags.Button3Dragged);

    private static string FormatFlags(IReadOnlyList<MouseFlags> flags) =>
        flags.Count == 0 ? "None" : string.Join(",", flags);

    private static (int Width, int Height) GetTerminalSize()
    {
        var width = Console.WindowWidth;
        var height = Console.WindowHeight;
        return (Math.Max(20, width), Math.Max(10, height));
    }

    private static void EnterAlternateScreen() => Console.Write($"{Escape}?1049h");

    private static void ExitAlternateScreen() => Console.Write($"{Escape}?1049l");

    private static void HideCursor() => Console.Write($"{Escape}?25l");

    private static void ShowCursor() => Console.Write($"{Escape}?25h");

    private sealed class DemoState
    {
        private readonly ConcurrentQueue<string> _recentEvents = new();
        private readonly Color[] _playerColors =
        [
            Color.Cyan,
            Color.Green,
            Color.Yellow,
            Color.Magenta,
            Color.FromIndex(208),
        ];

        private int _colorIndex;

        public int PlayerX { get; set; } = 10;
        public int PlayerY { get; set; } = 5;
        public bool ShouldExit { get; set; }
        public string LastMouseSummary { get; set; } = "Mouse: waiting for input";
        public Color PlayerColor => _playerColors[_colorIndex];

        public void AdvanceColor() => _colorIndex = (_colorIndex + 1) % _playerColors.Length;

        public void ClampToBounds(int width, int height)
        {
            PlayerX = Math.Clamp(PlayerX, 1, Math.Max(1, width - 2));
            PlayerY = Math.Clamp(PlayerY, 3, Math.Max(3, height - 4));
        }

        public void RecordEvent(string value)
        {
            _recentEvents.Enqueue(value);
            while (_recentEvents.Count > EventHistoryLimit && _recentEvents.TryDequeue(out _))
            {
            }
        }

        public IReadOnlyList<string> GetRecentEvents() => [.. _recentEvents];
    }
}
