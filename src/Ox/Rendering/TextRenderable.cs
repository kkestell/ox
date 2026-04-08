using System.Text;
using Te.Rendering;

namespace Ox.Rendering;

/// <summary>
/// A renderable block of flowing text. Used for:
///   - Streaming assistant messages (chunks appended as they arrive)
///   - User messages (full text set once)
///   - Error messages
///
/// Word-wrapping is applied in <see cref="Render"/> so the caller only needs
/// to supply the available width; the renderable handles layout.
///
/// Foreground, background, and decorations are typed <see cref="Color"/>/<see cref="TextDecoration"/>
/// values rather than raw ANSI strings — the terminal layer is the only place
/// that knows about escape sequences.
/// </summary>
internal sealed class TextRenderable : IRenderable
{
    private readonly StringBuilder _text = new();

    private readonly Color           _foreground;
    private readonly Color           _background;
    private readonly TextDecoration  _decorations;

    // Cache invalidation: only recompute wrapped rows when text or width changes.
    private string? _lastText;
    private int _lastWidth;
    private IReadOnlyList<CellRow>? _cachedRows;

    public event Action? Changed;

    /// <param name="foreground">Foreground color applied to every cell in every rendered row.</param>
    /// <param name="background">Background color applied to every cell in every rendered row.</param>
    /// <param name="decorations">Decoration flags (bold, dim, etc.) applied uniformly across all cells.</param>
    public TextRenderable(
        Color           foreground   = default,
        Color           background   = default,
        TextDecoration  decorations  = TextDecoration.None)
    {
        _foreground  = foreground;
        _background  = background;
        _decorations = decorations;
    }

    /// <summary>
    /// Appends a streaming chunk to the accumulated text and signals the viewport.
    /// Called on the event-dispatch thread; no locking needed because the viewport
    /// reads on the same thread (the redraw timer fires on a ThreadPool thread but
    /// accesses the cached rows, not the StringBuilder, after the dirty flag is set).
    /// </summary>
    public void Append(string chunk)
    {
        if (string.IsNullOrEmpty(chunk))
            return;
        _text.Append(chunk);
        _cachedRows = null; // invalidate cache
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
        _cachedRows = null;
        Changed?.Invoke();
    }

    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        var current = _text.ToString();

        // Return cached result when nothing has changed since last render.
        if (_cachedRows != null && current == _lastText && availableWidth == _lastWidth)
            return _cachedRows;

        _lastText    = current;
        _lastWidth   = availableWidth;
        _cachedRows  = WrapText(current, availableWidth, _foreground, _background, _decorations);
        return _cachedRows;
    }

    /// <summary>
    /// Wraps <paramref name="text"/> into CellRows that fit within <paramref name="width"/>
    /// columns. The strategy is word-aware: it tries to break at spaces, falling back to
    /// hard breaks when a single word exceeds the width.
    ///
    /// Newlines in the source text are treated as hard breaks so that markdown-style
    /// paragraph structure is preserved.
    ///
    /// Every character in every row gets the same fg/bg/decorations — the text block is
    /// uniformly styled. Per-character styling is not needed by any current consumer.
    /// </summary>
    private static List<CellRow> WrapText(
        string text, int width, Color fg, Color bg, TextDecoration decorations)
    {
        var rows = new List<CellRow>();

        if (string.IsNullOrEmpty(text))
        {
            rows.Add(CellRow.FromText("", fg, bg, decorations));
            return rows;
        }

        // Split on '\n' first so explicit newlines always start a new row.
        // Trim trailing newlines before splitting — the model typically emits a
        // trailing '\n' before tool calls, which would otherwise produce a blank
        // row at the end of the text bubble (appearing as a gap before the tool node).
        var paragraphs = text.TrimEnd('\r', '\n').Split('\n');

        foreach (var para in paragraphs)
        {
            if (para.Length == 0)
            {
                // Blank line in source → blank rendered row.
                rows.Add(CellRow.Empty);
                continue;
            }

            // If the paragraph fits, emit it directly.
            if (para.Length <= width)
            {
                rows.Add(CellRow.FromText(para, fg, bg, decorations));
                continue;
            }

            // Word-wrap the paragraph into chunks.
            var remaining = para.AsSpan();
            while (remaining.Length > 0)
            {
                if (remaining.Length <= width)
                {
                    rows.Add(CellRow.FromText(remaining.ToString(), fg, bg, decorations));
                    break;
                }

                // Check if the character exactly at the break boundary is a space.
                // If so, split there and skip the space rather than hard-breaking mid-word.
                // e.g. "hello world" at width=5 → ["hello", "world"], not ["hello", " worl", "d"].
                if (remaining[width] == ' ')
                {
                    rows.Add(CellRow.FromText(remaining[..width].ToString(), fg, bg, decorations));
                    remaining = remaining[(width + 1)..];
                    continue;
                }

                // Find the last space within the width limit for a word-boundary break.
                var breakAt = remaining[..width].LastIndexOf(' ');
                if (breakAt <= 0)
                    breakAt = width; // no space found — hard break

                rows.Add(CellRow.FromText(remaining[..breakAt].ToString(), fg, bg, decorations));
                // Skip the space that caused the break; if we hard-broke (breakAt==width),
                // there is no space to skip so we continue from that position directly.
                remaining = breakAt < remaining.Length
                    ? remaining[(breakAt == width ? breakAt : breakAt + 1)..]
                    : ReadOnlySpan<char>.Empty;
            }
        }

        return rows;
    }
}
