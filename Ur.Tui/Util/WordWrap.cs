namespace Ur.Tui.Util;

public static class WordWrap
{
    public static List<string> Wrap(string text, int width)
    {
        if (width <= 0)
            return [];

        if (string.IsNullOrEmpty(text))
            return [""];

        var lines = new List<string>();

        // Split on hard newlines first, then word-wrap each paragraph
        foreach (var paragraph in text.Split('\n'))
        {
            WrapParagraph(paragraph, width, lines);
        }

        return lines;
    }

    private static void WrapParagraph(string text, int width, List<string> lines)
    {
        if (text.Length == 0)
        {
            lines.Add("");
            return;
        }

        var remaining = text.AsSpan();

        while (remaining.Length > 0)
        {
            if (remaining.Length <= width)
            {
                lines.Add(remaining.ToString());
                break;
            }

            // Check if there's a space right at the width boundary
            if (remaining[width] == ' ')
            {
                lines.Add(remaining[..width].ToString());
                remaining = remaining[(width + 1)..];
                continue;
            }

            // Find the last space within the width limit
            var breakAt = -1;
            for (var i = width - 1; i >= 0; i--)
            {
                if (remaining[i] == ' ')
                {
                    breakAt = i;
                    break;
                }
            }

            if (breakAt <= 0)
            {
                // No space found — hard break at width
                lines.Add(remaining[..width].ToString());
                remaining = remaining[width..];
            }
            else
            {
                // Break at space, skip the space itself
                lines.Add(remaining[..breakAt].ToString());
                remaining = remaining[(breakAt + 1)..];
            }
        }
    }
}
