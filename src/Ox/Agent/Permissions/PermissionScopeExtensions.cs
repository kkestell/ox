namespace Ox.Agent.Permissions;

/// <summary>
/// Display helpers for <see cref="PermissionScope"/>.
///
/// Lives in the Permissions module rather than on the view because the shortform
/// aliases ("o", "s", "ws", "a") are a property of the scope itself — the same
/// aliases are accepted as input in the permission prompt. Keeping both sides in
/// one place here avoids drift between what the prompt offers and what it accepts.
/// </summary>
internal static class PermissionScopeExtensions
{
    /// <summary>
    /// Returns the compact alias used in the permission prompt legend
    /// (e.g. "[y/n/o/s/ws/a]"). Falls back to the lowercased enum name for
    /// any future scopes that haven't been given a dedicated alias.
    /// </summary>
    public static string ToDisplayShort(this PermissionScope scope) => scope switch
    {
        PermissionScope.Once => "o",
        PermissionScope.Session => "s",
        PermissionScope.Workspace => "ws",
        PermissionScope.Always => "a",
        _ => scope.ToString().ToLowerInvariant(),
    };
}
