using Ur.AgentLoop;
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
    public static void RegisterAll(ToolRegistry registry, Workspace workspace)
    {
        // Skip tools that are already registered — this lets tests inject fakes
        // before host startup without having them overwritten.

        // read_file is auto-allowed (ReadInWorkspace doesn't prompt).
        var readFileTool = new ReadFileTool(workspace);
        if (registry.Get(readFileTool.Name) is null)
            registry.Register(readFileTool, OperationType.ReadInWorkspace, targetExtractor: ExtractFilePath);

        // write_file and update_file both require write permission.
        var writeFileTool = new WriteFileTool(workspace);
        if (registry.Get(writeFileTool.Name) is null)
            registry.Register(writeFileTool, OperationType.WriteInWorkspace, targetExtractor: ExtractFilePath);

        var updateFileTool = new UpdateFileTool(workspace);
        if (registry.Get(updateFileTool.Name) is null)
            registry.Register(updateFileTool, OperationType.WriteInWorkspace, targetExtractor: ExtractFilePath);

        // glob and grep are read operations — auto-allowed like read_file.
        var globTool = new GlobTool(workspace);
        if (registry.Get(globTool.Name) is null)
            registry.Register(globTool, OperationType.ReadInWorkspace, targetExtractor: args => ToolArgHelpers.ExtractStringArg(args, "pattern"));

        var grepTool = new GrepTool(workspace);
        if (registry.Get(grepTool.Name) is null)
            registry.Register(grepTool, OperationType.ReadInWorkspace, targetExtractor: args => ToolArgHelpers.ExtractStringArg(args, "pattern"));

        // bash requires per-invocation approval — ExecuteCommand is Once-only.
        var bashTool = new BashTool(workspace);
        if (registry.Get(bashTool.Name) is null)
            registry.Register(bashTool, OperationType.ExecuteCommand, targetExtractor: args => ToolArgHelpers.ExtractStringArg(args, "command"));
    }

    /// <summary>
    /// Pulls the file_path argument out of the tool arguments so the permission
    /// prompt can show the user which file is being accessed.
    /// </summary>
    private static string ExtractFilePath(IDictionary<string, object?> args) =>
        ToolArgHelpers.ExtractStringArg(args, "file_path");
}
