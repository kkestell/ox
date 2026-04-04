using Ur.Console;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A viewport widget that scrolls a single content widget vertically.
///
/// Design overview (post-refactor):
/// - Content lives in Children, so the Renderer tree-walk handles drawing naturally.
///   No offscreen buffer, no manual blit loop.
/// - Scrolling works via Widget.OffsetY inherited from the base class. When the
///   Renderer draws our children, each child is translated by -(OffsetY), and
///   SubCanvas clips anything that falls outside our viewport. ScrollView's only
///   job is to set OffsetY correctly and paint the scrollbar.
/// - Layout(w, h) measures content height by calling _content.Layout(w-1, 0);
///   height=0 is the "unconstrained" convention used throughout this codebase.
///   _content.Height after that call is the natural height, available for scrollbar
///   calculations in HandleInput without caching a stale value.
/// - AutoScroll pins the viewport to the bottom until the user scrolls up, then
///   pauses. Scrolling back to the bottom re-enables it.
/// </summary>
public class ScrollView : Widget
{
    private readonly Widget _content;

    // Whether auto-scroll is currently engaged. Starts true so initial content
    // appears at the bottom (chat window UX).
    private bool _isAutoScrollActive = true;

    /// <summary>
    /// When true and the viewport is pinned to the bottom, new content automatically
    /// scrolls into view. Pauses when the user scrolls up; resumes at the bottom.
    /// </summary>
    public bool AutoScroll { get; set; } = true;

    /// <param name="content">
    /// The widget to scroll. Typically a ListView or Flex. Must not be null.
    /// </param>
    public ScrollView(Widget content)
    {
        ArgumentNullException.ThrowIfNull(content);
        _content = content;

        // Add to Children so the Renderer tree-walk draws it. OffsetY on this widget
        // translates the child downward (negative), clipped by SubCanvas.
        AddChild(_content);

        // Grow to fill the available space — ScrollView is typically the main body.
        HorizontalSizing = SizingMode.Grow;
        VerticalSizing   = SizingMode.Grow;

        // Focus so HandleInput receives Up/Down arrow keys for scrolling.
        Focusable = true;
    }

    /// <summary>
    /// Sizes this viewport (Width=w, Height=h) and measures the content's natural
    /// height by giving it the viewport width minus one column for the scrollbar.
    /// After this call _content.Height is valid and HandleInput can use it directly.
    /// </summary>
    public override void Layout(int availableWidth, int availableHeight)
    {
        Width  = availableWidth;
        Height = availableHeight;

        if (availableWidth <= 1)
            return;

        // height=0 means unconstrained — content reports its natural height.
        var contentWidth = availableWidth - 1;
        _content.X = 0;
        _content.Y = 0;
        _content.Layout(contentWidth, 0);

        // Now that we know content height, clamp OffsetY and handle auto-scroll.
        var maxScroll = Math.Max(0, _content.Height - Height);

        if (AutoScroll && _isAutoScrollActive)
            OffsetY = maxScroll;

        OffsetY = Math.Clamp(OffsetY, 0, maxScroll);
    }

    /// <summary>
    /// Draws the scrollbar only — the Renderer handles the content via Children.
    /// </summary>
    public override void Draw(ICanvas canvas)
    {
        if (Width <= 1 || Height <= 0)
            return;

        DrawScrollbar(canvas, _content.Height);
    }

    /// <summary>
    /// Handles Up/Down arrow keys to scroll the viewport.
    /// Up disables auto-scroll (user is reading history); Down re-enables it at bottom.
    /// </summary>
    public override void HandleInput(InputEvent input)
    {
        var maxScroll = Math.Max(0, _content.Height - Height);

        if (input is KeyEvent { Key: Key.Up })
        {
            OffsetY = Math.Max(0, OffsetY - 1);
            // Leaving the bottom pauses auto-scroll so the viewport stays put
            // while the user reads earlier messages.
            _isAutoScrollActive = false;
        }
        else if (input is KeyEvent { Key: Key.Down })
        {
            OffsetY = Math.Min(maxScroll, OffsetY + 1);
            // Re-enable auto-scroll once the user scrolls back to the bottom.
            if (AutoScroll && OffsetY >= maxScroll)
                _isAutoScrollActive = true;
        }
    }

    /// <summary>
    /// Draws the scrollbar in the rightmost column of the canvas.
    /// Full-block characters (█) mark the thumb; pipe characters (│) fill the track.
    /// When all content fits in the viewport, only the track is drawn (no thumb).
    /// </summary>
    private void DrawScrollbar(ICanvas canvas, int contentHeight)
    {
        var scrollbarX = Width - 1;

        if (contentHeight <= Height)
        {
            // Content fits — draw track only to visually mark the scrollbar region.
            canvas.DrawVLine(scrollbarX, 0, Height, '│', Style.Default);
            return;
        }

        var maxScroll = contentHeight - Height;

        // Thumb size is proportional to how much of the content is visible.
        var thumbSize = Math.Max(1, Height * Height / contentHeight);

        // Thumb position is proportional to scroll offset within the scrollable range.
        var thumbTop = (int)((float)OffsetY / maxScroll * (Height - thumbSize));

        for (var row = 0; row < Height; row++)
        {
            var ch = (row >= thumbTop && row < thumbTop + thumbSize) ? '█' : '│';
            canvas.SetCell(scrollbarX, row, ch, Style.Default);
        }
    }
}
