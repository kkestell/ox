namespace Ur.Tui.Rendering;

/// <summary>
/// Core display contract for all visual elements in the conversation.
///
/// Renderables are "live" objects — their content can change between redraws.
/// The viewport calls <see cref="Render"/> each frame and writes the returned
/// rows to a <see cref="ScreenBuffer"/>. When a renderable's content changes
/// (e.g. a streaming chunk arrives), it raises <see cref="Changed"/> to tell
/// the viewport a redraw is needed.
///
/// Renderables do not know about the viewport or the terminal. They produce
/// typed <see cref="CellRow"/> values — no ANSI escape codes, no string
/// concatenation, no width-measurement hacks. All ANSI encoding happens in
/// <see cref="Terminal.Flush(ScreenBuffer)"/>, which is the sole point of
/// contact with raw escape sequences.
/// </summary>
internal interface IRenderable
{
    /// <summary>
    /// Returns rows to display, each containing cells that fit within
    /// <paramref name="availableWidth"/> columns. The renderable is responsible
    /// for word-wrapping text and truncating content to the given width.
    /// </summary>
    IReadOnlyList<CellRow> Render(int availableWidth);

    /// <summary>
    /// Raised when content changes and the viewport should schedule a redraw.
    /// </summary>
    event Action? Changed;
}
