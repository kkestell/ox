namespace Ur.Permissions;

/// <summary>
/// Categories of operations a tool can perform. These describe *what* the operation
/// does, not *where* it targets. Workspace containment is a policy concern resolved
/// at invocation time by <see cref="Ur.AgentLoop.ToolInvoker"/>, which combines the
/// operation type with an <c>isInWorkspace</c> flag to determine whether a prompt
/// is needed.
/// </summary>
public enum OperationType
{
    Read,
    Write,
    Execute
}
