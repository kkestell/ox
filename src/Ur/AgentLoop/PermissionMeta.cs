using Ur.Permissions;

namespace Ur.AgentLoop;

/// <summary>
/// Permission metadata attached to a registered tool.
/// Carried alongside the AIFunction so AgentLoop can build a PermissionRequest
/// before invoking the tool, without AgentLoop needing to know which extension
/// owns each tool.
/// </summary>
internal sealed record PermissionMeta(
    OperationType OperationType,
    string? ExtensionId,
    // Extracts a human-readable target string from the tool's arguments
    // (e.g. the file path being written). Falls back to the tool name if null.
    Func<System.Collections.Generic.IDictionary<string, object?>, string>? TargetExtractor);
