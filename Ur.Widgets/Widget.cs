using Ur.Console;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// Abstract base for all layout and rendering elements in the TUI.
/// Widgets form a tree where each node can have children and must implement Draw()
/// to render its content to a canvas.
/// The LayoutEngine mutates the X, Y, Width, Height properties to position widgets
/// on the screen; Draw() receives a canvas constrained to that region.
/// </summary>
public abstract class Widget
{
    /// <summary>
    /// The parent widget in the tree, or null if this is the root.
    /// Internal setter so container widgets (ListView, ScrollView) in this assembly
    /// can manage parent references when they manipulate Children directly.
    /// </summary>
    public Widget? Parent { get; internal set; }

    /// <summary>
    /// Unordered children of this widget.
    /// </summary>
    public IList<Widget> Children { get; } = new List<Widget>();

    /// <summary>
    /// Absolute X coordinate assigned by the layout engine.
    /// </summary>
    public int X { get; set; }

    /// <summary>
    /// Absolute Y coordinate assigned by the layout engine.
    /// </summary>
    public int Y { get; set; }

    /// <summary>
    /// Width in columns assigned by the layout engine.
    /// </summary>
    public int Width { get; set; }

    /// <summary>
    /// Height in rows assigned by the layout engine.
    /// </summary>
    public int Height { get; set; }

    /// <summary>
    /// Axis along which children are arranged (Vertical or Horizontal).
    /// </summary>
    public LayoutDirection Direction { get; set; } = LayoutDirection.Vertical;

    /// <summary>
    /// How this widget sizes itself horizontally (Fit, Grow, or Fixed).
    /// </summary>
    public SizingMode HorizontalSizing { get; set; } = SizingMode.Fit;

    /// <summary>
    /// How this widget sizes itself vertically (Fit, Grow, or Fixed).
    /// </summary>
    public SizingMode VerticalSizing { get; set; } = SizingMode.Fit;

    /// <summary>
    /// Preferred width in columns (used when HorizontalSizing is Fit).
    /// </summary>
    public int PreferredWidth { get; set; }

    /// <summary>
    /// Preferred height in rows (used when VerticalSizing is Fit).
    /// </summary>
    public int PreferredHeight { get; set; }

    /// <summary>
    /// Fixed width in columns (overrides all sizing logic if non-zero).
    /// </summary>
    public int FixedWidth { get; set; }

    /// <summary>
    /// Fixed height in rows (overrides all sizing logic if non-zero).
    /// </summary>
    public int FixedHeight { get; set; }

    /// <summary>
    /// Minimum width in columns. The layout engine will not assign less.
    /// </summary>
    public int MinWidth { get; set; }

    /// <summary>
    /// Maximum width in columns. The layout engine will not exceed this.
    /// </summary>
    public int MaxWidth { get; set; }

    /// <summary>
    /// Minimum height in rows. The layout engine will not assign less.
    /// </summary>
    public int MinHeight { get; set; }

    /// <summary>
    /// Maximum height in rows. The layout engine will not exceed this.
    /// </summary>
    public int MaxHeight { get; set; }

    /// <summary>
    /// Space in rows/columns between adjacent children (used by LayoutEngine).
    /// </summary>
    public int ChildGap { get; set; }

    /// <summary>
    /// Space between the widget's boundary and its children.
    /// </summary>
    public Margin Margin { get; set; } = Margin.None;

    /// <summary>
    /// Space between the widget's boundary and its drawn content.
    /// </summary>
    public Padding Padding { get; set; } = Padding.None;

    /// <summary>
    /// Foreground and background colors, text modifiers, applied during Draw().
    /// </summary>
    public Style Style { get; set; } = Style.Default;

    /// <summary>
    /// Whether this widget can receive keyboard focus (not currently enforced).
    /// </summary>
    public bool Focusable { get; set; }

    /// <summary>
    /// Whether this widget currently has keyboard focus (not currently enforced).
    /// </summary>
    public bool IsFocused { get; set; }

    /// <summary>
    /// Adds a child widget and sets its Parent reference.
    /// </summary>
    public void AddChild(Widget child)
    {
        ArgumentNullException.ThrowIfNull(child);
        Children.Add(child);
        child.Parent = this;
    }

    /// <summary>
    /// Removes a child widget and clears its Parent reference if the removal succeeds.
    /// </summary>
    public void RemoveChild(Widget child)
    {
        if (Children.Remove(child))
            child.Parent = null;
    }

    /// <summary>
    /// Renders this widget to the provided canvas.
    /// X, Y, Width, Height have been set by the layout engine to constrain drawing.
    /// The canvas is already clipped to this widget's bounds.
    /// Subclasses should fill their content and may call canvas.Fill(), canvas.Write(), etc.
    /// </summary>
    public abstract void Draw(ICanvas canvas);

    /// <summary>
    /// Called by runners that support direct widget input handling (e.g., OopRunner).
    /// The default implementation is a no-op so that widgets which don't handle input
    /// (like Label, Stack) need no changes. Subclasses override this to respond to
    /// keyboard events when they have focus.
    /// </summary>
    public virtual void HandleInput(InputEvent input) { }
}
