namespace Ox.Terminal.Input;

/// <summary>
/// Bridges platform-specific <see cref="ConsoleKeyInfo"/> values into the
/// portable <see cref="KeyCode"/> space used by Te.
/// </summary>
public static class KeyCodeExtensions
{
    public static bool HasShift(this KeyCode keyCode) => (keyCode & KeyCode.ShiftMask) != 0;

    public static bool HasCtrl(this KeyCode keyCode) => (keyCode & KeyCode.CtrlMask) != 0;

    public static bool HasAlt(this KeyCode keyCode) => (keyCode & KeyCode.AltMask) != 0;

    public static KeyCode WithoutModifiers(this KeyCode keyCode) =>
        keyCode & ~(KeyCode.ShiftMask | KeyCode.CtrlMask | KeyCode.AltMask);

    public static KeyCode FromConsoleKeyInfo(ConsoleKeyInfo keyInfo)
    {
        var keyCode = MapBaseKey(keyInfo);

        if ((keyInfo.Modifiers & ConsoleModifiers.Shift) != 0 && ShouldEncodeShiftModifier(keyInfo))
            keyCode |= KeyCode.ShiftMask;
        if ((keyInfo.Modifiers & ConsoleModifiers.Control) != 0)
            keyCode |= KeyCode.CtrlMask;
        if ((keyInfo.Modifiers & ConsoleModifiers.Alt) != 0)
            keyCode |= KeyCode.AltMask;

        return keyCode;
    }

    private static bool ShouldEncodeShiftModifier(ConsoleKeyInfo keyInfo) =>
        keyInfo.KeyChar == '\0' || char.IsLetterOrDigit(keyInfo.KeyChar);

    private static KeyCode MapBaseKey(ConsoleKeyInfo keyInfo)
    {
        if (TryMapAlphaNumericKey(keyInfo.Key, out var alphaNumeric))
            return alphaNumeric;

        if (TryMapSpecialKey(keyInfo.Key, out var special))
            return special;

        if (keyInfo.KeyChar != '\0' && !char.IsControl(keyInfo.KeyChar))
            return (KeyCode)keyInfo.KeyChar;

        return KeyCode.Null;
    }

    private static bool TryMapAlphaNumericKey(ConsoleKey consoleKey, out KeyCode keyCode)
    {
        if (consoleKey is >= ConsoleKey.A and <= ConsoleKey.Z)
        {
            keyCode = (KeyCode)((uint)KeyCode.A + (uint)(consoleKey - ConsoleKey.A));
            return true;
        }

        if (consoleKey is >= ConsoleKey.D0 and <= ConsoleKey.D9)
        {
            keyCode = (KeyCode)((uint)KeyCode.D0 + (uint)(consoleKey - ConsoleKey.D0));
            return true;
        }

        keyCode = KeyCode.Null;
        return false;
    }

    private static bool TryMapSpecialKey(ConsoleKey consoleKey, out KeyCode keyCode)
    {
        keyCode = consoleKey switch
        {
            ConsoleKey.Backspace => KeyCode.Backspace,
            ConsoleKey.Tab => KeyCode.Tab,
            ConsoleKey.Enter => KeyCode.Enter,
            ConsoleKey.Clear => KeyCode.Clear,
            ConsoleKey.Escape => KeyCode.Esc,
            ConsoleKey.Spacebar => KeyCode.Space,
            ConsoleKey.UpArrow => KeyCode.CursorUp,
            ConsoleKey.DownArrow => KeyCode.CursorDown,
            ConsoleKey.LeftArrow => KeyCode.CursorLeft,
            ConsoleKey.RightArrow => KeyCode.CursorRight,
            ConsoleKey.PageUp => KeyCode.PageUp,
            ConsoleKey.PageDown => KeyCode.PageDown,
            ConsoleKey.Home => KeyCode.Home,
            ConsoleKey.End => KeyCode.End,
            ConsoleKey.Insert => KeyCode.Insert,
            ConsoleKey.Delete => KeyCode.Delete,
            ConsoleKey.PrintScreen => KeyCode.PrintScreen,
            ConsoleKey.F1 => KeyCode.F1,
            ConsoleKey.F2 => KeyCode.F2,
            ConsoleKey.F3 => KeyCode.F3,
            ConsoleKey.F4 => KeyCode.F4,
            ConsoleKey.F5 => KeyCode.F5,
            ConsoleKey.F6 => KeyCode.F6,
            ConsoleKey.F7 => KeyCode.F7,
            ConsoleKey.F8 => KeyCode.F8,
            ConsoleKey.F9 => KeyCode.F9,
            ConsoleKey.F10 => KeyCode.F10,
            ConsoleKey.F11 => KeyCode.F11,
            ConsoleKey.F12 => KeyCode.F12,
            ConsoleKey.F13 => KeyCode.F13,
            ConsoleKey.F14 => KeyCode.F14,
            ConsoleKey.F15 => KeyCode.F15,
            ConsoleKey.F16 => KeyCode.F16,
            ConsoleKey.F17 => KeyCode.F17,
            ConsoleKey.F18 => KeyCode.F18,
            ConsoleKey.F19 => KeyCode.F19,
            ConsoleKey.F20 => KeyCode.F20,
            ConsoleKey.F21 => KeyCode.F21,
            ConsoleKey.F22 => KeyCode.F22,
            ConsoleKey.F23 => KeyCode.F23,
            ConsoleKey.F24 => KeyCode.F24,
            _ => KeyCode.Null,
        };

        return keyCode != KeyCode.Null;
    }
}
