namespace Ox.Views;

/// <summary>
/// Pure text layout engine for conversation entries.
///
/// Takes a list of styled text fragments and wraps them into lines that fit
/// within a given column width. Handles word-boundary wrapping, hard breaks
/// for oversized words, and explicit newlines within fragment text.
///
/// The engine is generic over the style type so tests can use simple strings
/// while production code uses rendering-specific style objects.
/// </summary>
public static class ConversationTextLayout
{
    /// <summary>
    /// Lay out <paramref name="segments"/> into wrapped lines of at most
    /// <paramref name="width"/> columns.
    /// </summary>
    /// <returns>
    /// A list of lines, where each line is a list of fragments. Fragment text
    /// never contains newlines and always fits within the remaining width of
    /// its line.
    /// </returns>
    public static IReadOnlyList<IReadOnlyList<LayoutFragment<TStyle>>> LayoutSegments<TStyle>(
        IReadOnlyList<LayoutFragment<TStyle>> segments,
        int width)
    {
        if (width < 1) width = 1;

        var state = new LayoutState<TStyle>(width);

        foreach (var segment in segments)
        {
            var text = segment.Text;
            var style = segment.Style;

            // Split the fragment on explicit newlines. The first piece continues
            // the current line; each subsequent piece starts a new line.
            var parts = text.Split('\n');

            for (var partIndex = 0; partIndex < parts.Length; partIndex++)
            {
                // A newline boundary (partIndex > 0) forces a new line.
                if (partIndex > 0)
                    state.FinishLine();

                var part = parts[partIndex];
                if (part.Length == 0)
                    continue;

                WordWrap(part, style, state);
            }
        }

        return state.Finish();
    }

    /// <summary>
    /// Word-wrap a single piece of text (no embedded newlines) into the layout state.
    /// Words are separated by spaces. Inter-word spaces are included in the output
    /// so that concatenated fragment text reads naturally.
    /// </summary>
    private static void WordWrap<TStyle>(string text, TStyle style, LayoutState<TStyle> state)
    {
        var words = SplitWords(text);

        foreach (var word in words)
        {
            if (word.Length == 0)
                continue;

            // If the line already has content, we need a space before this word.
            // Check whether space + word fits on the current line.
            if (state.Col > 0)
            {
                if (state.Col + 1 + word.Length > state.Width)
                {
                    // Doesn't fit — wrap to a new line (no leading space).
                    state.FinishLine();
                }
                else
                {
                    // Fits — emit the word with a leading space.
                    state.Emit(new LayoutFragment<TStyle>(" " + word, style), word.Length + 1);
                    continue;
                }
            }

            // First word on the line (no leading space needed).
            // Hard-break if the word itself is wider than the line.
            if (word.Length > state.Width)
            {
                HardBreak(word, style, state);
            }
            else
            {
                state.Emit(new LayoutFragment<TStyle>(word, style), word.Length);
            }
        }
    }

    /// <summary>
    /// Break a word that's wider than the line width into column-width chunks.
    /// </summary>
    private static void HardBreak<TStyle>(string word, TStyle style, LayoutState<TStyle> state)
    {
        var pos = 0;
        while (pos < word.Length)
        {
            var available = state.Width - state.Col;
            if (available <= 0)
            {
                state.FinishLine();
                available = state.Width;
            }

            var chunkLen = Math.Min(available, word.Length - pos);
            var chunk = word.Substring(pos, chunkLen);
            state.Emit(new LayoutFragment<TStyle>(chunk, style), chunkLen);
            pos += chunkLen;
        }
    }

    /// <summary>
    /// Split text into words at space boundaries. Inter-word spaces are consumed
    /// as separators. Leading whitespace is preserved by attaching it to the
    /// first word — this keeps intentional indentation (e.g. tool result lines
    /// like "   install:") intact through the layout.
    /// </summary>
    private static List<string> SplitWords(string text)
    {
        var words = new List<string>();
        var i = 0;

        // Count leading whitespace — it'll be prepended to the first word.
        while (i < text.Length && text[i] == ' ') i++;
        var leadingSpaces = i;

        while (i < text.Length)
        {
            var start = i;
            while (i < text.Length && text[i] != ' ') i++;

            var word = text[start..i];

            // Prepend leading whitespace to the first word so indentation survives layout.
            if (words.Count == 0 && leadingSpaces > 0)
                word = text[..leadingSpaces] + word;

            words.Add(word);

            // Skip spaces between words.
            while (i < text.Length && text[i] == ' ') i++;
        }

        return words;
    }

    /// <summary>
    /// Mutable layout state tracked across all segments. Avoids threading
    /// ref-parameters through the recursive helpers.
    /// </summary>
    private sealed class LayoutState<TStyle>(int width)
    {
        private readonly List<List<LayoutFragment<TStyle>>> _lines = [];
        private List<LayoutFragment<TStyle>> _currentLine = [];

        public int Width { get; } = width;
        public int Col { get; private set; }

        /// <summary>Append a fragment to the current line.</summary>
        public void Emit(LayoutFragment<TStyle> fragment, int colAdvance)
        {
            _currentLine.Add(fragment);
            Col += colAdvance;
        }

        /// <summary>Finish the current line and start a new one.</summary>
        public void FinishLine()
        {
            _lines.Add(_currentLine);
            _currentLine = [];
            Col = 0;
        }

        /// <summary>Flush the last line and return all lines.</summary>
        public List<List<LayoutFragment<TStyle>>> Finish()
        {
            // Always include the current line — even if empty (represents
            // a trailing newline or a single-line layout).
            if (_currentLine.Count > 0 || _lines.Count == 0)
                _lines.Add(_currentLine);
            return _lines;
        }
    }
}
