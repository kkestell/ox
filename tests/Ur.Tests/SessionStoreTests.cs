using Microsoft.Extensions.AI;
using Ur.Sessions;

namespace Ur.Tests;

/// <summary>
/// Tests for <see cref="SessionStore"/>. These verify the persistence contract:
/// JSONL append, round-trip fidelity, and resilience to malformed data.
/// The session store is the foundation of conversation persistence — if it
/// silently loses or corrupts messages, the user loses work.
/// </summary>
public sealed class SessionStoreTests : IDisposable
{
    private readonly string _root = Path.Combine(
        Path.GetTempPath(),
        "ur-sessionstore-tests",
        Guid.NewGuid().ToString("N"));

    private string SessionsDir => Path.Combine(_root, "sessions");

    public SessionStoreTests()
    {
        Directory.CreateDirectory(_root);
    }

    public void Dispose()
    {
        if (Directory.Exists(_root))
            Directory.Delete(_root, recursive: true);
    }

    // ─── Create + Append + ReadAll round-trip ─────────────────────────

    [Fact]
    public async Task AppendAndReadAll_RoundTripsMessages()
    {
        var store = new SessionStore(SessionsDir);
        var session = store.Create();

        var userMsg = new ChatMessage(ChatRole.User, "hello");
        var assistantMsg = new ChatMessage(ChatRole.Assistant, "hi there");

        await store.AppendAsync(session, userMsg);
        await store.AppendAsync(session, assistantMsg);

        var messages = await store.ReadAllAsync(session);

        Assert.Equal(2, messages.Count);
        Assert.Equal(ChatRole.User, messages[0].Role);
        Assert.Equal("hello", messages[0].Text);
        Assert.Equal(ChatRole.Assistant, messages[1].Role);
        Assert.Equal("hi there", messages[1].Text);
    }

    // ─── ReadAll resilience ───────────────────────────────────────────

    [Fact]
    public async Task ReadAllAsync_MalformedTrailingLine_SkippedGracefully()
    {
        // Simulates a crash during write — the last line is truncated JSON.
        // The store should recover all valid messages and skip the bad line.
        var store = new SessionStore(SessionsDir);
        var session = store.Create();

        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "saved"));

        // Append a truncated JSON line to simulate a crash mid-write.
        await File.AppendAllTextAsync(session.FilePath, "{\"role\":\"assistant\",\"contents\":[{\"$type\":\"tex\n");

        var messages = await store.ReadAllAsync(session);

        Assert.Single(messages);
        Assert.Equal("saved", messages[0].Text);
    }

    [Fact]
    public async Task ReadAllAsync_EmptyFile_ReturnsEmpty()
    {
        var store = new SessionStore(SessionsDir);
        var session = store.Create();

        // Create the file but leave it empty.
        Directory.CreateDirectory(SessionsDir);
        await File.WriteAllTextAsync(session.FilePath, "");

        var messages = await store.ReadAllAsync(session);

        Assert.Empty(messages);
    }

    [Fact]
    public async Task ReadAllAsync_MissingFile_ReturnsEmpty()
    {
        var store = new SessionStore(SessionsDir);
        var session = store.Create();
        // Don't write any file — it shouldn't exist.

        var messages = await store.ReadAllAsync(session);

        Assert.Empty(messages);
    }

    // ─── List ─────────────────────────────────────────────────────────

    [Fact]
    public void List_NoSessionsDirectory_ReturnsEmpty()
    {
        var store = new SessionStore(Path.Combine(_root, "nonexistent"));

        var sessions = store.List();

        Assert.Empty(sessions);
    }

    [Fact]
    public async Task List_MultipleSessions_ReturnsAllSessions()
    {
        var store = new SessionStore(SessionsDir);

        // Create first session and write to disk.
        var session1 = store.Create();
        await store.AppendAsync(session1, new ChatMessage(ChatRole.User, "first"));

        // Ensure a distinct timestamp by waiting past the millisecond boundary;
        // SessionStore encodes creation time in the filename with ms precision.
        await Task.Delay(10);

        var session2 = store.Create();
        await store.AppendAsync(session2, new ChatMessage(ChatRole.User, "second"));

        var sessions = store.List();

        Assert.Equal(2, sessions.Count);
        // IDs encode timestamps, so sorting by ID is chronological.
        Assert.True(
            string.Compare(sessions[0].Id, sessions[1].Id, StringComparison.Ordinal) <= 0,
            "Sessions should be in chronological order");
    }

    // ─── Get ──────────────────────────────────────────────────────────

    [Fact]
    public async Task Get_ExistingSession_ReturnsSession()
    {
        var store = new SessionStore(SessionsDir);
        var created = store.Create();
        await store.AppendAsync(created, new ChatMessage(ChatRole.User, "test"));

        var retrieved = store.Get(created.Id);

        Assert.NotNull(retrieved);
        Assert.Equal(created.Id, retrieved.Id);
    }

    [Fact]
    public void Get_NonexistentSession_ReturnsNull()
    {
        var store = new SessionStore(SessionsDir);

        Assert.Null(store.Get("nonexistent-session-id"));
    }

    // ─── Append creates directory ─────────────────────────────────────

    [Fact]
    public async Task AppendAsync_CreatesSessionsDirectoryIfMissing()
    {
        var missingDir = Path.Combine(_root, "new-sessions-dir");
        var store = new SessionStore(missingDir);
        var session = store.Create();

        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "hello"));

        Assert.True(Directory.Exists(missingDir));
        Assert.True(File.Exists(session.FilePath));
    }
}
