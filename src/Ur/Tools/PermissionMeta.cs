using Microsoft.Extensions.AI;
using Ur.Permissions;

namespace Ur.Tools;

/// <summary>
/// Opt-in interface for tool classes that want to self-declare their permission
/// metadata instead of having it assigned at registration time.
///
/// When the registration loop encounters a tool that implements this interface,
/// it reads <see cref="OperationType"/> and <see cref="TargetExtractor"/> directly
/// from the tool rather than from the factory or call site. This keeps permission
/// semantics co-located with the tool's implementation — the right place for them.
/// </summary>
internal interface IToolMeta
{
    OperationType OperationType { get; }
    // Non-nullable: if a tool has no meaningful target, it should not implement IToolMeta —
    // leave targetExtractor null at registration time instead (the convention for all builtins).
    ITargetExtractor TargetExtractor { get; }
}

/// <summary>
/// Permission metadata attached to a registered tool.
/// Carried alongside the AIFunction so AgentLoop can build a PermissionRequest
/// before invoking the tool.
/// </summary>
internal sealed record PermissionMeta(
    OperationType OperationType,
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
