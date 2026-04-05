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
    // Extracts a human-readable target string from the tool's typed arguments
    // (e.g. the file path being written). Falls back to the tool name if null.
    Func<AIFunctionArguments, string>? TargetExtractor)
{
    /// <summary>
    /// Resolves a human-readable target string from a tool call's arguments.
    /// Encapsulates the argument marshaling (converting to AIFunctionArguments,
    /// invoking the extractor) so callers don't mix permission logic with argument
    /// plumbing. Falls back to the tool call's name when no extractor is configured.
    /// </summary>
    internal string ResolveTarget(FunctionCallContent call)
    {
        var args = new AIFunctionArguments(call.Arguments ?? new Dictionary<string, object?>());
        return TargetExtractor?.Invoke(args) ?? call.Name;
    }
}
