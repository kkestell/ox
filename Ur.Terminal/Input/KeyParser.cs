namespace Ur.Terminal.Input;

public static class KeyParser
{
    public static KeyEvent? Parse(ReadOnlySpan<byte> input, out int consumed)
    {
        consumed = 0;
        if (input.IsEmpty)
            return null;

        var b = input[0];

        // Escape or CSI sequence
        if (b == 0x1B)
            return ParseEscape(input, out consumed);

        // Ctrl+A through Ctrl+Z (0x01-0x1A), except special cases
        if (b >= 0x01 && b <= 0x1A)
            return ParseCtrl(b, out consumed);

        // Backspace (0x7F)
        if (b == 0x7F)
        {
            consumed = 1;
            return new KeyEvent(Key.Backspace, Modifiers.None, null);
        }

        // Space
        if (b == 0x20)
        {
            consumed = 1;
            return new KeyEvent(Key.Space, Modifiers.None, ' ');
        }

        // Printable ASCII
        if (b >= 0x21 && b <= 0x7E)
        {
            consumed = 1;
            var key = MapPrintable(b);
            return new KeyEvent(key, Modifiers.None, (char)b);
        }

        // Unknown byte
        consumed = 1;
        return new KeyEvent(Key.Unknown, Modifiers.None, null);
    }

    private static KeyEvent? ParseEscape(ReadOnlySpan<byte> input, out int consumed)
    {
        consumed = 0;

        // Bare escape — only if no more bytes follow
        if (input.Length == 1)
        {
            consumed = 1;
            return new KeyEvent(Key.Escape, Modifiers.None, null);
        }

        // Not a CSI sequence — treat as bare Escape
        if (input[1] != 0x5B)
        {
            consumed = 1;
            return new KeyEvent(Key.Escape, Modifiers.None, null);
        }

        // CSI sequence: \e[...
        if (input.Length < 3)
        {
            // Incomplete CSI — need more data
            return null;
        }

        return ParseCsi(input, out consumed);
    }

    private static KeyEvent? ParseCsi(ReadOnlySpan<byte> input, out int consumed)
    {
        consumed = 0;
        var b = input[2];

        // Simple single-byte CSI finals
        switch (b)
        {
            case 0x41:
                consumed = 3;
                return new KeyEvent(Key.Up, Modifiers.None, null);
            case 0x42:
                consumed = 3;
                return new KeyEvent(Key.Down, Modifiers.None, null);
            case 0x43:
                consumed = 3;
                return new KeyEvent(Key.Right, Modifiers.None, null);
            case 0x44:
                consumed = 3;
                return new KeyEvent(Key.Left, Modifiers.None, null);
            case 0x48:
                consumed = 3;
                return new KeyEvent(Key.Home, Modifiers.None, null);
            case 0x46:
                consumed = 3;
                return new KeyEvent(Key.End, Modifiers.None, null);
        }

        // If the byte is not a parameter digit (0x30-0x39), it's an unknown CSI final
        if (b < 0x30 || b > 0x39)
        {
            consumed = 3;
            return new KeyEvent(Key.Unknown, Modifiers.None, null);
        }

        // Extended CSI sequences: \e[N~ format
        if (input.Length < 4)
            return null;

        if (input[3] == 0x7E)
        {
            consumed = 4;
            return b switch
            {
                0x33 => new KeyEvent(Key.Delete, Modifiers.None, null),
                0x35 => new KeyEvent(Key.PageUp, Modifiers.None, null),
                0x36 => new KeyEvent(Key.PageDown, Modifiers.None, null),
                _ => new KeyEvent(Key.Unknown, Modifiers.None, null),
            };
        }

        // Function keys: \e[1N~ format (F1-F4: \e[11~ through \e[14~)
        if (b == 0x31 && input.Length >= 5 && input[4] == 0x7E)
        {
            consumed = 5;
            return input[3] switch
            {
                0x31 => new KeyEvent(Key.F1, Modifiers.None, null), // \e[11~
                0x32 => new KeyEvent(Key.F2, Modifiers.None, null), // \e[12~
                0x33 => new KeyEvent(Key.F3, Modifiers.None, null), // \e[13~
                0x34 => new KeyEvent(Key.F4, Modifiers.None, null), // \e[14~
                0x35 => new KeyEvent(Key.F5, Modifiers.None, null), // \e[15~
                0x37 => new KeyEvent(Key.F6, Modifiers.None, null), // \e[17~
                0x38 => new KeyEvent(Key.F7, Modifiers.None, null), // \e[18~
                0x39 => new KeyEvent(Key.F8, Modifiers.None, null), // \e[19~
                _ => new KeyEvent(Key.Unknown, Modifiers.None, null),
            };
        }

        // Function keys: \e[2N~ format (F9-F12: \e[20~ through \e[24~)
        if (b == 0x32 && input.Length >= 5 && input[4] == 0x7E)
        {
            consumed = 5;
            return input[3] switch
            {
                0x30 => new KeyEvent(Key.F9, Modifiers.None, null),  // \e[20~
                0x31 => new KeyEvent(Key.F10, Modifiers.None, null), // \e[21~
                0x33 => new KeyEvent(Key.F11, Modifiers.None, null), // \e[23~
                0x34 => new KeyEvent(Key.F12, Modifiers.None, null), // \e[24~
                _ => new KeyEvent(Key.Unknown, Modifiers.None, null),
            };
        }

        // Unknown CSI
        consumed = 3;
        return new KeyEvent(Key.Unknown, Modifiers.None, null);
    }

    private static KeyEvent ParseCtrl(byte b, out int consumed)
    {
        consumed = 1;

        return b switch
        {
            0x0A => new KeyEvent(Key.Enter, Modifiers.None, null), // LF — Enter after icrnl translation
            0x0D => new KeyEvent(Key.Enter, Modifiers.None, null), // CR — Enter without icrnl
            0x09 => new KeyEvent(Key.Tab, Modifiers.None, null),
            0x08 => new KeyEvent(Key.Backspace, Modifiers.None, null),
            _ => new KeyEvent((Key)(Key.A + (b - 1)), Modifiers.Ctrl, null),
        };
    }

    private static Key MapPrintable(byte b) => b switch
    {
        >= (byte)'a' and <= (byte)'z' => Key.A + (b - (byte)'a'),
        >= (byte)'A' and <= (byte)'Z' => Key.A + (b - (byte)'A'),
        >= (byte)'0' and <= (byte)'9' => Key.D0 + (b - (byte)'0'),
        _ => Key.Unknown,
    };
}
