using Ur.Terminal.Components;

namespace Ur.Tui.State;

/// <summary>
/// Central mutable state for the TUI chat application. Updated by
/// <see cref="ChatApp"/> during key processing and agent event draining,
/// read by the render loop each frame to draw the UI.
///
/// This is the single source of truth for what's on screen: the message list,
/// any active modal, scroll position, and whether a turn is in progress.
/// </summary>
public sealed class ChatState
{
    /// <summary>All messages in the conversation (user, assistant, tool, system).</summary>
    public List<DisplayMessage> Messages { get; } = new();

    /// <summary>
    /// The currently active modal (API key entry, model picker, extension manager),
    /// or null if no modal is open. Modals are rendered on the overlay layer and
    /// capture all keyboard input until dismissed.
    /// </summary>
    public Widget? ActiveModal { get; set; }

    /// <summary>Vertical scroll offset for the message list, in lines.</summary>
    public int ScrollOffset { get; set; }

    /// <summary>Whether the agent loop is currently processing a user message.</summary>
    public bool IsTurnRunning { get; set; }
}
