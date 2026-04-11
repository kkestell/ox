namespace Ur.Permissions;

/// <summary>
/// A persisted permission grant recording that the user approved a particular
/// operation type for a target path prefix. Grants are checked before prompting
/// the user — if an existing grant covers the requested operation, the prompt
/// is skipped. This avoids re-asking the user for the same permission.
/// </summary>
public sealed record PermissionGrant(
    OperationType OperationType,
    string TargetPrefix,
    PermissionScope Scope,
    string ToolName);
