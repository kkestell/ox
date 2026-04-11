using Ur.AgentLoop;
using Ur.Permissions;
using Ox.Views;

namespace Ox;

/// <summary>
/// Builds <see cref="TurnCallbacks"/> that route events and permission requests
/// through the Terminal.Gui rendering stack.
///
/// Permission prompts are shown via the self-contained PermissionPromptView
/// widget. The view owns its own TCS and TextField — the handler only calls
/// ShowAsync/Hide and parses the result. The ComposerController is no longer
/// involved in permission flow at all.
/// </summary>
internal static class PermissionHandler
{
    /// <summary>
    /// Creates <see cref="TurnCallbacks"/> that wire subagent event routing and
    /// interactive permission prompts through the TUI.
    /// </summary>
    public static TurnCallbacks Build(EventRouter router, OxApp oxApp, string workspaceRoot)
    {
        return new TurnCallbacks
        {
            // Relay subagent events to the router on the UI thread.
            SubagentEventEmitted = evt =>
            {
                if (evt is SubagentEvent subagentEvt)
                {
                    oxApp.App.Invoke(() => router.RouteSubagentEvent(subagentEvt));
                }
                return ValueTask.CompletedTask;
            },

            RequestPermissionAsync = async (req, ct) =>
            {
                var scopeHints = req.AllowedScopes.Count > 1
                    ? $" [{string.Join(", ", req.AllowedScopes.Select(s => s.ToString().ToLowerInvariant()))}]"
                    : "";

                var displayTarget = FormatTarget(req.Target, workspaceRoot);

                var promptText =
                    $"Allow '{req.ToolName}' to {req.OperationType} '{displayTarget}'?"
                    + $" (y/n{scopeHints}): ";

                // Step 1: Show the permission prompt on the UI thread.
                //
                // ShowAsync must be called on the UI thread so the view's Visible
                // flip and focus transfer happen atomically. We use a TCS to
                // propagate the returned permission Task back to this background
                // thread without any async work running inside the Invoke callback.
                var showTcs = new TaskCompletionSource<Task<string?>>(
                    TaskCreationOptions.RunContinuationsAsynchronously);

                oxApp.App.Invoke(() =>
                {
                    var permTask = oxApp.PermissionPromptView.ShowAsync(promptText, ct);
                    showTcs.SetResult(permTask);
                });

                // Step 2: Await the permission response from the background thread.
                // No async work is running inside App.Invoke; the Invoke above is
                // synchronous and only sets the result of the TCS.
                var permissionTask = await showTcs.Task;
                var input = await permissionTask;

                // Step 3: Hide the prompt and restore focus on the UI thread.
                oxApp.App.Invoke(() =>
                {
                    oxApp.PermissionPromptView.Hide();
                    oxApp.InputAreaView.SetFocus();
                });

                input = input?.Trim().ToLowerInvariant();

                var candidate = input switch
                {
                    "y" or "yes" => new PermissionResponse(true, PermissionScope.Once),
                    "session"    => new PermissionResponse(true, PermissionScope.Session),
                    "workspace"  => new PermissionResponse(true, PermissionScope.Workspace),
                    "always"     => new PermissionResponse(true, PermissionScope.Always),
                    _            => new PermissionResponse(false, null)
                };

                // If the user chose a scope the operation does not support, deny.
                return candidate is { Granted: true, Scope: not null }
                    && !req.AllowedScopes.Contains(candidate.Scope.Value)
                    ? new PermissionResponse(false, null)
                    : candidate;
            }
        };
    }

    /// <summary>
    /// Strips the workspace root prefix from a target path for shorter display.
    /// </summary>
    private static string FormatTarget(string target, string workspaceRoot)
    {
        var prefix = workspaceRoot.EndsWith(Path.DirectorySeparatorChar)
            ? workspaceRoot
            : workspaceRoot + Path.DirectorySeparatorChar;

        return target.StartsWith(prefix, StringComparison.Ordinal)
            ? target[prefix.Length..]
            : target;
    }
}
