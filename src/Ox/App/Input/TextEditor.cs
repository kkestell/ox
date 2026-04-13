namespace Ox.App.Input;

/// <summary>
/// Single-line editable text buffer with a cursor position.
///
/// This is the editing model for the input field — it handles character
/// insertion, deletion, and cursor movement but knows nothing about rendering.
/// The view reads <see cref="Text"/> and <see cref="CursorPosition"/> to
/// draw the field and show the cursor.
/// </summary>
public sealed class TextEditor
{
    private string _text = string.Empty;
    private int _cursorPosition;

    /// <summary>Current text content of the buffer.</summary>
    public string Text => _text;

    /// <summary>
    /// Cursor position as a zero-based character index. Always in
    /// [0, Text.Length] — the cursor can sit one past the last character
    /// (the append position).
    /// </summary>
    public int CursorPosition => _cursorPosition;

    /// <summary>Insert a character at the cursor position and advance the cursor.</summary>
    public void InsertChar(char c)
    {
        _text = _text.Insert(_cursorPosition, c.ToString());
        _cursorPosition++;
    }

    /// <summary>Delete the character before the cursor (backspace).</summary>
    public void Backspace()
    {
        if (_cursorPosition <= 0) return;
        _text = _text.Remove(_cursorPosition - 1, 1);
        _cursorPosition--;
    }

    /// <summary>Delete the character at the cursor position (forward delete).</summary>
    public void Delete()
    {
        if (_cursorPosition >= _text.Length) return;
        _text = _text.Remove(_cursorPosition, 1);
    }

    /// <summary>Move the cursor one position to the left.</summary>
    public void MoveLeft()
    {
        if (_cursorPosition > 0) _cursorPosition--;
    }

    /// <summary>Move the cursor one position to the right.</summary>
    public void MoveRight()
    {
        if (_cursorPosition < _text.Length) _cursorPosition++;
    }

    /// <summary>Move the cursor to the beginning of the buffer.</summary>
    public void Home()
    {
        _cursorPosition = 0;
    }

    /// <summary>Move the cursor to the end of the buffer.</summary>
    public void End()
    {
        _cursorPosition = _text.Length;
    }

    /// <summary>Clear the buffer and reset the cursor to position 0.</summary>
    public void Clear()
    {
        _text = string.Empty;
        _cursorPosition = 0;
    }

    /// <summary>
    /// Replace the buffer contents and move the cursor to the end.
    /// Used by autocomplete to append a completion suffix.
    /// </summary>
    public void SetText(string text)
    {
        _text = text;
        _cursorPosition = _text.Length;
    }
}
