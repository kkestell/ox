namespace Ox.Views;

/// <summary>
/// Pure text layout helpers shared by ConversationView and SidebarView.
/// No Terminal.Gui dependency — safe to test in isolation.
/// </summary>
internal static class TextLayout
{
    /// <summary>
    /// Word-wraps text to fit within the given width. Breaks at spaces when possible,
    /// falls back to hard breaks for long words. Newlines in the source are respected.
    ///
    /// Ported from the original TextRenderable.WrapText algorithm.
    /// </summary>
    public static List<string> WrapText(string text, int width)
    {
        var rows = new List<string>();

        if (string.IsNullOrEmpty(text))
        {
            rows.Add("");
            return rows;
        }

        // Split on '\n' first so explicit newlines always start a new row.
        // Trim trailing newlines before splitting — the model typically emits a
        // trailing '\n' before tool calls, which would otherwise produce a blank
        // row at the end of the text bubble.
        var paragraphs = text.TrimEnd('\r', '\n').Split('\n');

        foreach (var para in paragraphs)
        {
            if (para.Length == 0)
            {
                rows.Add("");
                continue;
            }

            if (para.Length <= width)
            {
                rows.Add(para);
                continue;
            }

            var remaining = para.AsSpan();
            while (remaining.Length > 0)
            {
                if (remaining.Length <= width)
                {
                    rows.Add(remaining.ToString());
                    break;
                }

                // Check if the character at the break boundary is a space.
                if (remaining[width] == ' ')
                {
                    rows.Add(remaining[..width].ToString());
                    remaining = remaining[(width + 1)..];
                    continue;
                }

                // Find the last space within the width limit for a word-boundary break.
                // breakAt == -1 means no space found; breakAt == 0 means space at
                // start of remaining — both should fall through to a hard break at
                // width to avoid emitting zero-length lines.
                var breakAt = remaining[..width].LastIndexOf(' ');
                if (breakAt < 1)
                    breakAt = width;

                rows.Add(remaining[..breakAt].ToString());
                remaining = breakAt < remaining.Length
                    ? remaining[(breakAt == width ? breakAt : breakAt + 1)..]
                    : ReadOnlySpan<char>.Empty;
            }
        }

        return rows;
    }
}
