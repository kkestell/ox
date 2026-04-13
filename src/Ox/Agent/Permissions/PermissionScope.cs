namespace Ox.Agent.Permissions;

/// <summary>
/// How long a granted permission remains valid. Controls whether the user is
/// re-prompted on subsequent tool invocations.
///
///   Once      — valid for a single tool invocation only.
///   Session   — valid until the current chat session ends.
///   Workspace — valid across all sessions in this workspace (persisted).
///   Always    — valid globally for all workspaces (persisted).
/// </summary>
public enum PermissionScope
{
    Once,
    Session,
    Workspace,
    Always
}
