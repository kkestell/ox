namespace Ox.App.Views;

/// <summary>
/// Visual classification of a conversation entry, used to decide layout
/// chrome (circle prefix, spacing) without coupling to the entry's data type.
/// </summary>
public enum EntryStyle
{
    /// <summary>
    /// Standard circle-prefixed entry (assistant text, tool calls, errors).
    /// Gets a colored circle prefix and inter-entry spacing.
    /// </summary>
    Circle,

    /// <summary>
    /// User message — blue circle prefix and inter-entry spacing.
    /// Functionally similar to Circle but semantically distinct so the
    /// renderer can pick the right circle color.
    /// </summary>
    User,

    /// <summary>
    /// Plain continuation content (cancellation markers, etc.).
    /// No circle prefix, no inter-entry spacing.
    /// </summary>
    Plain,
}
