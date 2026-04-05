namespace Ur.Permissions;

/// <summary>
/// Defines which scopes are available for each operation type.
/// </summary>
public static class PermissionPolicy
{
    public static IReadOnlyList<PermissionScope> AllowedScopes(OperationType operationType) => operationType switch
    {
        OperationType.ReadInWorkspace => [],  // Always allowed, no grant needed
        OperationType.WriteInWorkspace => [PermissionScope.Once, PermissionScope.Session, PermissionScope.Workspace, PermissionScope.Always],
        _ => [PermissionScope.Once],  // ReadOutsideWorkspace, WriteOutsideWorkspace, Network, ExecuteCommand
    };

    public static bool RequiresPrompt(OperationType operationType) =>
        operationType != OperationType.ReadInWorkspace;
}
