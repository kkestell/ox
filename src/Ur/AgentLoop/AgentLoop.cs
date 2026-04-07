using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;
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
/// Drives the core conversation cycle: user input → LLM → tool calls → repeat.
/// Emits events for the UI layer to render.
/// </summary>
internal sealed class AgentLoop(IChatClient client, ToolRegistry tools, Workspace workspace, ILogger<AgentLoop> logger)
{
    // Delegate tool dispatch (permission check → lookup → invoke → result) to a
    // dedicated helper so RunTurnAsync stays at a single abstraction level.
    // Workspace is passed so the invoker can enforce containment at the permission layer.
    private readonly ToolInvoker _toolInvoker = new(tools, workspace);
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

        while (true)
        {
            // We prepend the system prompt (if any) to a transient view of the messages —
            // it's never added to the persistent `messages` list so it won't be saved to disk.
            var llmMessages = BuildLlmMessages(systemPrompt, messages);

            List<FunctionCallContent> toolCalls = [];
            ChatMessage assistantMessage = new(ChatRole.Assistant, []);
            string? text = null;

            // C# prohibits yield inside try/catch, so we route LLM errors through
            // a side-channel: StreamLlmAsync catches API exceptions, stores them in
            // the error holder, and terminates the stream cleanly. We check and emit
            // the Error event after the await foreach finishes.
            var errorHolder = new LlmErrorHolder();
            await foreach (var update in StreamLlmAsync(llmMessages, options, errorHolder, ct))
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

            messages.Add(assistantMessage);

            if (toolCalls.Count == 0)
            {
                yield return new TurnCompleted();
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
    /// </summary>
    private static IEnumerable<ChatMessage> BuildLlmMessages(
        string? systemPrompt,
        List<ChatMessage> messages)
    {
        if (systemPrompt is not null)
            yield return new ChatMessage(ChatRole.System, systemPrompt);

        foreach (var msg in messages)
            yield return msg;
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
                yield return enumerator.Current;
            }
        }
        finally
        {
            await enumerator.DisposeAsync();
        }
    }

}
