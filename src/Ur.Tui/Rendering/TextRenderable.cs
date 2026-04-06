using System.Text;

namespace Ur.Tui.Rendering;

/// <summary>
/// A renderable block of flowing text. Used for:
///   - Streaming assistant messages (chunks appended as they arrive)
///   - User messages (full text set once)
///   - Error messages
///
/// Word-wrapping is applied in <see cref="Render"/> so the caller only needs
/// to supply the available width; the renderable handles layout.
///
/// An optional ANSI prefix applied to every rendered line allows the same type
/// to show user messages faintly and assistant text at full brightness.
/// </summary>
internal sealed class TextRenderable : IRenderable
{
    private readonly StringBuilder _text = new();

    // Optional ANSI style applied to every line. Empty string = no styling.
    private readonly string _linePrefix;
    private readonly string _lineSuffix;

    // Cache invalidation: only recompute wrapped lines when the text has changed.
    private string? _lastText;
    private int _lastWidth;
    private IReadOnlyList<string>? _cachedLines;

    public event Action? Changed;

    /// <param name="linePrefix">ANSI code to prepend to each rendered line (e.g. "\e[90m" for dim).</param>
    /// <param name="lineSuffix">ANSI reset suffix appended after each line (e.g. "\e[0m").</param>
    public TextRenderable(string linePrefix = "", string lineSuffix = "")
    {
        _linePrefix = linePrefix;
        _lineSuffix = lineSuffix;
    }

    /// <summary>
    /// Appends a streaming chunk to the accumulated text and signals the viewport.
    /// Called on the event-dispatch thread; no locking needed because the viewport
    /// reads on the same thread (the redraw timer fires on a ThreadPool thread but
    /// accesses the cached lines, not the StringBuilder, after the dirty flag is set).
    /// </summary>
    public void Append(string chunk)
    {
        if (string.IsNullOrEmpty(chunk))
            return;
        _text.Append(chunk);
        _cachedLines = null; // invalidate cache
        Changed?.Invoke();
    }

    /// <summary>
    /// Replaces the entire content with <paramref name="text"/> and signals the viewport.
    /// Used when the full text is known upfront (user messages, errors).
    /// </summary>
    public void SetText(string text)
    {
        _text.Clear();
        _text.Append(text);
        _cachedLines = null;
        Changed?.Invoke();
    }

    public IReadOnlyList<string> Render(int availableWidth)
    {
        var current = _text.ToString();

        // Return cached result when nothing has changed since last render.
        if (_cachedLines != null && current == _lastText && availableWidth == _lastWidth)
            return _cachedLines;

        _lastText = current;
        _lastWidth = availableWidth;
        _cachedLines = WrapText(current, availableWidth, _linePrefix, _lineSuffix);
        return _cachedLines;
    }

    /// <summary>
    /// Wraps <paramref name="text"/> into lines that fit within <paramref name="width"/>
    /// visible characters. The wrapping strategy is word-aware: it tries to break at
    /// spaces, falling back to hard breaks when a single word exceeds the width.
    ///
    /// Newlines in the source text are treated as hard breaks so that markdown-style
    /// paragraph structure is preserved.
    /// </summary>
    private static IReadOnlyList<string> WrapText(string text, int width, string prefix, string suffix)
    {
        var lines = new List<string>();

        if (string.IsNullOrEmpty(text))
        {
            lines.Add(prefix + suffix);
            return lines;
        }

        // Split on '\n' first so explicit newlines always start a new line.
        var paragraphs = text.Split('\n');

        foreach (var para in paragraphs)
        {
            if (para.Length == 0)
            {
                // Blank line in source → blank rendered line.
                lines.Add(prefix + suffix);
                continue;
            }

            // If the paragraph fits, emit it directly.
            if (para.Length <= width)
            {
                lines.Add(prefix + para + suffix);
                continue;
            }

            // Word-wrap the paragraph into chunks.
            var remaining = para.AsSpan();
            while (remaining.Length > 0)
            {
                if (remaining.Length <= width)
                {
                    lines.Add(prefix + remaining.ToString() + suffix);
                    break;
                }

                // Check if the character exactly at the break boundary is a space.
                // If so, split there and skip the space rather than hard-breaking mid-word.
                // e.g. "hello world" at width=5 → ["hello", "world"], not ["hello", " worl", "d"].
                if (remaining[width] == ' ')
                {
                    lines.Add(prefix + remaining[..width].ToString() + suffix);
                    remaining = remaining[(width + 1)..];
                    continue;
                }

                // Find the last space within the width limit for a word-boundary break.
                var breakAt = remaining[..width].LastIndexOf(' ');
                if (breakAt <= 0)
                    breakAt = width; // no space found — hard break

                lines.Add(prefix + remaining[..breakAt].ToString() + suffix);
                // Skip the space that caused the break; if we hard-broke (breakAt==width),
                // there is no space to skip so we continue from that position directly.
                remaining = breakAt < remaining.Length
                    ? remaining[(breakAt == width ? breakAt : breakAt + 1)..]
                    : ReadOnlySpan<char>.Empty;
            }
        }

        return lines;
    }
}
