namespace Ur.Permissions;

/// <summary>
/// Defines which scopes are available for each operation type.
/// </summary>
public static class PermissionPolicy
{
    public static IReadOnlyList<PermissionScope> AllowedScopes(OperationType operationType) => operationType switch
    {
        OperationType.ReadInWorkspace => [],  // Always allowed, no grant needed
        OperationType.ReadOutsideWorkspace => [PermissionScope.Once],
        OperationType.WriteInWorkspace => [PermissionScope.Once, PermissionScope.Session, PermissionScope.Workspace, PermissionScope.Always],
        OperationType.WriteOutsideWorkspace => [PermissionScope.Once],
        OperationType.Network => [PermissionScope.Once],
        OperationType.ExecuteCommand => [PermissionScope.Once],
        _ => [PermissionScope.Once],
    };

    public static bool RequiresPrompt(OperationType operationType) =>
        operationType != OperationType.ReadInWorkspace;
}
