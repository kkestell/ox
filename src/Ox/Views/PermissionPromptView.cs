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
/// The view is self-contained: it owns its own Label, TextField, and TCS lifecycle.
/// PermissionHandler calls <see cref="ShowAsync"/> to display the prompt and awaits
/// the user's response; it calls <see cref="Hide"/> to dismiss the view afterward.
/// No mode concept leaks into other components — InputAreaView stays in chat mode
/// permanently.
///
/// Layout (4 rows):
///   Row 0: top border (rounded)
///   Row 1: prompt label
///   Row 2: input field with "> " prefix
///   Row 3: bottom border (rounded)
///
/// Drawing follows the same custom rounded-border pattern as InputAreaView: manual
/// Move/AddRune/SetAttribute calls with the same box-drawing characters and colors.
/// </summary>
internal sealed class PermissionPromptView : View
{
    /// <summary>Total height of the permission prompt: top border + label + input + bottom border.</summary>
    internal const int ViewHeight = 4;

    // Rounded box-drawing characters — identical to InputAreaView.
    private const char TopLeftCorner = '╭';
    private const char TopRightCorner = '╮';
    private const char BottomLeftCorner = '╰';
    private const char BottomRightCorner = '╯';
    private const char HorizontalBorder = '─';
    private const char VerticalBorder = '│';

    // Colors — amber border distinguishes the permission prompt from the
    // chat input area so the user immediately notices the approval request.
    private static readonly Color Bg = new(ColorName16.Black);
    private static readonly Color BorderColor = new(255, 200, 50);  // Amber

    private readonly Label _label;
    private readonly TextField _textField;

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

        // Row 0 is the top border (drawn manually). Label sits at row 1.
        _label = new Label
        {
            X = 2,
            Y = 1,
            Width = Dim.Fill(Dim.Absolute(2)),
            Height = 1,
        };

        // TextField sits at row 2, inside the border chrome.
        _textField = new TextField
        {
            X = 2,
            Y = 2,
            Width = Dim.Fill(Dim.Absolute(2)),
            Height = 1,
            BorderStyle = LineStyle.None,
            CanFocus = true,
        };

        // Enter submits the response — same accept-path pattern as InputAreaView.
        _textField.Accepting += OnTextFieldAccepting;

        // Escape and Ctrl+C deny the permission request.
        _textField.KeyDown += OnTextFieldKeyDown;

        Add(_label, _textField);
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
        _label.Text = prompt;
        _textField.Text = "";

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
        _label.Text = "";
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

        // Row 1: label row — draw border edges (the Label draws its own content)
        DrawContentRowBorderEdges(1, width);

        // Row 2: input row — draw border edges (the TextField draws its own content)
        DrawContentRowBorderEdges(2, width);

        // Row 3: bottom border
        DrawBorderRow(3, width, BottomLeftCorner, BottomRightCorner);

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
    /// Draws the left and right border edges for a content row. The child view
    /// (Label or TextField) handles the content area between the edges.
    /// </summary>
    private void DrawContentRowBorderEdges(int row, int width)
    {
        // Left border + padding
        Move(0, row);
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
        AddRune(' ');

        // Right padding + border
        Move(width - 2, row);
        AddRune(' ');
        SetAttribute(new Attribute(BorderColor, Bg));
        AddRune(VerticalBorder);
    }
}
