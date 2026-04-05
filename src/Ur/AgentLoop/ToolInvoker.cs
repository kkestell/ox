using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.Permissions;
using Ur.Tools;

namespace Ur.AgentLoop;

/// <summary>
/// Encapsulates the tool dispatch sequence: permission check → lookup → invoke → result.
///
/// Extracted from <see cref="AgentLoop.RunTurnAsync"/> so that the turn loop reads as a
/// single-level workflow (stream → yield events → persist) without inline tool dispatch
/// mechanics. The invoker owns the permission check and result construction that were
/// previously spread across 40+ lines inside the turn loop.
/// </summary>
internal sealed class ToolInvoker(ToolRegistry tools)
{
    /// <summary>
    /// Invokes all tool calls from a single LLM response, appending results to
    /// <paramref name="resultMessage"/>. Yields <see cref="ToolCallStarted"/> and
    /// <see cref="ToolCallCompleted"/> events for each call so the UI can render progress.
    ///
    /// Callers add <paramref name="resultMessage"/> to the conversation after this
    /// method completes — the invoker only populates its contents.
    /// </summary>
    internal async IAsyncEnumerable<AgentLoopEvent> InvokeAllAsync(
        IReadOnlyList<FunctionCallContent> calls,
        ChatMessage resultMessage,
        TurnCallbacks? callbacks,
        [EnumeratorCancellation] CancellationToken ct)
    {
        foreach (var call in calls)
        {
            yield return new ToolCallStarted
            {
                CallId = call.CallId,
                ToolName = call.Name
            };

            var (result, isError) = await InvokeOneAsync(call, callbacks, ct);

            resultMessage.Contents.Add(new FunctionResultContent(call.CallId, result));

            yield return new ToolCallCompleted
            {
                CallId = call.CallId,
                ToolName = call.Name,
                Result = result,
                IsError = isError
            };
        }
    }

    /// <summary>
    /// Handles a single tool call: checks permission, looks up the handler, invokes it,
    /// and returns the result string plus an error flag. Each step is a distinct early
    /// return so the happy path reads linearly.
    /// </summary>
    private async ValueTask<(string Result, bool IsError)> InvokeOneAsync(
        FunctionCallContent call,
        TurnCallbacks? callbacks,
        CancellationToken ct)
    {
        // Permission gate — deny before invoking if the operation requires approval
        // and none was granted.
        if (await IsPermissionDeniedAsync(call, callbacks, ct))
            return ("Permission denied.", true);

        var handler = tools.Get(call.Name);
        if (handler is null)
            return ($"Unknown tool: {call.Name}", true);

        try
        {
            var args = new AIFunctionArguments(call.Arguments ?? new Dictionary<string, object?>());
            var raw = await handler.InvokeAsync(args, ct);
            return (raw?.ToString() ?? "", false);
        }
        catch (Exception ex)
        {
            return (ex.Message, true);
        }
    }

    /// <summary>
    /// Returns true if the tool call should be blocked (permission not granted).
    /// Looks up the tool's <see cref="PermissionMeta"/> from the registry, checks
    /// whether the operation type requires a prompt, and delegates to the callback.
    /// Falls back to <see cref="OperationType.WriteInWorkspace"/> for tools without
    /// explicit metadata (conservative default).
    /// </summary>
    private async ValueTask<bool> IsPermissionDeniedAsync(
        FunctionCallContent call,
        TurnCallbacks? callbacks,
        CancellationToken ct)
    {
        var meta = tools.GetPermissionMeta(call.Name);
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
