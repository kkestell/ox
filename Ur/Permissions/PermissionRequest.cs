namespace Ur.Permissions;

/// <summary>
/// Sent from the agent loop to the UI when a tool needs permission to perform
/// a sensitive operation. The UI presents a prompt to the user and returns a
/// <see cref="PermissionResponse"/> with their decision.
/// </summary>
public sealed record PermissionRequest(
    OperationType OperationType,
    string Target,
    string RequestingExtension,
    IReadOnlyList<PermissionScope> AllowedScopes);
