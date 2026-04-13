namespace Ox.App.Views;

/// <summary>
/// Convenience wrapper around <see cref="ConversationTextLayout"/> for simple
/// string-in, list-of-strings-out word wrapping. Used by callers that don't
/// need the full styled-fragment layout pipeline.
/// </summary>
public static class TextLayout
{
    /// <summary>
    /// Word-wrap <paramref name="text"/> into lines of at most <paramref name="width"/>
    /// columns. Respects explicit newlines, breaks at word boundaries, and hard-breaks
    /// words that exceed the width. Trailing newlines are trimmed.
    /// </summary>
    public static IReadOnlyList<string> WrapText(string text, int width)
    {
        // Trim trailing newlines to avoid phantom empty lines at the end.
        text = text.TrimEnd('\n', '\r');

        // Delegate to the full layout engine with a dummy style.
        var segments = new List<LayoutFragment<string>>
        {
            new(text, "default"),
        };

        var lines = ConversationTextLayout.LayoutSegments(segments, width);

        // Flatten each line's fragments into a single string.
        return lines
            .Select(line => string.Concat(line.Select(f => f.Text)))
            .ToList();
    }
}
