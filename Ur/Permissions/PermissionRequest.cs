namespace Ur.Permissions;

public sealed record PermissionRequest(
    OperationType OperationType,
    string Target,
    string RequestingExtension,
    IReadOnlyList<PermissionScope> AllowedScopes);
