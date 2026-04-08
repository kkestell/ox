using Ur.AgentLoop;
using Ur.Permissions;
using Ur.Tui.Rendering;

namespace Ur.Tui;

/// <summary>
/// Builds <see cref="TurnCallbacks"/> that route events and permission requests
/// through the rendering layer instead of writing directly to the console.
///
/// Extracted from Program.cs so that the callback-building logic (which wires
/// together the router, input reader, and viewport for permission prompts) lives
/// in its own module. Program.Main calls <see cref="Build"/> and passes the
/// result to <c>host.CreateSession</c>.
/// </summary>
internal static class PermissionHandler
{
    /// <summary>
    /// Creates <see cref="TurnCallbacks"/> that wire subagent event routing and
    /// interactive permission prompts through the TUI rendering stack.
    /// </summary>
    public static TurnCallbacks Build(EventRouter router, InputReader inputReader, Viewport viewport)
    {
        return new TurnCallbacks
        {
            // Relay subagent events to the router, which finds or creates the right
            // SubagentRenderable and routes the inner event to it.
            SubagentEventEmitted = evt =>
            {
                if (evt is SubagentEvent subagentEvt)
                    router.RouteSubagentEvent(subagentEvt);
                return ValueTask.CompletedTask;
            },

            RequestPermissionAsync = (req, ct) =>
            {
                // The awaiting-approval visual transition is now handled by the
                // ToolAwaitingApproval event emitted by ToolInvoker before this
                // callback fires — no side-channel call needed here.

                var scopeHints = req.AllowedScopes.Count > 1
                    ? $" [{string.Join(", ", req.AllowedScopes.Select(s => s.ToString().ToLowerInvariant()))}]"
                    : "";

                var promptText =
                    $"Allow {req.OperationType} on '{req.Target}' by '{req.RequestingExtension}'?"
                    + $" (y/n{scopeHints}): ";

                // InputReader internally pauses the escape monitor while reading,
                // so no external flag management is needed. Permission prompts don't
                // use autocomplete — pass null for onCompletionChanged.
                var input = inputReader.ReadLineInViewport(promptText, viewport.SetInputPrompt, null, ct);

                input = input?.Trim().ToLowerInvariant();

                var candidate = input switch
                {
                    "y" or "yes" => new PermissionResponse(true, PermissionScope.Once),
                    "session"    => new PermissionResponse(true, PermissionScope.Session),
                    "workspace"  => new PermissionResponse(true, PermissionScope.Workspace),
                    "always"     => new PermissionResponse(true, PermissionScope.Always),
                    _            => new PermissionResponse(false, null)
                };

                // If the user chose a scope the operation does not support, deny rather
                // than silently granting more than permitted.
                var response = candidate is { Granted: true, Scope: not null }
                    && !req.AllowedScopes.Contains(candidate.Scope.Value)
                    ? new PermissionResponse(false, null)
                    : candidate;

                // Restore the running indicator after permission is resolved.
                viewport.SetInputPrompt("");

                return ValueTask.FromResult(response);
            }
        };
    }
}
