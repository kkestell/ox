using Ur.Console;
using Ur.Drawing;

namespace Ur.Widgets;

/// <summary>
/// A viewport widget that renders a single content widget into an offscreen buffer
/// and blits only the visible rows to the screen, enabling scrolling of content
/// that is taller than the viewport.
///
/// Design overview:
/// - Content is stored in a private field (NOT in Children) so the normal Renderer
///   tree-walk skips it. ScrollView owns content rendering entirely in Draw().
/// - Scrolling is implemented via offscreen buffer + blit. The full content is
///   rendered each frame into a temporary Screen, and only the visible slice is
///   copied to the actual canvas. This trades CPU for simplicity — no coordinate
///   translation, no partial rendering, no clip stack manipulation.
/// - AutoScroll pins the viewport to the bottom until the user scrolls up, then
///   pauses. Scrolling back to the bottom re-enables it. This is the standard
///   chat/log viewer UX.
/// </summary>
public class ScrollView : Widget
{
    private readonly Widget _content;

    // Scroll offset in rows from the top of the content.
    // 0 = content top aligned with viewport top.
    // maxScroll = content bottom aligned with viewport bottom.
    private int _scrollOffset;

    // Cached content height from the most recent Draw() — used in HandleInput
    // to compute maxScroll without re-running layout on every key press.
    private int _contentHeight;

    // Whether auto-scroll is currently engaged. Starts true so initial content
    // appears at the bottom (as expected for a chat window).
    private bool _isAutoScrollActive = true;

    /// <summary>
    /// When true and the viewport is pinned to the bottom, new content automatically
    /// scrolls into view. Pauses when the user scrolls up; resumes when they
    /// scroll back to the bottom.
    /// </summary>
    public bool AutoScroll { get; set; } = true;

    /// <param name="content">
    /// The widget to scroll. Typically a ListView, Stack, or any widget that can
    /// grow taller than this viewport. Must not be null.
    /// </param>
    public ScrollView(Widget content)
    {
        ArgumentNullException.ThrowIfNull(content);
        _content = content;

        // Grow to fill the available space — ScrollView is typically the main body
        // of a layout and should expand to take whatever room the parent gives it.
        HorizontalSizing = SizingMode.Grow;
        VerticalSizing = SizingMode.Grow;

        // Needs focus so HandleInput receives Up/Down arrow keys for scrolling.
        Focusable = true;
    }

    /// <summary>
    /// Renders the scrollable content and scrollbar.
    ///
    /// Steps:
    /// 1. Layout content at natural height (width = viewport - 1 for scrollbar column).
    /// 2. Build offscreen buffer tall enough for all content rows.
    /// 3. Render full content tree into offscreen buffer.
    /// 4. Blit visible slice [_scrollOffset, _scrollOffset + Height) to canvas.
    /// 5. Draw scrollbar in the rightmost column.
    /// </summary>
    public override void Draw(ICanvas canvas)
    {
        if (Width <= 1 || Height <= 0)
            return;

        // Reserve the rightmost column for the scrollbar.
        var contentWidth = Width - 1;

        // Measure the content's natural height given our viewport width.
        // availableHeight=0 tells LayoutWithConstraints to leave height unconstrained
        // so the content can report its full natural height. Children are positioned
        // relative to (0, 0) in offscreen coordinate space, ready for RenderTree.
        LayoutEngine.LayoutWithConstraints(_content, 0, 0, contentWidth, 0);
        var contentHeight = _content.Height;
        _contentHeight = contentHeight; // cache for HandleInput

        var maxScroll = Math.Max(0, contentHeight - Height);

        // When auto-scroll is active, keep the viewport pinned to the bottom.
        if (AutoScroll && _isAutoScrollActive)
            _scrollOffset = maxScroll;

        _scrollOffset = Math.Clamp(_scrollOffset, 0, maxScroll);

        if (contentHeight <= 0)
        {
            DrawScrollbar(canvas, contentHeight);
            return;
        }

        // Render the full content into an offscreen screen buffer.
        // The entire content tree is rendered here every frame. For TUI scale
        // (hundreds of rows at most), this is not a performance concern.
        var offscreen = new Screen(contentWidth, contentHeight);
        var offscreenCanvas = CanvasFactory.CreateCanvas(offscreen);
        Renderer.RenderTree(_content, offscreenCanvas);

        // Blit only the visible rows to the actual canvas.
        // This is the viewport clipping step — translate from content coordinates
        // to canvas coordinates by subtracting the scroll offset.
        for (var row = 0; row < Height; row++)
        {
            var sourceRow = _scrollOffset + row;
            if (sourceRow >= contentHeight)
                break;

            for (var col = 0; col < contentWidth; col++)
            {
                var cell = offscreen.Get(col, sourceRow);
                canvas.SetCell(col, row, cell.Rune, cell.Style);
            }
        }

        DrawScrollbar(canvas, contentHeight);
    }

    /// <summary>
    /// Handles Up/Down arrow keys to scroll the viewport.
    /// Up scrolls toward the top and disables auto-scroll (viewport leaves the bottom).
    /// Down scrolls toward the bottom and re-enables auto-scroll if the user reaches it.
    /// </summary>
    public override void HandleInput(InputEvent input)
    {
        var maxScroll = Math.Max(0, _contentHeight - Height);

        if (input is KeyEvent { Key: Key.Up })
        {
            _scrollOffset = Math.Max(0, _scrollOffset - 1);
            // Leaving the bottom pauses auto-scroll — new content arrives but the
            // viewport stays put so the user can read earlier history.
            _isAutoScrollActive = false;
        }
        else if (input is KeyEvent { Key: Key.Down })
        {
            _scrollOffset = Math.Min(maxScroll, _scrollOffset + 1);
            // Re-enable auto-scroll once the user scrolls back to the bottom.
            if (AutoScroll && _scrollOffset >= maxScroll)
                _isAutoScrollActive = true;
        }
    }

    /// <summary>
    /// Draws the scrollbar in the rightmost column of the canvas.
    /// Full-block characters (█) mark the thumb; pipe characters (│) fill the track.
    /// When all content fits in the viewport, only the track is drawn (no thumb needed).
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

        // Thumb position proportional to scroll offset within the scrollable range.
        var thumbTop = (int)((float)_scrollOffset / maxScroll * (Height - thumbSize));

        for (var row = 0; row < Height; row++)
        {
            var ch = (row >= thumbTop && row < thumbTop + thumbSize) ? '█' : '│';
            canvas.SetCell(scrollbarX, row, ch, Style.Default);
        }
    }
}
