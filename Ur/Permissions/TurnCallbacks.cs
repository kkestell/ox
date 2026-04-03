namespace Ur.Permissions;

/// <summary>
/// Optional hooks that the agent loop calls back into the UI layer during a turn.
///
/// Currently only supports permission prompting — when a tool wants to perform
/// a sensitive operation (file write, network access), the loop invokes
/// <see cref="RequestPermissionAsync"/> so the UI can show a dialog and return
/// the user's decision. Not yet wired in the current agent loop implementation;
/// the parameter is accepted but ignored until the permission system is active.
/// </summary>
public sealed class TurnCallbacks
{
    public Func<PermissionRequest, CancellationToken, ValueTask<PermissionResponse>>?
        RequestPermissionAsync { get; init; }
}
