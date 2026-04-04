using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A modal dialog widget that renders as a bordered, titled overlay on top of the
/// main application content. Subclasses populate the Content area with form fields,
/// labels, or any other widgets to build custom dialogs (confirmations, input forms, etc.).
///
/// Dialog lives outside the Root widget tree — it's managed by Application's modal
/// stack. This is a deliberate architectural choice: modality is an Application concern
/// (input routing, focus scoping), while Dialog handles its own layout and rendering
/// like any other container widget.
///
/// Layout structure (top to bottom):
///   ╭── Title ──╮
///   │ Content    │  ← subclass populates via protected Content property
///   ├────────────┤
///   │   [OK] [X] │  ← right-aligned button row
///   ╰────────────╯
///
/// Follows the ScrollView pattern: a container widget with chrome (border, title,
/// separator) that manages child positioning directly in Layout() rather than
/// delegating to Flex. Children are added to the widget tree so the Renderer's
/// depth-first walk draws them automatically.
/// </summary>
public class Dialog : Widget
{
    private readonly string _title;
    private readonly Flex _buttonRow;

    /// <summary>
    /// Content area exposed to subclasses. Add labels, text inputs, or any widgets
    /// here to build the dialog's body. The content grows vertically to fill
    /// available space above the separator and button row.
    /// </summary>
    protected Flex Content { get; }

    /// <summary>
    /// Fired when the dialog is dismissed, either by clicking OK/Cancel or pressing
    /// Escape. Callers subscribe to this to retrieve form data after dismissal.
    /// Follows the Button.Clicked pattern — simple Action delegate with a result enum.
    /// </summary>
    public event Action<DialogResult>? Closed;

    /// <param name="title">Text displayed centered in the top border row.</param>
    /// <param name="showCancelButton">
    /// When false, only the OK button is shown. Useful for informational dialogs
    /// where cancellation doesn't make sense.
    /// </param>
    public Dialog(string title, bool showCancelButton = true)
    {
        _title = title;

        // Content area: subclasses add their widgets here. Fit sizing so that
        // the first (unconstrained) Layout pass measures natural content height
        // correctly — Grow would collapse to zero when availableHeight is 0.
        Content = Flex.Vertical();
        Content.HorizontalSizing = SizingMode.Grow;
        AddChild(Content);

        // Button row: a grow-mode spacer pushes buttons to the right edge,
        // following standard dialog UX conventions.
        _buttonRow = Flex.Horizontal();
        _buttonRow.HorizontalSizing = SizingMode.Grow;

        var spacer = Flex.Horizontal();
        spacer.HorizontalSizing = SizingMode.Grow;
        _buttonRow.AddChild(spacer);

        var okButton = new Button("OK");
        okButton.Clicked += () => Close(DialogResult.OK);
        _buttonRow.AddChild(okButton);

        if (showCancelButton)
        {
            var cancelButton = new Button("Cancel");
            cancelButton.Clicked += () => Close(DialogResult.Cancel);
            _buttonRow.AddChild(cancelButton);
        }

        AddChild(_buttonRow);
    }

    /// <summary>
    /// Fires the Closed event. Subclasses call this if they need to dismiss the
    /// dialog programmatically (e.g. on a custom key binding). Application's modal
    /// stack subscribes to Closed and pops the dialog automatically.
    /// </summary>
    /// <summary>
    /// protected: subclasses can dismiss programmatically.
    /// internal: Application needs access for Escape-key handling.
    /// </summary>
    protected internal void Close(DialogResult result) => Closed?.Invoke(result);

    /// <summary>
    /// Sizes the dialog proportionally to the available terminal space and positions
    /// the content area and button row inside the border chrome.
    ///
    /// Width: 60% of available, with at least a 2-cell margin on each side.
    /// Height: natural content height plus chrome (border + separator + buttons),
    /// capped at 80% of available height. Content-fitting gives dialogs a natural
    /// feel while the cap prevents them from dominating the screen.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        // Proportional width: 60% of terminal, but guarantee at least 2 cells of
        // visible background on each side so the user sees the dimmed overlay.
        Width = Math.Min((int)(availableWidth * 0.6), availableWidth - 4);
        Width = Math.Max(Width, 10); // usable minimum for border + buttons

        var interiorWidth = Width - 2; // inside left and right border columns

        // Measure content with unconstrained height (height=0 convention) to find
        // its natural size, then add chrome: top border, separator, button row,
        // bottom border = 4 extra rows.
        Content.Layout(interiorWidth, 0);
        var naturalHeight = Content.Height + 4;
        var maxHeight = (int)(availableHeight * 0.8);
        Height = Math.Clamp(naturalHeight, 5, maxHeight);

        // Position content inside the border, between the top border and separator.
        // Available content height is total minus chrome rows.
        var contentHeight = Height - 4;
        Content.X = 1;
        Content.Y = 1;
        Content.Layout(interiorWidth, contentHeight);

        // Button row: one row above the bottom border, below the separator.
        _buttonRow.X = 1;
        _buttonRow.Y = Height - 2;
        _buttonRow.Layout(interiorWidth, 1);
    }

    /// <summary>
    /// Draws the dialog chrome: filled background, rounded border, centered title
    /// in the top border, and a horizontal separator above the button row. Child
    /// widgets (content, buttons) are drawn by the Renderer's tree walk.
    /// </summary>
    public override void Draw(ICanvas canvas)
    {
        var style = Style.Default;

        // Fill interior so dialog content renders on a solid background rather
        // than showing through to the dimmed application behind it.
        canvas.DrawRect(new Rect(1, 1, Width - 2, Height - 2), ' ', style);

        // Rounded border gives dialogs a softer, modern look that visually
        // distinguishes them from the main application's UI chrome.
        canvas.DrawBorder(new Rect(0, 0, Width, Height), style, BorderSet.Rounded);

        // Title centered in the top border row, flanked by spaces for readability.
        if (!string.IsNullOrEmpty(_title))
        {
            var titleText = $" {_title} ";
            var titleX = Math.Max(1, (Width - titleText.Length) / 2);
            canvas.DrawText(titleX, 0, titleText, style);
        }

        // Horizontal separator between content and button row. Uses single-line
        // tee characters (├ ┤) which connect cleanly to the rounded border's
        // vertical lines (│).
        var separatorY = Height - 3;
        canvas.SetCell(0, separatorY, '├', style);
        canvas.DrawHLine(1, separatorY, Width - 2, '─', style);
        canvas.SetCell(Width - 1, separatorY, '┤', style);
    }
}
