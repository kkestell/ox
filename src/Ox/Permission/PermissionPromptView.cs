using Ox.Input;
using Te.Rendering;
using Ur.Permissions;

namespace Ox.Permission;

/// <summary>
/// Renders the floating 3-row permission approval panel that appears between
/// the conversation area and the input area when a tool requires permission.
///
/// Layout:
///   Row 0: Top border     ╭──────────────────────────────╮
///   Row 1: Prompt + input Allow 'write_file' to Write 'bar.txt'? (y/n [scopes]): █
///   Row 2: Bottom border  ╰──────────────────────────────╯
///
/// The view owns a secondary TextEditor for the inline input field. When
/// active, keyboard input is redirected to this editor instead of the main
/// input area.
/// </summary>
public sealed class PermissionPromptView
{
    /// <summary>Fixed height of the permission prompt in rows.</summary>
    public const int Height = 3;

    private readonly Views.OxThemePalette _theme = Views.OxThemePalette.Ox;

    /// <summary>Text editor for the inline permission input field.</summary>
    public TextEditor Editor { get; } = new();

    /// <summary>
    /// The current permission request being displayed, or null if the prompt
    /// is not active.
    /// </summary>
    public PermissionRequest? ActiveRequest { get; set; }

    /// <summary>Whether the permission prompt is currently visible.</summary>
    public bool IsActive => ActiveRequest is not null;

    /// <summary>
    /// Render the permission prompt at the given position. Should be called
    /// only when <see cref="IsActive"/> is true.
    /// </summary>
    public void Render(ConsoleBuffer buffer, int x, int y, int width)
    {
        if (ActiveRequest is null || width < 4) return;

        var borderColor = _theme.Border;
        var innerWidth = width - 2;

        // Row 0: Top border
        buffer.SetCell(x, y, '╭', borderColor, Color.Default);
        buffer.FillCells(x + 1, y, innerWidth, '─', borderColor, Color.Default);
        buffer.SetCell(x + width - 1, y, '╮', borderColor, Color.Default);

        // Row 1: Prompt text + input field
        var contentRow = y + 1;
        buffer.SetCell(x, contentRow, '│', borderColor, Color.Default);
        buffer.SetCell(x + width - 1, contentRow, '│', borderColor, Color.Default);

        // Build prompt text: Allow '{tool}' to {Operation} '{target}'? (y/n [scopes]):
        var req = ActiveRequest;
        var scopes = string.Join(", ", req.AllowedScopes.Select(s => s.ToString().ToLowerInvariant()));
        var prompt = $" Allow '{req.ToolName}' to {req.OperationType} '{req.Target}'? (y/n [{scopes}]): ";

        // Draw prompt text.
        var drawX = x + 1;
        for (var i = 0; i < prompt.Length && drawX < x + width - 1; i++)
        {
            buffer.SetCell(drawX, contentRow, prompt[i], _theme.Text, Color.Default);
            drawX++;
        }

        // Draw the input field text after the prompt.
        var inputText = Editor.Text;
        for (var i = 0; i < inputText.Length && drawX < x + width - 1; i++)
        {
            buffer.SetCell(drawX, contentRow, inputText[i], _theme.Text, Color.Default);
            drawX++;
        }

        // Show cursor.
        var cursorX = x + 1 + prompt.Length + Editor.CursorPosition;
        if (cursorX < x + width - 1)
        {
            var cursorChar = Editor.CursorPosition < inputText.Length
                ? inputText[Editor.CursorPosition]
                : ' ';
            buffer.SetCell(cursorX, contentRow, cursorChar, Color.Black, _theme.Text);
        }

        // Row 2: Bottom border
        var bottomRow = y + 2;
        buffer.SetCell(x, bottomRow, '╰', borderColor, Color.Default);
        buffer.FillCells(x + 1, bottomRow, innerWidth, '─', borderColor, Color.Default);
        buffer.SetCell(x + width - 1, bottomRow, '╯', borderColor, Color.Default);
    }
}
