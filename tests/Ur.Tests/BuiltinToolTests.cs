using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Tools;

namespace Ur.Tests;

public sealed class BuiltinToolTests
{
    // ─── Helpers ───────────────────────────────────────────────────────

    /// <summary>
    /// Creates a temp workspace directory with a disposable lifetime,
    /// plus a Workspace instance scoped to that directory.
    /// </summary>
    private sealed class ToolTestEnvironment : IDisposable
    {
        private readonly string _root = Path.Combine(
            Path.GetTempPath(),
            "ur-tool-tests",
            Guid.NewGuid().ToString("N"));

        public string WorkspacePath => Path.Combine(_root, "workspace");
        public Workspace Workspace { get; }

        // A path that sits outside the workspace — used to verify boundary checks.
        public string OutsidePath => Path.Combine(_root, "outside", "file.txt");

        public ToolTestEnvironment()
        {
            Directory.CreateDirectory(WorkspacePath);
            Directory.CreateDirectory(Path.Combine(_root, "outside"));
            Workspace = new Workspace(WorkspacePath);
        }

        /// <summary>
        /// Writes a file inside the workspace and returns its full path.
        /// </summary>
        public string WriteFile(string relativePath, string content)
        {
            var fullPath = Path.Combine(WorkspacePath, relativePath);
            var dir = Path.GetDirectoryName(fullPath);
            if (dir is not null) Directory.CreateDirectory(dir);
            File.WriteAllText(fullPath, content);
            return fullPath;
        }

        public void Dispose()
        {
            if (Directory.Exists(_root))
                Directory.Delete(_root, recursive: true);
        }
    }

    /// <summary>
    /// Invokes an AIFunction with the given named arguments.
    /// </summary>
    private static async Task<object?> InvokeAsync(
        AIFunction tool,
        params (string Key, object? Value)[] args)
    {
        var dict = new Dictionary<string, object?>();
        foreach (var (key, value) in args)
            dict[key] = value;
        return await tool.InvokeAsync(new AIFunctionArguments(dict));
    }

    // ─── read_file ─────────────────────────────────────────────────────

    [Fact]
    public async Task ReadFile_ReturnsFileContents()
    {
        using var env = new ToolTestEnvironment();
        var path = env.WriteFile("hello.txt", "line1\nline2\nline3");

        var tool = new ReadFileTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("file_path", path));

        Assert.Contains("line1", result);
        Assert.Contains("line2", result);
        Assert.Contains("line3", result);
    }

    [Fact]
    public async Task ReadFile_TruncatesLongFiles()
    {
        using var env = new ToolTestEnvironment();
        // Write a file with more lines than the default limit (2000).
        var lines = Enumerable.Range(1, 2500).Select(i => $"line {i}");
        var path = env.WriteFile("big.txt", string.Join('\n', lines));

        var tool = new ReadFileTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("file_path", path));

        Assert.Contains("[truncated: showing lines 1-2000 of 2500 lines]", result);
        Assert.StartsWith("line 1\n", result);
        Assert.DoesNotContain("\nline 2001\n", result);
    }

    [Fact]
    public async Task ReadFile_RespectsOffsetAndLimit()
    {
        using var env = new ToolTestEnvironment();
        var lines = Enumerable.Range(1, 100).Select(i => $"line {i}");
        var path = env.WriteFile("offset.txt", string.Join('\n', lines));

        var tool = new ReadFileTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool,
            ("file_path", path),
            ("offset", 10),
            ("limit", 5));

        // Offset 10 means we start at the 11th line ("line 11").
        Assert.Contains("line 11", result);
        Assert.Contains("line 15", result);
        Assert.DoesNotContain("line 10\n", result);
        Assert.DoesNotContain("line 16", result);
        Assert.Contains("[truncated: showing lines 11-15 of 100 lines]", result);
    }

    [Fact]
    public async Task ReadFile_RejectsPathOutsideWorkspace()
    {
        using var env = new ToolTestEnvironment();
        File.WriteAllText(env.OutsidePath, "secret");

        var tool = new ReadFileTool(env.Workspace);
        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool, ("file_path", env.OutsidePath)));

        Assert.Contains("outside the workspace", ex.Message);
    }

    [Fact]
    public async Task ReadFile_ErrorsOnMissingFile()
    {
        using var env = new ToolTestEnvironment();
        var tool = new ReadFileTool(env.Workspace);

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("file_path", Path.Combine(env.WorkspacePath, "nope.txt"))));

        Assert.Contains("File not found", ex.Message);
    }

    [Fact]
    public async Task ReadFile_ResolvesRelativePaths()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("sub/rel.txt", "relative content");

        var tool = new ReadFileTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("file_path", "sub/rel.txt"));

        Assert.Contains("relative content", result);
    }

    // ─── write_file ────────────────────────────────────────────────────

    [Fact]
    public async Task WriteFile_CreatesFileWithContent()
    {
        using var env = new ToolTestEnvironment();
        var filePath = Path.Combine(env.WorkspacePath, "new.txt");

        var tool = new WriteFileTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool,
            ("file_path", filePath),
            ("content", "hello world"));

        Assert.True(File.Exists(filePath));
        Assert.Equal("hello world", File.ReadAllText(filePath));
        Assert.Contains("Wrote", result);
    }

    [Fact]
    public async Task WriteFile_CreatesParentDirectories()
    {
        using var env = new ToolTestEnvironment();
        var filePath = Path.Combine(env.WorkspacePath, "a", "b", "c", "deep.txt");

        var tool = new WriteFileTool(env.Workspace);
        await InvokeAsync(tool, ("file_path", filePath), ("content", "deep"));

        Assert.True(File.Exists(filePath));
        Assert.Equal("deep", File.ReadAllText(filePath));
    }

    [Fact]
    public async Task WriteFile_RejectsPathOutsideWorkspace()
    {
        using var env = new ToolTestEnvironment();
        var tool = new WriteFileTool(env.Workspace);

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("file_path", env.OutsidePath),
                ("content", "nope")));

        Assert.Contains("outside the workspace", ex.Message);
    }

    [Fact]
    public async Task WriteFile_OverwritesExistingFile()
    {
        using var env = new ToolTestEnvironment();
        var path = env.WriteFile("overwrite.txt", "old content");

        var tool = new WriteFileTool(env.Workspace);
        await InvokeAsync(tool, ("file_path", path), ("content", "new content"));

        Assert.Equal("new content", File.ReadAllText(path));
    }

    // ─── update_file ───────────────────────────────────────────────────

    [Fact]
    public async Task UpdateFile_ReplacesUniqueMatch()
    {
        using var env = new ToolTestEnvironment();
        var path = env.WriteFile("update.txt", "foo bar baz");

        var tool = new UpdateFileTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool,
            ("file_path", path),
            ("old_string", "bar"),
            ("new_string", "qux"));

        Assert.Equal("foo qux baz", File.ReadAllText(path));
        Assert.Contains("Updated", result);
    }

    [Fact]
    public async Task UpdateFile_ErrorsOnZeroMatches()
    {
        using var env = new ToolTestEnvironment();
        var path = env.WriteFile("nope.txt", "foo bar baz");

        var tool = new UpdateFileTool(env.Workspace);
        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("file_path", path),
                ("old_string", "missing"),
                ("new_string", "x")));

        Assert.Contains("not found", ex.Message);
    }

    [Fact]
    public async Task UpdateFile_ErrorsOnMultipleMatches()
    {
        using var env = new ToolTestEnvironment();
        var path = env.WriteFile("multi.txt", "aaa bbb aaa");

        var tool = new UpdateFileTool(env.Workspace);
        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("file_path", path),
                ("old_string", "aaa"),
                ("new_string", "x")));

        Assert.Contains("appears 2 times", ex.Message);
    }

    [Fact]
    public async Task UpdateFile_RejectsPathOutsideWorkspace()
    {
        using var env = new ToolTestEnvironment();
        File.WriteAllText(env.OutsidePath, "content");

        var tool = new UpdateFileTool(env.Workspace);
        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("file_path", env.OutsidePath),
                ("old_string", "c"),
                ("new_string", "x")));

        Assert.Contains("outside the workspace", ex.Message);
    }

    [Fact]
    public async Task UpdateFile_ErrorsOnMissingFile()
    {
        using var env = new ToolTestEnvironment();
        var tool = new UpdateFileTool(env.Workspace);

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("file_path", Path.Combine(env.WorkspacePath, "gone.txt")),
                ("old_string", "a"),
                ("new_string", "b")));

        Assert.Contains("File not found", ex.Message);
    }

    // ─── Registration ──────────────────────────────────────────────────

    [Fact]
    public async Task BuiltinTools_AppearInSessionToolRegistry()
    {
        using var env = new TempExtensionEnvironment();
        var host = await env.StartHostAsync();
        var tools = host.BuildSessionToolRegistry("test");

        Assert.NotNull(tools.Get("read_file"));
        Assert.NotNull(tools.Get("write_file"));
        Assert.NotNull(tools.Get("update_file"));
        Assert.NotNull(tools.Get("glob"));
        Assert.NotNull(tools.Get("grep"));
        Assert.NotNull(tools.Get("bash"));
        Assert.NotNull(tools.Get("skill"));
    }

    [Fact]
    public void RegisterAll_SkipsAlreadyRegisteredTools()
    {
        using var env = new ToolTestEnvironment();
        var registry = new ToolRegistry();

        // Pre-register a fake tool with the same name as a built-in.
        var fake = AIFunctionFactory.Create(() => "fake", "read_file", "a fake");
        registry.Register(fake);

        BuiltinTools.RegisterAll(registry, env.Workspace);

        // The registry should still hold the original fake, not the real ReadFileTool.
        var retrieved = registry.Get("read_file");
        Assert.Same(fake, retrieved);

        // The other two should still be registered normally.
        Assert.NotNull(registry.Get("write_file"));
        Assert.NotNull(registry.Get("update_file"));
    }

    // ─── Path traversal ────────────────────────────────────────────────

    [Fact]
    public async Task ReadFile_RejectsPathTraversal()
    {
        using var env = new ToolTestEnvironment();
        File.WriteAllText(env.OutsidePath, "secret");

        // Attempt to escape the workspace via ../
        var traversalPath = Path.Combine(env.WorkspacePath, "..", "outside", "file.txt");
        var tool = new ReadFileTool(env.Workspace);

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool, ("file_path", traversalPath)));

        Assert.Contains("outside the workspace", ex.Message);
    }

    [Fact]
    public async Task WriteFile_RejectsPathTraversal()
    {
        using var env = new ToolTestEnvironment();
        var traversalPath = Path.Combine(env.WorkspacePath, "..", "outside", "escape.txt");
        var tool = new WriteFileTool(env.Workspace);

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool, ("file_path", traversalPath), ("content", "nope")));

        Assert.Contains("outside the workspace", ex.Message);
    }

    [Fact]
    public async Task UpdateFile_RejectsPathTraversal()
    {
        using var env = new ToolTestEnvironment();
        File.WriteAllText(env.OutsidePath, "content");

        var traversalPath = Path.Combine(env.WorkspacePath, "..", "outside", "file.txt");
        var tool = new UpdateFileTool(env.Workspace);

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("file_path", traversalPath),
                ("old_string", "c"),
                ("new_string", "x")));

        Assert.Contains("outside the workspace", ex.Message);
    }

    // ─── glob ──────────────────────────────────────────────────────────

    [Fact]
    public async Task Glob_MatchesFilesByPattern()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("a.cs", "");
        env.WriteFile("b.cs", "");
        env.WriteFile("readme.md", "");

        var tool = new GlobTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("pattern", "*.cs"));

        Assert.Contains("a.cs", result);
        Assert.Contains("b.cs", result);
        Assert.DoesNotContain("readme.md", result);
    }

    [Fact]
    public async Task Glob_RespectsSubdirectoryScoping()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("root.cs", "");
        env.WriteFile("src/nested.cs", "");

        var tool = new GlobTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool,
            ("pattern", "*.cs"),
            ("path", "src"));

        Assert.Contains("nested.cs", result);
        Assert.DoesNotContain("root.cs", result);
    }

    [Fact]
    public async Task Glob_ReturnsPathsRelativeToWorkspaceRoot()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("src/deep/file.cs", "");

        var tool = new GlobTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("pattern", "**/*.cs"));

        // Should be workspace-relative, not absolute.
        Assert.Contains(Path.Combine("src", "deep", "file.cs"), result);
        Assert.DoesNotContain(env.WorkspacePath, result);
    }

    [Fact]
    public async Task Glob_RejectsPathOutsideWorkspace()
    {
        using var env = new ToolTestEnvironment();
        var tool = new GlobTool(env.Workspace);

        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("pattern", "*.cs"),
                ("path", "/tmp")));

        Assert.Contains("outside the workspace", ex.Message);
    }

    [Fact]
    public async Task Glob_RejectsPathTraversal()
    {
        using var env = new ToolTestEnvironment();
        var tool = new GlobTool(env.Workspace);

        // Attempt to escape the workspace via ../
        var ex = await Assert.ThrowsAsync<InvalidOperationException>(
            () => InvokeAsync(tool,
                ("pattern", "*.txt"),
                ("path", "../../outside")));

        Assert.Contains("outside the workspace", ex.Message);
    }

    [Fact]
    public async Task Glob_ReturnsEmptyForNoMatches()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("test.txt", "");

        var tool = new GlobTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("pattern", "*.cs"));

        Assert.Equal("", result);
    }

    [Fact]
    public async Task Glob_TruncatesLargeResultSets()
    {
        using var env = new ToolTestEnvironment();
        // Create more files than the truncation limit (1000).
        for (var i = 0; i < 1100; i++)
            env.WriteFile($"file{i:D4}.txt", "");

        var tool = new GlobTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("pattern", "*.txt"));

        Assert.Contains("[truncated: showing 1000 of 1100 matches]", result);
    }

    // ─── grep ──────────────────────────────────────────────────────────

    [Fact]
    public async Task Grep_FindsMatchingLinesInFiles()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("code.cs", "int x = 42;\nstring y = \"hello\";\nint z = 99;");

        // Force .NET fallback so the test doesn't depend on rg being installed.
        GrepTool.SetRipgrepAvailable(false);
        try
        {
            var tool = new GrepTool(env.Workspace);
            var result = (string?)await InvokeAsync(tool, ("pattern", "int"));

            Assert.Contains("code.cs", result);
            Assert.Contains("int x = 42", result);
            Assert.Contains("int z = 99", result);
            Assert.DoesNotContain("hello", result);
        }
        finally
        {
            GrepTool.SetRipgrepAvailable(null); // Reset for other tests.
        }
    }

    [Fact]
    public async Task Grep_RespectsIncludeFilter()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("code.cs", "needle");
        env.WriteFile("readme.md", "needle");

        GrepTool.SetRipgrepAvailable(false);
        try
        {
            var tool = new GrepTool(env.Workspace);
            var result = (string?)await InvokeAsync(tool,
                ("pattern", "needle"),
                ("include", "*.cs"));

            Assert.Contains("code.cs", result);
            Assert.DoesNotContain("readme.md", result);
        }
        finally
        {
            GrepTool.SetRipgrepAvailable(null);
        }
    }

    [Fact]
    public async Task Grep_ReturnsLineNumbers()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("lines.txt", "aaa\nbbb\nccc\nbbb\neee");

        GrepTool.SetRipgrepAvailable(false);
        try
        {
            var tool = new GrepTool(env.Workspace);
            var result = (string?)await InvokeAsync(tool, ("pattern", "bbb"));

            // Should contain line numbers (1-based) in file:line:content format.
            Assert.Contains("lines.txt:2:", result);
            Assert.Contains("lines.txt:4:", result);
        }
        finally
        {
            GrepTool.SetRipgrepAvailable(null);
        }
    }

    [Fact]
    public async Task Grep_RejectsPathOutsideWorkspace()
    {
        using var env = new ToolTestEnvironment();

        GrepTool.SetRipgrepAvailable(false);
        try
        {
            var tool = new GrepTool(env.Workspace);

            var ex = await Assert.ThrowsAsync<InvalidOperationException>(
                () => InvokeAsync(tool,
                    ("pattern", "test"),
                    ("path", "/tmp")));

            Assert.Contains("outside the workspace", ex.Message);
        }
        finally
        {
            GrepTool.SetRipgrepAvailable(null);
        }
    }

    [Fact]
    public async Task Grep_ReturnsEmptyForNoMatches()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("code.cs", "nothing here");

        GrepTool.SetRipgrepAvailable(false);
        try
        {
            var tool = new GrepTool(env.Workspace);
            var result = (string?)await InvokeAsync(tool, ("pattern", "zzz_not_found"));

            Assert.Equal("", result);
        }
        finally
        {
            GrepTool.SetRipgrepAvailable(null);
        }
    }

    [Fact]
    public async Task Grep_HandlesContextLines()
    {
        using var env = new ToolTestEnvironment();
        env.WriteFile("ctx.txt", "line1\nline2\nMATCH\nline4\nline5");

        GrepTool.SetRipgrepAvailable(false);
        try
        {
            var tool = new GrepTool(env.Workspace);
            var result = (string?)await InvokeAsync(tool,
                ("pattern", "MATCH"),
                ("context_lines", 1));

            // Should include the line before and after the match, but not
            // lines outside the 1-line context window.
            Assert.Contains("line2", result);
            Assert.Contains("MATCH", result);
            Assert.Contains("line4", result);
            Assert.DoesNotContain("line1", result);
            Assert.DoesNotContain("line5", result);
        }
        finally
        {
            GrepTool.SetRipgrepAvailable(null);
        }
    }

    [Fact]
    public async Task Grep_UsesRipgrepWhenAvailable()
    {
        // This test exercises the ripgrep backend. If rg is not installed,
        // it verifies the .NET fallback produces the same output contract.
        using var env = new ToolTestEnvironment();
        env.WriteFile("rg_test.txt", "alpha\nbeta\ngamma");

        // Reset to auto-detect so we exercise whichever backend is available.
        GrepTool.SetRipgrepAvailable(null);
        try
        {
            var tool = new GrepTool(env.Workspace);
            var result = (string?)await InvokeAsync(tool, ("pattern", "beta"));

            // Both backends should produce file:line:content format.
            Assert.Contains("rg_test.txt", result);
            Assert.Contains("beta", result);
            Assert.DoesNotContain("alpha", result);
            Assert.DoesNotContain("gamma", result);
        }
        finally
        {
            GrepTool.SetRipgrepAvailable(null);
        }
    }

    // ─── bash ──────────────────────────────────────────────────────────

    [Fact]
    public async Task Bash_ExecutesCommandAndReturnsOutput()
    {
        using var env = new ToolTestEnvironment();
        var tool = new BashTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("command", "echo hello"));

        Assert.Contains("Exit code: 0", result);
        Assert.Contains("hello", result);
    }

    [Fact]
    public async Task Bash_ReturnsExitCode()
    {
        using var env = new ToolTestEnvironment();
        var tool = new BashTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool, ("command", "exit 42"));

        Assert.Contains("Exit code: 42", result);
    }

    [Fact]
    public async Task Bash_CapturesStderr()
    {
        using var env = new ToolTestEnvironment();
        var tool = new BashTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool,
            ("command", "echo error_msg >&2"));

        Assert.Contains("stderr", result);
        Assert.Contains("error_msg", result);
    }

    [Fact]
    public async Task Bash_TimesOutLongRunningCommands()
    {
        using var env = new ToolTestEnvironment();
        var tool = new BashTool(env.Workspace);

        // Use a very short timeout to trigger the timeout path.
        var result = (string?)await InvokeAsync(tool,
            ("command", "sleep 60"),
            ("timeout_ms", 500));

        Assert.Contains("timed out", result);
    }

    [Fact]
    public async Task Bash_TruncatesLargeOutput()
    {
        using var env = new ToolTestEnvironment();
        var tool = new BashTool(env.Workspace);

        // Generate more than 2000 lines of output.
        var result = (string?)await InvokeAsync(tool,
            ("command", "seq 1 3000"));

        Assert.Contains("[truncated]", result);
    }

    [Fact]
    public async Task Bash_SetsWorkingDirectoryToWorkspaceRoot()
    {
        using var env = new ToolTestEnvironment();
        // Write a marker file so we can verify pwd matches exactly, not a parent.
        env.WriteFile("marker.txt", "");

        var tool = new BashTool(env.Workspace);
        var result = (string?)await InvokeAsync(tool,
            ("command", "pwd && test -f marker.txt && echo MARKER_FOUND"));

        Assert.Contains(env.WorkspacePath, result);
        Assert.Contains("MARKER_FOUND", result);
    }

    // ─── Registration (updated) ────────────────────────────────────────

    [Fact]
    public void RegisterAll_AddsAllSixToolsToRegistry()
    {
        using var env = new ToolTestEnvironment();
        var registry = new ToolRegistry();

        BuiltinTools.RegisterAll(registry, env.Workspace);

        Assert.NotNull(registry.Get("read_file"));
        Assert.NotNull(registry.Get("write_file"));
        Assert.NotNull(registry.Get("update_file"));
        Assert.NotNull(registry.Get("glob"));
        Assert.NotNull(registry.Get("grep"));
        Assert.NotNull(registry.Get("bash"));
    }
}
