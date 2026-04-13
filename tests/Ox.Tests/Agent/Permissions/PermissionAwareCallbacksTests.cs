using Ox.Agent.Permissions;

namespace Ox.Tests.Agent.Permissions;

/// <summary>
/// Unit tests for <see cref="PermissionAwareCallbacks.Build"/>. These cover the
/// three decision branches that used to live as a private method on OxSession:
///   1. grant store covers the request → no prompt;
///   2. no host callback → auto-deny;
///   3. host callback present → delegate, and persist durable grants.
/// </summary>
public sealed class PermissionAwareCallbacksTests
{
    [Fact]
    public async Task Build_NullHostCallbacks_WithoutCoveringGrant_AutoDenies()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        var callbacks = PermissionAwareCallbacks.Build(hostCallbacks: null, store);
        Assert.NotNull(callbacks.RequestPermissionAsync);

        var response = await callbacks.RequestPermissionAsync!(
            new PermissionRequest(OperationType.Write, "/x", "write_file", []),
            CancellationToken.None);

        Assert.False(response.Granted);
        Assert.Null(response.Scope);
    }

    [Fact]
    public async Task Build_GrantStoreCovers_ReturnsGrantedWithoutPrompting()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();
        await store.StoreAsync(new PermissionGrant(
            OperationType.Write, "/x", PermissionScope.Session, "write_file"));

        var promptCount = 0;
        var host = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
            {
                promptCount++;
                return ValueTask.FromResult(new PermissionResponse(false, null));
            }
        };

        var callbacks = PermissionAwareCallbacks.Build(host, store);
        var response = await callbacks.RequestPermissionAsync!(
            new PermissionRequest(OperationType.Write, "/x", "write_file", []),
            CancellationToken.None);

        Assert.True(response.Granted);
        Assert.Equal(0, promptCount);
    }

    [Fact]
    public async Task Build_HostGrantsDurableScope_PersistsGrant()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        var host = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Workspace))
        };

        var callbacks = PermissionAwareCallbacks.Build(host, store);
        await callbacks.RequestPermissionAsync!(
            new PermissionRequest(OperationType.Write, "/proj/foo.txt", "write_file", []),
            CancellationToken.None);

        // Re-check via IsCovered — a second request for the same target should
        // be covered by the persisted workspace grant without calling the host.
        Assert.True(store.IsCovered(
            new PermissionRequest(OperationType.Write, "/proj/foo.txt", "write_file", [])));
    }

    [Fact]
    public async Task Build_HostGrantsOnceScope_DoesNotPersist()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        var host = new TurnCallbacks
        {
            RequestPermissionAsync = (_, _) =>
                ValueTask.FromResult(new PermissionResponse(true, PermissionScope.Once))
        };

        var callbacks = PermissionAwareCallbacks.Build(host, store);
        await callbacks.RequestPermissionAsync!(
            new PermissionRequest(OperationType.Write, "/proj/foo.txt", "write_file", []),
            CancellationToken.None);

        // Once-scoped grants are single-use — must not be stored.
        Assert.False(store.IsCovered(
            new PermissionRequest(OperationType.Write, "/proj/foo.txt", "write_file", [])));
    }

    [Fact]
    public void Build_PassesSubagentEventEmitterThrough()
    {
        using var tmp = new TempGrantDir();
        var store = tmp.CreateStore();

        Func<Ox.Agent.AgentLoop.AgentLoopEvent, ValueTask> emitter = _ => default;
        var host = new TurnCallbacks { SubagentEventEmitted = emitter };

        var callbacks = PermissionAwareCallbacks.Build(host, store);
        Assert.Same(emitter, callbacks.SubagentEventEmitted);
    }

    // Minimal copy of TempGrantDir so these tests don't depend on internals of
    // the other permission-test file. Both keep grants on disk under a unique
    // temp root and clean up on Dispose.
    private sealed class TempGrantDir : IDisposable
    {
        private readonly string _root = Path.Combine(
            Path.GetTempPath(), "ox-permission-aware-tests", Guid.NewGuid().ToString("N"));

        public PermissionGrantStore CreateStore() => new(
            Path.Combine(_root, "workspace", ".ox", "permissions.jsonl"),
            Path.Combine(_root, "always", "permissions.jsonl"));

        public void Dispose()
        {
            if (Directory.Exists(_root))
                Directory.Delete(_root, recursive: true);
        }
    }
}
