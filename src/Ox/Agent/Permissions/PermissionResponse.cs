namespace Ox.Agent.Permissions;

/// <summary>
/// The user's decision in response to a <see cref="PermissionRequest"/>.
/// If <see cref="Granted"/> is true, <see cref="Scope"/> indicates how long
/// the grant remains valid (e.g. once, session, always). If denied, scope is null.
/// </summary>
public sealed record PermissionResponse(bool Granted, PermissionScope? Scope);
