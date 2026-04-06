namespace Ur.Tui.Rendering;

/// <summary>
/// Core display contract for all visual elements in the conversation.
///
/// Renderables are "live" objects — their content can change between redraws.
/// The viewport calls <see cref="Render"/> each frame and writes the returned
/// lines to the screen. When a renderable's content changes (e.g. a streaming
/// chunk arrives), it raises <see cref="Changed"/> to tell the viewport a redraw
/// is needed.
///
/// Renderables do not know about the viewport or the terminal. They produce
/// plain strings (with embedded ANSI color codes) and let the viewport decide
/// where those strings land on screen.
/// </summary>
internal interface IRenderable
{
    /// <summary>
    /// Returns lines to display, each fitting within <paramref name="availableWidth"/>
    /// visible characters. ANSI escape sequences embedded in lines do not count
    /// toward the width — callers are responsible for measuring only printable chars.
    /// </summary>
    IReadOnlyList<string> Render(int availableWidth);

    /// <summary>
    /// Raised when content changes and the viewport should schedule a redraw.
    /// </summary>
    event Action? Changed;
}
