namespace Ox.Views;

/// <summary>
/// Pure text-segmentation logic shared by the conversation entry renderer and
/// unit tests.
///
/// The bug here is about how adjacent text segments compose across newlines, not
/// about any particular UI framework type. Keeping the algorithm generic lets
/// tests validate line shaping without loading Terminal.Gui.
/// </summary>
internal static class ConversationTextLayout
{
    /// <summary>
    /// Wraps styled text fragments into rendered rows while preserving fragment
    /// boundaries for the caller.
    /// </summary>
    public static List<List<LayoutFragment<TStyle>>> LayoutSegments<TStyle>(
        IReadOnlyList<LayoutFragment<TStyle>> segments,
        int width)
    {
        width = Math.Max(1, width);

        // Segment boundaries are styling boundaries, not layout boundaries. We
        // first rebuild the logical text lines across all segments, then wrap
        // those lines. This prevents newline-prefixed follow-up segments from
        // creating phantom empty rows between adjacent lines.
        var logicalLines = BuildLogicalLines(TrimTrailingLineBreaks(segments));
        if (logicalLines.Count == 0)
            return [[]];

        var wrappedLines = new List<List<LayoutFragment<TStyle>>>();
        foreach (var logicalLine in logicalLines)
        {
            foreach (var wrappedLine in WrapLogicalLine(logicalLine, width))
                wrappedLines.Add(wrappedLine);
        }

        return wrappedLines.Count == 0 ? [[]] : wrappedLines;
    }

    private static List<LayoutFragment<TStyle>> TrimTrailingLineBreaks<TStyle>(
        IReadOnlyList<LayoutFragment<TStyle>> segments)
    {
        var trimmed = segments.ToList();

        while (trimmed.Count > 0)
        {
            var last = trimmed[^1];
            if (last.Text.Length == 0)
            {
                trimmed.RemoveAt(trimmed.Count - 1);
                continue;
            }

            var trimmedText = last.Text.TrimEnd('\r', '\n');
            if (trimmedText.Length == last.Text.Length)
                break;

            if (trimmedText.Length == 0)
            {
                trimmed.RemoveAt(trimmed.Count - 1);
                continue;
            }

            trimmed[^1] = last with { Text = trimmedText };
            break;
        }

        return trimmed;
    }

    private static List<List<LayoutFragment<TStyle>>> BuildLogicalLines<TStyle>(
        IReadOnlyList<LayoutFragment<TStyle>> segments)
    {
        var lines = new List<List<LayoutFragment<TStyle>>> { new List<LayoutFragment<TStyle>>() };

        foreach (var segment in segments)
        {
            var parts = segment.Text.Split('\n');
            for (var i = 0; i < parts.Length; i++)
            {
                if (parts[i].Length > 0)
                    lines[^1].Add(new LayoutFragment<TStyle>(parts[i], segment.Style));

                if (i < parts.Length - 1)
                    lines.Add([]);
            }
        }

        return lines;
    }

    private static List<List<LayoutFragment<TStyle>>> WrapLogicalLine<TStyle>(
        List<LayoutFragment<TStyle>> logicalLine,
        int width)
    {
        var characters = FlattenCharacters(logicalLine);
        if (characters.Count == 0)
            return [[]];

        var wrapped = new List<List<LayoutFragment<TStyle>>>();
        var start = 0;

        while (start < characters.Count)
        {
            var remaining = characters.Count - start;
            var take = remaining;
            var skip = 0;

            if (remaining > width)
            {
                if (characters[start + width].Character == ' ')
                {
                    take = width;
                    skip = 1;
                }
                else
                {
                    var breakAt = -1;
                    for (var i = 0; i < width; i++)
                    {
                        if (characters[start + i].Character == ' ')
                            breakAt = i;
                    }

                    if (breakAt < 1)
                    {
                        take = width;
                    }
                    else
                    {
                        take = breakAt;
                        skip = 1;
                    }
                }
            }

            wrapped.Add(CompactFragments(characters, start, take));
            start += take + skip;
        }

        return wrapped;
    }

    private static List<StyledCharacter<TStyle>> FlattenCharacters<TStyle>(
        List<LayoutFragment<TStyle>> logicalLine)
    {
        var characters = new List<StyledCharacter<TStyle>>();

        foreach (var fragment in logicalLine)
        {
            foreach (var character in fragment.Text)
                characters.Add(new StyledCharacter<TStyle>(character, fragment.Style));
        }

        return characters;
    }

    private static List<LayoutFragment<TStyle>> CompactFragments<TStyle>(
        List<StyledCharacter<TStyle>> characters,
        int start,
        int length)
    {
        var fragments = new List<LayoutFragment<TStyle>>();
        var comparer = EqualityComparer<TStyle>.Default;

        for (var i = 0; i < length; i++)
        {
            var current = characters[start + i];
            if (fragments.Count == 0 || !comparer.Equals(fragments[^1].Style, current.Style))
            {
                fragments.Add(new LayoutFragment<TStyle>(current.Character.ToString(), current.Style));
                continue;
            }

            fragments[^1] = fragments[^1] with { Text = fragments[^1].Text + current.Character };
        }

        return fragments;
    }
}

/// <summary>
/// Text plus an arbitrary style payload. The layout core keeps the payload opaque
/// so higher layers can decide how styling should be represented.
/// </summary>
internal readonly record struct LayoutFragment<TStyle>(string Text, TStyle Style);

internal readonly record struct StyledCharacter<TStyle>(char Character, TStyle Style);
