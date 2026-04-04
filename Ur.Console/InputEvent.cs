namespace Ur.Console;

/// <summary>
/// Base for all terminal input events.
/// </summary>
/// <remarks>
/// Discriminated-union pattern using record hierarchy ensures each event variant
/// is strongly-typed and pattern-matchable with `is` or `switch`. New event kinds
/// (mouse clicks, resize signals) can be added without touching existing match arms.
/// </remarks>
public abstract record InputEvent;

/// <summary>
/// Represents a key press event.
/// </summary>
/// <remarks>
/// Carries a logical Key (navigation keys, control chords, etc.) and for printable
/// characters, the actual Unicode char. Char is null for everything except
/// Key.Character, so callers never need to check for '\0' sentinels.
/// </remarks>
/// <param name="Key">The logical key that was pressed.</param>
/// <param name="Char">The Unicode character for Key.Character, null otherwise.</param>
public record KeyEvent(Key Key, char? Char = null) : InputEvent;
