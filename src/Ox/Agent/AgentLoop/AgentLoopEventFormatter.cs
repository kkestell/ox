namespace Ox.Agent.AgentLoop;

/// <summary>
/// Formats <see cref="AgentLoopEvent"/> values as single-line stream output
/// (the <c>[tool]</c>, <c>[done]</c>, <c>[compacted]</c>, etc. tags that the
/// headless runner writes to stderr).
///
/// The formatter is pure: one event in, one optional line out. No state, no I/O.
/// Event types whose rendering needs context the formatter can't see — thinking
/// chunks (stateful coalescing), sub-agent relays (recursion), response chunks
/// (they go to stdout), turn errors (they drive control flow) — are intentionally
/// not handled here. Callers detect them with <see cref="TryFormatForStream"/>
/// returning <c>false</c> and supply their own behavior.
///
/// Co-locating this with <see cref="AgentLoopEvent"/> means any new event type
/// added to the hierarchy has one obvious place to pick up a stream-format
/// representation — contrast with the previous layout where the switch lived in
/// the headless runner and could drift away from the event declarations.
/// </summary>
internal static class AgentLoopEventFormatter
{
    // Truncate tool results at this length when formatting for a one-line
    // stream. Long results (file contents, command output) would flood the
    // console and obscure surrounding events.
    private const int MaxResultLen = 120;

    /// <summary>
    /// Formats <paramref name="evt"/> as a single stream line prefixed with
    /// <paramref name="prefix"/>. Returns <c>true</c> and sets <paramref name="line"/>
    /// if the event has a pure-formatting representation; <c>false</c> if the
    /// caller must handle this event kind itself (e.g. thinking, sub-agent,
    /// response chunks, turn errors).
    /// </summary>
    public static bool TryFormatForStream(AgentLoopEvent evt, string prefix, out string line)
    {
        // A separate nullable local keeps the analyzer honest — when every arm
        // produces a string the switch expression is exhaustively non-null, so
        // we use the default arm to signal "caller handles this" explicitly.
        string? formatted = evt switch
        {
            ToolCallStarted started =>
                $"{prefix}[tool] {started.FormatCall()}",

            ToolCallCompleted { IsError: true } completed =>
                $"{prefix}[tool-err] {completed.ToolName}: {Truncate(completed.Result)}",

            ToolCallCompleted completed =>
                $"{prefix}[tool-ok] {completed.ToolName}: {Truncate(completed.Result)}",

            ToolAwaitingApproval { CallId: var callId } =>
                $"{prefix}[awaiting-approval] {callId}",

            TurnCompleted { InputTokens: { } tokens } =>
                $"{prefix}[done] {tokens} input tokens",

            TurnCompleted =>
                $"{prefix}[done]",

            Compacted { Message: var msg } =>
                $"{prefix}[compacted] {msg}",

            _ => null,
        };

        line = formatted ?? string.Empty;
        return formatted is not null;
    }

    /// <summary>
    /// Flattens a multi-line tool result and clips it to
    /// <see cref="MaxResultLen"/> so the event stream stays scannable.
    /// </summary>
    public static string Truncate(string result)
    {
        // Strip newlines so each tool result remains on a single stream line.
        var flat = result.ReplaceLineEndings(" ");
        return flat.Length <= MaxResultLen ? flat : flat[..MaxResultLen] + "…";
    }
}
