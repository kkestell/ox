using System.Collections.Concurrent;
using System.Diagnostics;
using System.Runtime.CompilerServices;
using System.Threading.Channels;
using Microsoft.Extensions.AI;
using Microsoft.Extensions.Logging;
using Ur.Permissions;
using Ur.Tools;

namespace Ur.AgentLoop;

/// <summary>
/// Encapsulates the tool dispatch sequence: permission check → lookup → invoke → result.
///
/// Keeps the turn loop in <see cref="AgentLoop.RunTurnAsync"/> focused on the high-level
/// workflow (stream → yield events → persist) by owning the permission check and result
/// construction for each tool call.
///
/// Workspace containment is enforced here. Tools are not responsible for knowing
/// whether a path is inside or outside the workspace — that is a policy concern
/// belonging to this layer. The invoker resolves the target path, checks containment,
/// and passes both the operation type and the containment result to PermissionPolicy.
/// </summary>
internal sealed class ToolInvoker(ToolRegistry tools, Workspace workspace, ILogger<ToolInvoker> logger)
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
        // Phase 1: Categorize all calls by permission status. This is fast — no user
        // interaction, just in-memory policy + grant store lookups.
        var categorized = calls.Select(call => (Call: call, Permission: CheckPermission(call, callbacks))).ToList();

        // Phase 2: Set up a channel to merge events from concurrent tool executions
        // into a single ordered stream for the caller's await foreach.
        var channel = Channel.CreateUnbounded<AgentLoopEvent>(
            new UnboundedChannelOptions { SingleReader = true });

        // Phase 3: Producer task — fires auto-allowed tools concurrently, runs the
        // approval pipeline serially for needs-approval tools (each spawning a
        // concurrent execution on grant).
        var results = new ConcurrentDictionary<string, FunctionResultContent>();

        var producerTask = Task.Run(async () =>
        {
            var executionTasks = new List<Task>();

            try
            {
                foreach (var (call, permission) in categorized)
                {
                    var args = call.Arguments ?? new Dictionary<string, object?>();

                    switch (permission.Status)
                    {
                        case PermissionStatus.Allowed:
                        {
                            // Auto-allowed: emit ToolCallStarted and fire execution concurrently.
                            await channel.Writer.WriteAsync(new ToolCallStarted
                            {
                                CallId = call.CallId, ToolName = call.Name, Arguments = args
                            }, ct);

                            executionTasks.Add(RunToolThenWriteEventsAsync(call, channel.Writer, results, ct));
                            break;
                        }

                        case PermissionStatus.RequiresApproval:
                        {
                            // Needs approval: emit ToolCallStarted, then ToolAwaitingApproval,
                            // then prompt the user. Approval prompts run serially so the user
                            // sees one at a time.
                            await channel.Writer.WriteAsync(new ToolCallStarted
                            {
                                CallId = call.CallId, ToolName = call.Name, Arguments = args
                            }, ct);

                            await channel.Writer.WriteAsync(new ToolAwaitingApproval
                            {
                                CallId = call.CallId
                            }, ct);

                            var response = await callbacks!.RequestPermissionAsync!(permission.Request!, ct)
                                .ConfigureAwait(false);
                            logger.LogDebug("Permission {Decision} for '{ToolName}' on '{Target}'",
                                response.Granted ? "granted" : "denied", call.Name, permission.Request!.Target);

                            if (response.Granted)
                            {
                                // Granted — spawn concurrent execution just like auto-allowed.
                                executionTasks.Add(RunToolThenWriteEventsAsync(call, channel.Writer, results, ct));
                            }
                            else
                            {
                                // Denied — write the completed event immediately.
                                results[call.CallId] = new FunctionResultContent(call.CallId, "Permission denied.");
                                await channel.Writer.WriteAsync(new ToolCallCompleted
                                {
                                    CallId = call.CallId, ToolName = call.Name,
                                    Result = "Permission denied.", IsError = true
                                }, ct);
                            }
                            break;
                        }

                        case PermissionStatus.Denied:
                        {
                            // Auto-denied (no callback): emit started + completed immediately.
                            await channel.Writer.WriteAsync(new ToolCallStarted
                            {
                                CallId = call.CallId, ToolName = call.Name, Arguments = args
                            }, ct);
                            results[call.CallId] = new FunctionResultContent(call.CallId, "Permission denied.");
                            await channel.Writer.WriteAsync(new ToolCallCompleted
                            {
                                CallId = call.CallId, ToolName = call.Name,
                                Result = "Permission denied.", IsError = true
                            }, ct);
                            break;
                        }
                    }
                }

                // Wait for all in-flight tool executions to finish.
                await Task.WhenAll(executionTasks);
            }
            finally
            {
                channel.Writer.Complete();
            }
        }, ct);

        // Phase 4: Consumer — yield events from the channel as they arrive.
        await foreach (var evt in channel.Reader.ReadAllAsync(ct))
            yield return evt;

        // Propagate any unhandled exception from the producer.
        await producerTask;

        // Phase 5: Populate resultMessage with all tool results in the original call order.
        // The agent loop adds this message to the conversation after InvokeAllAsync completes.
        foreach (var call in calls)
        {
            if (results.TryGetValue(call.CallId, out var functionResult))
                resultMessage.Contents.Add(functionResult);
        }
    }

    /// <summary>
    /// Executes a single tool and writes its <see cref="ToolCallCompleted"/> event to
    /// the channel. Also stores the <see cref="FunctionResultContent"/> in the shared
    /// results dictionary. Designed to run as a fire-and-forget task within the
    /// concurrent dispatch pipeline.
    /// </summary>
    private async Task RunToolThenWriteEventsAsync(
        FunctionCallContent call,
        ChannelWriter<AgentLoopEvent> writer,
        ConcurrentDictionary<string, FunctionResultContent> results,
        CancellationToken ct)
    {
        var (result, isError) = await ExecuteToolAsync(call, ct);
        results[call.CallId] = new FunctionResultContent(call.CallId, result);
        await writer.WriteAsync(new ToolCallCompleted
        {
            CallId = call.CallId, ToolName = call.Name,
            Result = result, IsError = isError
        }, ct);
    }

    /// <summary>
    /// Executes a single tool call after permission has already been resolved.
    /// Looks up the handler, invokes it, and returns the result string plus an
    /// error flag. Callers are responsible for checking permission before calling.
    /// </summary>
    private async ValueTask<(string Result, bool IsError)> ExecuteToolAsync(
        FunctionCallContent call,
        CancellationToken ct)
    {
        var handler = tools.Get(call.Name);
        if (handler is null)
            return ($"Unknown tool: {call.Name}", true);

        logger.LogInformation("Invoking tool '{ToolName}'", call.Name);
        var sw = Stopwatch.StartNew();

        try
        {
            var args = new AIFunctionArguments(call.Arguments ?? new Dictionary<string, object?>());
            var raw = await handler.InvokeAsync(args, ct);
            sw.Stop();
            logger.LogInformation("Tool '{ToolName}' completed in {ElapsedMs}ms", call.Name, sw.ElapsedMilliseconds);
            return (raw?.ToString() ?? "", false);
        }
        catch (Exception ex)
        {
            sw.Stop();
            // Log tool failures — these previously vanished, making it hard to
            // diagnose tool errors from the log file alone.
            logger.LogError(ex, "Tool '{ToolName}' failed after {ElapsedMs}ms", call.Name, sw.ElapsedMilliseconds);
            return (ex.Message, true);
        }
    }

    /// <summary>
    /// Categorizes a tool call's permission status without prompting the user.
    ///
    /// Returns <see cref="PermissionCheckResult.AutoAllowed"/> for operations that
    /// don't need approval (e.g. in-workspace reads), <see cref="PermissionCheckResult.NeedsApproval"/>
    /// for operations that require a user prompt, or <see cref="PermissionCheckResult.Denied"/>
    /// when no callback is available to ask.
    ///
    /// This is the first half of what <c>IsPermissionDeniedAsync</c> used to do — it
    /// resolves the target, checks containment and policy, and builds a
    /// <see cref="PermissionRequest"/> for the approval path. The actual prompt is
    /// deferred to the caller so that categorization can run for all calls up front
    /// before any user interaction happens.
    /// </summary>
    private PermissionCheckResult CheckPermission(FunctionCallContent call, TurnCallbacks? callbacks)
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
        {
            logger.LogDebug("Permission auto-allowed for '{ToolName}' ({Operation}, inWorkspace={InWorkspace})",
                call.Name, operationType, isInWorkspace);
            return PermissionCheckResult.AutoAllowed;
        }

        // No callback configured — auto-deny all sensitive operations.
        if (callbacks?.RequestPermissionAsync is null)
        {
            logger.LogDebug("Permission auto-denied for '{ToolName}': no callback configured", call.Name);
            return PermissionCheckResult.Denied;
        }

        // Build the request for the approval pipeline. The caller will present it
        // to the user via RequestPermissionAsync at the appropriate time.
        var extensionId = meta?.ExtensionId ?? call.Name;
        var allowedScopes = PermissionPolicy.AllowedScopes(operationType, isInWorkspace);

        // Execute targets are command strings, not file paths — don't resolve them.
        // File-based targets are resolved to absolute paths for grant prefix-matching.
        var resolvedTarget = operationType == OperationType.Execute
            ? target
            : ToolArgHelpers.ResolvePath(workspace.RootPath, target);
        var request = new PermissionRequest(operationType, resolvedTarget, extensionId, allowedScopes);

        return PermissionCheckResult.NeedsApproval(request);
    }

    /// <summary>
    /// Result of the fast, non-interactive permission categorization.
    ///
    /// Three outcomes: the operation is auto-allowed and can execute immediately,
    /// it needs user approval (with the <see cref="PermissionRequest"/> ready to go),
    /// or it's denied outright (no callback available).
    /// </summary>
    internal readonly struct PermissionCheckResult
    {
        public PermissionStatus Status { get; private init; }

        /// <summary>
        /// The pre-built permission request. Only populated when <see cref="Status"/>
        /// is <see cref="PermissionStatus.RequiresApproval"/> — callers pass this to
        /// <see cref="TurnCallbacks.RequestPermissionAsync"/>.
        /// </summary>
        public PermissionRequest? Request { get; private init; }

        public static PermissionCheckResult AutoAllowed => new() { Status = PermissionStatus.Allowed };
        public static PermissionCheckResult Denied => new() { Status = PermissionStatus.Denied };
        public static PermissionCheckResult NeedsApproval(PermissionRequest request) =>
            new() { Status = PermissionStatus.RequiresApproval, Request = request };
    }

    internal enum PermissionStatus { Allowed, RequiresApproval, Denied }
}
