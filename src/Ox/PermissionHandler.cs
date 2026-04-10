using Terminal.Gui.App;
using Terminal.Gui.Drawing;
using Ur.AgentLoop;
using Ur.Permissions;
using Ox.Views;

namespace Ox;

/// <summary>
/// Builds <see cref="TurnCallbacks"/> that route events and permission requests
/// through the Terminal.Gui rendering stack.
///
/// Permission prompts switch the composer into Permission mode via the
/// ComposerController, await the user's response from the background thread
/// without nesting async work inside App.Invoke, then restore Chat mode before
/// returning the decision to the caller.
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
                    $"Allow '{req.RequestingExtension}' to {req.OperationType} '{displayTarget}'?"
                    + $" (y/n{scopeHints}): ";

                // Step 1: Switch to Permission mode on the UI thread.
                //
                // EnterPermissionMode must be called on the UI thread so the mode
                // switch and the prompt update happen atomically — no keystroke can
                // arrive in between and be misrouted. We use a TCS to propagate the
                // returned permission Task back to this background thread without
                // any async work running inside the Invoke callback.
                var modeSwitchTcs = new TaskCompletionSource<Task<string?>>(
                    TaskCreationOptions.RunContinuationsAsynchronously);

                oxApp.App.Invoke(() =>
                {
                    oxApp.InputAreaView.SetPrompt(promptText);
                    var permTask = oxApp.ComposerController.EnterPermissionMode(ct);
                    modeSwitchTcs.SetResult(permTask);
                });

                // Step 2: Await the permission response from the background thread.
                // No async work is running inside App.Invoke; the Invoke above is
                // synchronous and only sets the result of the TCS.
                var permissionTask = await modeSwitchTcs.Task;
                var input = await permissionTask;

                // Step 3: Restore Chat mode on the UI thread.
                oxApp.App.Invoke(() =>
                {
                    oxApp.ComposerController.ExitPermissionMode();
                    oxApp.InputAreaView.SetPrompt("");
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
