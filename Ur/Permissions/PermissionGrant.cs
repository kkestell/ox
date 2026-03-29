namespace Ur.Permissions;

public sealed record PermissionGrant(
    OperationType OperationType,
    string TargetPrefix,
    PermissionScope Scope,
    string GrantingExtension);
