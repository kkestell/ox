using System.Buffers;
using System.Text;
using Ur.Terminal.Core;

namespace Ur.Terminal.Rendering;

public static class Screen
{
    public static void WriteDiff(Core.Buffer current, Core.Buffer previous, IBufferWriter<byte> output)
    {
        var width = current.Width;
        var height = current.Height;
        var lastX = -1;
        var lastY = -1;
        var lastFg = new Color(255, 255, 255);
        var lastBg = new Color(0, 0, 0);
        var colorInitialized = false;

        for (var y = 0; y < height; y++)
        for (var x = 0; x < width; x++)
        {
            var cur = current.Get(x, y);
            var prev = previous.Get(x, y);

            if (cur == prev)
                continue;

            if (cur.IsTransparent)
                continue;

            var needsMove = !(lastY == y && lastX == x);
            if (needsMove)
                WriteCursorPosition(output, x, y);

            if (!colorInitialized || cur.Fg != lastFg)
            {
                WriteFgColor(output, cur.Fg);
                lastFg = cur.Fg;
            }

            if (!colorInitialized || cur.Bg != lastBg)
            {
                WriteBgColor(output, cur.Bg);
                lastBg = cur.Bg;
            }

            colorInitialized = true;
            WriteChar(output, cur.Char);
            lastX = x + 1;
            lastY = y;
        }
    }

    public static void WriteFullFrame(Core.Buffer buffer, IBufferWriter<byte> output)
    {
        var width = buffer.Width;
        var height = buffer.Height;
        var lastFg = new Color(255, 255, 255);
        var lastBg = new Color(0, 0, 0);
        var colorInitialized = false;

        WriteCursorPosition(output, 0, 0);

        for (var y = 0; y < height; y++)
        {
            if (y > 0)
                WriteCursorPosition(output, 0, y);

            for (var x = 0; x < width; x++)
            {
                var cell = buffer.Get(x, y);
                if (cell.IsTransparent)
                {
                    WriteChar(output, ' ');
                    continue;
                }

                if (!colorInitialized || cell.Fg != lastFg)
                {
                    WriteFgColor(output, cell.Fg);
                    lastFg = cell.Fg;
                }

                if (!colorInitialized || cell.Bg != lastBg)
                {
                    WriteBgColor(output, cell.Bg);
                    lastBg = cell.Bg;
                }

                colorInitialized = true;
                WriteChar(output, cell.Char);
            }
        }
    }

    private static void WriteCursorPosition(IBufferWriter<byte> output, int x, int y)
    {
        Span<byte> buf = stackalloc byte[16];
        var written = Encoding.ASCII.GetBytes($"\x1b[{y + 1};{x + 1}H", buf);
        var span = output.GetSpan(written);
        buf[..written].CopyTo(span);
        output.Advance(written);
    }

    private static void WriteFgColor(IBufferWriter<byte> output, Color c)
    {
        Span<byte> buf = stackalloc byte[24];
        var written = Encoding.ASCII.GetBytes($"\x1b[38;2;{c.R};{c.G};{c.B}m", buf);
        var span = output.GetSpan(written);
        buf[..written].CopyTo(span);
        output.Advance(written);
    }

    private static void WriteBgColor(IBufferWriter<byte> output, Color c)
    {
        Span<byte> buf = stackalloc byte[24];
        var written = Encoding.ASCII.GetBytes($"\x1b[48;2;{c.R};{c.G};{c.B}m", buf);
        var span = output.GetSpan(written);
        buf[..written].CopyTo(span);
        output.Advance(written);
    }

    private static void WriteChar(IBufferWriter<byte> output, char c)
    {
        var span = output.GetSpan(4);
        var written = Encoding.UTF8.GetBytes([c], span);
        output.Advance(written);
    }
}
