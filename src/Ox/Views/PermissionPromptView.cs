using Terminal.Gui.Drawing;
using Terminal.Gui.Drivers;
using Terminal.Gui.Input;
using Terminal.Gui.ViewBase;
using Terminal.Gui.Views;
using Attribute = Terminal.Gui.Drawing.Attribute;

namespace Ox.Views;

/// <summary>
/// A floating permission prompt that overlaps the bottom of the ConversationView,
/// sitting directly above the InputAreaView. When a tool needs user approval, this
/// view becomes visible with the prompt text and a dedicated TextField for input.
///
/// The view is self-contained: it owns its own TextField and TCS lifecycle.
/// PermissionHandler calls <see cref="ShowAsync"/> to display the prompt and awaits
/// the user's response; it calls <see cref="Hide"/> to dismiss the view afterward.
/// No mode concept leaks into other components — InputAreaView stays in chat mode
/// permanently.
///
/// Layout (3 rows):
///   Row 0: top border (rounded)
///   Row 1: prompt text + text field on a single row
///   Row 2: bottom border (rounded)
///
/// The prompt text is drawn manually in OnDrawingContent (same pattern as
/// InputAreaView's border chrome). The TextField is repositioned in ShowAsync
/// to sit immediately after the prompt text.
/// </summary>
internal sealed class PermissionPromptView : View
{
    /// <summary>Total height: top border + content row + bottom border.</summary>
    internal const int ViewHeight = 3;

    // Rounded box-drawing characters — identical to InputAreaView.
    private const char TopLeftCorner = '╭';
    private const char TopRightCorner = '╮';
    private const char BottomLeftCorner = '╰';
    private const char BottomRightCorner = '╯';
    private const char HorizontalBorder = '─';
    private const char VerticalBorder = '│';

    // Colors — same as InputAreaView.
    private static readonly Color Bg = new(ColorName16.Black);
    private static readonly Color BorderColor = new(244, 244, 244);

    private readonly TextField _textField;

    // The prompt text drawn manually in OnDrawingContent, left of the TextField.
    private string _promptText = "";

    // Non-null only while a prompt is active (between ShowAsync and Hide).
    private TaskCompletionSource<string?>? _tcs;
    private CancellationTokenRegistration _ctRegistration;

    public PermissionPromptView()
    {
        // Overlapped arrangement lets this view float above siblings in the
        // SubView list without displacing them — the same pattern Terminal.Gui's
        // Window class uses for layered views.
        Arrangement = ViewArrangement.Overlapped;
        Visible = false;
        CanFocus = true;

        // TextField sits on row 1, repositioned in ShowAsync to follow the prompt text.
        _textField = new TextField
        {
            X = 2,
            Y = 1,
            Width = Dim.Fill(Dim.Absolute(2)),
            Height = 1,
            BorderStyle = LineStyle.None,
            CanFocus = true,
        };

        // Enter submits the response — same accept-path pattern as InputAreaView.
        _textField.Accepting += OnTextFieldAccepting;

        // Escape and Ctrl+C deny the permission request.
        _textField.KeyDown += OnTextFieldKeyDown;

        Add(_textField);
    }

    // --- Public API ---

    /// <summary>
    /// Displays the permission prompt and returns a task that resolves with the
    /// user's input string, or null if denied (Escape/Ctrl+C) or cancelled.
    ///
    /// Must be called on the UI thread (via App.Invoke). The caller awaits the
    /// returned task from the background thread — the same Invoke/TCS bridge
    /// pattern used throughout the app.
    /// </summary>
    public Task<string?> ShowAsync(string prompt, CancellationToken ct)
    {
        _promptText = prompt;
        _textField.Text = "";

        // Position the TextField immediately after the prompt text.
        // Left border (1) + space (1) + prompt text length = starting column.
        var textFieldX = 2 + _promptText.Length;
        _textField.X = textFieldX;
        _textField.Width = Dim.Fill(Dim.Absolute(2));

        _tcs = new TaskCompletionSource<string?>(
            TaskCreationOptions.RunContinuationsAsynchronously);

        // Link cancellation so a cancelled CT resolves the prompt as denied.
        if (ct.CanBeCanceled)
            _ctRegistration = ct.Register(() => _tcs.TrySetResult(null));

        Visible = true;
        _textField.SetFocus();

        return _tcs.Task;
    }

    /// <summary>
    /// Hides the permission prompt and clears internal state. Focus restoration
    /// is the caller's responsibility (PermissionHandler transfers focus back to
    /// the InputAreaView via App.Invoke).
    /// </summary>
    public void Hide()
    {
        Visible = false;
        _ctRegistration.Dispose();
        _ctRegistration = default;
        _tcs = null;
        _promptText = "";
        _textField.Text = "";
    }

    // --- Event handlers ---

    /// <summary>
    /// Enter key: resolve the TCS with the user's input text.
    /// </summary>
    private void OnTextFieldAccepting(object? sender, CommandEventArgs e)
    {
        _tcs?.TrySetResult(_textField.Text);
        e.Handled = true;
    }

    /// <summary>
    /// Escape and Ctrl+C: deny the permission by resolving the TCS with null.
    /// </summary>
    private void OnTextFieldKeyDown(object? sender, Key key)
    {
        var keyCode = key.KeyCode;

        if (keyCode == KeyCode.Esc || keyCode == (KeyCode.C | KeyCode.CtrlMask))
        {
            _tcs?.TrySetResult(null);
            key.Handled = true;
        }
    }

    // --- Drawing ---

    /// <inheritdoc/>
    protected override bool OnDrawingContent(DrawContext? context)
    {
        var width = Frame.Width;
        var height = Frame.Height;
        if (width <= 0 || height < ViewHeight)
            return true;

        // Row 0: top border
        DrawBorderRow(0, width, TopLeftCorner, TopRightCorner);

        // Row 1: prompt text + TextField. Draw the border edges and prompt text
        // manually; the TextField handles its own content area.
        DrawContentRow(1, width);

        // Row 2: bottom border
        DrawBorderRow(2, width, BottomLeftCorner, BottomRightCorner);

        return true;
    }

    /// <summary>Draws a horizontal border row with corner characters.</summary>
    private void DrawBorderRow(int row, int width, char leftCorner, char rightCorner)
    {
        Move(0, row);
        SetAttribute(new Attribute(BorderColor, Bg));

        AddRune(leftCorner);
        for (var i = 1; i < width - 1; i++)
            AddRune(HorizontalBorder);
        if (width > 1)
            AddRune(rightCorner);
    }

    /// <summary>
    /// Draws the content row: left border, prompt text, then the TextField fills
    /// the remainder. Right border is drawn at the end.
    /// </summary>
    private void DrawContentRow(int row, int width)
    {
        // Left border + padding
        Move(0, row);
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
        AddRune(' ');

        // Prompt text (drawn manually so the TextField can sit right after it)
        SetAttribute(new Attribute(new Color(ColorName16.White), Bg));
        var maxPromptLen = Math.Max(0, width - 4); // border+pad on each side
        var drawLen = Math.Min(_promptText.Length, maxPromptLen);
        AddStr(_promptText[..drawLen]);

        // Right padding + border
        Move(width - 2, row);
        AddRune(' ');
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
    }
}
