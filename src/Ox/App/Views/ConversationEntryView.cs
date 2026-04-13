namespace Ox.App.Views;

/// <summary>
/// Constants and helpers for rendering individual conversation entries.
/// The actual rendering logic lives in ConversationView; this type exposes
/// the shared constants that both the renderer and tests need.
/// </summary>
public static class ConversationEntryView
{
    /// <summary>
    /// Width in columns consumed by the circle prefix ("● "). Non-Plain
    /// entries subtract this from the content width to determine the
    /// available space for text wrapping.
    /// </summary>
    public const int CircleChrome = 2;
}
