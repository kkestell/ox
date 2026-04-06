using Ur.AgentLoop;

namespace Ur.Tui.Rendering;

/// <summary>
/// Represents the full lifecycle of a single tool call in the conversation.
///
/// A tool call passes through three states:
///   Started → (optionally) AwaitingApproval → Completed
///
/// Each state transition re-renders the same line, so the user sees a single
/// in-place status line rather than a separate line per event. The dark-gray
/// styling matches the current TUI convention: tool activity recedes visually
/// so the assistant's response text reads as the primary signal.
/// </summary>
internal sealed class ToolRenderable : IRenderable
{
    // ANSI codes for consistent tool-call styling throughout the lifecycle.
    private const string DarkGray = "\e[90m";
    private const string Yellow   = "\e[33m";
    private const string Reset    = "\e[0m";

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
    /// Renders an "[awaiting approval]" suffix so the user knows what they're deciding.
    /// </summary>
    public void SetAwaitingApproval()
    {
        _state = ToolState.AwaitingApproval;
        Changed?.Invoke();
    }

    /// <summary>
    /// Called when the tool call completes (successfully or with an error).
    /// Finalizes the rendered line with a "→ ok" or "→ error" suffix.
    /// </summary>
    public void SetCompleted(bool isError)
    {
        _state = ToolState.Completed;
        _isError = isError;
        Changed?.Invoke();
    }

    public IReadOnlyList<string> Render(int availableWidth)
    {
        var line = _state switch
        {
            // In-flight: just the call signature in dark gray.
            ToolState.Started =>
                $"{DarkGray}{_formattedCall}{Reset}",

            // Permission prompt active: highlight that the user must act.
            ToolState.AwaitingApproval =>
                $"{DarkGray}{_formattedCall}{Reset} {Yellow}[awaiting approval]{Reset}",

            // Done: append the outcome in dark gray.
            ToolState.Completed when _isError =>
                $"{DarkGray}{_formattedCall} \u2192 error{Reset}",

            ToolState.Completed =>
                $"{DarkGray}{_formattedCall} \u2192 ok{Reset}",

            _ => $"{DarkGray}{_formattedCall}{Reset}"
        };

        return [line];
    }

    private enum ToolState { Started, AwaitingApproval, Completed }
}
