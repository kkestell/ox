using Ur.Permissions;

namespace Ur.Tools;

/// <summary>
/// The canonical list of built-in tool factories.
///
/// Each factory is a lambda that takes a <see cref="ToolContext"/> and returns
/// a configured <see cref="Microsoft.Extensions.AI.AIFunction"/>. The
/// registration loop in <see cref="Ur.Sessions.UrSession"/> builds a
/// <see cref="ToolContext"/> once per turn and calls every factory in this list —
/// no special-casing required.
///
/// Permission metadata (OperationType, TargetExtractor) is supplied either via
/// <see cref="IToolMeta"/> on the tool class itself, or via the factory entry's
/// metadata below. Tools that implement <see cref="IToolMeta"/> are self-describing;
/// the registration loop reads metadata directly from the tool instance.
/// </summary>
internal static class BuiltinToolFactories
{
    // Shared target extractors for common argument keys. One instance per key
    // is sufficient — extractors are stateless.
    private static readonly ITargetExtractor FilePathExtractor = TargetExtractors.FromKey("file_path");
    private static readonly ITargetExtractor PatternExtractor = TargetExtractors.FromKey("pattern");
    private static readonly ITargetExtractor CommandExtractor = TargetExtractors.FromKey("command");

    /// <summary>
    /// Factories for all built-in tools, each paired with its permission metadata.
    /// Consumed by the registration loop in <see cref="Ur.Sessions.UrSession.RunTurnAsync"/>.
    ///
    /// Ordering is stable but not meaningful — tools are looked up by name in the registry.
    /// </summary>
    public static (ToolFactory Factory, OperationType OperationType, ITargetExtractor? TargetExtractor)[] All =>
    [
        // read_file: read-only, auto-allowed without prompting.
        (ctx => new ReadFileTool(ctx.Workspace), OperationType.Read, FilePathExtractor),

        // write_file: creates or overwrites files — requires write permission.
        (ctx => new WriteFileTool(ctx.Workspace), OperationType.Write, FilePathExtractor),

        // update_file: targeted in-place edits — same write permission as write_file.
        (ctx => new UpdateFileTool(ctx.Workspace), OperationType.Write, FilePathExtractor),

        // glob: directory listing via pattern — read-only, auto-allowed.
        (ctx => new GlobTool(ctx.Workspace), OperationType.Read, PatternExtractor),

        // grep: content search — read-only, auto-allowed.
        (ctx => new GrepTool(ctx.Workspace), OperationType.Read, PatternExtractor),

        // bash: arbitrary shell commands — Execute (prompts once per command, never remembered).
        (ctx => new BashTool(ctx.Workspace), OperationType.Execute, CommandExtractor)
    ];
}
