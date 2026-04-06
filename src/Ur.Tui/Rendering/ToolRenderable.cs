using Ur.AgentLoop;

namespace Ur.Tui.Rendering;

/// <summary>
/// Represents the full lifecycle of a single tool call in the conversation.
///
/// A tool call passes through three states:
///   Started → (optionally) AwaitingApproval → Completed
///
/// Each state transition re-renders the same row, so the user sees a single
/// in-place status line rather than a separate line per event. The dark-gray
/// styling (BrightBlack) matches the visual convention: tool activity recedes
/// so the assistant's response text reads as the primary signal.
/// </summary>
internal sealed class ToolRenderable : IRenderable
{
    private readonly string _formattedCall; // e.g. "read_file(path: "foo.txt")"

    private ToolState _state = ToolState.Started;
    private bool _isError;

    public event Action? Changed;

    public ToolRenderable(ToolCallStarted started)
    {
        _formattedCall = started.FormatCall();
    }

    /// <summary>
    /// Called when the user is being asked to approve or deny this tool call.
    /// Appends an "[awaiting approval]" segment so the user knows what they're deciding.
    /// </summary>
    public void SetAwaitingApproval()
    {
        _state = ToolState.AwaitingApproval;
        Changed?.Invoke();
    }

    /// <summary>
    /// Called when the tool call completes (successfully or with an error).
    /// Finalizes the rendered row with a "→ ok" or "→ error" suffix.
    /// </summary>
    public void SetCompleted(bool isError)
    {
        _state = ToolState.Completed;
        _isError = isError;
        Changed?.Invoke();
    }

    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        var row = new CellRow();

        // Tool signature in dark gray — always present in every state.
        row.Append(_formattedCall, Color.BrightBlack, Color.Default);

        switch (_state)
        {
            case ToolState.AwaitingApproval:
                // Highlight that the user must act: separate the suffix with a space,
                // then render "[awaiting approval]" in yellow to draw attention.
                row.Append(" ", Color.BrightBlack, Color.Default);
                row.Append("[awaiting approval]", Color.Yellow, Color.Default);
                break;

            case ToolState.Completed when _isError:
                row.Append(" \u2192 error", Color.BrightBlack, Color.Default);
                break;

            case ToolState.Completed:
                row.Append(" \u2192 ok", Color.BrightBlack, Color.Default);
                break;
        }

        return [row];
    }

    private enum ToolState { Started, AwaitingApproval, Completed }
}
