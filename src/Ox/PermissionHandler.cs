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
/// Permission prompts are shown inline in the input area via OxApp.ReadLineAsync
/// with a descriptive prompt prefix. The user types y/n/session/workspace/always
/// and the decision is returned to the caller.
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

                // Permission prompts don't use autocomplete — pass null for completion.
                // ReadLineAsync bridges to the UI thread internally.
                string? input = null;
                var tcs = new TaskCompletionSource<string?>();
                oxApp.App.Invoke(async () =>
                {
                    try
                    {
                        var result = await oxApp.InputAreaView.ReadLineAsync(promptText, null, ct);
                        tcs.TrySetResult(result);
                    }
                    catch (Exception)
                    {
                        // If ReadLineAsync throws (e.g. cancellation), ensure
                        // the awaiter completes rather than hanging forever.
                        tcs.TrySetResult(null);
                    }
                });
                input = await tcs.Task;

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
                var response = candidate is { Granted: true, Scope: not null }
                    && !req.AllowedScopes.Contains(candidate.Scope.Value)
                    ? new PermissionResponse(false, null)
                    : candidate;

                // Clear the permission prompt.
                oxApp.App.Invoke(() => oxApp.InputAreaView.SetPrompt(""));

                return response;
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
