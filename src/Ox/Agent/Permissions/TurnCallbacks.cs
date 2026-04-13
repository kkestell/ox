using Ox.Agent.AgentLoop;

namespace Ox.Agent.Permissions;

/// <summary>
/// Optional hooks that the agent loop calls back into the UI layer during a turn.
///
/// Currently supports permission prompting and sub-agent event relay.
/// When a tool wants to perform a sensitive operation (file write, network access),
/// the loop invokes <see cref="RequestPermissionAsync"/> so the UI can show a dialog
/// and return the user's decision.
/// When a sub-agent is running, the loop invokes <see cref="SubagentEventEmitted"/>
/// for each event from the sub-agent's inner loop so the UI can surface it in real time.
/// </summary>
public sealed class TurnCallbacks
{
    public Func<PermissionRequest, CancellationToken, ValueTask<PermissionResponse>>?
        RequestPermissionAsync { get; init; }

    // Fires for each event relayed from a running sub-agent, wrapped in a SubagentEvent
    // envelope that carries the sub-agent's short identifier. The UI layer uses this
    // to render sub-agent activity with a visual prefix or grouping.
    public Func<AgentLoopEvent, ValueTask>?
        SubagentEventEmitted { get; init; }
}
