using Microsoft.Extensions.AI;
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
    Func<System.Collections.Generic.IDictionary<string, object?>, string>? TargetExtractor)
{
    /// <summary>
    /// Resolves a human-readable target string from a tool call's arguments.
    /// Encapsulates the argument marshaling (casting to IDictionary, invoking the
    /// extractor) so callers don't mix permission logic with argument plumbing.
    /// Falls back to the tool call's name when no extractor is configured.
    /// </summary>
    internal string ResolveTarget(FunctionCallContent call)
    {
        var rawArgs = call.Arguments as IDictionary<string, object?>
            ?? new Dictionary<string, object?>();
        return TargetExtractor?.Invoke(rawArgs) ?? call.Name;
    }
}
