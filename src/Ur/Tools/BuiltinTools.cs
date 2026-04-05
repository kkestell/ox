using Ur.Permissions;

namespace Ur.Tools;

/// <summary>
/// Registers the built-in file tools into the tool registry.
/// These tools are always available — no extension enablement required.
/// Called during host startup before extensions load so that extension
/// tools cannot shadow built-in names.
/// </summary>
internal static class BuiltinTools
{
    // Shared extractors for the common argument keys. Using TargetExtractors.FromKey
    // keeps AIFunctionArguments out of this file's public-facing signatures.
    private static readonly ITargetExtractor FilePathExtractor = TargetExtractors.FromKey("file_path");
    private static readonly ITargetExtractor PatternExtractor = TargetExtractors.FromKey("pattern");
    private static readonly ITargetExtractor CommandExtractor = TargetExtractors.FromKey("command");

    public static void RegisterAll(ToolRegistry registry, Workspace workspace)
    {
        // Skip tools that are already registered — this lets tests inject fakes
        // before host startup without having them overwritten.

        // read_file is auto-allowed (ReadInWorkspace doesn't prompt).
        var readFileTool = new ReadFileTool(workspace);
        if (registry.Get(readFileTool.Name) is null)
            registry.Register(readFileTool, OperationType.ReadInWorkspace, targetExtractor: FilePathExtractor);

        // write_file and update_file both require write permission.
        var writeFileTool = new WriteFileTool(workspace);
        if (registry.Get(writeFileTool.Name) is null)
            registry.Register(writeFileTool, targetExtractor: FilePathExtractor);

        var updateFileTool = new UpdateFileTool(workspace);
        if (registry.Get(updateFileTool.Name) is null)
            registry.Register(updateFileTool, targetExtractor: FilePathExtractor);

        // glob and grep are read operations — auto-allowed like read_file.
        var globTool = new GlobTool(workspace);
        if (registry.Get(globTool.Name) is null)
            registry.Register(globTool, OperationType.ReadInWorkspace, targetExtractor: PatternExtractor);

        var grepTool = new GrepTool(workspace);
        if (registry.Get(grepTool.Name) is null)
            registry.Register(grepTool, OperationType.ReadInWorkspace, targetExtractor: PatternExtractor);

        // bash requires per-invocation approval — ExecuteCommand is Once-only.
        var bashTool = new BashTool(workspace);
        if (registry.Get(bashTool.Name) is null)
            registry.Register(bashTool, OperationType.ExecuteCommand, targetExtractor: CommandExtractor);
    }
}
