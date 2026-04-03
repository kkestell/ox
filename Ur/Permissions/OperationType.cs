namespace Ur.Permissions;

/// <summary>
/// Categories of operations a tool can perform. Each type maps to a different
/// permission level in the <see cref="PermissionPolicy"/>. The distinction
/// between "in workspace" and "outside workspace" allows tools to read project
/// files freely while requiring explicit consent for accessing anything outside
/// the project boundary.
/// </summary>
public enum OperationType
{
    ReadInWorkspace,
    ReadOutsideWorkspace,
    WriteInWorkspace,
    WriteOutsideWorkspace,
    Network,
}
