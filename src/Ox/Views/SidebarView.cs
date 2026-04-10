using Terminal.Gui.App;
using Terminal.Gui.Drawing;
using Terminal.Gui.ViewBase;
using Ur.Todo;
using Attribute = Terminal.Gui.Drawing.Attribute;

namespace Ox.Views;

/// <summary>
/// Right-side panel showing context usage and the todo/plan list.
/// Positioned as a full-height column on the right edge of the terminal.
///
/// The sidebar is hidden (Visible = false) when no sections have content,
/// which lets the conversation area reclaim the full terminal width on startup.
/// The parent layout uses Pos/Dim relative to the sidebar so toggling Visible
/// triggers an automatic relayout.
/// </summary>
internal sealed class SidebarView : View
{
    // Box-drawing character for the vertical separator on the left edge.
    private const char SeparatorChar = '│';
    private static readonly Color Bg = new(ColorName16.Black);

    private readonly IApplication _app;
    private string? _contextUsage;
    private readonly TodoStore _todoStore;

    public SidebarView(IApplication app, TodoStore todoStore)
    {
        _app = app;
        _todoStore = todoStore;
        CanFocus = false;

        // Subscribe to todo changes to trigger redraws.
        _todoStore.Changed += () =>
        {
            _app.Invoke(() =>
            {
                UpdateVisibility();
                SetNeedsDraw();
            });
        };
    }

    /// <summary>
    /// Updates the context usage display string and triggers a redraw.
    /// Pass null to hide the context section.
    /// </summary>
    public void SetContextUsage(string? usage)
    {
        _contextUsage = usage;
        UpdateVisibility();
        SetNeedsDraw();
    }

    /// <summary>
    /// Returns true when the sidebar has content worth showing.
    /// </summary>
    public bool HasContent => _contextUsage is not null || _todoStore.Items.Count > 0;

    /// <summary>
    /// Updates Visible based on whether any section has content.
    /// </summary>
    public void UpdateVisibility()
    {
        Visible = HasContent;
    }

    /// <inheritdoc/>
    protected override bool OnDrawingContent(DrawContext? context)
    {
        var width = Frame.Width;
        var height = Frame.Height;
        if (width <= 0 || height <= 0)
            return true;

        // Clear the drawing area
        for (var row = 0; row < height; row++)
        {
            Move(0, row);
            SetAttribute(new Attribute(Color.None, Bg));
            for (var col = 0; col < width; col++)
                AddRune(' ');
        }

        // Draw the vertical separator on the left edge.
        var sepAttr = new Attribute(new Color(ColorName16.DarkGray), Bg);
        for (var row = 0; row < height; row++)
        {
            Move(0, row);
            SetAttribute(sepAttr);
            AddRune(SeparatorChar);
        }

        // Content starts after separator + 1 padding column.
        var contentStart = 2;
        var contentWidth = Math.Max(1, width - contentStart);
        var currentRow = 0;

        // --- Context section ---
        if (_contextUsage is not null)
        {
            // Header
            Move(contentStart, currentRow);
            SetAttribute(new Attribute(new Color(ColorName16.White), Bg, TextStyle.Bold));
            AddStr(Clip("Context", contentWidth));
            currentRow++;

            // Usage text
            if (currentRow < height)
            {
                Move(contentStart, currentRow);
                SetAttribute(new Attribute(new Color(ColorName16.DarkGray), Bg));
                AddStr(Clip(_contextUsage, contentWidth));
                currentRow++;
            }

            // Blank separator line
            currentRow++;
        }

        // --- Plan/Todo section ---
        var items = _todoStore.Items;
        if (items.Count > 0 && currentRow < height)
        {
            // Header
            Move(contentStart, currentRow);
            SetAttribute(new Attribute(new Color(ColorName16.White), Bg, TextStyle.Bold));
            AddStr(Clip("Plan", contentWidth));
            currentRow++;

            foreach (var item in items)
            {
                if (currentRow >= height) break;

                var (prefix, color) = item.Status switch
                {
                    TodoStatus.Completed => ("\u2713 ", new Color(ColorName16.Green)),       // ✓
                    TodoStatus.InProgress => ("\u25cf ", new Color(ColorName16.Yellow)),      // ●
                    _ => ("\u25cb ", new Color(ColorName16.DarkGray))                          // ○
                };

                var itemContentWidth = Math.Max(1, contentWidth - prefix.Length);
                var lines = TextLayout.WrapText(item.Content, itemContentWidth);

                for (var li = 0; li < lines.Count && currentRow < height; li++)
                {
                    Move(contentStart, currentRow);
                    if (li == 0)
                    {
                        SetAttribute(new Attribute(color, Bg));
                        AddStr(prefix);
                    }
                    else
                    {
                        SetAttribute(new Attribute(Color.None, Bg));
                        AddStr(new string(' ', prefix.Length));
                    }
                    SetAttribute(new Attribute(color, Bg));
                    AddStr(Clip(lines[li], itemContentWidth));
                    currentRow++;
                }
            }
        }

        return true;
    }

    /// <summary>Clips text to fit within the given width.</summary>
    private static string Clip(string text, int width) =>
        text.Length <= width ? text : text[..width];
}
