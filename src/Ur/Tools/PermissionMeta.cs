using Microsoft.Extensions.AI;
using Ur.Permissions;

namespace Ur.Tools;

/// <summary>
/// Permission metadata attached to a registered tool.
/// Carried alongside the AIFunction so AgentLoop can build a PermissionRequest
/// before invoking the tool, without AgentLoop needing to know which extension
/// owns each tool.
/// </summary>
internal sealed record PermissionMeta(
    OperationType OperationType,
    string? ExtensionId,
    // Extracts a human-readable target string from the tool's typed arguments
    // (e.g. the file path being written). Falls back to the tool name if null.
    ITargetExtractor? TargetExtractor)
{
    /// <summary>
    /// Resolves a human-readable target string from a tool call's arguments.
    /// Encapsulates the argument marshaling (converting to a dictionary,
    /// invoking the extractor) so callers don't mix permission logic with argument
    /// plumbing. Falls back to the tool call's name when no extractor is configured.
    /// </summary>
    internal string ResolveTarget(FunctionCallContent call)
    {
        if (TargetExtractor is null)
            return call.Name;

        var arguments = (IReadOnlyDictionary<string, object?>?)call.Arguments
            ?? new Dictionary<string, object?>();
        return TargetExtractor.Extract(arguments);
    }
}
