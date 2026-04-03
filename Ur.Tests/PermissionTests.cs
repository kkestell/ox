using System.Runtime.CompilerServices;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Permissions;
using AgentLoopClass = Ur.AgentLoop.AgentLoop;

namespace Ur.Tests;

public sealed class PermissionGrantStoreTests
{
    // -------------------------------------------------------------------------
    // IsCovered — session grants
    // -------------------------------------------------------------------------

    [Fact]
    public async Task IsCovered_ExactSessionGrant_ReturnsTrue()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        await store.StoreAsync(new PermissionGrant(
            OperationType.WriteInWorkspace,
            "/proj/foo.txt",
            PermissionScope.Session,
            "ext"));

        var request = new PermissionRequest(
            OperationType.WriteInWorkspace,
            "/proj/foo.txt",
            "ext",
            []);

        Assert.True(store.IsCovered(request));
    }

    [Fact]
    public async Task IsCovered_PrefixSessionGrant_CoversChildPath()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        // Grant covers the /proj/ directory — any child path should be covered.
        await store.StoreAsync(new PermissionGrant(
            OperationType.WriteInWorkspace,
            "/proj/",
            PermissionScope.Session,
            "ext"));

        var request = new PermissionRequest(
            OperationType.WriteInWorkspace,
            "/proj/subdir/bar.cs",
            "ext",
            []);

        Assert.True(store.IsCovered(request));
    }

    [Fact]
    public async Task IsCovered_WrongOperationType_ReturnsFalse()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        await store.StoreAsync(new PermissionGrant(
            OperationType.ReadOutsideWorkspace,
            "/proj/foo.txt",
            PermissionScope.Session,
            "ext"));

        var request = new PermissionRequest(
            OperationType.WriteInWorkspace,
            "/proj/foo.txt",
            "ext",
            []);

        Assert.False(store.IsCovered(request));
    }

    [Fact]
    public void IsCovered_NoMatchingGrant_ReturnsFalse()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        var request = new PermissionRequest(
            OperationType.WriteInWorkspace,
            "/proj/foo.txt",
            "ext",
            []);

        Assert.False(store.IsCovered(request));
    }

    // -------------------------------------------------------------------------
    // IsCovered — workspace grants loaded from disk
    // -------------------------------------------------------------------------

    [Fact]
    public async Task IsCovered_WorkspaceGrantLoadedFromFile_CoversMatchingRequest()
    {
        using var tmp = new TempGrantDir();

        // Write the grant directly to the workspace JSONL file before creating the store,
        // simulating a grant that was persisted in a previous session.
        var grant = new PermissionGrant(
            OperationType.WriteInWorkspace,
            "/proj/",
            PermissionScope.Workspace,
            "ext");
        await WriteGrantToFileAsync(tmp.WorkspacePermissionsPath, grant);

        var store = tmp.CreateStore();

        var request = new PermissionRequest(
            OperationType.WriteInWorkspace,
            "/proj/main.cs",
            "ext",
            []);

        Assert.True(store.IsCovered(request));
    }

    // -------------------------------------------------------------------------
    // StoreAsync — Once scope is never persisted
    // -------------------------------------------------------------------------

    [Fact]
    public async Task StoreAsync_OnceScope_NeitherPersistedNorCachedForNextCheck()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        await store.StoreAsync(new PermissionGrant(
            OperationType.WriteInWorkspace,
            "/proj/foo.txt",
            PermissionScope.Once,
            "ext"));

        // A second request for the same target must not be covered — Once is single-use.
        var request = new PermissionRequest(
            OperationType.WriteInWorkspace,
            "/proj/foo.txt",
            "ext",
            []);

        Assert.False(store.IsCovered(request));
        Assert.False(File.Exists(tmp.WorkspacePermissionsPath));
        Assert.False(File.Exists(tmp.AlwaysPermissionsPath));
    }

    // -------------------------------------------------------------------------
    // StoreAsync — Workspace scope persists to disk
    // -------------------------------------------------------------------------

    [Fact]
    public async Task StoreAsync_WorkspaceScope_WritesJsonlFile()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        await store.StoreAsync(new PermissionGrant(
            OperationType.WriteInWorkspace,
            "/proj/src/",
            PermissionScope.Workspace,
            "my-ext"));

        Assert.True(File.Exists(tmp.WorkspacePermissionsPath));
        var lines = await File.ReadAllLinesAsync(tmp.WorkspacePermissionsPath);
        var nonEmpty = lines.Where(l => !string.IsNullOrWhiteSpace(l)).ToList();
        Assert.Single(nonEmpty);
        Assert.Contains("writeInWorkspace", nonEmpty[0]);
    }

    // -------------------------------------------------------------------------
    // Helpers
    // -------------------------------------------------------------------------

    private static async Task WriteGrantToFileAsync(string path, PermissionGrant grant)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        var json = System.Text.Json.JsonSerializer.Serialize(grant,
            new System.Text.Json.JsonSerializerOptions
            {
                PropertyNamingPolicy = System.Text.Json.JsonNamingPolicy.CamelCase
            });
        await File.WriteAllTextAsync(path, json + "\n");
    }

    private sealed class TempGrantDir : IDisposable
    {
        private readonly string _root = Path.Combine(
            Path.GetTempPath(), "ur-permission-tests", Guid.NewGuid().ToString("N"));

        public string WorkspacePermissionsPath => Path.Combine(_root, "workspace", ".ur", "permissions.jsonl");
        public string AlwaysPermissionsPath    => Path.Combine(_root, "always", "permissions.jsonl");

        public PermissionGrantStore CreateStore() =>
            new(WorkspacePermissionsPath, AlwaysPermissionsPath);

        public void Dispose()
        {
            if (Directory.Exists(_root))
                Directory.Delete(_root, recursive: true);
        }
    }
}

public sealed class AgentLoopPermissionTests
{
    // -------------------------------------------------------------------------
    // Callback denies — tool produces "Permission denied." result, loop continues
    // -------------------------------------------------------------------------

    [Fact]
    public async Task RunTurnAsync_DenyingCallback_ProducesPermissionDeniedResult()
    {
        var tools = new ToolRegistry();
        var tool  = MakeTool("write_file", "Writes a file", "ok");
        tools.Register(tool, OperationType.WriteInWorkspace);

        var client = new FakeToolCallingClient("write_file");
        var loop   = new AgentLoopClass(client, tools);

        var denyingCallbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) => ValueTask.FromResult(new PermissionResponse(false, null))
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], denyingCallbacks));

        var completed = events.OfType<ToolCallCompleted>().Single();
        Assert.Equal("Permission denied.", completed.Result);
        Assert.True(completed.IsError);
    }

    // -------------------------------------------------------------------------
    // Callback grants — tool executes normally
    // -------------------------------------------------------------------------

    [Fact]
    public async Task RunTurnAsync_GrantingCallback_ExecutesTool()
    {
        var tools = new ToolRegistry();
        var tool  = MakeTool("write_file", "Writes a file", "wrote it");
        tools.Register(tool, OperationType.WriteInWorkspace);

        var client = new FakeToolCallingClient("write_file");
        var loop   = new AgentLoopClass(client, tools);

        var grantingCallbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Once))
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], grantingCallbacks));

        var completed = events.OfType<ToolCallCompleted>().Single();
        Assert.Equal("wrote it", completed.Result);
        Assert.False(completed.IsError);
    }

    // -------------------------------------------------------------------------
    // No callback — sensitive operations are auto-denied
    // -------------------------------------------------------------------------

    [Fact]
    public async Task RunTurnAsync_NullCallback_SensitiveOperationIsDenied()
    {
        var tools = new ToolRegistry();
        var tool  = MakeTool("write_file", "Writes a file", "wrote it");
        tools.Register(tool, OperationType.WriteInWorkspace);

        var client = new FakeToolCallingClient("write_file");
        var loop   = new AgentLoopClass(client, tools);

        // No callbacks — should auto-deny WriteInWorkspace.
        var events = await CollectEventsAsync(loop.RunTurnAsync([], callbacks: null));

        var completed = events.OfType<ToolCallCompleted>().Single();
        Assert.Equal("Permission denied.", completed.Result);
        Assert.True(completed.IsError);
    }

    // -------------------------------------------------------------------------
    // ReadInWorkspace — never prompts, always executes
    // -------------------------------------------------------------------------

    [Fact]
    public async Task RunTurnAsync_ReadInWorkspace_NeverCallsCallback()
    {
        var tools     = new ToolRegistry();
        var tool      = MakeTool("read_file", "Reads a file", "file contents");
        var callCount = 0;
        tools.Register(tool, OperationType.ReadInWorkspace);

        var client = new FakeToolCallingClient("read_file");
        var loop   = new AgentLoopClass(client, tools);

        var callbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
            {
                callCount++;
                return ValueTask.FromResult(new PermissionResponse(false, null));
            }
        };

        var events = await CollectEventsAsync(loop.RunTurnAsync([], callbacks));

        // Tool should have executed without invoking the callback.
        Assert.Equal(0, callCount);
        var completed = events.OfType<ToolCallCompleted>().Single();
        Assert.Equal("file contents", completed.Result);
        Assert.False(completed.IsError);
    }

    // -------------------------------------------------------------------------
    // Helpers
    // -------------------------------------------------------------------------

    private static AIFunction MakeTool(string name, string description, string returnValue) =>
        AIFunctionFactory.Create(
            () => returnValue,
            name,
            description);

    private static async Task<List<AgentLoopEvent>> CollectEventsAsync(
        IAsyncEnumerable<AgentLoopEvent> events)
    {
        var collected = new List<AgentLoopEvent>();
        await foreach (var evt in events)
            collected.Add(evt);
        return collected;
    }

    /// <summary>
    /// Fake IChatClient that emits exactly one tool call then a final text response.
    /// </summary>
    private sealed class FakeToolCallingClient : IChatClient
    {
        private readonly string _toolName;
        private bool _toolCallEmitted;

        public FakeToolCallingClient(string toolName) => _toolName = toolName;

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken ct = default)
        {
            await Task.CompletedTask;

            // First call: emit the tool call. Second call (after receiving tool result): emit text.
            if (!_toolCallEmitted)
            {
                _toolCallEmitted = true;
                yield return new ChatResponseUpdate
                {
                    Contents = [new FunctionCallContent("call-1", _toolName, new Dictionary<string, object?>())]
                };
            }
            else
            {
                yield return new ChatResponseUpdate(ChatRole.Assistant, "done");
            }
        }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken ct = default) =>
            Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, "done")));

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }
}

public sealed class UrSessionPermissionTests
{
    // -------------------------------------------------------------------------
    // Covered grant skips host callback
    // -------------------------------------------------------------------------

    [Fact]
    public async Task RunTurnAsync_ExistingSessionGrant_SkipsHostCallback()
    {
        using var workspace = new TempWorkspace();
        var callCount = 0;

        var callbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
            {
                callCount++;
                return ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Session));
            }
        };

        var tools    = new ToolRegistry();
        var fakeTool = AIFunctionFactory.Create(() => "ok", "write_file", "writes a file");
        tools.Register(fakeTool, OperationType.WriteInWorkspace);

        // Client calls the tool on the first turn; on the second turn it calls it again
        // (to verify the session grant suppresses the second prompt).
        var client = new TwoTurnToolCallingClient("write_file");

        var host = await UrHost.StartAsync(
            workspace.WorkspacePath,
            new TestKeyring(),
            workspace.UserSettingsPath,
            _ => client,
            tools,
            userDataDirectory: workspace.UserDataDirectory);

        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("test-model");

        var session = host.CreateSession(callbacks);

        // Turn 1 — callback fires, grants Session scope.
        await DrainAsync(session.RunTurnAsync("turn 1"));
        Assert.Equal(1, callCount);

        // Turn 2 — same operation; session grant should suppress the callback.
        await DrainAsync(session.RunTurnAsync("turn 2"));
        Assert.Equal(1, callCount); // still 1 — no second prompt
    }

    // -------------------------------------------------------------------------
    // Workspace-scope grant is stored to disk
    // -------------------------------------------------------------------------

    [Fact]
    public async Task RunTurnAsync_WorkspaceScopeGrant_PersistsToPermissionsFile()
    {
        using var workspace = new TempWorkspace();

        var callbacks = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Workspace))
        };

        var tools    = new ToolRegistry();
        var fakeTool = AIFunctionFactory.Create(() => "ok", "write_file", "writes a file");
        tools.Register(fakeTool, OperationType.WriteInWorkspace);

        var client = new SingleTurnToolCallingClient("write_file");

        var host = await UrHost.StartAsync(
            workspace.WorkspacePath,
            new TestKeyring(),
            workspace.UserSettingsPath,
            _ => client,
            tools,
            userDataDirectory: workspace.UserDataDirectory);

        await host.Configuration.SetApiKeyAsync("test-key");
        await host.Configuration.SetSelectedModelAsync("test-model");

        var session = host.CreateSession(callbacks);
        await DrainAsync(session.RunTurnAsync("write something"));

        var permissionsFile = Path.Combine(workspace.WorkspacePath, ".ur", "permissions.jsonl");
        Assert.True(File.Exists(permissionsFile));
        var content = await File.ReadAllTextAsync(permissionsFile);
        Assert.Contains("writeInWorkspace", content);
    }

    // -------------------------------------------------------------------------
    // Helpers
    // -------------------------------------------------------------------------

    private static async Task DrainAsync(IAsyncEnumerable<AgentLoopEvent> events)
    {
        await foreach (var _ in events) { }
    }

    /// <summary>
    /// Fake client that calls a tool once per turn for two turns.
    /// The tool call ID is made unique per turn to avoid any dedup logic.
    /// </summary>
    private sealed class TwoTurnToolCallingClient : IChatClient
    {
        private readonly string _toolName;
        private int _turnCallCount;

        public TwoTurnToolCallingClient(string toolName) => _toolName = toolName;

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken ct = default)
        {
            await Task.CompletedTask;

            // If the last message is a tool result, close the turn with text.
            var msgList = messages.ToList();
            if (msgList.LastOrDefault()?.Role == ChatRole.Tool)
            {
                yield return new ChatResponseUpdate(ChatRole.Assistant, "done");
            }
            else
            {
                // Emit a tool call for this turn.
                var callId = $"call-turn-{_turnCallCount++}";
                yield return new ChatResponseUpdate
                {
                    Contents = [new FunctionCallContent(callId, _toolName, new Dictionary<string, object?>())]
                };
            }
        }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken ct = default) =>
            Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, "done")));

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }

    /// <summary>
    /// Fake client that calls a tool exactly once, then returns text.
    /// </summary>
    private sealed class SingleTurnToolCallingClient : IChatClient
    {
        private readonly string _toolName;
        private bool _toolCallEmitted;

        public SingleTurnToolCallingClient(string toolName) => _toolName = toolName;

        public async IAsyncEnumerable<ChatResponseUpdate> GetStreamingResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            [EnumeratorCancellation] CancellationToken ct = default)
        {
            await Task.CompletedTask;

            var msgList = messages.ToList();
            if (!_toolCallEmitted && msgList.LastOrDefault()?.Role != ChatRole.Tool)
            {
                _toolCallEmitted = true;
                yield return new ChatResponseUpdate
                {
                    Contents = [new FunctionCallContent("call-1", _toolName, new Dictionary<string, object?>())]
                };
            }
            else
            {
                yield return new ChatResponseUpdate(ChatRole.Assistant, "done");
            }
        }

        public Task<ChatResponse> GetResponseAsync(
            IEnumerable<ChatMessage> messages,
            ChatOptions? options = null,
            CancellationToken ct = default) =>
            Task.FromResult(new ChatResponse(new ChatMessage(ChatRole.Assistant, "done")));

        public object? GetService(Type serviceType, object? serviceKey = null) => null;
        public void Dispose() { }
    }
}
