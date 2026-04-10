using Terminal.Gui.Drawing;

namespace Ox.Views;

/// <summary>
/// The visual style for a conversation entry, matching the old BubbleStyle.
/// Determines how the entry is prefixed in the conversation view.
/// </summary>
internal enum EntryStyle
{
    /// <summary>User messages: blue circle prefix.</summary>
    User,
    /// <summary>Circle-prefixed items with a dynamic color (tool calls, assistant text).</summary>
    Circle,
    /// <summary>No prefix — full-width verbatim text.</summary>
    Plain
}

/// <summary>
/// One logical item in the conversation stream. This is a data model, not a View —
/// each entry is rendered by a <see cref="ConversationEntryView"/> SubView managed
/// by <see cref="ConversationView"/>.
///
/// Entries can be streaming (text appended incrementally), static (tool signatures),
/// or compound (subagent blocks with nested entries). The Changed event notifies
/// the owning ConversationEntryView that a relayout and redraw are needed.
/// </summary>
internal sealed class ConversationEntry
{
    /// <summary>The visual style controlling circle prefix rendering.</summary>
    public EntryStyle Style { get; }

    /// <summary>
    /// Dynamic circle color supplier. Only used when Style is Circle.
    /// Called on every render pass so tool state transitions update in-place.
    /// </summary>
    public Func<Color>? GetCircleColor { get; }

    /// <summary>The lines of styled text to render for this entry.</summary>
    public List<StyledSegment> Segments { get; } = [];

    /// <summary>
    /// Nested entries for subagent blocks. The ConversationView renders these
    /// indented beneath the parent entry's signature row.
    /// </summary>
    public List<ConversationEntry> Children { get; } = [];

    /// <summary>
    /// Maximum number of visible child rows before tail-clipping kicks in.
    /// Only applies to subagent entries.
    /// </summary>
    public int MaxChildRows { get; set; } = int.MaxValue;

    /// <summary>Raised when content changes and the view should redraw.</summary>
    public event Action? Changed;

    public ConversationEntry(EntryStyle style, Func<Color>? getCircleColor = null)
    {
        Style = style;
        GetCircleColor = getCircleColor;
    }

    /// <summary>Appends a styled text segment and signals a redraw.</summary>
    public void AppendSegment(string text, Color foreground, Color? background = null, bool bold = false)
    {
        Segments.Add(new StyledSegment(text, foreground, background ?? new Color(ColorName16.Black), bold));
        Changed?.Invoke();
    }

    /// <summary>Replaces all segments with a single styled text block.</summary>
    public void SetSegment(string text, Color foreground, Color? background = null, bool bold = false)
    {
        Segments.Clear();
        Segments.Add(new StyledSegment(text, foreground, background ?? new Color(ColorName16.Black), bold));
        Changed?.Invoke();
    }

    /// <summary>
    /// Appends a child entry (for subagent inner events) and wires its Changed
    /// event to propagate upward.
    /// </summary>
    public void AddChild(ConversationEntry child)
    {
        Children.Add(child);
        child.Changed += () => Changed?.Invoke();
        Changed?.Invoke();
    }

    /// <summary>Fires the Changed event externally (e.g., when circle color changes).</summary>
    public void NotifyChanged() => Changed?.Invoke();
}

/// <summary>
/// A segment of uniformly-styled text within a conversation entry.
/// Multiple segments can compose a single entry (e.g., tool signature + result lines).
/// </summary>
internal readonly record struct StyledSegment(
    string Text,
    Color Foreground,
    Color Background,
    bool Bold = false);
