using System.Text;

namespace Ur.Terminal.Input;

public static class KeyParser
{
    private const byte EscapeByte = 0x1B;
    private const byte CsiByte = 0x5B;

    public static KeyEvent? Parse(ReadOnlySpan<byte> input, out int consumed)
    {
        consumed = 0;
        if (input.IsEmpty)
            return null;

        var b = input[0];

        // Escape or CSI sequence
        if (b == EscapeByte)
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
        if (input[1] != CsiByte)
        {
            consumed = 1;
            return new KeyEvent(Key.Escape, Modifiers.None, null);
        }

        return ParseCsi(input, out consumed);
    }

    private static KeyEvent? ParseCsi(ReadOnlySpan<byte> input, out int consumed)
    {
        consumed = 0;

        var sequenceLength = FindCsiSequenceLength(input, out var invalid);
        if (sequenceLength == 0)
            return null;

        consumed = sequenceLength;
        if (invalid)
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        var finalByte = input[sequenceLength - 1];
        var parameters = input[2..(sequenceLength - 1)];

        return finalByte switch
        {
            (byte)'A' or (byte)'B' or (byte)'C' or (byte)'D' or (byte)'H' or (byte)'F'
                => ParseCursorKey(parameters, finalByte),
            (byte)'~' => ParseTildeKey(parameters),
            (byte)'u' => ParseKittyKey(parameters),
            _ => new KeyEvent(Key.Unknown, Modifiers.None, null),
        };
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

    private static int FindCsiSequenceLength(ReadOnlySpan<byte> input, out bool invalid)
    {
        invalid = false;

        if (input.Length < 3)
            return 0;

        for (var i = 2; i < input.Length; i++)
        {
            var b = input[i];
            if (b >= 0x40 && b <= 0x7E)
                return i + 1;

            if (b is < 0x20 or > 0x3F)
            {
                invalid = true;
                return i + 1;
            }
        }

        return 0;
    }

    private static KeyEvent ParseCursorKey(ReadOnlySpan<byte> parameters, byte finalByte)
    {
        var key = finalByte switch
        {
            (byte)'A' => Key.Up,
            (byte)'B' => Key.Down,
            (byte)'C' => Key.Right,
            (byte)'D' => Key.Left,
            (byte)'H' => Key.Home,
            (byte)'F' => Key.End,
            _ => Key.Unknown,
        };

        if (key == Key.Unknown)
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        if (!TryParseCursorModifiers(parameters, out var mods, out var eventType))
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        return new KeyEvent(key, mods, null, eventType);
    }

    private static KeyEvent ParseTildeKey(ReadOnlySpan<byte> parameters)
    {
        var text = Encoding.ASCII.GetString(parameters);
        var parts = text.Split(';');

        if (parts.Length == 0 || !TryParseNumber(parts[0], out var number))
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        var key = MapTildeKey(number);
        if (key == Key.Unknown)
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        if (parts.Length > 2)
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        if (parts.Length == 1)
            return new KeyEvent(key, Modifiers.None, null);

        if (!TryParseKittyModifierSegment(parts[1], out var mods, out var eventType))
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        return new KeyEvent(key, mods, null, eventType);
    }

    private static KeyEvent ParseKittyKey(ReadOnlySpan<byte> parameters)
    {
        var text = Encoding.ASCII.GetString(parameters);
        var parts = text.Split(';');
        if (parts.Length is 0 or > 3)
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        // First parameter: unicode-key-code[:shifted-key[:base-layout-key]]
        var keyPart = parts[0];
        var colonIndex = keyPart.IndexOf(':');
        if (colonIndex >= 0)
            keyPart = keyPart[..colonIndex];

        if (!TryParseNumber(keyPart, out var keyCode))
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        // Second parameter: modifiers[:event-type]
        var mods = Modifiers.None;
        var eventType = KeyEventType.Press;

        if (parts.Length >= 2 && !TryParseKittyModifierSegment(parts[1], out mods, out eventType))
            return new KeyEvent(Key.Unknown, Modifiers.None, null);

        // Third parameter: associated text as codepoints (colon-separated)
        char? associatedText = null;
        if (parts.Length == 3)
            associatedText = ParseAssociatedText(parts[2]);

        var key = MapKittyKey(keyCode, ref mods, out var ch);

        // Associated text from the terminal is the authoritative printable character
        if (associatedText.HasValue)
            ch = associatedText.Value;

        return new KeyEvent(key, mods, ch, eventType);
    }

    /// <summary>
    /// Parses the associated text parameter (third field in CSI u).
    /// Text is encoded as colon-separated Unicode codepoints.
    /// Returns the first codepoint as a char, or null if empty/invalid.
    /// </summary>
    private static char? ParseAssociatedText(string textParam)
    {
        if (string.IsNullOrEmpty(textParam))
            return null;

        // Take the first codepoint (before any colon separator)
        var colonIndex = textParam.IndexOf(':');
        var first = colonIndex >= 0 ? textParam[..colonIndex] : textParam;

        if (!TryParseNumber(first, out var codepoint))
            return null;

        if (codepoint is < char.MinValue or > char.MaxValue)
            return null;

        var c = (char)codepoint;
        return char.IsControl(c) ? null : c;
    }

    private static bool TryParseCursorModifiers(
        ReadOnlySpan<byte> parameters,
        out Modifiers mods,
        out KeyEventType eventType)
    {
        mods = Modifiers.None;
        eventType = KeyEventType.Press;

        if (parameters.IsEmpty)
            return true;

        var text = Encoding.ASCII.GetString(parameters);
        var parts = text.Split(';');
        if (parts.Length == 0)
            return true;

        return TryParseKittyModifierSegment(parts[^1], out mods, out eventType);
    }

    private static bool TryParseKittyModifierSegment(
        string segment,
        out Modifiers mods,
        out KeyEventType eventType)
    {
        mods = Modifiers.None;
        eventType = KeyEventType.Press;

        if (string.IsNullOrEmpty(segment))
            return true;

        var colonIndex = segment.IndexOf(':');
        if (colonIndex < 0)
        {
            if (!TryParseEncodedModifiers(segment, out mods))
                return false;

            return true;
        }

        var modifierText = segment[..colonIndex];
        if (!string.IsNullOrEmpty(modifierText) && !TryParseEncodedModifiers(modifierText, out mods))
            return false;

        var eventTypeText = segment[(colonIndex + 1)..];
        return string.IsNullOrEmpty(eventTypeText) || TryParseEventType(eventTypeText, out eventType);
    }

    private static bool TryParseEncodedModifiers(string text, out Modifiers mods)
    {
        mods = Modifiers.None;
        if (!TryParseNumber(text, out var encoded) || encoded < 1)
            return false;

        mods = TranslateKittyModifiers(encoded - 1);
        return true;
    }

    private static bool TryParseEventType(string text, out KeyEventType eventType)
    {
        eventType = KeyEventType.Press;
        if (!TryParseNumber(text, out var encoded))
            return false;

        eventType = encoded switch
        {
            1 => KeyEventType.Press,
            2 => KeyEventType.Repeat,
            3 => KeyEventType.Release,
            _ => default,
        };

        return encoded is >= 1 and <= 3;
    }

    private static bool TryParseNumber(string text, out int value)
    {
        value = 0;
        if (string.IsNullOrEmpty(text))
            return false;

        foreach (var ch in text)
        {
            if (ch is < '0' or > '9')
                return false;

            var digit = ch - '0';
            if (value > (int.MaxValue - digit) / 10)
                return false;

            value = (value * 10) + digit;
        }

        return true;
    }

    private static Modifiers TranslateKittyModifiers(int kittyBits)
    {
        var mods = Modifiers.None;

        if ((kittyBits & 0b001) != 0)
            mods |= Modifiers.Shift;

        if ((kittyBits & 0b010) != 0)
            mods |= Modifiers.Alt;

        if ((kittyBits & 0b100) != 0)
            mods |= Modifiers.Ctrl;

        return mods;
    }

    private static Key MapTildeKey(int number) => number switch
    {
        1 => Key.Home,
        3 => Key.Delete,
        4 => Key.End,
        5 => Key.PageUp,
        6 => Key.PageDown,
        11 => Key.F1,
        12 => Key.F2,
        13 => Key.F3,
        14 => Key.F4,
        15 => Key.F5,
        17 => Key.F6,
        18 => Key.F7,
        19 => Key.F8,
        20 => Key.F9,
        21 => Key.F10,
        23 => Key.F11,
        24 => Key.F12,
        _ => Key.Unknown,
    };

    private static Key MapKittyKey(int keyCode, ref Modifiers mods, out char? ch)
    {
        ch = null;

        switch (keyCode)
        {
            case 9:
                return Key.Tab;
            case 13:
                return Key.Enter;
            case 27:
                return Key.Escape;
            case 32:
                ch = ' ';
                return Key.Space;
            case 127:
                return Key.Backspace;
        }

        if (keyCode is < char.MinValue or > char.MaxValue)
            return Key.Unknown;

        var value = (char)keyCode;

        if (value is >= 'A' and <= 'Z')
        {
            mods |= Modifiers.Shift;
            ch = value;
            return Key.A + (value - 'A');
        }

        if (value is >= 'a' and <= 'z')
        {
            ch = value;
            return Key.A + (value - 'a');
        }

        if (value is >= '0' and <= '9')
        {
            ch = value;
            return Key.D0 + (value - '0');
        }

        if (!char.IsControl(value) && !char.IsSurrogate(value))
            ch = value;

        return value <= 0x7E ? MapPrintable((byte)value) : Key.Unknown;
    }
}
