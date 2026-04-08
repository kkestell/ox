namespace Ox.Rendering;

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
internal sealed class ToolRenderable(string formattedCall) : IRenderable
{
    private ToolState _state = ToolState.Started;
    private bool _isError;

    public event Action? Changed;

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
    /// Updates the circle color to green (success) or red (error).
    /// </summary>
    public void SetCompleted(bool isError)
    {
        _state = ToolState.Completed;
        _isError = isError;
        Changed?.Invoke();
    }

    /// <summary>
    /// The color to use for the ● circle glyph when this tool call is rendered
    /// in <see cref="BubbleStyle.Circle"/> mode. Evaluated on every render pass
    /// so the icon updates in-place as the tool transitions through its lifecycle:
    ///   Started / AwaitingApproval → yellow (pending)
    ///   Completed (success)        → green
    ///   Completed (error)          → red
    /// </summary>
    public Color CircleColor => _state switch
    {
        ToolState.Started          => Color.Yellow,
        ToolState.AwaitingApproval => Color.Yellow,
        ToolState.Completed        => _isError ? Color.Red : Color.Green,
        _                          => Color.BrightBlack
    };

    public IReadOnlyList<CellRow> Render(int availableWidth)
    {
        var row = new CellRow();

        // Tool signature in dark gray — always present in every state.
        row.Append(formattedCall, Color.BrightBlack, Color.Default);

        // Only AwaitingApproval adds a text suffix — the yellow circle alone doesn't
        // distinguish "awaiting" from "running". Completed states rely on circle color
        // (green/red) to convey success/failure, so no → ok / → error suffixes.
        if (_state != ToolState.AwaitingApproval)
            return [row];

        row.Append(" ", Color.BrightBlack, Color.Default);
        row.Append("[awaiting approval]", Color.Yellow, Color.Default);

        return [row];
    }

    private enum ToolState { Started, AwaitingApproval, Completed }
}
