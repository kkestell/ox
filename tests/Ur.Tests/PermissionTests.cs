using System.Runtime.CompilerServices;
using System.Text.Json;
using System.Text.Json.Serialization;
using Microsoft.Extensions.AI;
using Ur.AgentLoop;
using Ur.Permissions;
using Ur.Tests.TestSupport;
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
    // IsCovered — empty target prefix covers any target
    // -------------------------------------------------------------------------

    [Fact]
    public async Task IsCovered_EmptyTargetPrefix_CoversAnyTarget()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        // An empty prefix means "grant this operation type everywhere".
        await store.StoreAsync(new PermissionGrant(
            OperationType.WriteInWorkspace,
            "",
            PermissionScope.Session,
            "ext"));

        Assert.True(store.IsCovered(new PermissionRequest(
            OperationType.WriteInWorkspace, "/any/path/at/all.txt", "ext", [])));
        Assert.True(store.IsCovered(new PermissionRequest(
            OperationType.WriteInWorkspace, "/completely/different/path", "other-ext", [])));
    }

    // -------------------------------------------------------------------------
    // Round-trip: StoreAsync then fresh store instance reads it back
    // -------------------------------------------------------------------------

    [Fact]
    public async Task StoreAsync_ThenFreshStoreInstance_CanReadPersistedGrant()
    {
        using var tmp = new TempGrantDir();

        // Write via one store instance.
        var writer = tmp.CreateStore();
        await writer.StoreAsync(new PermissionGrant(
            OperationType.WriteInWorkspace,
            "/proj/",
            PermissionScope.Workspace,
            "ext"));

        // A fresh instance (simulating a new session) must find the grant on disk.
        var reader = tmp.CreateStore();
        Assert.True(reader.IsCovered(new PermissionRequest(
            OperationType.WriteInWorkspace, "/proj/src/main.cs", "ext", [])));
    }

    // -------------------------------------------------------------------------
    // Helpers
    // -------------------------------------------------------------------------

    // Match the serialization options used by PermissionGrantStore so that
    // pre-seeded test data is readable by the store under test.
    private static readonly JsonSerializerOptions GrantJsonOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
        Converters = { new JsonStringEnumConverter(JsonNamingPolicy.CamelCase) }
    };

    private static async Task WriteGrantToFileAsync(string path, PermissionGrant grant)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        var json = JsonSerializer.Serialize(grant, GrantJsonOptions);
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
        tools.Register(tool);

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
        tools.Register(tool);

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
        tools.Register(tool);

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
    private sealed class FakeToolCallingClient(string toolName) : IChatClient
    {
        private bool _toolCallEmitted;

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
                    Contents = [new FunctionCallContent("call-1", toolName, new Dictionary<string, object?>())]
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
        tools.Register(fakeTool);

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

        // Turn 1 — callback fires, tool executes, grants Session scope.
        var turn1Events = await CollectAsync(session.RunTurnAsync("turn 1"));
        Assert.Equal(1, callCount);
        var turn1Completed = turn1Events.OfType<ToolCallCompleted>().Single();
        Assert.False(turn1Completed.IsError);  // tool ran successfully
        Assert.Equal("ok", turn1Completed.Result);

        // Turn 2 — same operation; session grant should suppress the callback.
        var turn2Events = await CollectAsync(session.RunTurnAsync("turn 2"));
        Assert.Equal(1, callCount); // still 1 — no second prompt
        var turn2Completed = turn2Events.OfType<ToolCallCompleted>().Single();
        Assert.False(turn2Completed.IsError);  // tool still ran successfully via cached grant
        Assert.Equal("ok", turn2Completed.Result);
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
        tools.Register(fakeTool);

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
        var events = await CollectAsync(session.RunTurnAsync("write something"));

        // Tool ran successfully.
        var completed = events.OfType<ToolCallCompleted>().Single();
        Assert.False(completed.IsError);
        Assert.Equal("ok", completed.Result);

        // Grant persisted to disk with correct operation type.
        var permissionsFile = Path.Combine(workspace.WorkspacePath, ".ur", "permissions.jsonl");
        Assert.True(File.Exists(permissionsFile));
        var content = await File.ReadAllTextAsync(permissionsFile);
        Assert.Contains("writeInWorkspace", content);
    }

    // -------------------------------------------------------------------------
    // Helpers
    // -------------------------------------------------------------------------

    private static async Task<List<AgentLoopEvent>> CollectAsync(IAsyncEnumerable<AgentLoopEvent> events)
    {
        var list = new List<AgentLoopEvent>();
        await foreach (var e in events) list.Add(e);
        return list;
    }

    /// <summary>
    /// Fake client that calls a tool once per turn for two turns.
    /// The tool call ID is made unique per turn to avoid any dedup logic.
    /// </summary>
    private sealed class TwoTurnToolCallingClient(string toolName) : IChatClient
    {
        private int _turnCallCount;

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
                    Contents = [new FunctionCallContent(callId, toolName, new Dictionary<string, object?>())]
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
    private sealed class SingleTurnToolCallingClient(string toolName) : IChatClient
    {
        private bool _toolCallEmitted;

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
                    Contents = [new FunctionCallContent("call-1", toolName, new Dictionary<string, object?>())]
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
