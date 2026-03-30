namespace Ur.Terminal.Input;

public readonly record struct KeyEvent(
    Key Key,
    Modifiers Mods,
    char? Char,
    KeyEventType EventType = KeyEventType.Press);
