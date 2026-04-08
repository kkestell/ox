namespace Te.Input;

/// <summary>
/// Bit flags describing mouse button state, movement, modifiers, and wheel input.
/// This is a direct low-level event vocabulary so higher layers can decide their
/// own interpretation instead of inheriting one from a larger widget framework.
/// </summary>
[Flags]
public enum MouseFlags
{
    None = 0,
    Button1Released = 0x1,
    Button1Pressed = 0x2,
    Button1Clicked = 0x4,
    Button1DoubleClicked = 0x8,
    Button1TripleClicked = 0x10,
    Button1Dragged = 0x20,

    Button2Released = 0x40,
    Button2Pressed = 0x80,
    Button2Clicked = 0x100,
    Button2DoubleClicked = 0x200,
    Button2TripleClicked = 0x400,
    Button2Dragged = 0x800,

    Button3Released = 0x1000,
    Button3Pressed = 0x2000,
    Button3Clicked = 0x4000,
    Button3DoubleClicked = 0x8000,
    Button3TripleClicked = 0x10000,
    Button3Dragged = 0x20000,

    Button4Released = 0x40000,
    Button4Pressed = 0x80000,
    Button4Clicked = 0x100000,
    Button4DoubleClicked = 0x200000,
    Button4TripleClicked = 0x400000,

    ButtonCtrl = 0x1000000,
    ButtonShift = 0x2000000,
    ButtonAlt = 0x4000000,

    ReportMousePosition = 0x8000000,
    WheeledUp = 0x10000000,
    WheeledDown = 0x20000000,
    WheeledLeft = ButtonCtrl | WheeledUp,
    WheeledRight = ButtonCtrl | WheeledDown,
    MouseEnter = 0x40000000,
    MouseLeave = unchecked((int)0x80000000),
    AllEvents = unchecked((int)0xffffffff),
}
