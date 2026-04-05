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
///
/// Workspace containment is enforced here. Tools are not responsible for knowing
/// whether a path is inside or outside the workspace — that is a policy concern
/// belonging to this layer. The invoker resolves the target path, checks containment,
/// and passes both the operation type and the containment result to PermissionPolicy.
/// </summary>
internal sealed class ToolInvoker(ToolRegistry tools, Workspace workspace)
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
    ///
    /// Looks up the tool's <see cref="PermissionMeta"/>, resolves the target to an
    /// absolute path, checks workspace containment, and passes both to
    /// <see cref="PermissionPolicy"/> to determine if a prompt is needed. This is
    /// the single enforcement point for workspace boundary policy — tools themselves
    /// do not check containment.
    ///
    /// Falls back to <see cref="OperationType.Write"/> for tools without explicit
    /// metadata (conservative default).
    /// </summary>
    private async ValueTask<bool> IsPermissionDeniedAsync(
        FunctionCallContent call,
        TurnCallbacks? callbacks,
        CancellationToken ct)
    {
        var meta = tools.GetPermissionMeta(call.Name);
        var operationType = meta?.OperationType ?? OperationType.Write;

        // Extract a human-readable target from the call's arguments.
        var target = meta?.ResolveTarget(call) ?? call.Name;

        // Resolve the target to an absolute path to check workspace containment.
        // Execute operations are treated as always outside-workspace: commands can
        // reach anything and should never be auto-allowed based on a file path check.
        var isInWorkspace = operationType != OperationType.Execute
            && workspace.Contains(ToolArgHelpers.ResolvePath(workspace.RootPath, target));

        // In-workspace reads are auto-allowed — no prompt needed.
        if (!PermissionPolicy.RequiresPrompt(operationType, isInWorkspace))
            return false;

        // No callback configured — auto-deny all sensitive operations.
        if (callbacks?.RequestPermissionAsync is null)
            return true;

        var extensionId = meta?.ExtensionId ?? call.Name;
        var allowedScopes = PermissionPolicy.AllowedScopes(operationType, isInWorkspace);

        // Use the resolved absolute path as the target so grant prefix matching
        // works correctly against stored grants (which also use absolute paths).
        var resolvedTarget = ToolArgHelpers.ResolvePath(workspace.RootPath, target);
        var request = new PermissionRequest(operationType, resolvedTarget, extensionId, allowedScopes);
        var response = await callbacks.RequestPermissionAsync(request, ct).ConfigureAwait(false);

        return !response.Granted;
    }
}
