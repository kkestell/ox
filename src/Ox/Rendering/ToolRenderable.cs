using Te.Rendering;

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
    // Cap how many result lines we show beneath the tool signature.
    // Keeps large outputs (file reads, verbose glob results) from flooding
    // the viewport. Lines beyond this limit are summarized as "(N more lines)".
    private const int MaxResultLines = 5;

    private ToolState _state = ToolState.Started;
    private bool _isError;
    private string? _result;

    public event Action? Changed;

    /// <summary>
    /// Called when the user is being asked to approve or deny this tool call.
    /// Transitions the circle color state so it stays yellow until resolved.
    /// </summary>
    public void SetAwaitingApproval()
    {
        _state = ToolState.AwaitingApproval;
        Changed?.Invoke();
    }

    /// <summary>
    /// Called when the tool call completes (successfully or with an error).
    /// Updates the circle color to green (success) or red (error) and stores
    /// the result text for rendering beneath the signature row.
    /// </summary>
    public void SetCompleted(bool isError, string? result)
    {
        _state = ToolState.Completed;
        _isError = isError;
        // Treat whitespace-only results as empty — no result lines to show.
        _result = string.IsNullOrWhiteSpace(result) ? null : result;
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
        var rows = new List<CellRow>();

        // Row 0: tool signature in dark gray — always present in every state.
        // The circle color (yellow → green/red) conveys lifecycle state;
        // no text suffix is needed. The permission prompt itself is shown
        // in the input area by InputReader, not inline here.
        var sigRow = new CellRow();
        sigRow.Append(formattedCall, Color.BrightBlack, Color.Default);
        rows.Add(sigRow);

        // Result rows: shown only after completion when the tool returned
        // non-empty output. Uses └─ on the first line and indentation on
        // continuations — visually subordinates the output to the tool call
        // without implying a separate tree node.
        if (_result is not null)
        {
            var lines = _result.Split('\n');
            var visibleCount = Math.Min(lines.Length, MaxResultLines);

            for (var i = 0; i < visibleCount; i++)
            {
                var resultRow = new CellRow();
                // First result line gets └─ prefix; continuations get 3-space indent.
                var prefix = i == 0 ? "└─ " : "   ";
                resultRow.Append(prefix, Color.BrightBlack, Color.Default);
                resultRow.Append(lines[i], Color.BrightBlack, Color.Default);
                rows.Add(resultRow);
            }

            // Truncation indicator when the result exceeds MaxResultLines.
            if (lines.Length > MaxResultLines)
            {
                var truncRow = new CellRow();
                truncRow.Append($"   ({lines.Length - MaxResultLines} more lines)", Color.BrightBlack, Color.Default);
                rows.Add(truncRow);
            }
        }

        return rows;
    }

    private enum ToolState { Started, AwaitingApproval, Completed }
}
