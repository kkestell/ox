using Ur.Terminal.Components;

namespace Ur.Tui.State;

public sealed class ChatState
{
    public List<DisplayMessage> Messages { get; } = new();
    public Widget? ActiveModal { get; set; }
    public int ScrollOffset { get; set; }
    public bool IsTurnRunning { get; set; }
}
