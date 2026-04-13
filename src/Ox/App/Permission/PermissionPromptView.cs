using Ox.App.Input;
using Ox.Terminal.Rendering;
using Ox.Agent.Permissions;

namespace Ox.App.Permission;

/// <summary>
/// Renders the floating 3-row permission approval panel that appears between
/// the conversation area and the input area when a tool requires permission.
///
/// Layout:
///   Row 0: Top border     ┌──────────────────────────────┐
///   Row 1: Prompt + input Allow 'write_file' to Write 'bar.txt'? (y/n [scopes]): █
///   Row 2: Bottom border  └──────────────────────────────┘
///
/// The view owns a secondary TextEditor for the inline input field. When
/// active, keyboard input is redirected to this editor instead of the main
/// input area.
/// </summary>
public sealed class PermissionPromptView
{
    private const int InputReserve = 4;

    /// <summary>Fixed height of the permission prompt in rows.</summary>
    public const int Height = 3;

    /// <summary>
    /// The approval prompt now uses a flush border instead of an offset shadow,
    /// so layout does not need to hold an extra gutter below it.
    /// </summary>
    public const int ShadowHeight = 0;

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
    /// The active workspace root. This lets the prompt collapse file targets to
    /// workspace-relative paths instead of repeating the full absolute path.
    /// </summary>
    public string WorkspacePath { get; set; } = string.Empty;

    /// <summary>
    /// Render the permission prompt at the given position. Should be called
    /// only when <see cref="IsActive"/> is true.
    /// </summary>
    public void Render(ConsoleBuffer buffer, int x, int y, int width)
    {
        if (ActiveRequest is null || width < 4) return;

        var bgColor = _theme.Background;
        var borderColor = _theme.ChromeBorder;
        var innerWidth = width - 2;

        // Paint the panel surface first so clipped or shortened prompt text
        // never leaves stale transcript content showing through the middle.
        for (var row = y; row < y + Height && row < buffer.Height; row++)
            buffer.FillCells(x, row, width, ' ', _theme.Text, bgColor);

        // Row 0: Top border
        buffer.SetCell(x, y, '┌', borderColor, bgColor);
        buffer.FillCells(x + 1, y, innerWidth, '─', borderColor, bgColor);
        buffer.SetCell(x + width - 1, y, '┐', borderColor, bgColor);

        // Row 1: Prompt text + input field
        var contentRow = y + 1;
        buffer.SetCell(x, contentRow, '│', borderColor, bgColor);
        buffer.SetCell(x + width - 1, contentRow, '│', borderColor, bgColor);

        var req = ActiveRequest;
        var prompt = BuildPrompt(req, innerWidth);

        // Draw prompt text.
        var drawX = x + 1;
        for (var i = 0; i < prompt.Length && drawX < x + width - 1; i++)
        {
            buffer.SetCell(drawX, contentRow, prompt[i], _theme.Text, bgColor);
            drawX++;
        }

        // Draw the input field text after the prompt.
        var inputText = Editor.Text;
        for (var i = 0; i < inputText.Length && drawX < x + width - 1; i++)
        {
            buffer.SetCell(drawX, contentRow, inputText[i], _theme.Text, bgColor);
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
        buffer.SetCell(x, bottomRow, '└', borderColor, bgColor);
        buffer.FillCells(x + 1, bottomRow, innerWidth, '─', borderColor, bgColor);
        buffer.SetCell(x + width - 1, bottomRow, '┘', borderColor, bgColor);
    }

    private string BuildPrompt(PermissionRequest request, int innerWidth)
    {
        var verb = FormatVerb(request);
        var target = FormatTarget(request.Target);
        var scopeLegend = request.AllowedScopes.Count > 0
            ? $"/{string.Join('/', request.AllowedScopes.Select(s => s.ToDisplayShort()))}"
            : string.Empty;
        var prefix = $" Allow {verb}";
        var suffix = $"? [y/n{scopeLegend}]: ";

        // Keep a small typing area available even when a tool target is long.
        var maxPromptLength = Math.Max(0, innerWidth - InputReserve);
        var targetBudget = Math.Max(0, maxPromptLength - prefix.Length - suffix.Length - 1);
        var targetSegment = string.IsNullOrWhiteSpace(target)
            ? string.Empty
            : $" {ShortenTarget(target, targetBudget)}";

        return prefix + targetSegment + suffix;
    }

    private static string FormatVerb(PermissionRequest request) => request.ToolName switch
    {
        "read_file" => "read",
        "write_file" => "write",
        "update_file" => "edit",
        "bash" => "run",
        _ => request.OperationType.ToString().ToLowerInvariant(),
    };

    private string FormatTarget(string target)
    {
        if (string.IsNullOrWhiteSpace(target))
            return string.Empty;

        if (TryMakeWorkspaceRelative(target, out var relative))
            return relative;

        return target;
    }

    private bool TryMakeWorkspaceRelative(string target, out string relative)
    {
        relative = string.Empty;
        if (string.IsNullOrWhiteSpace(WorkspacePath) || !Path.IsPathRooted(target))
            return false;

        try
        {
            var workspaceFull = Path.GetFullPath(WorkspacePath);
            var targetFull = Path.GetFullPath(target);
            var candidate = Path.GetRelativePath(workspaceFull, targetFull);
            if (candidate == "." ||
                candidate.StartsWith("..", StringComparison.Ordinal) ||
                Path.IsPathRooted(candidate))
            {
                return false;
            }

            relative = candidate;
            return true;
        }
        catch
        {
            return false;
        }
    }

    private static string ShortenTarget(string target, int maxLength)
    {
        if (maxLength <= 0)
            return string.Empty;

        if (target.Length <= maxLength)
            return target;

        if (maxLength <= 3)
            return target[..maxLength];

        if (target.Contains(Path.DirectorySeparatorChar) || target.Contains(Path.AltDirectorySeparatorChar))
        {
            var leaf = Path.GetFileName(target);
            if (!string.IsNullOrEmpty(leaf) && leaf.Length + 4 <= maxLength)
                return $".../{leaf}";
        }

        return target[..(maxLength - 3)] + "...";
    }
}
