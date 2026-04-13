using Ox.Agent.AgentLoop;
using Ox.Agent.Hosting;
using Ox.Agent.Permissions;

namespace Ox.App;

/// <summary>
/// Drives the Ox agent loop without a TUI — sends a single prompt to the agent,
/// streams output to stdout/stderr, and exits. Sits at the same level as
/// <see cref="OxApp"/> in the Ox architecture: it uses <see cref="OxHost"/>
/// to create a session and run the one turn, but never touches any TUI code.
///
/// Metrics are not collected here — <see cref="Ox.Agent.Sessions.OxSession"/> accumulates
/// them during RunTurnAsync and writes the metrics JSON file on DisposeAsync.
/// HeadlessRunner's only job is to deliver the prompt and stream output.
///
/// Every AgentLoopEvent except ResponseChunk and TurnError is printed to stderr
/// as it arrives, matching the pattern of `ur chat` (tool calls → stderr, response
/// text → stdout). This lets developers watch agent activity during eval runs without
/// a TUI. Callers that don't want event output can redirect or discard stderr.
///
/// <paramref name="maxIterations"/> is forwarded to <see cref="OxHost.CreateSession"/>
/// and caps how many ReAct loop iterations (LLM calls) the agent loop may make before
/// aborting with a fatal error. Null means no cap.
/// </summary>
internal sealed class HeadlessRunner(OxHost host, string prompt, bool yolo, int? maxIterations = null)
{
    // Truncate tool results at this length when printing to stderr. Long results
    // (e.g. file contents) would flood the console and obscure the event stream.
    private const int MaxResultLen = 120;

    // Thinking chunks arrive from the provider one token at a time. Printing each
    // chunk on its own line (as we did originally) turned the model's internal
    // reasoning into a vertical column of single words — unreadable. Instead, we
    // coalesce consecutive ThinkingChunks into a single line of stderr, emitting
    // the "[thinking] " prefix only on the first chunk and terminating the line
    // when any non-thinking event arrives or the stream ends. The field holds the
    // current prefix ("" for main-stream, "  [sub] " for subagent) so that main
    // and subagent thinking streams are flushed independently when the scope
    // switches between them.
    private string? _currentThinkingPrefix;

    /// <summary>
    /// Runs the single prompt against a new session.
    /// Returns 0 on success, 1 on fatal error.
    /// </summary>
    public async Task<int> RunAsync(CancellationToken ct)
    {
        // Always create TurnCallbacks so we can wire SubagentEventEmitted for
        // real-time subagent visibility. In non-yolo mode RequestPermissionAsync
        // is left null, which means auto-deny (the existing safe-dry-run contract
        // in ToolInvoker's CheckPermission is preserved).
        var callbacks = yolo
            ? new TurnCallbacks
            {
                RequestPermissionAsync = (_, _) =>
                    ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Session)),
                SubagentEventEmitted = PrintSubagentEvent,
            }
            : new TurnCallbacks
            {
                SubagentEventEmitted = PrintSubagentEvent,
            };

        // maxIterations flows from the CLI (--max-iterations) down through CreateSession
        // to AgentLoop, where the while (true) loop checks it at the top of each
        // iteration. This is the correct layer: spinning happens in AgentLoop, not here.
        await using var session = host.CreateSession(callbacks, maxIterations: maxIterations);

        var hadFatalError = false;

        await foreach (var evt in session.RunTurnAsync(prompt, ct))
        {
            switch (evt)
            {
                case ResponseChunk { Text: var text }:
                    FlushThinkingLine();
                    Console.Write(text);
                    break;

                case TurnError { IsFatal: true, Message: var msg }:
                    FlushThinkingLine();
                    await Console.Error.WriteLineAsync(msg);
                    hadFatalError = true;
                    break;

                case TurnError { Message: var msg }:
                    FlushThinkingLine();
                    await Console.Error.WriteLineAsync($"Warning: {msg}");
                    break;

                default:
                    // All other event types go to stderr via the shared helper so
                    // main-stream and subagent events use identical formatting.
                    PrintEvent(evt);
                    break;
            }
        }

        // Flush any thinking line still in progress before closing out the streams.
        FlushThinkingLine();
        // Ensure the response text ends on a new line on stdout.
        Console.WriteLine();
        // Blank line on stderr visually separates the event stream from any following output.
        Console.Error.WriteLine();

        return hadFatalError ? 1 : 0;
    }

    /// <summary>
    /// Prints a single agent event to stderr. <paramref name="prefix"/> is empty for
    /// main-stream events and <c>"  [sub] "</c> for events relayed from a subagent,
    /// creating a visually indented sub-stream without changing the tag vocabulary.
    ///
    /// TurnError is intentionally excluded — it is handled in the main loop because it
    /// controls the <c>hadFatalError</c> flag and early exit; routing it through here
    /// would couple event printing to control flow.
    /// ResponseChunk is also excluded — it goes to stdout, not here.
    /// </summary>
    private void PrintEvent(AgentLoopEvent evt, string prefix = "")
    {
        // Thinking chunks stream one token at a time; handle them before the switch
        // so we can coalesce consecutive chunks onto a single line rather than
        // printing one "[thinking] …" line per token.
        if (evt is ThinkingChunk { Text: var thinkText })
        {
            // Headless needs to surface internal reasoning too; otherwise the
            // "thinking enabled" work is invisible outside the TUI. We keep it
            // on stderr so stdout stays reserved for the model's final answer.
            //
            // Replace any embedded newlines with a single space so a contiguous
            // thinking block remains one logical line in the event stream.
            var flat = thinkText.ReplaceLineEndings(" ");
            if (flat.Length == 0)
                return;

            // A prefix change (main ↔ subagent) ends the current thinking line so
            // the two streams don't run together on the same line.
            if (_currentThinkingPrefix != prefix)
            {
                FlushThinkingLine();
                Console.Error.Write($"{prefix}[thinking] ");
                _currentThinkingPrefix = prefix;
            }
            Console.Error.Write(flat);
            return;
        }

        // Any other event ends the current thinking line before its own output.
        FlushThinkingLine();

        var line = evt switch
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

            // SubagentEvent is a relay envelope. Recurse with the inner event so the
            // same switch handles all event types; the [sub] prefix is threaded through.
            SubagentEvent { Inner: var inner } =>
                null, // handled below via recursion

            // Ignore ResponseChunk (stdout), TurnError (main loop), TodoUpdated
            // (TUI only), and any future event types we don't know about yet.
            _ => null,
        };

        if (line is not null)
        {
            Console.Error.WriteLine(line);
            return;
        }

        // Recurse for SubagentEvent so the inner event flows through the same switch.
        if (evt is SubagentEvent { Inner: var subInner })
            PrintEvent(subInner, "  [sub] ");
    }

    /// <summary>
    /// Terminates the in-progress thinking line (if any) with a newline. Called
    /// before any non-thinking stderr output so a thinking block is never left
    /// open on the same line as a subsequent event.
    /// </summary>
    private void FlushThinkingLine()
    {
        if (_currentThinkingPrefix is not null)
        {
            Console.Error.WriteLine();
            _currentThinkingPrefix = null;
        }
    }

    /// <summary>
    /// Adapter that satisfies the <see cref="TurnCallbacks.SubagentEventEmitted"/>
    /// delegate signature. The SubagentEvent envelope has already been constructed
    /// by SubagentRunner; we pass it directly to PrintEvent which unwraps it.
    /// </summary>
    private ValueTask PrintSubagentEvent(AgentLoopEvent evt)
    {
        PrintEvent(evt);
        return default;
    }

    /// <summary>
    /// Truncates a tool result string to <see cref="MaxResultLen"/> so that long
    /// outputs (file contents, command output) don't flood the event stream.
    /// </summary>
    private static string Truncate(string result)
    {
        // Strip newlines to keep each tool result on one line in the terminal.
        var flat = result.ReplaceLineEndings(" ");
        return flat.Length <= MaxResultLen ? flat : flat[..MaxResultLen] + "…";
    }
}
