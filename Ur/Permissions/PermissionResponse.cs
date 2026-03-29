namespace Ur.Permissions;

public sealed record PermissionResponse(bool Granted, PermissionScope? Scope);
