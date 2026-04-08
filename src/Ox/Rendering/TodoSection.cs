using Ur.Todo;

namespace Ox.Rendering;

/// <summary>
/// Renders the current todo list as a sidebar section. Subscribes to
/// <see cref="TodoStore.Changed"/> so the sidebar re-renders when the LLM
/// updates the list via <c>todo_write</c>.
///
/// Layout:
///   Plan                    (bold white — section header)
///   ✓ Read config           (green — completed)
///   ● Implement feature     (yellow — in progress)
///   ○ Write tests           (BrightBlack — pending)
/// </summary>
internal sealed class TodoSection : ISidebarSection
{
    private readonly TodoStore _store;

    // Status indicator glyphs and their colors. Compact prefixes — no leading
    // indent since the viewport already pads the sidebar content by one column.
    private const string CompletedPrefix  = "\u2713 "; // ✓
    private const string InProgressPrefix = "\u25cf "; // ●
    private const string PendingPrefix    = "\u25cb "; // ○

    public TodoSection(TodoStore store)
    {
        _store = store;
        // Propagate store changes to the sidebar's Changed event chain so
        // the viewport picks up todo updates at the next timer tick.
        _store.Changed += () => Changed?.Invoke();
    }

    public bool HasContent => _store.Items.Count > 0;

    public event Action? Changed;

    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        var items = _store.Items;
        if (items.Count == 0)
            return [];

        var rows = new List<CellRow>();

        // Header: bold bright white to stand out as a section title.
        rows.Add(CellRow.FromText("Plan", Color.White, Color.Default, CellStyle.Bold));

        // Each item with a status indicator. Content is word-wrapped to the
        // available width minus the prefix length.
        foreach (var item in items)
        {
            var (prefix, color) = item.Status switch
            {
                TodoStatus.Completed  => (CompletedPrefix,  Color.Green),
                TodoStatus.InProgress => (InProgressPrefix, Color.Yellow),
                _                     => (PendingPrefix,    Color.BrightBlack)
            };

            var contentWidth = Math.Max(1, availableWidth - prefix.Length);
            var lines = WordWrap(item.Content, contentWidth);

            for (var i = 0; i < lines.Count; i++)
            {
                var row = new CellRow();
                if (i == 0)
                {
                    // First line: prefix + content in the status color.
                    row.Append(prefix, color, Color.Default);
                }
                else
                {
                    // Continuation lines: same indent width as the prefix, but spaces only.
                    row.Append(new string(' ', prefix.Length), Color.Default, Color.Default);
                }
                row.Append(lines[i], color, Color.Default);
                rows.Add(row);
            }
        }

        return rows;
    }

    /// <summary>
    /// Word-wraps text to fit within the given width. Breaks at spaces when
    /// possible, falls back to hard breaks for single words longer than the width.
    /// </summary>
    private static List<string> WordWrap(string text, int width)
    {
        if (text.Length <= width)
            return [text];

        var lines = new List<string>();
        var remaining = text.AsSpan();

        while (remaining.Length > 0)
        {
            if (remaining.Length <= width)
            {
                lines.Add(remaining.ToString());
                break;
            }

            // Check if the character at the break boundary is a space.
            if (remaining[width] == ' ')
            {
                lines.Add(remaining[..width].ToString());
                remaining = remaining[(width + 1)..];
                continue;
            }

            // Find the last space within the width limit.
            var breakAt = remaining[..width].LastIndexOf(' ');
            if (breakAt <= 0)
                breakAt = width; // no space found — hard break

            lines.Add(remaining[..breakAt].ToString());
            remaining = breakAt < remaining.Length
                ? remaining[(breakAt == width ? breakAt : breakAt + 1)..]
                : ReadOnlySpan<char>.Empty;
        }

        return lines;
    }
}
