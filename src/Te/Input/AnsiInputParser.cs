using System.Text;

namespace Te.Input;

/// <summary>
/// Stateful ANSI input parser for raw terminal byte streams.
/// Te keeps this parser internal because callers should interact with the stable
/// event model, not terminal escape sequences. The parser exists to turn Unix raw
/// stdin into the same key and mouse events that the coordinator already knows how
/// to serialize.
/// </summary>
internal sealed class AnsiInputParser
{
    private const int MaxPendingBytes = 256;
    private const int Utf8MaxBytes = 4;

    private enum ParserState
    {
        Ground,
        Escape,
        CsiParam,
        Ss3,
        Utf8,
    }

    private ParserState _state = ParserState.Ground;
    private readonly List<byte> _pending = new(MaxPendingBytes);
    private readonly StringBuilder _csiParams = new(32);
    private bool _csiIsSgr;
    private int _utf8Remaining;
    private readonly byte[] _utf8Buffer = new byte[Utf8MaxBytes];
    private int _utf8Index;

    public IReadOnlyList<InputEvent> Parse(ReadOnlySpan<byte> bytes)
    {
        var events = new List<InputEvent>();

        foreach (var nextByte in bytes)
        {
            switch (_state)
            {
                case ParserState.Ground:
                    ProcessGround(nextByte, events);
                    break;
                case ParserState.Escape:
                    ProcessEscape(nextByte, events);
                    break;
                case ParserState.CsiParam:
                    ProcessCsiParam(nextByte, events);
                    break;
                case ParserState.Ss3:
                    ProcessSs3(nextByte, events);
                    break;
                case ParserState.Utf8:
                    ProcessUtf8(nextByte, events);
                    break;
            }
        }

        return events;
    }

    /// <summary>
    /// Flushes pending parser state after an input timeout.
    /// This is how we distinguish a plain Escape key from the start of an ANSI
    /// escape sequence without blocking the reader indefinitely.
    /// </summary>
    public IReadOnlyList<InputEvent> Flush()
    {
        if (_state == ParserState.Escape)
        {
            Reset();
            return [new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo('\x1b', ConsoleKey.Escape, false, false, false)))];
        }

        // For incomplete CSI, SS3, or UTF-8 sequences we discard the pending
        // bytes instead of surfacing a public "unknown input" event. Te's
        // current abstraction only promises normalized input, not protocol
        // diagnostics.
        Reset();
        return [];
    }

    private void Reset()
    {
        _state = ParserState.Ground;
        _pending.Clear();
        _csiParams.Clear();
        _csiIsSgr = false;
        _utf8Index = 0;
        _utf8Remaining = 0;
    }

    private void ProcessGround(byte nextByte, List<InputEvent> events)
    {
        if (nextByte == 0x00)
        {
            events.Add(new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo('\0', ConsoleKey.Spacebar, false, false, true))));
            return;
        }

        if (nextByte == 0x1B)
        {
            _state = ParserState.Escape;
            _pending.Clear();
            _pending.Add(nextByte);
            return;
        }

        if (nextByte >= 0x01 && nextByte <= 0x1A)
        {
            switch (nextByte)
            {
                case 0x09:
                    events.Add(MakeKey(ConsoleKey.Tab, false, false, false));
                    return;
                case 0x0D:
                case 0x0A:
                    events.Add(MakeKey(ConsoleKey.Enter, false, false, false));
                    return;
                case 0x08:
                    events.Add(MakeKey(ConsoleKey.Backspace, false, false, false));
                    return;
                default:
                {
                    var baseChar = (char)(nextByte + 'A' - 1);
                    events.Add(new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo((char)nextByte, (ConsoleKey)baseChar, false, false, true))));
                    return;
                }
            }
        }

        if (nextByte == 0x7F)
        {
            events.Add(MakeKey(ConsoleKey.Backspace, false, false, false));
            return;
        }

        if (nextByte >= 0x20 && nextByte <= 0x7E)
        {
            var character = (char)nextByte;
            events.Add(new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo(character, CharToConsoleKey(character), char.IsUpper(character), false, false))));
            return;
        }

        if (nextByte >= 0xC0)
        {
            _utf8Index = 0;
            _utf8Buffer[_utf8Index++] = nextByte;

            _utf8Remaining = nextByte switch
            {
                < 0xE0 => 1,
                < 0xF0 => 2,
                _ => 3,
            };

            _state = ParserState.Utf8;
        }
    }

    private void ProcessEscape(byte nextByte, List<InputEvent> events)
    {
        _pending.Add(nextByte);

        if (nextByte == '[')
        {
            _state = ParserState.CsiParam;
            _csiParams.Clear();
            _csiIsSgr = false;
            return;
        }

        if (nextByte == 'O')
        {
            _state = ParserState.Ss3;
            return;
        }

        if (nextByte >= 0x20 && nextByte <= 0x7E)
        {
            var character = (char)nextByte;
            events.Add(new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo(character, CharToConsoleKey(character), char.IsUpper(character), true, false))));
            Reset();
            return;
        }

        events.Add(new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo('\x1b', ConsoleKey.Escape, false, false, false))));
        Reset();
        ProcessGround(nextByte, events);
    }

    private void ProcessCsiParam(byte nextByte, List<InputEvent> events)
    {
        _pending.Add(nextByte);

        if (_csiParams.Length == 0 && nextByte == '<')
        {
            _csiIsSgr = true;
            _csiParams.Append((char)nextByte);
            return;
        }

        if ((nextByte >= '0' && nextByte <= '9') || nextByte == ';')
        {
            _csiParams.Append((char)nextByte);
            return;
        }

        if (nextByte is >= 0x40 and <= 0x7E)
        {
            DispatchCsi((char)nextByte, events);
            if (_state == ParserState.CsiParam)
                Reset();
            return;
        }

        if (_pending.Count > MaxPendingBytes)
            Reset();
    }

    private void ProcessSs3(byte nextByte, List<InputEvent> events)
    {
        switch ((char)nextByte)
        {
            case 'P':
                events.Add(MakeKey(ConsoleKey.F1, false, false, false));
                break;
            case 'Q':
                events.Add(MakeKey(ConsoleKey.F2, false, false, false));
                break;
            case 'R':
                events.Add(MakeKey(ConsoleKey.F3, false, false, false));
                break;
            case 'S':
                events.Add(MakeKey(ConsoleKey.F4, false, false, false));
                break;
            case 'A':
                events.Add(MakeKey(ConsoleKey.UpArrow, false, false, false));
                break;
            case 'B':
                events.Add(MakeKey(ConsoleKey.DownArrow, false, false, false));
                break;
            case 'C':
                events.Add(MakeKey(ConsoleKey.RightArrow, false, false, false));
                break;
            case 'D':
                events.Add(MakeKey(ConsoleKey.LeftArrow, false, false, false));
                break;
            case 'H':
                events.Add(MakeKey(ConsoleKey.Home, false, false, false));
                break;
            case 'F':
                events.Add(MakeKey(ConsoleKey.End, false, false, false));
                break;
        }

        Reset();
    }

    private void ProcessUtf8(byte nextByte, List<InputEvent> events)
    {
        if ((nextByte & 0xC0) != 0x80)
        {
            Reset();
            ProcessGround(nextByte, events);
            return;
        }

        _utf8Buffer[_utf8Index++] = nextByte;
        _utf8Remaining--;

        if (_utf8Remaining != 0)
            return;

        var decoded = Encoding.UTF8.GetString(_utf8Buffer, 0, _utf8Index);
        if (!string.IsNullOrEmpty(decoded))
            events.Add(new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo(decoded[0], ConsoleKey.NoName, false, false, false))));

        Reset();
    }

    private void DispatchCsi(char finalByte, List<InputEvent> events)
    {
        var parameterString = _csiParams.ToString();

        if (_csiIsSgr && (finalByte == 'M' || finalByte == 'm'))
        {
            TryParseSgrMouse(parameterString, finalByte == 'M', events);
            return;
        }

        ParseModifiers(parameterString, out var numericPart, out var shift, out var alt, out var ctrl);

        switch (finalByte)
        {
            case 'A':
                events.Add(MakeKey(ConsoleKey.UpArrow, shift, alt, ctrl));
                break;
            case 'B':
                events.Add(MakeKey(ConsoleKey.DownArrow, shift, alt, ctrl));
                break;
            case 'C':
                events.Add(MakeKey(ConsoleKey.RightArrow, shift, alt, ctrl));
                break;
            case 'D':
                events.Add(MakeKey(ConsoleKey.LeftArrow, shift, alt, ctrl));
                break;
            case 'H':
                events.Add(MakeKey(ConsoleKey.Home, shift, alt, ctrl));
                break;
            case 'F':
                events.Add(MakeKey(ConsoleKey.End, shift, alt, ctrl));
                break;
            case 'E':
                events.Add(MakeKey(ConsoleKey.Clear, shift, alt, ctrl));
                break;
            case 'I':
                events.Add(new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo('\t', ConsoleKey.Tab, shift, alt, ctrl))));
                break;
            case 'Z':
                events.Add(new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo('\t', ConsoleKey.Tab, true, alt, ctrl))));
                break;
            case '~':
                DispatchTilde(numericPart, shift, alt, ctrl, events);
                break;
        }
    }

    private static void DispatchTilde(string numericPart, bool shift, bool alt, bool ctrl, List<InputEvent> events)
    {
        switch (numericPart)
        {
            case "1":
                events.Add(MakeKey(ConsoleKey.Home, shift, alt, ctrl));
                break;
            case "2":
                events.Add(MakeKey(ConsoleKey.Insert, shift, alt, ctrl));
                break;
            case "3":
                events.Add(MakeKey(ConsoleKey.Delete, shift, alt, ctrl));
                break;
            case "4":
                events.Add(MakeKey(ConsoleKey.End, shift, alt, ctrl));
                break;
            case "5":
                events.Add(MakeKey(ConsoleKey.PageUp, shift, alt, ctrl));
                break;
            case "6":
                events.Add(MakeKey(ConsoleKey.PageDown, shift, alt, ctrl));
                break;
            case "15":
                events.Add(MakeKey(ConsoleKey.F5, shift, alt, ctrl));
                break;
            case "17":
                events.Add(MakeKey(ConsoleKey.F6, shift, alt, ctrl));
                break;
            case "18":
                events.Add(MakeKey(ConsoleKey.F7, shift, alt, ctrl));
                break;
            case "19":
                events.Add(MakeKey(ConsoleKey.F8, shift, alt, ctrl));
                break;
            case "20":
                events.Add(MakeKey(ConsoleKey.F9, shift, alt, ctrl));
                break;
            case "21":
                events.Add(MakeKey(ConsoleKey.F10, shift, alt, ctrl));
                break;
            case "23":
                events.Add(MakeKey(ConsoleKey.F11, shift, alt, ctrl));
                break;
            case "24":
                events.Add(MakeKey(ConsoleKey.F12, shift, alt, ctrl));
                break;
        }
    }

    private static void TryParseSgrMouse(string parameterString, bool isPress, List<InputEvent> events)
    {
        var data = parameterString.StartsWith('<') ? parameterString[1..] : parameterString;
        var parts = data.Split(';');
        if (parts.Length < 3)
            return;

        if (!int.TryParse(parts[0], out var buttonCode) ||
            !int.TryParse(parts[1], out var x) ||
            !int.TryParse(parts[2], out var y))
        {
            return;
        }

        var flags = new List<MouseFlags>();
        if ((buttonCode & 0x04) != 0)
            flags.Add(MouseFlags.ButtonShift);
        if ((buttonCode & 0x08) != 0)
            flags.Add(MouseFlags.ButtonAlt);
        if ((buttonCode & 0x10) != 0)
            flags.Add(MouseFlags.ButtonCtrl);

        var motion = (buttonCode & 0x20) != 0;
        var wheel = (buttonCode & 0x40) != 0;
        var baseButton = buttonCode & 0x03;

        if (wheel)
        {
            switch (baseButton)
            {
                case 0:
                    flags.Add(MouseFlags.WheeledUp);
                    break;
                case 1:
                    flags.Add(MouseFlags.WheeledDown);
                    break;
                case 2:
                    flags.Add(MouseFlags.WheeledLeft);
                    break;
                case 3:
                    flags.Add(MouseFlags.WheeledRight);
                    break;
            }
        }
        else if (motion)
        {
            flags.Add(MouseFlags.ReportMousePosition);
            switch (baseButton)
            {
                case 0:
                    flags.Add(MouseFlags.Button1Pressed);
                    flags.Add(MouseFlags.Button1Dragged);
                    break;
                case 1:
                    flags.Add(MouseFlags.Button2Pressed);
                    flags.Add(MouseFlags.Button2Dragged);
                    break;
                case 2:
                    flags.Add(MouseFlags.Button3Pressed);
                    flags.Add(MouseFlags.Button3Dragged);
                    break;
            }
        }
        else
        {
            switch (baseButton)
            {
                case 0:
                    flags.Add(isPress ? MouseFlags.Button1Pressed : MouseFlags.Button1Released);
                    break;
                case 1:
                    flags.Add(isPress ? MouseFlags.Button2Pressed : MouseFlags.Button2Released);
                    break;
                case 2:
                    flags.Add(isPress ? MouseFlags.Button3Pressed : MouseFlags.Button3Released);
                    break;
                case 3:
                    flags.Add(MouseFlags.Button1Released);
                    break;
            }
        }

        if (flags.Count == 0)
            return;

        var position = new Point(Math.Max(0, x - 1), Math.Max(0, y - 1));
        events.Add(new MouseInputEvent(new MouseEventArgs(flags, position)));
    }

    private static void ParseModifiers(string parameterString, out string numericPart, out bool shift, out bool alt, out bool ctrl)
    {
        shift = false;
        alt = false;
        ctrl = false;
        numericPart = parameterString;

        var parts = parameterString.Split(';');
        if (parts.Length < 2)
            return;

        numericPart = parts[0];
        if (!int.TryParse(parts[1], out var modifierCode))
            return;

        modifierCode--;
        shift = (modifierCode & 1) != 0;
        alt = (modifierCode & 2) != 0;
        ctrl = (modifierCode & 4) != 0;
    }

    private static KeyInputEvent MakeKey(ConsoleKey key, bool shift, bool alt, bool ctrl)
    {
        var keyChar = key switch
        {
            ConsoleKey.Enter => '\r',
            ConsoleKey.Tab => '\t',
            ConsoleKey.Backspace => '\b',
            ConsoleKey.Spacebar => ' ',
            _ => '\0',
        };

        return new KeyInputEvent(MakeKeyEvent(new ConsoleKeyInfo(keyChar, key, shift, alt, ctrl)));
    }

    private static KeyEventArgs MakeKeyEvent(ConsoleKeyInfo keyInfo) => KeyEventArgs.FromConsoleKeyInfo(keyInfo);

    private static ConsoleKey CharToConsoleKey(char character) => character switch
    {
        >= 'a' and <= 'z' => (ConsoleKey)(character - 'a' + 'A'),
        >= 'A' and <= 'Z' => (ConsoleKey)character,
        >= '0' and <= '9' => (ConsoleKey)character,
        ' ' => ConsoleKey.Spacebar,
        '\t' => ConsoleKey.Tab,
        '\r' => ConsoleKey.Enter,
        '\b' => ConsoleKey.Backspace,
        '/' => ConsoleKey.Divide,
        '*' => ConsoleKey.Multiply,
        '-' => ConsoleKey.OemMinus,
        '+' => ConsoleKey.OemPlus,
        '.' => ConsoleKey.OemPeriod,
        ',' => ConsoleKey.OemComma,
        ';' => ConsoleKey.Oem1,
        '=' => ConsoleKey.OemPlus,
        '[' => ConsoleKey.Oem4,
        ']' => ConsoleKey.Oem6,
        '\\' => ConsoleKey.Oem5,
        '\'' => ConsoleKey.Oem7,
        '`' => ConsoleKey.Oem3,
        _ => ConsoleKey.NoName,
    };
}
