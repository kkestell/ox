namespace Ox.Agent.Permissions;

/// <summary>
/// Defines the permission policy matrix: maps (operation type, workspace containment)
/// to whether a prompt is required and which grant scopes are available.
///
/// The behavioral matrix:
///   Read  + in-workspace  → auto-allow (no prompt, empty scopes)
///   Read  + out-of-workspace → prompt (Once only)
///   Write + in-workspace  → prompt (all scopes)
///   Write + out-of-workspace → prompt (Once only — more restrictive outside the project)
///   Execute + any         → prompt (Once only — commands are always high-risk)
/// </summary>
public static class PermissionPolicy
{
    public static IReadOnlyList<PermissionScope> AllowedScopes(OperationType operationType, bool isInWorkspace) =>
        (operationType, isInWorkspace) switch
        {
            // Reads inside the workspace are auto-allowed; no grant scope is needed.
            (OperationType.Read, true)  => [],
            // Writes inside the workspace offer the full range of grant durations.
            (OperationType.Write, true) => [PermissionScope.Once, PermissionScope.Session, PermissionScope.Workspace, PermissionScope.Always],
            // All other cases (outside workspace, or execute) are Once-only — the user
            // must explicitly re-approve each time to prevent silent long-lived grants.
            _                           => [PermissionScope.Once]
        };

    public static bool RequiresPrompt(OperationType operationType, bool isInWorkspace) =>
        // Only in-workspace reads are auto-allowed; everything else needs approval.
        !(operationType == OperationType.Read && isInWorkspace);
}
