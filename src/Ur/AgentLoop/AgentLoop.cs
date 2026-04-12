using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;
using Ur.Compaction;
using Ur.Permissions;
using Ur.Tools;

namespace Ur.AgentLoop;

/// <summary>
/// Carries an LLM exception out of <see cref="AgentLoop.StreamLlmAsync"/> into
/// the caller. C# prohibits yield inside try/catch, so StreamLlmAsync catches
/// API errors and stores them here. The caller checks after iteration completes.
/// Using a named record instead of a raw <c>Exception?[1]</c> array makes the
/// side-channel's purpose explicit at every usage site.
/// </summary>
internal record LlmErrorHolder
{
    public Exception? Exception { get; set; }
}

/// <summary>
/// Carries accumulated token usage out of <see cref="AgentLoop.StreamLlmAsync"/>
/// into the caller, using the same side-channel pattern as <see cref="LlmErrorHolder"/>.
/// StreamLlmAsync accumulates <see cref="UsageContent"/> items from the response
/// stream; the caller reads the result after iteration completes.
/// </summary>
internal record UsageHolder
{
    public UsageDetails? Usage { get; set; }
}

/// <summary>
/// Drives the core conversation cycle: user input → LLM → tool calls → repeat.
/// Emits events for the UI layer to render.
///
/// <paramref name="maxIterations"/> caps how many times the <c>while (true)</c> loop
/// iterates before the turn is aborted with a fatal <see cref="TurnError"/>. Each
/// iteration is one LLM call: the first call gets the assistant's response, subsequent
/// calls consume tool results and loop back. Null means no cap — the loop runs until
/// the LLM stops issuing tool calls (or an error occurs).
///
/// This is the right layer for the cap because spinning happens here. A cap at a higher
/// layer (session, CLI) would stop sending user messages, not prevent a single runaway
/// ReAct loop from burning through tokens.
/// </summary>
internal sealed class AgentLoop(IChatClient client, ToolRegistry tools, Workspace workspace, ILogger<AgentLoop> logger, ILoggerFactory loggerFactory, int turnsToKeepToolResults = 3, int? maxIterations = null)
{
    // Delegate tool dispatch (permission check → lookup → invoke → result) to a
    // dedicated helper so RunTurnAsync stays at a single abstraction level.
    // Workspace is passed so the invoker can enforce containment at the permission layer.
    private readonly ToolInvoker _toolInvoker = new(tools, workspace, loggerFactory.CreateLogger<ToolInvoker>());
    /// <summary>
    /// Runs a single turn: sends the user message (plus conversation history) to the LLM,
    /// streams the response, executes any tool calls, and loops until the LLM produces
    /// a final text response with no further tool calls.
    ///
    /// <paramref name="systemPrompt"/> is an optional transient system message prepended
    /// to what the LLM sees each iteration, but never added to the persistent
    /// <paramref name="messages"/> list. This keeps ephemeral context (skill listings,
    /// instructions) out of the on-disk conversation history.
    ///
    /// <paramref name="callbacks"/> is invoked before each tool call that requires a prompt.
    /// If callbacks is null and the operation requires a prompt, the call is denied silently.
    /// </summary>
    public async IAsyncEnumerable<AgentLoopEvent> RunTurnAsync(
        List<ChatMessage> messages,
        TurnCallbacks? callbacks = null,
        string? systemPrompt = null,
        [EnumeratorCancellation] CancellationToken ct = default)
    {
        var options = new ChatOptions
        {
            // All() returns IReadOnlyList to prevent external mutation, but the
            // underlying List<AITool> also implements IList<AITool> which ChatOptions needs.
            Tools = (IList<AITool>)tools.All(),
            ToolMode = ChatToolMode.Auto
        };

        // Local counter: incremented at the top of each iteration. AgentLoop is
        // constructed once per user turn, so a local is equivalent to a field here —
        // but locals avoid the _camelCase instance-field prefix that would be misleading.
        var iterationCount = 0;

        while (true)
        {
            // Cap check: abort the turn if the iteration limit is reached. Each
            // iteration is one LLM call. This prevents a runaway ReAct loop from
            // consuming unbounded tokens when the agent keeps calling tools.
            if (maxIterations.HasValue && ++iterationCount > maxIterations.Value)
            {
                yield return new TurnError
                {
                    Message = $"Stopped: agent iteration limit ({maxIterations.Value}) reached.",
                    IsFatal = true
                };
                yield break;
            }


            // We prepend the system prompt (if any) to a transient view of the messages —
            // it's never added to the persistent `messages` list so it won't be saved to disk.
            var llmMessages = BuildLlmMessages(systemPrompt, messages, turnsToKeepToolResults);

            List<FunctionCallContent> toolCalls = [];
            ChatMessage assistantMessage = new(ChatRole.Assistant, []);
            string? text = null;

            // C# prohibits yield inside try/catch, so we route LLM errors through
            // a side-channel: StreamLlmAsync catches API exceptions, stores them in
            // the error holder, and terminates the stream cleanly. We check and emit
            // the Error event after the await foreach finishes.
            // UsageHolder accumulates token counts from the response stream so we can
            // persist them in the assistant message and report context fill to the UI.
            var errorHolder = new LlmErrorHolder();
            var usageHolder = new UsageHolder();
            await foreach (var update in StreamLlmAsync(llmMessages, options, errorHolder, usageHolder, ct))
            {
                foreach (var content in update.Contents)
                {
                    switch (content)
                    {
                        case TextContent tc:
                            text = (text ?? "") + tc.Text;
                            yield return new ResponseChunk { Text = tc.Text };
                            break;

                        case FunctionCallContent fcc:
                            toolCalls.Add(fcc);
                            break;
                    }
                }
            }

            if (errorHolder.Exception is { } llmError)
            {
                yield return new TurnError { Message = llmError.Message, IsFatal = true };
                yield break;
            }

            if (text is not null)
                assistantMessage.Contents.Add(new TextContent(text));

            foreach (var call in toolCalls)
                assistantMessage.Contents.Add(call);

            // Persist token usage in the assistant message so it survives in the
            // session JSONL file. On session reload, the last assistant message's
            // UsageContent gives the most recent context fill level.
            if (usageHolder.Usage is not null)
                assistantMessage.Contents.Add(new UsageContent(usageHolder.Usage));

            messages.Add(assistantMessage);

            if (toolCalls.Count == 0)
            {
                // The last LLM call's InputTokenCount reflects the full conversation
                // size — this is the context fill level the UI displays.
                yield return new TurnCompleted
                {
                    InputTokens = usageHolder.Usage?.InputTokenCount,
                    OutputTokens = usageHolder.Usage?.OutputTokenCount,
                };
                yield break;
            }

            // Dispatch all tool calls through the invoker, which handles permission
            // checks, handler lookup, invocation, and result construction. We only
            // need to collect events and add the result message to the conversation.
            ChatMessage toolResultMessage = new(ChatRole.Tool, []);

            await foreach (var toolEvent in _toolInvoker.InvokeAllAsync(toolCalls, toolResultMessage, callbacks, ct))
                yield return toolEvent;

            messages.Add(toolResultMessage);
        }
    }

    /// <summary>
    /// Builds the message list the LLM sees: optional system prompt + conversation.
    /// We don't add the system message to `messages` because that list is persisted
    /// to disk by UrSession — the system prompt is transient and rebuilt each turn.
    ///
    /// Three projection steps are applied before sending:
    ///   1. Tool results older than <paramref name="turnsToKeep"/> assistant turns
    ///      are replaced with "[Tool result cleared]" via <see cref="ToolResultClearer"/>
    ///      to reclaim context window space without mutating the persisted history.
    ///   2. Compaction summaries (detected by <see cref="Autocompactor.IsCompactionSummary"/>)
    ///      are re-roled from their stored <c>ChatRole.User</c> to <c>ChatRole.System</c>.
    ///      The summary is structured system context, not something the user said.
    ///   3. UsageContent items are stripped — they're persisted in the JSONL for
    ///      session-reload context fill display, but some providers (e.g. Gemini)
    ///      can't map them to native request parts.
    /// </summary>
    private static IEnumerable<ChatMessage> BuildLlmMessages(
        string? systemPrompt,
        List<ChatMessage> messages,
        int turnsToKeep)
    {
        if (systemPrompt is not null)
            yield return new ChatMessage(ChatRole.System, systemPrompt);

        // Pipeline: clear old tool results first, then apply role projection and strip UsageContent.
        // ToolResultClearer returns a new sequence — the original list is untouched.
        var projected = ToolResultClearer.ClearOldToolResults(messages, turnsToKeep);

        foreach (var msg in projected)
        {
            // Compaction summaries are stored as User messages but should reach the
            // LLM as System — the summary is structured context, not user speech.
            // We project the role here rather than changing the stored role so the
            // persisted conversation format stays consistent (BuildLlmMessages is the
            // designated projection layer between storage and the LLM).
            if (Autocompactor.IsCompactionSummary(msg))
            {
                var contents = msg.Contents.Where(c => c is not UsageContent).ToList();
                yield return new ChatMessage(ChatRole.System, contents);
                continue;
            }

            // Fast path: most messages have no UsageContent.
            if (!msg.Contents.Any(c => c is UsageContent))
            {
                yield return msg;
                continue;
            }

            // Clone with UsageContent filtered out so providers never see it.
            var filtered = new ChatMessage(msg.Role,
                msg.Contents.Where(c => c is not UsageContent).ToList());
            yield return filtered;
        }
    }

    /// <summary>
    /// Streams LLM response updates, routing any non-cancellation exception into
    /// <paramref name="errorHolder"/> rather than propagating it. This indirection
    /// exists because C# prohibits yield statements inside try/catch blocks — callers
    /// check <see cref="LlmErrorHolder.Exception"/> after the foreach to emit an Error event.
    /// </summary>
    private async IAsyncEnumerable<ChatResponseUpdate> StreamLlmAsync(
        IEnumerable<ChatMessage> messages,
        ChatOptions options,
        LlmErrorHolder errorHolder,
        UsageHolder usageHolder,
        [EnumeratorCancellation] CancellationToken ct)
    {
        var enumerator = client.GetStreamingResponseAsync(messages, options, ct)
            .GetAsyncEnumerator(ct);
        try
        {
            while (true)
            {
                bool hasNext;

                // MoveNextAsync is in its own try/catch so we can react to errors
                // without having a yield inside the catch block (which C# forbids).
                try
                {
                    hasNext = await enumerator.MoveNextAsync();
                }
                catch (OperationCanceledException)
                {
                    throw;
                }
                catch (Exception ex)
                {
                    // Log the full exception here before reducing it to a message string.
                    // Once stored in LlmErrorHolder only the Message survives; the type
                    // and stack trace are lost. This is the only place the full context exists.
                    logger.LogError(ex, "LLM streaming error");
                    errorHolder.Exception = ex;
                    hasNext = false;
                }

                if (!hasNext)
                    break;

                // yield is outside the try/catch above, satisfying the C# constraint.
                // Accumulate token usage from UsageContent items in the stream.
                // Providers typically report usage on the final chunk; we use Add()
                // so partial reports are summed correctly.
                foreach (var uc in enumerator.Current.Contents.OfType<UsageContent>())
                {
                    usageHolder.Usage ??= new UsageDetails();
                    usageHolder.Usage.Add(uc.Details);
                }

                yield return enumerator.Current;
            }
        }
        finally
        {
            await enumerator.DisposeAsync();
        }
    }

}
