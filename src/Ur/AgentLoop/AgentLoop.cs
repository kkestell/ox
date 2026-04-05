using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.Permissions;

namespace Ur.AgentLoop;

/// <summary>
/// Drives the core conversation cycle: user input → LLM → tool calls → repeat.
/// Emits events for the UI layer to render.
/// </summary>
public sealed class AgentLoop
{
    private readonly IChatClient _client;
    private readonly ToolRegistry _tools;

    public AgentLoop(IChatClient client, ToolRegistry tools)
    {
        _client = client;
        _tools = tools;
    }

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
            Tools = _tools.All(),
            ToolMode = ChatToolMode.Auto,
        };

        while (true)
        {
            // Send messages to LLM and stream the response. We prepend the
            // system prompt (if any) to a transient view of the messages — it's
            // never added to the persistent `messages` list so it won't be saved to disk.
            var llmMessages = BuildLlmMessages(systemPrompt, messages);

            List<FunctionCallContent> toolCalls = [];
            ChatMessage assistantMessage = new(ChatRole.Assistant, []);
            string? text = null;

            // C# prohibits yield inside try/catch, so we route LLM errors through
            // a side-channel: StreamLlmAsync catches API exceptions, stores them in
            // errorSink, and terminates the stream cleanly. We check and emit the
            // Error event after the await foreach finishes.
            var errorSink = new Exception?[1];
            await foreach (var update in StreamLlmAsync(llmMessages, options, errorSink, ct))
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

            if (errorSink[0] is { } llmError)
            {
                yield return new Error { Message = llmError.Message, IsFatal = true };
                yield break;
            }

            // Build the assistant message from what we accumulated.
            if (text is not null)
                assistantMessage.Contents.Add(new TextContent(text));

            foreach (var call in toolCalls)
                assistantMessage.Contents.Add(call);

            messages.Add(assistantMessage);

            // If no tool calls, the turn is done.
            if (toolCalls.Count == 0)
            {
                yield return new TurnCompleted();
                yield break;
            }

            // Execute tool calls and collect results.
            ChatMessage toolResultMessage = new(ChatRole.Tool, []);

            foreach (var call in toolCalls)
            {
                yield return new ToolCallStarted
                {
                    CallId = call.CallId,
                    ToolName = call.Name,
                };

                string result;
                bool isError;

                // Check permission before invoking the tool. If the operation requires
                // a prompt and no callback is wired, deny silently. On denial, add a
                // "Permission denied" result and continue to the next tool call — the
                // LLM will see the denial and may respond accordingly.
                if (await IsPermissionDeniedAsync(call, callbacks, ct))
                {
                    result = "Permission denied.";
                    isError = true;
                }
                else
                {
                    var handler = _tools.Get(call.Name);
                    if (handler is null)
                    {
                        result = $"Unknown tool: {call.Name}";
                        isError = true;
                    }
                    else
                    {
                        try
                        {
                            var args = new AIFunctionArguments(call.Arguments ?? new Dictionary<string, object?>());
                            var raw = await handler.InvokeAsync(args, ct);
                            result = raw?.ToString() ?? "";
                            isError = false;
                        }
                        catch (Exception ex)
                        {
                            result = ex.Message;
                            isError = true;
                        }
                    }
                }

                toolResultMessage.Contents.Add(new FunctionResultContent(call.CallId, result));

                yield return new ToolCallCompleted
                {
                    CallId = call.CallId,
                    ToolName = call.Name,
                    Result = result,
                    IsError = isError,
                };
            }

            messages.Add(toolResultMessage);

            // Loop back to send tool results to the LLM.
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
    /// <paramref name="errorSink"/> rather than propagating it. This indirection
    /// exists because C# prohibits yield statements inside try/catch blocks — callers
    /// check errorSink[0] after the foreach to emit an Error event.
    /// </summary>
    private async IAsyncEnumerable<ChatResponseUpdate> StreamLlmAsync(
        IEnumerable<ChatMessage> messages,
        ChatOptions options,
        Exception?[] errorSink,
        [EnumeratorCancellation] CancellationToken ct)
    {
        var enumerator = _client.GetStreamingResponseAsync(messages, options, ct)
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
                    errorSink[0] = ex;
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

    /// <summary>
    /// Returns true if the tool call should be blocked (i.e. permission was not granted).
    /// Looks up the tool's PermissionMeta from the registry, checks RequiresPrompt,
    /// and delegates to the callback. Falls back to WriteInWorkspace for unknown tools.
    /// </summary>
    private async ValueTask<bool> IsPermissionDeniedAsync(
        FunctionCallContent call,
        TurnCallbacks? callbacks,
        CancellationToken ct)
    {
        var meta = _tools.GetPermissionMeta(call.Name);
        var operationType = meta?.OperationType ?? OperationType.WriteInWorkspace;

        // ReadInWorkspace never requires a prompt.
        if (!PermissionPolicy.RequiresPrompt(operationType))
            return false;

        // No callback configured — auto-deny all sensitive operations.
        if (callbacks?.RequestPermissionAsync is null)
            return true;

        // Extract a human-readable target from the call's arguments.
        var target = meta?.ResolveTarget(call) ?? call.Name;

        var extensionId = meta?.ExtensionId ?? call.Name;
        var allowedScopes = PermissionPolicy.AllowedScopes(operationType);

        var request = new PermissionRequest(operationType, target, extensionId, allowedScopes);
        var response = await callbacks.RequestPermissionAsync(request, ct).ConfigureAwait(false);

        return !response.Granted;
    }
}
