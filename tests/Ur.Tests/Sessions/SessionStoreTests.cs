using Microsoft.Extensions.AI;
using Ur.Sessions;

namespace Ur.Tests.Sessions;

/// <summary>
/// Tests for <see cref="JsonlSessionStore"/>. These verify the persistence contract:
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
        var store = new JsonlSessionStore(SessionsDir);
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
        var store = new JsonlSessionStore(SessionsDir);
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
        var store = new JsonlSessionStore(SessionsDir);
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
        var store = new JsonlSessionStore(SessionsDir);
        var session = store.Create();
        // Don't write any file — it shouldn't exist.

        var messages = await store.ReadAllAsync(session);

        Assert.Empty(messages);
    }

    // ─── List ─────────────────────────────────────────────────────────

    [Fact]
    public void List_NoSessionsDirectory_ReturnsEmpty()
    {
        var store = new JsonlSessionStore(Path.Combine(_root, "nonexistent"));

        var sessions = store.List();

        Assert.Empty(sessions);
    }

    [Fact]
    public async Task List_MultipleSessions_ReturnsAllSessions()
    {
        var store = new JsonlSessionStore(SessionsDir);

        // Create first session and write to disk.
        var session1 = store.Create();
        await store.AppendAsync(session1, new ChatMessage(ChatRole.User, "first"));

        // Ensure a distinct timestamp by waiting past the millisecond boundary;
        // JsonlSessionStore encodes creation time in the filename with ms precision.
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
        var store = new JsonlSessionStore(SessionsDir);
        var created = store.Create();
        await store.AppendAsync(created, new ChatMessage(ChatRole.User, "test"));

        var retrieved = store.GetById(created.Id);

        Assert.NotNull(retrieved);
        Assert.Equal(created.Id, retrieved.Id);
    }

    [Fact]
    public void Get_NonexistentSession_ReturnsNull()
    {
        var store = new JsonlSessionStore(SessionsDir);

        Assert.Null(store.GetById("nonexistent-session-id"));
    }

    // ─── Append creates directory ─────────────────────────────────────

    [Fact]
    public async Task AppendAsync_CreatesSessionsDirectoryIfMissing()
    {
        var missingDir = Path.Combine(_root, "new-sessions-dir");
        var store = new JsonlSessionStore(missingDir);
        var session = store.Create();

        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "hello"));

        Assert.True(Directory.Exists(missingDir));
        Assert.True(File.Exists(session.FilePath));
    }

    // ─── Compact boundary ─────────────────────────────────────────────

    [Fact]
    public async Task ReplaceAllAsync_OnlyReplacedMessagesVisibleAfterReplace()
    {
        var store = new JsonlSessionStore(SessionsDir);
        var session = store.Create();

        // Write some pre-compaction messages.
        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "old message 1"));
        await store.AppendAsync(session, new ChatMessage(ChatRole.Assistant, "old response 1"));

        // Replace with post-compaction messages via ReplaceAllAsync.
        var replacementMessages = new List<ChatMessage>
        {
            new(ChatRole.User, "summary"),
            new(ChatRole.User, "new message"),
            new(ChatRole.Assistant, "new response"),
        };
        await store.ReplaceAllAsync(session, replacementMessages);

        var messages = await store.ReadAllAsync(session);

        // Only the 3 replacement messages should be returned.
        Assert.Equal(3, messages.Count);
        Assert.Equal("summary", messages[0].Text);
        Assert.Equal("new message", messages[1].Text);
        Assert.Equal("new response", messages[2].Text);
    }

    [Fact]
    public async Task ReplaceAllAsync_MultipleReplacements_UsesLatest()
    {
        var store = new JsonlSessionStore(SessionsDir);
        var session = store.Create();

        // First compaction cycle.
        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "very old"));
        await store.ReplaceAllAsync(session, [new ChatMessage(ChatRole.User, "first summary")]);

        // Second compaction cycle.
        var secondReplacement = new List<ChatMessage>
        {
            new(ChatRole.User, "second summary"),
            new(ChatRole.User, "recent message"),
        };
        await store.ReplaceAllAsync(session, secondReplacement);

        var messages = await store.ReadAllAsync(session);

        // Only post-second-replacement messages.
        Assert.Equal(2, messages.Count);
        Assert.Equal("second summary", messages[0].Text);
        Assert.Equal("recent message", messages[1].Text);
    }

    [Fact]
    public async Task ReadAllAsync_NoBoundary_ReturnsAllMessages()
    {
        // Backwards compatibility: sessions without any boundary work as before.
        var store = new JsonlSessionStore(SessionsDir);
        var session = store.Create();

        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "message 1"));
        await store.AppendAsync(session, new ChatMessage(ChatRole.Assistant, "response 1"));
        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "message 2"));

        var messages = await store.ReadAllAsync(session);

        Assert.Equal(3, messages.Count);
    }

    // ─── Metrics persistence ──────────────────────────────────────────

    [Fact]
    public async Task WriteMetricsAsync_CreatesJsonFile_AlongsideSessionJsonl()
    {
        var store = new JsonlSessionStore(SessionsDir);
        var session = store.Create();

        // Write a user message first so the session file exists.
        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "test"));

        var metrics = new SessionMetrics
        {
            Turns = 3,
            InputTokens = 12400,
            OutputTokens = 980,
            ToolCallsTotal = 7,
            ToolCallsErrored = 1,
            DurationSeconds = 34.2,
            Error = null,
        };

        await store.WriteMetricsAsync(session, metrics);

        var metricsPath = Path.Combine(SessionsDir, $"{session.Id}.metrics.json");
        Assert.True(File.Exists(metricsPath));

        var json = await File.ReadAllTextAsync(metricsPath);
        Assert.Contains("\"turns\": 3", json);
        Assert.Contains("\"input_tokens\": 12400", json);
        Assert.Contains("\"output_tokens\": 980", json);
        Assert.Contains("\"tool_calls_total\": 7", json);
        Assert.Contains("\"tool_calls_errored\": 1", json);
        Assert.Contains("\"duration_seconds\": 34.2", json);
        Assert.Contains("\"error\": null", json);
    }

    [Fact]
    public async Task WriteMetricsAsync_WithError_IncludesErrorString()
    {
        var store = new JsonlSessionStore(SessionsDir);
        var session = store.Create();
        await store.AppendAsync(session, new ChatMessage(ChatRole.User, "test"));

        var metrics = new SessionMetrics
        {
            Turns = 1,
            InputTokens = 500,
            OutputTokens = 50,
            ToolCallsTotal = 0,
            ToolCallsErrored = 0,
            DurationSeconds = 2.5,
            Error = "API error: rate limit exceeded",
        };

        await store.WriteMetricsAsync(session, metrics);

        var metricsPath = Path.Combine(SessionsDir, $"{session.Id}.metrics.json");
        var json = await File.ReadAllTextAsync(metricsPath);
        Assert.Contains("\"error\": \"API error: rate limit exceeded\"", json);
    }
}
